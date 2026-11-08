// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
)

// IncomingDir is where uploads are staged (inside the library so the final
// move is an atomic rename on the same filesystem).
const IncomingDir = ".incoming"

var audioExts = map[string]bool{".m4b": true, ".m4a": true, ".mp3": true}

// IsAudio reports whether a filename is a supported audio file.
func IsAudio(name string) bool { return audioExts[strings.ToLower(filepath.Ext(name))] }

var discDir = regexp.MustCompile(`(?i)^(cd|disc|disk|part)[\s._-]*\d+$`)

// Candidate is a detected book on disk.
type Candidate struct {
	// Rel is the book root relative to the library: a directory, or a single
	// file when several m4b files share one directory.
	Rel string
	// Files are paths relative to the library, in play order.
	Files []string
	// Newest is the latest mtime among the files (for the settle check).
	Newest time.Time
	// Hint is a fallback title when the path says nothing (uploads).
	Hint string
}

// Detect walks the library and returns book candidates.
//
// Rules: a directory containing mp3 files is one book (all its audio files);
// a directory with exactly one m4b/m4a is one book; several m4b/m4a files in
// one directory are one book each; a directory without audio whose audio
// subdirectories are all named like "CD 1"/"Disc 2"/"Part 3" is one
// multi-disc book.
func Detect(root string) ([]Candidate, error) {
	type dirInfo struct {
		audio   []string
		subdirs []string
	}
	dirs := map[string]*dirInfo{}
	mtimes := map[string]time.Time{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}
			return nil // unreadable entry: skip, keep scanning
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		name := d.Name()
		if path != root && strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			dirs[rel] = &dirInfo{}
			if rel != "." {
				parent := filepath.ToSlash(filepath.Dir(rel))
				if p := dirs[parent]; p != nil {
					p.subdirs = append(p.subdirs, rel)
				}
			}
			return nil
		}
		if IsAudio(name) && d.Type().IsRegular() {
			parent := filepath.ToSlash(filepath.Dir(rel))
			if p := dirs[parent]; p != nil {
				p.audio = append(p.audio, rel)
			}
			if info, err := d.Info(); err == nil {
				mtimes[rel] = info.ModTime()
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	newest := func(files []string) time.Time {
		var t time.Time
		for _, f := range files {
			if m := mtimes[f]; m.After(t) {
				t = m
			}
		}
		return t
	}
	var out []Candidate
	claimed := map[string]bool{}
	keys := make([]string, 0, len(dirs))
	for k := range dirs {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	// Multi-disc parents first, so their disc folders aren't treated as books.
	for _, rel := range keys {
		d := dirs[rel]
		if rel == "." || len(d.audio) > 0 || len(d.subdirs) < 2 {
			continue
		}
		var discs []string
		allDiscs := true
		for _, s := range d.subdirs {
			if len(dirs[s].audio) == 0 {
				continue
			}
			if !discDir.MatchString(filepath.Base(s)) || len(dirs[s].subdirs) > 0 {
				allDiscs = false
				break
			}
			discs = append(discs, s)
		}
		if !allDiscs || len(discs) < 2 {
			continue
		}
		slices.SortFunc(discs, NaturalCompare)
		var files []string
		for _, s := range discs {
			fs := slices.Clone(dirs[s].audio)
			slices.SortFunc(fs, NaturalCompare)
			files = append(files, fs...)
			claimed[s] = true
		}
		out = append(out, Candidate{Rel: rel, Files: files, Newest: newest(files)})
	}
	for _, rel := range keys {
		d := dirs[rel]
		if claimed[rel] || len(d.audio) == 0 {
			continue
		}
		files := slices.Clone(d.audio)
		slices.SortFunc(files, NaturalCompare)
		hasMP3 := slices.ContainsFunc(files, func(f string) bool { return strings.EqualFold(filepath.Ext(f), ".mp3") })
		switch {
		case hasMP3 || len(files) == 1:
			if rel == "." {
				// Loose files in the library root: each is its own book.
				for _, f := range files {
					out = append(out, Candidate{Rel: f, Files: []string{f}, Newest: newest([]string{f})})
				}
				continue
			}
			out = append(out, Candidate{Rel: rel, Files: files, Newest: newest(files)})
		default:
			for _, f := range files {
				out = append(out, Candidate{Rel: f, Files: []string{f}, Newest: newest([]string{f})})
			}
		}
	}
	slices.SortFunc(out, func(a, b Candidate) int { return strings.Compare(a.Rel, b.Rel) })
	return out, nil
}

// Exists reports whether a library-relative path exists.
func Exists(root, rel string) bool {
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil
}

// NaturalCompare orders strings with embedded numbers numerically
// ("Chapter 2" < "Chapter 10"), case-insensitively.
func NaturalCompare(a, b string) int {
	a, b = strings.ToLower(a), strings.ToLower(b)
	for a != "" && b != "" {
		ra, rb := rune(a[0]), rune(b[0])
		if unicode.IsDigit(ra) && unicode.IsDigit(rb) {
			na, restA := leadingDigits(a)
			nb, restB := leadingDigits(b)
			na, nb = strings.TrimLeft(na, "0"), strings.TrimLeft(nb, "0")
			if len(na) != len(nb) {
				return len(na) - len(nb)
			}
			if c := strings.Compare(na, nb); c != 0 {
				return c
			}
			a, b = restA, restB
			continue
		}
		if ra != rb {
			return int(ra) - int(rb)
		}
		a, b = a[1:], b[1:]
	}
	return len(a) - len(b)
}

func leadingDigits(s string) (string, string) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[:i], s[i:]
}

var unsafeChars = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)

var reservedWin = map[string]bool{"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "lpt1": true, "lpt2": true, "lpt3": true}

// SafeName turns metadata into a safe single path component.
func SafeName(s, fallback string) string {
	s = unsafeChars.ReplaceAllString(strings.TrimSpace(s), "_")
	s = strings.Trim(s, " .")
	if r := []rune(s); len(r) > 100 {
		s = strings.TrimRight(string(r[:100]), " .")
	}
	if s == "" {
		s = fallback
	}
	if reservedWin[strings.ToLower(s)] {
		s = "_" + s
	}
	return s
}
