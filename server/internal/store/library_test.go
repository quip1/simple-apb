// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func titles(t *testing.T, db *DB, user string, f LibraryFilter) []string {
	t.Helper()
	items, err := ListLibrary(context.Background(), db, user, f)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, it := range items {
		out = append(out, it.Book.Title)
	}
	return out
}

func TestVisibilityRules(t *testing.T) {
	forEachDialect(t, func(t *testing.T, db *DB) {
		ctx := context.Background()
		mk := func(title, uploader string, size int64, genres ...string) string {
			id, err := InsertBook(ctx, db, &NewBook{Meta: BookMeta{Title: title, Authors: []string{"Auth " + title}, Genres: genres},
				FolderPath: "f/" + title, Origin: "scan", Status: "ready", UploaderID: uploader, SizeBytes: size, Checksum: "c-" + title})
			if err != nil {
				t.Fatal(err)
			}
			return id
		}
		u1, _ := CreateUser(ctx, db, NewUser{Username: "u1", Status: "active", Role: "user"})
		u2, _ := CreateUser(ctx, db, NewUser{Username: "u2", Status: "active", Role: "user"})
		friends, _ := CreateGroup(ctx, db, "Friends", true)
		_ = AddGroupMember(ctx, db, friends, u1)

		a := mk("Alpha", "", 100)             // distributed to Friends (u1)
		b := mk("Bravo", "", 200)             // distributed directly to u2
		c := mk("Charlie", u1, 300, "Mature") // u1's own upload
		d := mk("Delta", "", 400)             // undistributed: master store only
		_ = d
		if _, err := CreateDistribution(ctx, db, a, "group", friends, "", "for friends"); err != nil {
			t.Fatal(err)
		}
		if again, _ := CreateDistribution(ctx, db, a, "group", friends, "", ""); again == "" {
			t.Fatal("re-distribution should return the existing id")
		}
		distB, _ := CreateDistribution(ctx, db, b, "user", u2, "", "")
		_ = AddEntry(ctx, db, u1, c, "own_upload")

		all := LibraryFilter{Sort: "title"}
		if got := titles(t, db, u1, all); !slices.Equal(got, []string{"Alpha", "Charlie"}) {
			t.Fatalf("u1 sees %v", got)
		}
		if got := titles(t, db, u2, all); !slices.Equal(got, []string{"Bravo"}) {
			t.Fatalf("u2 sees %v", got)
		}

		// Remove (distributed book: creates an entry) → recently removed → restore.
		if err := SetEntryFlag(ctx, db, u1, a, "removed_at", true); err != nil {
			t.Fatal(err)
		}
		if got := titles(t, db, u1, all); !slices.Equal(got, []string{"Charlie"}) {
			t.Fatalf("after remove: %v", got)
		}
		if got := titles(t, db, u1, LibraryFilter{Shelf: "recently_removed"}); !slices.Equal(got, []string{"Alpha"}) {
			t.Fatalf("recently removed: %v", got)
		}
		if err := RestoreEntry(ctx, db, u1, a); err != nil {
			t.Fatal(err)
		}
		// Hide → archived shelf.
		_ = SetEntryFlag(ctx, db, u1, c, "hidden_at", true)
		if got := titles(t, db, u1, LibraryFilter{Shelf: "archived"}); !slices.Equal(got, []string{"Charlie"}) {
			t.Fatalf("archived: %v", got)
		}
		_ = RestoreEntry(ctx, db, u1, c)

		// Search by author name.
		if got := titles(t, db, u1, LibraryFilter{Query: "auth charl"}); !slices.Equal(got, []string{"Charlie"}) {
			t.Fatalf("search: %v", got)
		}
		if got := titles(t, db, u1, LibraryFilter{Query: "50%_off"}); len(got) != 0 {
			t.Fatalf("LIKE wildcards must be escaped: %v", got)
		}

		// Content filter hides tagged books everywhere for that user.
		var mature string
		_ = db.QueryRow(ctx, `SELECT id FROM book_tags WHERE lower(name) = 'mature'`).Scan(&mature)
		now := NowMS()
		_, _ = db.Exec(ctx, `INSERT INTO content_filters (id, user_id, book_tag_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, NewID(), u1, mature, now, now)
		if got := titles(t, db, u1, all); !slices.Equal(got, []string{"Alpha"}) {
			t.Fatalf("content filter: %v", got)
		}
		if _, err := VisibleBook(ctx, db, u1, c); !errors.Is(err, ErrNotFound) {
			t.Fatal("filtered book must not be visible by id either")
		}
		_, _ = db.Exec(ctx, `DELETE FROM content_filters`)

		// Takedown and trash hide immediately.
		_, _ = db.Exec(ctx, `INSERT INTO takedowns (id, book_id, reason, taken_at, created_at, updated_at) VALUES (?, ?, 'dmca', ?, ?, ?)`, NewID(), a, now, now, now)
		if got := titles(t, db, u1, all); !slices.Equal(got, []string{"Charlie"}) {
			t.Fatalf("takedown: %v", got)
		}
		_ = SetBookStatus(ctx, db, c, "trashed", "")
		if got := titles(t, db, u1, all); len(got) != 0 {
			t.Fatalf("trash: %v", got)
		}

		// Revoking a distribution removes it.
		if _, err := RevokeDistribution(ctx, db, distB); err != nil {
			t.Fatal(err)
		}
		if got := titles(t, db, u2, all); len(got) != 0 {
			t.Fatalf("after revoke: %v", got)
		}

		// Quota: own uploads not removed, not trashed.
		_ = SetBookStatus(ctx, db, c, "ready", "")
		if used, _ := UsedBytes(ctx, db, u1); used != 300 {
			t.Fatalf("used %d", used)
		}
		_ = SetEntryFlag(ctx, db, u1, c, "removed_at", true)
		if used, _ := UsedBytes(ctx, db, u1); used != 0 {
			t.Fatalf("removed upload still counted: %d", used)
		}
		// 30-day purge of removed entries.
		if n, _ := PurgeRemovedEntries(ctx, db, NowMS()+1); n != 1 {
			t.Fatalf("purged %d", n)
		}
	})
}
