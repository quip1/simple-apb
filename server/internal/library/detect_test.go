// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func touch(t *testing.T, root string, rels ...string) {
	t.Helper()
	for _, r := range rels {
		p := filepath.Join(root, filepath.FromSlash(r))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDetectRules(t *testing.T) {
	root := t.TempDir()
	touch(t, root,
		"Author A/Single/book.m4b",                                                   // one m4b in a folder
		"Author A/Series S/Two Books/one.m4b", "Author A/Series S/Two Books/two.m4b", // two m4b = two books
		"Author B/Mp3 Book/02 - Two.mp3", "Author B/Mp3 Book/10 - Ten.mp3", "Author B/Mp3 Book/01 - One.mp3", "Author B/Mp3 Book/cover.jpg",
		"Author C/Big Book/CD 2/01.mp3", "Author C/Big Book/CD 10/01.mp3", "Author C/Big Book/CD 1/01.mp3", // multi-disc
		"Author C/Not Discs/Bonus/a.mp3", "Author C/Not Discs/Extras/b.mp3", // subfolders not named like discs = separate books
		".incoming/upload-1", ".incoming/abc.d/x.mp3", // staging is ignored
		"loose.m4b", // loose file in the root
		"Author D/notes.txt",
	)
	cands, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, c := range cands {
		got[c.Rel] = c.Files
	}
	want := map[string][]string{
		"Author A/Single":                     {"Author A/Single/book.m4b"},
		"Author A/Series S/Two Books/one.m4b": {"Author A/Series S/Two Books/one.m4b"},
		"Author A/Series S/Two Books/two.m4b": {"Author A/Series S/Two Books/two.m4b"},
		"Author B/Mp3 Book":                   {"Author B/Mp3 Book/01 - One.mp3", "Author B/Mp3 Book/02 - Two.mp3", "Author B/Mp3 Book/10 - Ten.mp3"},
		"Author C/Big Book":                   {"Author C/Big Book/CD 1/01.mp3", "Author C/Big Book/CD 2/01.mp3", "Author C/Big Book/CD 10/01.mp3"},
		"Author C/Not Discs/Bonus":            {"Author C/Not Discs/Bonus/a.mp3"},
		"Author C/Not Discs/Extras":           {"Author C/Not Discs/Extras/b.mp3"},
		"loose.m4b":                           {"loose.m4b"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d candidates: %v", len(got), got)
	}
	for k, v := range want {
		if !slices.Equal(got[k], v) {
			t.Errorf("%s: got %v, want %v", k, got[k], v)
		}
	}
}

func TestNaturalCompareAndSafeName(t *testing.T) {
	in := []string{"Chapter 10", "chapter 2", "Chapter 1", "Chapter 01a", "Intro"}
	slices.SortFunc(in, NaturalCompare)
	want := []string{"Chapter 1", "Chapter 01a", "chapter 2", "Chapter 10", "Intro"}
	if !slices.Equal(in, want) {
		t.Fatalf("got %v", in)
	}
	cases := map[string]string{
		`AC/DC: "Live"?`: "AC_DC_ _Live__",
		"  ..dots..  ":   "dots",
		"":               "Unknown",
		"CON":            "_CON",
		"../../etc":      "_.._etc",
	}
	for in, want := range cases {
		if got := SafeName(in, "Unknown"); got != want {
			t.Errorf("SafeName(%q) = %q, want %q", in, got, want)
		}
	}
}
