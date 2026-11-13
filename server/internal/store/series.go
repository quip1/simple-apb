// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"regexp"
	"strconv"
	"strings"
)

var seriesNumber = regexp.MustCompile(`\d+(?:[.,]\d+)?`)

// SeriesOrder is the number a series index sorts by: "3" → 3, "Book 10" →
// 10, "2.5" or "2,5" → 2.5. Nil when there's no number ("Prequel").
func SeriesOrder(index string) *float64 {
	m := seriesNumber.FindString(index)
	if m == "" {
		return nil
	}
	v, err := strconv.ParseFloat(strings.Replace(m, ",", ".", 1), 64)
	if err != nil {
		return nil
	}
	return &v
}

func seriesOrderArg(index string) any {
	if v := SeriesOrder(index); v != nil {
		return *v
	}
	return nil
}

// BackfillSeriesOrder fills series_order for books that have a series index
// but no order yet (books from before the column, or edited by hand in the
// database). It returns how many it set.
func BackfillSeriesOrder(ctx context.Context, db *DB) (int, error) {
	rows, err := db.Query(ctx, `SELECT id, series_index FROM books WHERE series_index IS NOT NULL AND series_index <> '' AND series_order IS NULL`)
	if err != nil {
		return 0, err
	}
	type row struct{ id, index string }
	var todo []row
	for rows.Next() {
		var r row
		var idx sql.NullString
		if err := rows.Scan(&r.id, &idx); err != nil {
			_ = rows.Close()
			return 0, err
		}
		r.index = idx.String
		todo = append(todo, r)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, r := range todo {
		if v := SeriesOrder(r.index); v != nil {
			if _, err := db.Exec(ctx, `UPDATE books SET series_order = ? WHERE id = ?`, *v, r.id); err != nil {
				return n, err
			}
			n++
		}
	}
	return n, nil
}
