// SPDX-License-Identifier: AGPL-3.0-or-later

// Package sysinfo reads host health for the admin dashboard (brief §6.23):
// disk space per volume, memory and load. Values a platform can't provide
// are reported as unknown (zero / false), never guessed.
package sysinfo

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// Disk is free/total space of the filesystem holding a path.
type Disk struct {
	Free, Total uint64
}

// DiskUsage returns the space of the filesystem that holds path.
func DiskUsage(path string) (Disk, error) { return diskUsage(path) }

// Memory is host memory (Linux /proc/meminfo).
type Memory struct {
	Total, Available uint64
	OK               bool
}

// HostMemory reads /proc/meminfo where it exists.
func HostMemory() Memory {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return Memory{}
	}
	defer func() { _ = f.Close() }()
	var m Memory
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			m.Total = kb * 1024
		case "MemAvailable:":
			m.Available = kb * 1024
		}
	}
	m.OK = m.Total > 0
	return m
}

// LoadAverage returns the 1-minute load average where /proc/loadavg exists.
func LoadAverage() (float64, bool) {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	return v, err == nil
}

// ProcessMemory is the memory the Go runtime holds from the OS.
func ProcessMemory() uint64 {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.Sys
}
