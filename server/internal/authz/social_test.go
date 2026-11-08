// SPDX-License-Identifier: AGPL-3.0-or-later

package authz

import "testing"

func TestBookShare(t *testing.T) {
	user := func(over map[Action]*bool) Subject {
		return Subject{Status: "active", Role: "user", Defaults: DefaultCapabilities(), UserOverrides: over}
	}
	sharer := user(map[Action]*bool{Share: b(true)})
	poster := user(map[Action]*bool{PostPublic: b(true)})
	both := user(map[Action]*bool{Share: b(true), PostPublic: b(true)})
	admin := Subject{Status: "active", Role: "admin"}
	restricted := Subject{Status: "restricted", Role: "user", UserOverrides: map[Action]*bool{Share: b(true), PostPublic: b(true)}}

	cases := []struct {
		name     string
		subj     Subject
		uploader bool
		target   ShareTarget
		want     ShareOutcome
	}{
		{"default user cannot share", user(nil), true, TargetUser, ShareDenied},
		{"sharer to a person", sharer, true, TargetUser, ShareDirect},
		{"sharer to own group", sharer, true, TargetMemberGroup, ShareDirect},
		{"sharer to a group they're not in", sharer, true, TargetOtherGroup, ShareDenied},
		{"sharer without post_public cannot go public", sharer, true, TargetPublic, ShareDenied},
		{"poster goes public through review", poster, true, TargetPublic, ShareNeedsReview},
		{"poster without can_share cannot share privately", poster, true, TargetUser, ShareDenied},
		{"both: public still reviewed", both, true, TargetPublic, ShareNeedsReview},
		{"not the uploader", both, false, TargetUser, ShareDenied},
		{"not the uploader, public", both, false, TargetPublic, ShareDenied},
		{"admin anything directly", admin, false, TargetPublic, ShareDirect},
		{"admin other group", admin, false, TargetOtherGroup, ShareDirect},
		{"restricted is read-only", restricted, true, TargetUser, ShareDenied},
		{"banned", Subject{Status: "banned", Role: "admin"}, true, TargetUser, ShareDenied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := BookShare(tc.subj, tc.uploader, tc.target); got != tc.want {
				t.Fatalf("BookShare = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCollectionShareAndSocial(t *testing.T) {
	active := Subject{Status: "active", Role: "user", Defaults: DefaultCapabilities()}
	restricted := Subject{Status: "restricted", Role: "user"}
	admin := Subject{Status: "active", Role: "admin"}
	cases := []struct {
		name string
		got  bool
		want bool
	}{
		{"user to person", CollectionShare(active, TargetUser), true},
		{"user to own group", CollectionShare(active, TargetMemberGroup), true},
		{"user to Public", CollectionShare(active, TargetPublic), true},
		{"user to other group", CollectionShare(active, TargetOtherGroup), false},
		{"admin to other group", CollectionShare(admin, TargetOtherGroup), true},
		{"restricted cannot share collections", CollectionShare(restricted, TargetUser), false},
		{"active can recommend", Allowed(active, Recommend), true},
		{"restricted cannot recommend", Allowed(restricted, Recommend), false},
		{"author sees own hidden review", SeeHiddenReviews(active, true), true},
		{"others don't", SeeHiddenReviews(active, false), false},
		{"admin does", SeeHiddenReviews(admin, false), true},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, tc.got, tc.want)
		}
	}
}
