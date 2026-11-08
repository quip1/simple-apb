// SPDX-License-Identifier: AGPL-3.0-or-later

package sysinfo

import "testing"

func TestDiskUsage(t *testing.T) {
	d, err := DiskUsage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if d.Total == 0 || d.Free > d.Total {
		t.Fatalf("implausible disk usage %+v", d)
	}
	if ProcessMemory() == 0 {
		t.Fatal("process memory is zero")
	}
}
