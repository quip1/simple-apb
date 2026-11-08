// SPDX-License-Identifier: AGPL-3.0-or-later

package fsutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFileAtomicReplaces(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "f.json")
	if err := WriteFileAtomic(p, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(p, []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "two" {
		t.Fatalf("got %q", b)
	}
	assertNoTemps(t, filepath.Dir(p))
}

func TestAbortLeavesTargetUntouched(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	if err := WriteFileAtomic(p, []byte("orig"), 0o600); err != nil {
		t.Fatal(err)
	}
	w, err := NewAtomicWriter(p, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("partial"))
	w.Abort()
	b, _ := os.ReadFile(p)
	if string(b) != "orig" {
		t.Fatalf("target changed: %q", b)
	}
	assertNoTemps(t, dir)
	if err := w.Commit(); err == nil {
		t.Fatal("commit after abort should fail")
	}
}

func assertNoTemps(t *testing.T, dir string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("leftover temp file %s", e.Name())
		}
	}
}
