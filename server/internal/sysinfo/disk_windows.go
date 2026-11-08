// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package sysinfo

import "golang.org/x/sys/windows"

func diskUsage(path string) (Disk, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return Disk{}, err
	}
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, &total, &totalFree); err != nil {
		return Disk{}, err
	}
	return Disk{Free: free, Total: total}, nil
}
