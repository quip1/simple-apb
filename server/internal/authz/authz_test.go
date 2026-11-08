// SPDX-License-Identifier: AGPL-3.0-or-later

package authz

import "testing"

func b(v bool) *bool { return &v }

func TestAllowed(t *testing.T) {
	defaults := DefaultCapabilities()
	active := func() Subject { return Subject{Status: "active", Role: "user", Defaults: defaults} }

	cases := []struct {
		name string
		subj Subject
		act  Action
		want bool
	}{
		// 1. locked-out statuses
		{"banned cannot read", Subject{Status: "banned", Role: "user"}, Read, false},
		{"banned can export", Subject{Status: "banned", Role: "user"}, Export, true},
		{"banned can delete account", Subject{Status: "banned", Role: "user"}, DeleteAccount, true},
		{"suspended can logout", Subject{Status: "suspended", Role: "user"}, Logout, true},
		{"suspended admin is still locked out", Subject{Status: "suspended", Role: "admin"}, Admin, false},
		{"pending cannot read", Subject{Status: "pending", Role: "user"}, Read, false},
		{"unknown status denied", Subject{Status: "weird", Role: "admin"}, Read, false},
		// 2. restricted
		{"restricted can read", Subject{Status: "restricted", Role: "user", Defaults: defaults}, Read, true},
		{"restricted cannot upload even if default allows", Subject{Status: "restricted", Role: "user", Defaults: defaults}, Upload, false},
		{"restricted cannot report", Subject{Status: "restricted", Role: "user"}, Report, false},
		{"restricted cannot review despite override", Subject{Status: "restricted", Role: "user", UserOverrides: map[Action]*bool{Review: b(true)}}, Review, false},
		// 3. admin
		{"admin admin action", Subject{Status: "active", Role: "admin"}, Admin, true},
		{"admin ignores restrictive tag", Subject{Status: "active", Role: "admin", TagOverrides: []map[string]bool{{"can_upload": false}}}, Upload, true},
		// 4/5. baseline
		{"user cannot admin", active(), Admin, false},
		{"user can read", active(), Read, true},
		{"user can report", active(), Report, true},
		{"unknown action denied", active(), Action("fly"), false},
		// 6. capabilities
		{"default allows upload", active(), Upload, true},
		{"default denies share", active(), Share, false},
		{"user override beats default", func() Subject { s := active(); s.UserOverrides = map[Action]*bool{Share: b(true)}; return s }(), Share, true},
		{"user override beats tag", func() Subject {
			s := active()
			s.UserOverrides = map[Action]*bool{Upload: b(true)}
			s.TagOverrides = []map[string]bool{{"can_upload": false}}
			return s
		}(), Upload, true},
		{"nil override falls through", func() Subject { s := active(); s.UserOverrides = map[Action]*bool{Upload: nil}; return s }(), Upload, true},
		{"tag beats default", func() Subject { s := active(); s.TagOverrides = []map[string]bool{{"can_share": true}}; return s }(), Share, true},
		{"most restrictive tag wins", func() Subject {
			s := active()
			s.TagOverrides = []map[string]bool{{"can_upload": true}, {"can_upload": false}}
			return s
		}(), Upload, false},
		{"unrelated tag key ignored", func() Subject { s := active(); s.TagOverrides = []map[string]bool{{"can_share": true}}; return s }(), Upload, true},
		{"missing default denies", Subject{Status: "active", Role: "user"}, Upload, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Allowed(tc.subj, tc.act); got != tc.want {
				t.Fatalf("Allowed(%s) = %v, want %v", tc.act, got, tc.want)
			}
		})
	}
}

func TestEffectiveCoversAllCapabilities(t *testing.T) {
	e := Effective(Subject{Status: "active", Role: "user", Defaults: DefaultCapabilities()})
	if len(e) != len(Capabilities) {
		t.Fatalf("got %d capabilities", len(e))
	}
	if !e[Upload] || e[PostPublic] {
		t.Fatalf("unexpected %v", e)
	}
}
