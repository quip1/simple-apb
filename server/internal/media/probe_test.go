// SPDX-License-Identifier: AGPL-3.0-or-later

package media

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func tools(t *testing.T) Tools {
	t.Helper()
	tl := DefaultTools()
	if !tl.Available() {
		t.Skip("ffprobe not installed")
	}
	return tl
}

func TestProbeM4BWithChaptersAndCover(t *testing.T) {
	tl := tools(t)
	ctx := context.Background()
	p := filepath.Join(t.TempDir(), "book.m4b")
	err := tl.MakeM4B(ctx, p, FixtureBook{Title: "The Test Book", Author: "Jane Author", Narrator: "Nick Narrator",
		Genre: "Fantasy", Seconds: 4, Cover: true, Chapters: []FixtureChapter{
			{Title: "Opening", StartMS: 0, EndMS: 1500}, {Title: "Middle; part = 2", StartMS: 1500, EndMS: 3000}, {Title: "End", StartMS: 3000, EndMS: 4000}}})
	if err != nil {
		t.Fatal(err)
	}
	pr, err := tl.ProbeFile(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if pr.DurationMS < 3900 || pr.DurationMS > 4200 || pr.Codec != "aac" || pr.MimeType != "audio/mp4" {
		t.Fatalf("probe: %+v", pr)
	}
	if len(pr.Chapters) != 3 || pr.Chapters[1].Title != "Middle; part = 2" || pr.Chapters[1].StartMS != 1500 {
		t.Fatalf("chapters: %+v", pr.Chapters)
	}
	if pr.Tag("album", "title") != "The Test Book" || pr.Tag("artist") != "Jane Author" || pr.Tag("composer") != "Nick Narrator" {
		t.Fatalf("tags: %v", pr.Tags)
	}
	if !pr.HasCover {
		t.Fatal("cover not detected")
	}
	cover := filepath.Join(t.TempDir(), "c", "cover.jpg")
	if err := tl.ExtractCover(ctx, p, cover); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(cover); err != nil || st.Size() == 0 {
		t.Fatal("cover not written")
	}
}

func TestProbeMP3AndCorrupt(t *testing.T) {
	tl := tools(t)
	ctx := context.Background()
	dir := t.TempDir()
	mp3 := filepath.Join(dir, "01.mp3")
	if err := tl.MakeMP3(ctx, mp3, FixtureBook{Title: "Album", Author: "A", Seconds: 2}, "Track One", 1); err != nil {
		t.Fatal(err)
	}
	pr, err := tl.ProbeFile(ctx, mp3)
	if err != nil || pr.Codec != "mp3" || pr.MimeType != "audio/mpeg" || pr.Tag("title") != "Track One" {
		t.Fatalf("mp3: %+v %v", pr, err)
	}
	bad := filepath.Join(dir, "corrupt.m4b")
	_ = os.WriteFile(bad, []byte("this is not audio at all"), 0o600)
	if _, err := tl.ProbeFile(ctx, bad); !errors.Is(err, ErrNotAudio) {
		t.Fatalf("want ErrNotAudio, got %v", err)
	}
}
