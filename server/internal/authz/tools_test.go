// SPDX-License-Identifier: AGPL-3.0-or-later

package authz

import "testing"

func TestBookTools(t *testing.T) {
	no := false
	admin := Subject{Status: "active", Role: "admin"}
	user := Subject{Status: "active", Role: "user", Defaults: DefaultCapabilities()}
	noUpload := Subject{Status: "active", Role: "user", Defaults: DefaultCapabilities(), UserOverrides: map[Action]*bool{Upload: &no}}
	noEdit := Subject{Status: "active", Role: "user", Defaults: DefaultCapabilities(), UserOverrides: map[Action]*bool{EditMetadata: &no}}
	restricted := Subject{Status: "restricted", Role: "user", Defaults: DefaultCapabilities()}
	cases := []struct {
		name             string
		s                Subject
		uploader         bool
		tools, asNewBook bool
	}{
		{"admin, any book", admin, false, true, true},
		{"uploader", user, true, true, true},
		{"someone else's book", user, false, false, false},
		{"uploader without upload", noUpload, true, true, false},
		{"uploader without metadata editing", noEdit, true, false, false},
		{"restricted uploader", restricted, true, false, false},
	}
	for _, c := range cases {
		if got := UseBookTools(c.s, c.uploader); got != c.tools {
			t.Errorf("%s: UseBookTools = %v, want %v", c.name, got, c.tools)
		}
		if got := MergeAsNewBook(c.s, c.uploader); got != c.asNewBook {
			t.Errorf("%s: MergeAsNewBook = %v, want %v", c.name, got, c.asNewBook)
		}
	}
}
