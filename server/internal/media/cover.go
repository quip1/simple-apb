// SPDX-License-Identifier: AGPL-3.0-or-later

package media

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/quip1/simple-apb/server/internal/fsutil"
)

// CoverSizes are the resized variants clients may request (brief §6.12).
var CoverSizes = []int{128, 256, 512, 1024}

// ResizeCover writes a JPEG no larger than size×size (aspect kept, never
// upscaled) to dest, crash-safely.
func (t Tools) ResizeCover(ctx context.Context, src, dest string, size int) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return err
	}
	tmp := dest + ".tmp.jpg"
	defer func() { _ = os.Remove(tmp) }()
	filter := fmt.Sprintf("scale='min(%d,iw)':'min(%d,ih)':force_original_aspect_ratio=decrease", size, size)
	cmd := exec.CommandContext(ctx, t.FFmpeg, "-v", "error", "-y", "-i", src, "-frames:v", "1", "-vf", filter,
		"-c:v", "mjpeg", "-q:v", "3", "-f", "image2", tmp)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("resize cover: %v: %s", err, firstLine(stderr.String()))
	}
	data, err := os.ReadFile(tmp)
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(dest, data, 0o640)
}
