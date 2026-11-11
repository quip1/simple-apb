// SPDX-License-Identifier: AGPL-3.0-or-later

package authz

// ShareTarget is who a user wants to share one of their books with.
type ShareTarget int

const (
	// TargetUser is one other active user.
	TargetUser ShareTarget = iota
	// TargetMemberGroup is a group the sharer belongs to (not Public).
	TargetMemberGroup
	// TargetOtherGroup is a group the sharer does not belong to.
	TargetOtherGroup
	// TargetPublic is the built-in Public group.
	TargetPublic
)

// ShareOutcome is what happens to a share request.
type ShareOutcome int

const (
	// ShareDenied means the request is refused.
	ShareDenied ShareOutcome = iota
	// ShareDirect means the share takes effect at once.
	ShareDirect
	// ShareNeedsReview means it goes to the admins' review queue first.
	ShareNeedsReview
)

// BookShare decides a user's request to share a book (brief §6.5 rules 5–6):
//   - admins share anything directly (the admin distribution path);
//   - others may only share books they uploaded;
//   - people and the sharer's own groups need can_share;
//   - Public needs can_post_public and is always reviewed first;
//   - groups the sharer isn't in are refused.
func BookShare(s Subject, uploader bool, t ShareTarget) ShareOutcome {
	if !Allowed(s, Read) {
		return ShareDenied
	}
	if Allowed(s, Admin) {
		return ShareDirect
	}
	if !uploader {
		return ShareDenied
	}
	switch t {
	case TargetUser, TargetMemberGroup:
		if Allowed(s, Share) {
			return ShareDirect
		}
	case TargetPublic:
		if Allowed(s, PostPublic) {
			return ShareNeedsReview
		}
	}
	return ShareDenied
}

// CollectionShare decides whether a user may share a collection with a
// target. Collections only ever show recipients books they can already see,
// so any active user may share with people and their own groups (Public
// included); groups they aren't in are refused.
func CollectionShare(s Subject, t ShareTarget) bool {
	if !Allowed(s, ShareCollection) {
		return false
	}
	if Allowed(s, Admin) {
		return true
	}
	return t != TargetOtherGroup
}

// EditBookMetadata decides who may edit a book's metadata (brief §6.12):
// admins anywhere, users with can_edit_metadata only on their own uploads.
func EditBookMetadata(s Subject, uploader bool) bool {
	return Allowed(s, Admin) || (uploader && Allowed(s, EditMetadata))
}

// SeeHiddenReviews reports whether the subject sees reviews a moderator
// hid (authors always see their own).
func SeeHiddenReviews(s Subject, author bool) bool {
	return author || Allowed(s, Admin)
}

// UseBookTools decides who may use the book tools (brief §6.13: chapter
// editor, merge to m4b, cover picker): admins on any book, uploaders on their
// own uploads when they may edit metadata.
func UseBookTools(s Subject, uploader bool) bool {
	return EditBookMetadata(s, uploader)
}

// MergeAsNewBook decides whether a merge may keep the original and add the
// m4b as a new book, which is an upload too.
func MergeAsNewBook(s Subject, uploader bool) bool {
	return UseBookTools(s, uploader) && Allowed(s, Upload)
}
