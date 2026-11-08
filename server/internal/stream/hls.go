// SPDX-License-Identifier: AGPL-3.0-or-later

package stream

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/quip1/simple-apb/server/internal/fsutil"
)

// Profile is a low-bitrate HLS rendition.
//
// HE-AAC needs libfdk_aac, which isn't redistributable in our image, so the
// "low" profile is AAC-LC 32 kbps mono at 22.05 kHz (fine for speech).
type Profile struct {
	Name        string
	Channels    int
	SampleRate  int
	BitrateKbps int
}

// Profiles are the available renditions (brief §6.9).
var Profiles = map[string]Profile{
	"low":    {Name: "low", Channels: 1, SampleRate: 22050, BitrateKbps: 32},
	"medium": {Name: "medium", Channels: 2, SampleRate: 44100, BitrateKbps: 64},
}

// aacFrame is samples per AAC frame.
const aacFrame = 1024

// SegmentSeconds is ~10 s rounded to a whole number of AAC frames, so a
// transcoder restarted at segment N cuts at exactly the same sample positions
// as one that ran from the start.
func (p Profile) SegmentSeconds() float64 {
	frames := math.Round(10 * float64(p.SampleRate) / aacFrame)
	return frames * aacFrame / float64(p.SampleRate)
}

// Segments returns how many segments cover durationMS.
func (p Profile) Segments(durationMS int64) int {
	return int(math.Ceil(float64(durationMS) / 1000 / p.SegmentSeconds()))
}

// SourceFile is one input file on the book timeline.
type SourceFile struct {
	Path       string // absolute
	OffsetMS   int64
	DurationMS int64
}

// Source is a book to transcode. Key changes when the files change.
type Source struct {
	BookID, Key string
	DurationMS  int64
	Files       []SourceFile
}

// HLS transcodes books on demand into /cache/hls with an LRU size cap.
type HLS struct {
	FFmpeg   string
	CacheDir string
	Log      *slog.Logger
	// MaxAhead: a transcoder this many segments past the last request is
	// stopped (no need to encode a whole 40 h book for a 5-minute listen).
	MaxAhead int
	mu       sync.Mutex
	running  map[string]*transcode
}

// NewHLS creates the transcoder manager.
func NewHLS(ffmpeg, cacheDir string, log *slog.Logger) *HLS {
	return &HLS{FFmpeg: ffmpeg, CacheDir: cacheDir, Log: log, MaxAhead: 60, running: map[string]*transcode{}}
}

type transcode struct {
	start    int
	cancel   context.CancelFunc
	done     chan struct{}
	err      error
	lastWant int // guarded by HLS.mu
}

// ErrBadSegment: segment number out of range.
var ErrBadSegment = errors.New("no such segment")

func (h *HLS) dir(src Source, p Profile) string {
	return filepath.Join(h.CacheDir, "hls", src.BookID, src.Key, p.Name)
}

// SegmentName is the file name for segment n.
func SegmentName(n int) string { return fmt.Sprintf("seg_%06d.ts", n) }

