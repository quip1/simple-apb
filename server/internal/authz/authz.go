// SPDX-License-Identifier: AGPL-3.0-or-later

// Package authz makes every permission decision in the server. Handlers ask
// Allowed(subject, action); they never inspect roles or statuses themselves.
//
// Evaluation order (brief §6.7; the first matching rule wins):
//  1. banned / suspended / pending / deleted → only export, delete account, logout
//  2. restricted → read-only (plus export, delete account, logout)
//  3. admin → everything
//  4. admin-only actions → denied for everyone else
//  5. baseline actions (read, report, export…) → allowed for active users
//  6. capability actions → explicit per-user override, else user-tag overrides
//     (most restrictive tag wins), else instance defaults
package authz

// Action is something a user may try to do.
type Action string

const (
	Read          Action = "read"
	Report        Action = "report"
	Export        Action = "export"
	DeleteAccount Action = "delete_account"
	Logout        Action = "logout"
	Admin         Action = "admin"

	// Capability actions (configurable per user / tag / instance).
	Upload       Action = "can_upload"
	EditMetadata Action = "can_edit_metadata"
	DeleteOwn    Action = "can_delete_own"
	Share        Action = "can_share"
	Review       Action = "can_review"
	PostPublic   Action = "can_post_public"
)

// Capabilities lists the configurable actions, in a stable order.
var Capabilities = []Action{Upload, EditMetadata, DeleteOwn, Share, Review, PostPublic}

// Defaults are the instance-wide capability defaults.
type Defaults map[Action]bool

// DefaultCapabilities are used until an admin changes them (docs/DECISIONS.md ADR-0011).
func DefaultCapabilities() Defaults {
	return Defaults{
		Upload:       true,
		EditMetadata: true, // only ever applies to the user's own uploads
		DeleteOwn:    true,
		Share:        false,
		Review:       true,
		PostPublic:   false,
	}
}

// Subject is everything authz needs to know about the acting user.
type Subject struct {
	Status string // pending|active|restricted|suspended|banned|deleted
	Role   string // admin|user
	// UserOverrides are explicit per-user values (nil entry or missing = unset).
	UserOverrides map[Action]*bool
	// TagOverrides come from each assigned user tag.
	TagOverrides []map[string]bool
	Defaults     Defaults
}

// Allowed reports whether the subject may perform the action.
func Allowed(s Subject, a Action) bool {
	switch s.Status {
	case "active":
	case "restricted":
		return a == Read || a == Export || a == DeleteAccount || a == Logout
	default: // pending, suspended, banned, deleted, unknown → locked out
		return a == Export || a == DeleteAccount || a == Logout
	}
	if s.Role == "admin" {
		return true
	}
	switch a {
	case Admin:
		return false
	case Read, Report, Export, DeleteAccount, Logout:
		return true
	}
	if !isCapability(a) {
		return false // unknown actions are denied
	}
	if v, ok := s.UserOverrides[a]; ok && v != nil {
		return *v
	}
	tagSet, tagAllow := false, true
	for _, t := range s.TagOverrides {
		if v, ok := t[string(a)]; ok {
			tagSet = true
			tagAllow = tagAllow && v // most restrictive wins
		}
	}
	if tagSet {
		return tagAllow
	}
	return s.Defaults[a]
}

// Effective returns the resolved value of every capability.
func Effective(s Subject) map[Action]bool {
	out := make(map[Action]bool, len(Capabilities))
	for _, c := range Capabilities {
		out[c] = Allowed(s, c)
	}
	return out
}

func isCapability(a Action) bool {
	for _, c := range Capabilities {
		if c == a {
			return true
		}
	}
	return false
}
