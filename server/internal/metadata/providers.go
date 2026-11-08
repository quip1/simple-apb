// SPDX-License-Identifier: AGPL-3.0-or-later

package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const maxJSON = 4 << 20

func decodeLimited(res *http.Response, dest any) error {
	return json.NewDecoder(io.LimitReader(res.Body, maxJSON)).Decode(dest)
}

var yearRe = regexp.MustCompile(`\b(1[5-9]\d\d|20\d\d)\b`)

func yearOf(s string) int {
	if m := yearRe.FindString(s); m != "" {
		y, _ := strconv.Atoi(m)
		return y
	}
	return 0
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

// plainText turns provider HTML (Audnexus summaries) into text.
func plainText(s string) string {
	s = strings.NewReplacer("<br>", "\n", "<br/>", "\n", "<br />", "\n", "</p>", "\n\n").Replace(s)
	s = html.UnescapeString(tagRe.ReplaceAllString(s, ""))
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	return strings.TrimSpace(regexp.MustCompile(`\n{3,}`).ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}

// ── Open Library (https://openlibrary.org/dev/docs/api/search) ──

type openLibrary struct {
	base, covers string
	hc           *http.Client
	limit        *limiter
}

func (p *openLibrary) Name() string { return "openlibrary" }

func (p *openLibrary) Lookup(ctx context.Context, q Query) ([]Record, error) {
	v := url.Values{"limit": {"5"}, "fields": {"key,title,subtitle,author_name,first_publish_year,isbn,publisher,language,cover_i,subject"}}
	switch {
	case q.ISBN != "":
		v.Set("isbn", q.ISBN)
	case q.Title != "":
		v.Set("title", q.Title)
		if q.Author != "" {
			v.Set("author", q.Author)
		}
	default:
		return nil, errNotApplicable
	}
	var res struct {
		Docs []struct {
			Title     string   `json:"title"`
			Subtitle  string   `json:"subtitle"`
			Authors   []string `json:"author_name"`
			Year      int      `json:"first_publish_year"`
			ISBN      []string `json:"isbn"`
			Publisher []string `json:"publisher"`
			Language  []string `json:"language"`
			CoverID   int64    `json:"cover_i"`
			Subjects  []string `json:"subject"`
		} `json:"docs"`
	}
	if _, err := getJSON(ctx, p.hc, p.limit, p.base+"/search.json?"+v.Encode(), &res); err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(res.Docs))
	for _, d := range res.Docs {
		// A search result is a work: its ISBN and publisher lists span every
		// edition, so they are only used when we searched by one ISBN.
		r := Record{Title: d.Title, Subtitle: d.Subtitle, Authors: trimAll(d.Authors), PublishedYear: d.Year}
		if q.ISBN != "" {
			r.ISBN = NormalizeISBN(q.ISBN)
			if len(d.Publisher) == 1 {
				r.Publisher = d.Publisher[0]
			}
		}
		if len(d.Language) == 1 {
			r.Language = lang3to2(d.Language[0])
		}
		if d.CoverID > 0 {
			r.CoverURL = fmt.Sprintf("%s/b/id/%d-L.jpg", p.covers, d.CoverID)
		}
		if len(d.Subjects) > 3 {
			d.Subjects = d.Subjects[:3]
		}
		r.Genres = trimAll(d.Subjects)
		out = append(out, r)
	}
	return out, nil
}

// lang3to2 maps the common MARC codes Open Library uses to ISO 639-1.
func lang3to2(s string) string {
	m := map[string]string{"eng": "en", "ger": "de", "deu": "de", "fre": "fr", "fra": "fr", "spa": "es", "ita": "it",
		"dut": "nl", "nld": "nl", "swe": "sv", "nor": "no", "dan": "da", "fin": "fi", "por": "pt", "pol": "pl", "rus": "ru", "jpn": "ja"}
	if v, ok := m[strings.ToLower(s)]; ok {
		return v
	}
	return ""
}

// ── Google Books (https://developers.google.com/books/docs/v1/using) ──

type googleBooks struct {
	base, key string
	hc        *http.Client
	limit     *limiter
}

func (p *googleBooks) Name() string { return "googlebooks" }

func (p *googleBooks) Lookup(ctx context.Context, q Query) ([]Record, error) {
	var terms string
	switch {
	case q.ISBN != "":
		terms = "isbn:" + q.ISBN
	case q.Title != "":
		terms = "intitle:" + q.Title
		if q.Author != "" {
			terms += " inauthor:" + q.Author
		}
	default:
		return nil, errNotApplicable
	}
	v := url.Values{"q": {terms}, "maxResults": {"5"}, "printType": {"books"}}
	if p.key != "" {
		v.Set("key", p.key)
	}
	var res struct {
		Items []struct {
			Info struct {
				Title       string   `json:"title"`
				Subtitle    string   `json:"subtitle"`
				Authors     []string `json:"authors"`
				Publisher   string   `json:"publisher"`
				Published   string   `json:"publishedDate"`
				Description string   `json:"description"`
				Categories  []string `json:"categories"`
				Language    string   `json:"language"`
				IDs         []struct {
					Type string `json:"type"`
					ID   string `json:"identifier"`
				} `json:"industryIdentifiers"`
				Images map[string]string `json:"imageLinks"`
			} `json:"volumeInfo"`
		} `json:"items"`
	}
	if _, err := getJSON(ctx, p.hc, p.limit, p.base+"/volumes?"+v.Encode(), &res); err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(res.Items))
	for _, it := range res.Items {
		in := it.Info
		r := Record{Title: in.Title, Subtitle: in.Subtitle, Authors: trimAll(in.Authors), Publisher: in.Publisher,
			PublishedYear: yearOf(in.Published), Description: plainText(in.Description), Genres: trimAll(in.Categories), Language: in.Language}
		for _, id := range in.IDs {
			if id.Type == "ISBN_13" || (id.Type == "ISBN_10" && r.ISBN == "") {
				r.ISBN = NormalizeISBN(id.ID)
			}
		}
		for _, k := range []string{"extraLarge", "large", "medium", "thumbnail"} {
			if u := in.Images[k]; u != "" {
				r.CoverURL = strings.Replace(strings.Replace(u, "http://", "https://", 1), "&edge=curl", "", 1)
				break
			}
		}
		out = append(out, r)
	}
	return out, nil
}