// Playlist renders the full VOD playlist; query (e.g. "sig=…") is appended to
// segment URIs so casting devices stay authorized.
func (h *HLS) Playlist(src Source, p Profile, query string) []byte {
	seg := p.SegmentSeconds()
	n := p.Segments(src.DurationMS)
	var b bytes.Buffer
	fmt.Fprintf(&b, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXT-X-TARGETDURATION:%d\n#EXT-X-MEDIA-SEQUENCE:0\n",
		int(math.Ceil(seg)))
	total := float64(src.DurationMS) / 1000
	for i := range n {
		d := min(seg, total-float64(i)*seg)
		uri := SegmentName(i)
		if query != "" {
			uri += "?" + query
		}
		fmt.Fprintf(&b, "#EXTINF:%.5f,\n%s\n", d, uri)
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return b.Bytes()
}

// Segment returns the path of a finished segment, transcoding as needed.
func (h *HLS) Segment(ctx context.Context, src Source, p Profile, n int) (string, error) {
	total := p.Segments(src.DurationMS)
	if n < 0 || n >= total {
		return "", ErrBadSegment
	}
	dir := h.dir(src, p)
	path := filepath.Join(dir, SegmentName(n))
	key := dir
	deadline := time.Now().Add(60 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			now := time.Now()
			_ = os.Chtimes(filepath.Join(h.CacheDir, "hls", src.BookID), now, now) // LRU "last used"
			h.noteWant(key, n)
			return path, nil
		}
		if err := h.ensure(src, p, n, key, dir); err != nil {
			return "", err
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("segment %d not ready in time", n)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (h *HLS) noteWant(key string, n int) {
	h.mu.Lock()
	if t := h.running[key]; t != nil && n > t.lastWant {
		t.lastWant = n
	}
	h.mu.Unlock()
}

// ensure makes sure a transcoder will produce segment n soon: an existing one
// that started at or before n and is close behind is kept; otherwise a new one
// starts exactly at n.
func (h *HLS) ensure(src Source, p Profile, n int, key, dir string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if t := h.running[key]; t != nil {
		select {
		case <-t.done:
			if t.err != nil && t.start == n {
				delete(h.running, key)
				return fmt.Errorf("transcode failed: %w", t.err)
			}
			delete(h.running, key)
		default:
			if n >= t.start && n <= h.produced(dir, t.start)+6 {
				if n > t.lastWant {
					t.lastWant = n
				}
				return nil
			}
			t.cancel() // too far away: restart where the listener is
			delete(h.running, key)
		}
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	t := &transcode{start: n, cancel: cancel, done: make(chan struct{}), lastWant: n}
	h.running[key] = t
	go h.run(ctx, t, src, p, dir)
	return nil
}

// produced returns the highest consecutive finished segment from start.
func (h *HLS) produced(dir string, start int) int {
	n := start - 1
	for {
		if _, err := os.Stat(filepath.Join(dir, SegmentName(n+1))); err != nil {
			return n
		}
		n++
	}
}

func (h *HLS) run(ctx context.Context, t *transcode, src Source, p Profile, dir string) {
	defer close(t.done)
	seg := p.SegmentSeconds()
	startMS := int64(math.Round(float64(t.start) * seg * 1000))
	list, err := concatList(src, startMS)
	if err != nil {
		t.err = err
		return
	}
	listPath := filepath.Join(dir, fmt.Sprintf("input_%06d.txt", t.start))
	if err := fsutil.WriteFileAtomic(listPath, []byte(list), 0o640); err != nil {
		t.err = err
		return
	}
	defer func() { _ = os.Remove(listPath) }()
	// hls_time just under the exact segment length: the muxer cuts at the
	// first frame at or after it, i.e. exactly at the frame boundary.
	hlsTime := seg - 0.5*aacFrame/float64(p.SampleRate)
	args := []string{"-v", "error", "-nostdin", "-y",
		"-f", "concat", "-safe", "0", "-i", listPath,
		"-map", "0:a:0", "-vn", "-sn", "-dn",
		"-c:a", "aac", "-b:a", fmt.Sprintf("%dk", p.BitrateKbps), "-ac", fmt.Sprint(p.Channels), "-ar", fmt.Sprint(p.SampleRate),
		"-output_ts_offset", fmt.Sprintf("%.6f", float64(t.start)*seg),
		"-f", "hls", "-hls_time", fmt.Sprintf("%.6f", hlsTime), "-hls_list_size", "0",
		"-hls_segment_type", "mpegts", "-hls_flags", "temp_file+independent_segments",
		"-start_number", fmt.Sprint(t.start),
		"-hls_segment_filename", filepath.Join(dir, "seg_%06d.ts"),
		filepath.Join(dir, fmt.Sprintf("ffmpeg_%06d.m3u8", t.start)),
	}
	cmd := exec.CommandContext(ctx, h.FFmpeg, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.err = err
		return
	}
	// Watchdog: stop when far ahead of the listener.
	stopWatch := make(chan struct{})
	go func() {
		tk := time.NewTicker(2 * time.Second)
		defer tk.Stop()
		for {
			select {
			case <-stopWatch:
				return
			case <-tk.C:
				h.mu.Lock()
				want := t.lastWant
				h.mu.Unlock()
				if h.produced(dir, t.start) > want+h.MaxAhead {
					t.cancel()
					return
				}
			}
		}
	}()
	err = cmd.Wait()
	close(stopWatch)
	if err != nil && ctx.Err() == nil {
		t.err = fmt.Errorf("ffmpeg: %v: %s", err, strings.TrimSpace(stderr.String()))
		h.Log.Warn("hls transcode failed", "book", src.BookID, "profile", p.Name, "err", t.err)
	}
	// Leftover temp segments from a cancelled run are useless.
	if tmps, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(tmps) > 0 {
		for _, f := range tmps {
			_ = os.Remove(f)
		}
	}
}

// concatList builds an ffmpeg concat-demuxer list starting at startMS on the
// book timeline (inpoint into the first file).
func concatList(src Source, startMS int64) (string, error) {
	var b strings.Builder
	b.WriteString("ffconcat version 1.0\n")
	found := false
	for _, f := range src.Files {
		if f.OffsetMS+f.DurationMS <= startMS {
			continue
		}
		fmt.Fprintf(&b, "file '%s'\n", strings.ReplaceAll(filepath.ToSlash(f.Path), "'", `'\''`))
		if !found {
			found = true
			if in := startMS - f.OffsetMS; in > 0 {
				fmt.Fprintf(&b, "inpoint %.3f\n", float64(in)/1000)
			}
		}
	}
	if !found {
		return "", ErrBadSegment
	}
	return b.String(), nil
}

// Prune deletes the least recently used books' HLS caches until the total is
// under capBytes. Books with a running transcoder are skipped.
func (h *HLS) Prune(capBytes int64) (int, error) {
	root := filepath.Join(h.CacheDir, "hls")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	type bookDir struct {
		path string
		size int64
		used time.Time
	}
	var dirs []bookDir
	var total int64
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(root, e.Name())
		var size int64
		_ = filepath.Walk(p, func(_ string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() {
				size += info.Size()
			}
			return nil
		})
		info, err := e.Info()
		if err != nil {
			continue
		}
		dirs = append(dirs, bookDir{p, size, info.ModTime()})
		total += size
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].used.Before(dirs[j].used) })
	removed := 0
	for _, d := range dirs {
		if total <= capBytes {
			break
		}
		if h.busy(d.path) {
			continue
		}
		if err := os.RemoveAll(d.path); err != nil {
			return removed, err
		}
		total -= d.size
		removed++
	}
	return removed, nil
}

func (h *HLS) busy(bookDir string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for key, t := range h.running {
		if strings.HasPrefix(key, bookDir+string(filepath.Separator)) {
			select {
			case <-t.done:
			default:
				return true
			}
		}
	}
	return false
}

// StopAll cancels running transcoders (shutdown).
func (h *HLS) StopAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for k, t := range h.running {
		t.cancel()
		delete(h.running, k)
	}
}
