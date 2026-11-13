// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"strings"
	"testing"
)

func TestSeriesOrder(t *testing.T) {
	for in, want := range map[string]float64{"3": 3, "10": 10, "Book 10": 10, "2.5": 2.5, "2,5": 2.5, " 07 ": 7} {
		if got := SeriesOrder(in); got == nil || *got != want {
			t.Errorf("SeriesOrder(%q) = %v, want %v", in, got, want)
		}
	}
	for _, in := range []string{"", "Prequel", "Novella"} {
		if got := SeriesOrder(in); got != nil {
			t.Errorf("SeriesOrder(%q) = %v, want nil", in, *got)
		}
	}
}

func TestSeriesSortsByNumber(t *testing.T) {
	forEachDialect(t, func(t *testing.T, db *DB) {
		ctx := context.Background()
		u, _ := CreateUser(ctx, db, NewUser{Username: "reader", Status: "active", Role: "user"})
		add := func(title, author, series, index string) {
			t.Helper()
			id, err := InsertBook(ctx, db, &NewBook{Meta: BookMeta{Title: title, Authors: []string{author}, Series: series, SeriesIndex: index},
				FolderPath: "f/" + title, Origin: "upload", Status: "ready", Checksum: "c-" + title})
			if err != nil {
				t.Fatal(err)
			}
			if err := AddEntry(ctx, db, u, id, "own_upload"); err != nil {
				t.Fatal(err)
			}
		}
		add("Carl's Doomsday Scenario", "Matt Dinniman", "Dungeon Crawler Carl", "2")
		add("The Eye of the Bedlam Bride", "Matt Dinniman", "Dungeon Crawler Carl", "10")
		add("Dungeon Crawler Carl", "Matt Dinniman", "Dungeon Crawler Carl", "1")
		add("The Dungeon Anarchist's Cookbook", "Matt Dinniman", "Dungeon Crawler Carl", "Book 3")
		add("A Side Story", "Matt Dinniman", "Dungeon Crawler Carl", "Novella")
		add("Kaiju: Battlefield Surgeon", "Matt Dinniman", "", "")
		want := []string{"Dungeon Crawler Carl", "Carl's Doomsday Scenario", "The Dungeon Anarchist's Cookbook",
			"The Eye of the Bedlam Bride", "A Side Story", "Kaiju: Battlefield Surgeon"}
		for _, sort := range []string{"series", "author"} {
			if got := titles(t, db, u, LibraryFilter{Sort: sort, Limit: 50}); strings.Join(got, "|") != strings.Join(want, "|") {
				t.Errorf("sort %s:\n got %v\nwant %v", sort, got, want)
			}
		}
	})
}