// ── Audnexus (https://audnex.us, community Audible metadata; ASIN only) ──

type audnexus struct {
	base  string
	hc    *http.Client
	limit *limiter
}

func (p *audnexus) Name() string { return "audnexus" }

func (p *audnexus) Lookup(ctx context.Context, q Query) ([]Record, error) {
	asin := strings.ToUpper(strings.TrimSpace(q.ASIN))
	if !asinRe.MatchString(asin) {
		return nil, errNotApplicable
	}
	var b struct {
		ASIN      string                  `json:"asin"`
		Title     string                  `json:"title"`
		Subtitle  string                  `json:"subtitle"`
		Authors   []struct{ Name string } `json:"authors"`
		Narrators []struct{ Name string } `json:"narrators"`
		Publisher string                  `json:"publisherName"`
		Summary   string                  `json:"summary"`
		Release   string                  `json:"releaseDate"`
		Language  string                  `json:"language"`
		Image     string                  `json:"image"`
		ISBN      string                  `json:"isbn"`
		Genres    []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"genres"`
		Series *struct {
			Name     string `json:"name"`
			Position string `json:"position"`
		} `json:"seriesPrimary"`
	}
	found, err := getJSON(ctx, p.hc, p.limit, p.base+"/books/"+url.PathEscape(asin), &b)
	if err != nil || !found {
		return nil, err
	}
	r := Record{Title: b.Title, Subtitle: b.Subtitle, Publisher: b.Publisher, Description: plainText(b.Summary),
		PublishedYear: yearOf(b.Release), ASIN: asin, ISBN: NormalizeISBN(b.ISBN), CoverURL: b.Image, Language: languageName(b.Language)}
	for _, a := range b.Authors {
		r.Authors = append(r.Authors, a.Name)
	}
	for _, n := range b.Narrators {
		r.Narrators = append(r.Narrators, n.Name)
	}
	for _, g := range b.Genres {
		if g.Type == "genre" {
			r.Genres = append(r.Genres, g.Name)
		}
	}
	r.Authors, r.Narrators, r.Genres = trimAll(r.Authors), trimAll(r.Narrators), trimAll(r.Genres)
	if b.Series != nil {
		r.Series, r.SeriesIndex = b.Series.Name, b.Series.Position
	}
	return []Record{r}, nil
}

var asinRe = regexp.MustCompile(`^[A-Z0-9]{10}$`)

// languageName maps the English language names Audnexus uses to ISO 639-1.
func languageName(s string) string {
	m := map[string]string{"english": "en", "german": "de", "french": "fr", "spanish": "es", "italian": "it", "dutch": "nl",
		"swedish": "sv", "norwegian": "no", "danish": "da", "finnish": "fi", "portuguese": "pt", "polish": "pl", "japanese": "ja"}
	return m[strings.ToLower(strings.TrimSpace(s))]
}
