// SPDX-License-Identifier: AGPL-3.0-or-later

// Package media wraps the ffprobe/ffmpeg binaries (run as subprocesses).
package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/quip1/simple-apb/server/internal/fsutil"
)

// Tools locates the ffmpeg binaries (env SIMPLEAPB_FFPROBE / SIMPLEAPB_FFMPEG, else PATH).
type Tools struct {
	FFprobe, FFmpeg string
}

// DefaultTools returns binaries from env or PATH.
func DefaultTools() Tools {
	t := Tools{FFprobe: os.Getenv("SIMPLEAPB_FFPROBE"), FFmpeg: os.Getenv("SIMPLEAPB_FFMPEG")}
	if t.FFprobe == "" {
		t.FFprobe = "ffprobe"
	}
	if t.FFmpeg == "" {
		t.FFmpeg = "ffmpeg"
	}
	return t
}

// Available reports whether ffprobe can be run.
func (t Tools) Available() bool {
	_, err := exec.LookPath(t.FFprobe)
	return err == nil
}

// Chapter is a chapter inside one file (file-relative times).
type Chapter struct {
	Title          string
	StartMS, EndMS int64
}

// Probe is what ffprobe tells us about one audio file.
type Probe struct {
	DurationMS  int64
	Codec       string
	BitrateKbps int
	MimeType    string
	Tags        map[string]string // lowercased keys
	Chapters    []Chapter
	HasCover    bool
}

// Tag returns the first non-empty tag among keys.
func (p *Probe) Tag(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(p.Tags[k]); v != "" {
			return v
		}
	}
	return ""
}

// ErrNotAudio means the file isn't a readable audio file.
var ErrNotAudio = errors.New("not a readable audio file")

type ffprobeOut struct {
	Format struct {
		Duration string            `json:"duration"`
		BitRate  string            `json:"bit_rate"`
		Tags     map[string]string `json:"tags"`
	} `json:"format"`
	Streams []struct {
		CodecType   string            `json:"codec_type"`
		CodecName   string            `json:"codec_name"`
		BitRate     string            `json:"bit_rate"`
		Tags        map[string]string `json:"tags"`
		Disposition map[string]int    `json:"disposition"`
	} `json:"streams"`
	Chapters []struct {
		StartTime string            `json:"start_time"`
		EndTime   string            `json:"end_time"`
		Tags      map[string]string `json:"tags"`
	} `json:"chapters"`
}

// ProbeFile runs ffprobe on a file.
func (t Tools) ProbeFile(ctx context.Context, path string) (*Probe, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, t.FFprobe, "-v", "error", "-print_format", "json",
		"-show_format", "-show_streams", "-show_chapters", "--", path)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("ffprobe %s: %w", filepath.Base(path), ctx.Err())
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("%w: %s", ErrNotAudio, firstLine(stderr.String()))
		}
		return nil, fmt.Errorf("run ffprobe: %w", err)
	}
	var out ffprobeOut
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return nil, fmt.Errorf("parse ffprobe output: %w", err)
	}
	p := &Probe{Tags: map[string]string{}}
	for k, v := range out.Format.Tags {
		p.Tags[strings.ToLower(k)] = v
	}
	audio := false
	for _, s := range out.Streams {
		switch s.CodecType {
		case "audio":
			if !audio {
				audio = true
				p.Codec = s.CodecName
				p.BitrateKbps = kbps(s.BitRate)
				for k, v := range s.Tags { // some files keep tags on the stream
					if _, ok := p.Tags[strings.ToLower(k)]; !ok {
						p.Tags[strings.ToLower(k)] = v
					}
				}
			}
		case "video":
			if s.Disposition["attached_pic"] == 1 {
				p.HasCover = true
			}
		}
	}
	if !audio {
		return nil, fmt.Errorf("%w: no audio stream", ErrNotAudio)
	}
	if p.BitrateKbps == 0 {
		p.BitrateKbps = kbps(out.Format.BitRate)
	}
	p.DurationMS = secondsToMS(out.Format.Duration)
	if p.DurationMS <= 0 {
		return nil, fmt.Errorf("%w: unknown duration", ErrNotAudio)
	}
	for i, c := range out.Chapters {
		title := strings.TrimSpace(c.Tags["title"])
		if title == "" {
			title = fmt.Sprintf("Chapter %d", i+1)
		}
		p.Chapters = append(p.Chapters, Chapter{Title: title, StartMS: secondsToMS(c.StartTime), EndMS: secondsToMS(c.EndTime)})
	}
	p.MimeType = mimeFor(path, p.Codec)
	return p, nil
}

// ExtractCover writes the embedded cover as JPEG to dest (crash-safe).
func (t Tools) ExtractCover(ctx context.Context, audioPath, dest string) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return err
	}
	tmp := dest + ".tmp.jpg"
	defer func() { _ = os.Remove(tmp) }()
	cmd := exec.CommandContext(ctx, t.FFmpeg, "-v", "error", "-y", "-i", audioPath, "-map", "0:v:0", "-frames:v", "1",
		"-c:v", "mjpeg", "-q:v", "3", "-f", "image2", tmp)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("extract cover: %v: %s", err, firstLine(stderr.String()))
	}
	f, err := os.Open(tmp)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	w, err := fsutil.NewAtomicWriter(dest, 0o640)
	if err != nil {
		return err
	}
	if _, err := w.ReadFrom(f); err != nil {
		w.Abort()
		return err
	}
	return w.Commit()
}

func secondsToMS(s string) int64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(f) || f < 0 {
		return 0
	}
	return int64(math.Round(f * 1000))
}

func kbps(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return (n + 500) / 1000
}

func mimeFor(path, codec string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".m4b", ".m4a", ".mp4":
		return "audio/mp4"
	case ".mp3":
		return "audio/mpeg"
	case ".ogg", ".oga", ".opus":
		return "audio/ogg"
	case ".flac":
		return "audio/flac"
	}
	if codec == "mp3" {
		return "audio/mpeg"
	}
	return "application/octet-stream"
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}
