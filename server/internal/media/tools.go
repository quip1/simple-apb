// SPDX-License-Identifier: AGPL-3.0-or-later

package media

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The ffmpeg side of the book tools (brief §6.13): silence detection,
// writing chapters into an m4b without re-encoding, and merging files into
// one chaptered m4b.

// Silence is one quiet stretch of a file, in milliseconds.
type Silence struct {
	StartMS, EndMS int64
}

var (
	silenceStart = regexp.MustCompile(`silence_start: (-?[0-9.]+)`)
	silenceEnd   = regexp.MustCompile(`silence_end: ([0-9.]+)`)
)

// DetectSilences runs ffmpeg's silencedetect over a file. noiseDB is the
// level below which audio counts as silent (e.g. -35); minSilence how long
// it has to last.
func (t Tools) DetectSilences(ctx context.Context, path string, noiseDB float64, minSilence time.Duration) ([]Silence, error) {
	filter := fmt.Sprintf("silencedetect=noise=%.1fdB:d=%.2f", noiseDB, minSilence.Seconds())
	cmd := exec.CommandContext(ctx, t.FFmpeg, "-hide_banner", "-nostats", "-i", path, "-vn", "-af", filter, "-f", "null", "-")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("silencedetect %s: %v: %s", filepath.Base(path), err, lastLine(stderr.String()))
	}
	return parseSilences(stderr.String()), nil
}

func parseSilences(log string) []Silence {
	var out []Silence
	open := int64(-1)
	sc := bufio.NewScanner(strings.NewReader(log))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if m := silenceStart.FindStringSubmatch(line); m != nil {
			open = max(0, secondsToMS(m[1]))
		} else if m := silenceEnd.FindStringSubmatch(line); m != nil && open >= 0 {
			out = append(out, Silence{StartMS: open, EndMS: secondsToMS(m[1])})
			open = -1
		}
	}
	return out
}

// ChapterMark is a chapter to write, on the timeline of the output file.
type ChapterMark struct {
	Title          string
	StartMS, EndMS int64
}

// BookTags are the tags written into a merged m4b.
type BookTags struct {
	Title, Authors, Narrators, Series, SeriesPart, Year, Description string
}

// ffmetadata builds an FFMETADATA1 file with tags and chapters.
func ffmetadata(tags map[string]string, chapters []ChapterMark) string {
	var b strings.Builder
	b.WriteString(";FFMETADATA1\n")
	for _, k := range []string{"title", "album", "artist", "album_artist", "composer", "genre", "date", "series", "series-part", "comment"} {
		if v := strings.TrimSpace(tags[k]); v != "" {
			fmt.Fprintf(&b, "%s=%s\n", k, escapeMeta(v))
		}
	}
	for _, c := range chapters {
		fmt.Fprintf(&b, "[CHAPTER]\nTIMEBASE=1/1000\nSTART=%d\nEND=%d\ntitle=%s\n", c.StartMS, c.EndMS, escapeMeta(c.Title))
	}
	return b.String()
}

func (t Tools) runFFmpeg(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, t.FFmpeg, append([]string{"-hide_banner", "-nostats", "-v", "error", "-y"}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("ffmpeg: %v: %s", err, lastLine(stderr.String()))
	}
	return nil
}

// WriteChapters copies src to dst (an .m4b/.m4a) with these chapters,
// keeping its audio, tags and cover as they are (no re-encode).
func (t Tools) WriteChapters(ctx context.Context, src, dst string, chapters []ChapterMark) error {
	meta := dst + ".ffmeta"
	if err := os.WriteFile(meta, []byte(ffmetadata(nil, chapters)), 0o600); err != nil {
		return err
	}
	defer func() { _ = os.Remove(meta) }()
	return t.runFFmpeg(ctx, "-i", src, "-i", meta,
		"-map", "0", "-map_metadata", "0", "-map_chapters", "1",
		"-c", "copy", "-movflags", "+faststart", "-f", "mp4", dst)
}

// MergeOptions controls MergeToM4B.
type MergeOptions struct {
	Inputs   []string // in play order
	Chapters []ChapterMark
	Tags     BookTags
	Cover    string // optional JPEG/PNG to embed
	Reencode bool   // convert to AAC
	// BitrateKbps for AAC (0 = 64 kbps per channel).
	BitrateKbps int
	Channels    int
}

// MergeToM4B concatenates the inputs into one chaptered m4b at dst.
func (t Tools) MergeToM4B(ctx context.Context, dst string, o MergeOptions) error {
	dir := filepath.Dir(dst)
	list := filepath.Join(dir, filepath.Base(dst)+".list")
	meta := filepath.Join(dir, filepath.Base(dst)+".ffmeta")
	defer func() {
		_ = os.Remove(list)
		_ = os.Remove(meta)
	}()
	var lb strings.Builder
	for _, in := range o.Inputs {
		abs, err := filepath.Abs(in)
		if err != nil {
			return err
		}
		// The concat demuxer's quoting: wrap in '…', and write ' as '\''.
		fmt.Fprintf(&lb, "file '%s'\n", strings.ReplaceAll(filepath.ToSlash(abs), "'", `'\''`))
	}
	if err := os.WriteFile(list, []byte(lb.String()), 0o600); err != nil {
		return err
	}
	tags := map[string]string{
		"title": o.Tags.Title, "album": o.Tags.Title, "artist": o.Tags.Authors, "album_artist": o.Tags.Authors,
		"composer": o.Tags.Narrators, "genre": "Audiobook", "date": o.Tags.Year, "series": o.Tags.Series,
		"series-part": o.Tags.SeriesPart, "comment": o.Tags.Description,
	}
	if err := os.WriteFile(meta, []byte(ffmetadata(tags, o.Chapters)), 0o600); err != nil {
		return err
	}
	args := []string{"-f", "concat", "-safe", "0", "-i", list, "-i", meta}
	if o.Cover != "" {
		args = append(args, "-i", o.Cover)
	}
	args = append(args, "-map", "0:a", "-map_metadata", "1", "-map_chapters", "1")
	if o.Cover != "" {
		args = append(args, "-map", "2:v", "-c:v", "mjpeg", "-disposition:v", "attached_pic")
	}
	if o.Reencode {
		kbps := o.BitrateKbps
		if kbps <= 0 {
			kbps = 64 * max(1, min(2, o.Channels))
		}
		args = append(args, "-c:a", "aac", "-b:a", strconv.Itoa(kbps)+"k")
	} else {
		args = append(args, "-c:a", "copy")
	}
	args = append(args, "-movflags", "+faststart", "-f", "mp4", dst)
	return t.runFFmpeg(ctx, args...)
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
