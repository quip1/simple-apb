// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watch observes the library with fsnotify and calls trigger (debounced) when
// something changes. inotify misses events on some mounts (NFS, some FUSE),
// so the caller also schedules a periodic full rescan as a fallback.
func (l *Library) Watch(ctx context.Context, trigger func()) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	addTree := func(root string) {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return nil
			}
			if p != l.Root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			if err := w.Add(p); err != nil {
				l.Log.Debug("watch add failed", "path", p, "err", err)
			}
			return nil
		})
	}
	addTree(l.Root)
	go func() {
		defer func() { _ = w.Close() }()
		var timer *time.Timer
		fire := make(chan struct{}, 1)
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				rel, _ := filepath.Rel(l.Root, ev.Name)
				if strings.HasPrefix(filepath.ToSlash(rel), ".") {
					continue // staging and hidden files
				}
				if ev.Has(fsnotify.Create) {
					if st, err := os.Stat(ev.Name); err == nil && st.IsDir() {
						addTree(ev.Name)
					}
				}
				// Debounce: wait until the folder has settled before scanning.
				if timer != nil {
					timer.Stop()
				}
				timer = time.AfterFunc(SettleTime+5*time.Second, func() {
					select {
					case fire <- struct{}{}:
					default:
					}
				})
			case <-fire:
				trigger()
			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				l.Log.Warn("library watcher", "err", err)
			}
		}
	}()
	return nil
}
