// SPDX-License-Identifier: AGPL-3.0-or-later

// Package fsutil holds crash-safe filesystem helpers. Every file the server
// writes goes through WriteFileAtomic or AtomicWriter: write to a temp file in
// the same directory, fsync it, rename over the target, then fsync the
// directory so the rename itself survives a power cut.
package fsutil

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// WriteFileAtomic writes data to path crash-safely.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	w, err := NewAtomicWriter(path, perm)
	if err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		w.Abort()
		return err
	}
	return w.Commit()
}

// AtomicWriter streams into a temp file and only replaces the target on Commit.
// Abort (or a crash) leaves the target untouched.
type AtomicWriter struct {
	f      *os.File
	target string
	done   bool
}

// NewAtomicWriter creates the temp file next to path.
func NewAtomicWriter(path string, perm os.FileMode) (*AtomicWriter, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create dir %s: %w", dir, err)
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	if err := f.Chmod(perm); err != nil && runtime.GOOS != "windows" {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return nil, fmt.Errorf("chmod temp file: %w", err)
	}
	return &AtomicWriter{f: f, target: path}, nil
}

func (w *AtomicWriter) Write(p []byte) (int, error) { return w.f.Write(p) }

// ReadFrom lets io.Copy stream efficiently.
func (w *AtomicWriter) ReadFrom(r io.Reader) (int64, error) { return io.Copy(w.f, r) }

// Commit fsyncs, renames over the target and fsyncs the directory.
func (w *AtomicWriter) Commit() error {
	if w.done {
		return errors.New("atomic writer already finished")
	}
	w.done = true
	tmp := w.f.Name()
	if err := w.f.Sync(); err != nil {
		_ = w.f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("fsync %s: %w", tmp, err)
	}
	if err := w.f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, w.target); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename to %s: %w", w.target, err)
	}
	return SyncDir(filepath.Dir(w.target))
}

// Abort discards the temp file.
func (w *AtomicWriter) Abort() {
	if w.done {
		return
	}
	w.done = true
	_ = w.f.Close()
	_ = os.Remove(w.f.Name())
}

// SyncDir fsyncs a directory so renames/creates inside it are durable.
// Windows cannot fsync directories (and NTFS journals metadata), so it's a no-op there.
func SyncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open dir %s: %w", dir, err)
	}
	defer func() { _ = d.Close() }()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("fsync dir %s: %w", dir, err)
	}
	return nil
}

// RenameDurable renames old to new (same filesystem) and fsyncs both parent dirs.
func RenameDurable(oldPath, newPath string) error {
	if err := os.MkdirAll(filepath.Dir(newPath), 0o750); err != nil {
		return err
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		return fmt.Errorf("rename %s → %s: %w", oldPath, newPath, err)
	}
	if err := SyncDir(filepath.Dir(newPath)); err != nil {
		return err
	}
	if filepath.Dir(oldPath) != filepath.Dir(newPath) {
		return SyncDir(filepath.Dir(oldPath))
	}
	return nil
}
