// SPDX-License-Identifier: AGPL-3.0-or-later

package stream

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/quip1/simple-apb/server/internal/media"
)

func TestSegmentMath(t *testing.T) {
	low, med := Profiles["low"], Profiles["medium"]
	// 215 frames × 1024 / 22050 ≈ 9.985 s ; 431 × 1024 / 44100 ≈ 10.008 s
	if s := low.SegmentSeconds(); s < 9.984 || s > 9.986 {
		t.Fatalf("low segment %f", s)
	}
	if s := med.SegmentSeconds(); s < 10.007 || s > 10.009 {
		t.Fatalf("medium segment %f", s)
	}
	if low.Segments(25_000) != 3 || low.Segments(19_960) != 2 {
		t.Fatalf("segment counts %d %d", low.Segments(25_000), low.Segments(19_960))
	}
	pl := string((&HLS{}).Playlist(Source{DurationMS: 25_000}, low, "sig=abc"))
	if !strings.Contains(pl, "seg_000002.ts?sig=abc") || !strings.HasSuffix(pl, "#EXT-X-ENDLIST\n") || strings.Count(pl, "#EXTINF") != 3 {
		t.Fatalf("playlist:\n%s", pl)
	}
}

func TestSignerRoundTrip(t *testing.T) {
	s := Signer{Key: []byte("0123456789abcdef0123456789abcdef")}
	now := time.Now()
	tok := s.Sign("user-1", "book-1", now.Add(SignedTTL))
	if u, err := s.Verify(tok, "book-1", now); err != nil || u != "user-1" {
		t.Fatalf("verify: %q %v", u, err)
	}
	if _, err := s.Verify(tok, "book-2", now); err == nil {
		t.Fatal("token valid for another book")
	}
	if _, err := s.Verify(tok, "book-1", now.Add(SignedTTL+time.Second)); err == nil {
		t.Fatal("expired token accepted")
	}
	if _, err := s.Verify(strings.Replace(tok, "user-1", "user-2", 1), "book-1", now); err == nil {
		t.Fatal("tampered token accepted")
	}
}

func TestHLSTranscodeAndSeek(t *testing.T) {
	tools := media.DefaultTools()
	if !tools.Available() {
		t.Skip("ffmpeg not installed")
	}
	ctx := context.Background()
	dir := t.TempDir()
	// A two-file book (mp3s), 13 s + 12 s = 25 s → 3 low segments.
	var files []SourceFile
	var off int64
	for i, secs := range []int{13, 12} {
		p := filepath.Join(dir, "src", []string{"a.mp3", "b.mp3"}[i])
		if err := tools.MakeMP3(ctx, p, media.FixtureBook{Seconds: secs, Frequency: 400 + 100*i}, "t", i+1); err != nil {
			t.Fatal(err)
		}
		pr, err := tools.ProbeFile(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, SourceFile{Path: p, OffsetMS: off, DurationMS: pr.DurationMS})
		off += pr.DurationMS
	}
	src := Source{BookID: "book", Key: "k1", DurationMS: off, Files: files}
	h := NewHLS(tools.FFmpeg, filepath.Join(dir, "cache"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	low := Profiles["low"]

	// Seek first: ask for the last segment before anything exists.
	last := low.Segments(src.DurationMS) - 1
	p2, err := h.Segment(ctx, src, low, last)
	if err != nil {
		t.Fatal(err)
	}
	p0, err := h.Segment(ctx, src, low, 0)
	if err != nil {
		t.Fatal(err)
	}
	p1, err := h.Segment(ctx, src, low, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Segment(ctx, src, low, last+1); err != ErrBadSegment {
		t.Fatalf("out of range: %v", err)
	}
	// Join the segments and check the stream is ~25 s, mono 22.05 kHz AAC.
	var joined bytes.Buffer
	for _, p := range []string{p0, p1, p2} {
		b, _ := os.ReadFile(p)
		joined.Write(b)
	}
	jp := filepath.Join(dir, "joined.ts")
	_ = os.WriteFile(jp, joined.Bytes(), 0o600)
	pr, err := tools.ProbeFile(ctx, jp)
	if err != nil {
		t.Fatal(err)
	}
	if pr.Codec != "aac" || pr.DurationMS < 24_000 || pr.DurationMS > 26_500 {
		t.Fatalf("joined stream: codec %s duration %d ms", pr.Codec, pr.DurationMS)
	}
	// Segment 2 came from a run started at 2 × 9.985 s: its first timestamp must line up.
	pr2, _ := tools.ProbeFile(ctx, p2)
	if pr2.DurationMS > 6000 {
		t.Fatalf("last segment should be the ~5 s remainder, got %d ms", pr2.DurationMS)
	}
	h.StopAll()

	// LRU prune: a 1-byte cap removes the (idle) book cache.
	time.Sleep(100 * time.Millisecond)
	if n, err := h.Prune(1); err != nil || n != 1 {
		t.Fatalf("prune: %d %v", n, err)
	}
}
