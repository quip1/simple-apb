// SPDX-License-Identifier: AGPL-3.0-or-later

package media

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Test fixtures: tiny real audio files generated with ffmpeg, so integration
// tests exercise the real probe/import path. Used only by tests.

// FixtureChapter is a chapter for MakeM4B.
type FixtureChapter struct {
	Title          string
	StartMS, EndMS int64
}

// FixtureBook describes a generated book.
type FixtureBook struct {
	Title, Author, Narrator, Series, SeriesPart, Genre string
	Seconds                                            int
	Chapters                                           []FixtureChapter
	Cover                                              bool
	Frequency                                          int // vary to make different checksums
}

func escapeMeta(s string) string {
	return strings.NewReplacer("\\", "\\\\", "=", "\\=", ";", "\\;", "#", "\\#", "\n", "\\\n").Replace(s)
}

// MakeM4B writes a chaptered AAC .m4b.
func (t Tools) MakeM4B(ctx context.Context, path string, b FixtureBook) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	var meta strings.Builder
	meta.WriteString(";FFMETADATA1\n")
	for k, v := range map[string]string{"title": b.Title, "album": b.Title, "artist": b.Author, "album_artist": b.Author,
		"composer": b.Narrator, "genre": b.Genre, "series": b.Series, "series-part": b.SeriesPart} {
		if v != "" {
			fmt.Fprintf(&meta, "%s=%s\n", k, escapeMeta(v))
		}
	}
	for _, c := range b.Chapters {
		fmt.Fprintf(&meta, "[CHAPTER]\nTIMEBASE=1/1000\nSTART=%d\nEND=%d\ntitle=%s\n", c.StartMS, c.EndMS, escapeMeta(c.Title))
	}
	metaPath := path + ".ffmeta"
	if err := os.WriteFile(metaPath, []byte(meta.String()), 0o600); err != nil {
		return err
	}
	defer func() { _ = os.Remove(metaPath) }()
	args := []string{"-v", "error", "-y", "-f", "lavfi", "-i", sine(b), "-i", metaPath}
	if b.Cover {
		// Render the cover to a JPEG first: a single-image input ends by itself,
		// whereas -frames:v on a lavfi source would also cut the audio short.
		coverPath := path + ".cover.jpg"
		if err := t.run(ctx, []string{"-v", "error", "-y", "-f", "lavfi", "-i", "color=c=red:s=64x64", "-frames:v", "1", coverPath}); err != nil {
			return err
		}
		defer func() { _ = os.Remove(coverPath) }()
		args = append(args, "-i", coverPath)
	}
	args = append(args, "-map", "0:a", "-map_metadata", "1", "-map_chapters", "1")
	if b.Cover {
		args = append(args, "-map", "2:v", "-c:v", "copy", "-disposition:v:0", "attached_pic")
	}
	args = append(args, "-c:a", "aac", "-b:a", "32k", "-ac", "1")
	// ffmpeg limitation: custom tags (series…) need use_metadata_tags, which in
	// turn drops attached covers. Real taggers write both; fixtures pick one.
	if b.Cover {
		args = append(args, "-f", "ipod", path)
	} else {
		args = append(args, "-movflags", "use_metadata_tags", "-f", "mp4", path)
	}
	return t.run(ctx, args)
}

// MakeMP3 writes one tagged .mp3.
func (t Tools) MakeMP3(ctx context.Context, path string, b FixtureBook, trackTitle string, track int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	args := []string{"-v", "error", "-y", "-f", "lavfi", "-i", sine(b)}
	for k, v := range map[string]string{"title": trackTitle, "album": b.Title, "artist": b.Author, "composer": b.Narrator,
		"genre": b.Genre, "track": fmt.Sprint(track)} {
		if v != "" && v != "0" {
			args = append(args, "-metadata", k+"="+v)
		}
	}
	args = append(args, "-c:a", "libmp3lame", "-b:a", "32k", "-ac", "1", "-id3v2_version", "3", "-f", "mp3", path)
	return t.run(ctx, args)
}

func sine(b FixtureBook) string {
	f, s := b.Frequency, b.Seconds
	if f == 0 {
		f = 440
	}
	if s == 0 {
		s = 3
	}
	return fmt.Sprintf("sine=frequency=%d:duration=%d:sample_rate=22050", f, s)
}

func (t Tools) run(ctx context.Context, args []string) error {
	cmd := exec.CommandContext(ctx, t.FFmpeg, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg: %v: %s", err, stderr.String())
	}
	return nil
}
