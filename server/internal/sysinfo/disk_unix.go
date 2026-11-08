// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !windows

package sysinfo

import "golang.org/x/sys/unix"

func diskUsage(path string) (Disk, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return Disk{}, err
	}
	bs := uint64(st.Bsize) // int64 on Linux, uint32 on some BSDs
	return Disk{Free: st.Bavail * bs, Total: st.Blocks * bs}, nil
}
