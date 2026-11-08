// SPDX-License-Identifier: AGPL-3.0-or-later

package metadata

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// DefaultThreshold is the score a match needs to be applied automatically.
const DefaultThreshold = 0.8

// Score rates how well a record matches a book, from 0 to 1 (brief §6.12):
// a shared ASIN or ISBN is a certain match; otherwise the title counts 70 %
// and the author 30 %. Without a known author the title alone reaches at
// most 0.75, below the lowest default threshold, so a bare title ("Test",
// "Untitled") never auto-applies some other book's details: it goes to review.
func Score(q Query, r Record) float64 {
	if q.ASIN != "" && strings.EqualFold(strings.TrimSpace(q.ASIN), r.ASIN) {
		return 1
	}
	if qi := NormalizeISBN(q.ISBN); qi != "" && qi == NormalizeISBN(r.ISBN) {
		return 1
	}
	t := titleSimilarity(q.Title, r.Title, r.Subtitle)
	if strings.TrimSpace(q.Author) == "" {
		return round2(t * 0.75)
	}
	a := 0.0
	for _, name := range r.Authors {
		a = max(a, nameSimilarity(q.Author, name))
	}
	return round2(0.7*t + 0.3*a)
}

func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }

var (
	bracketRe = regexp.MustCompile(`[\(\[][^\)\]]*[\)\]]`)
	noiseRe   = regexp.MustCompile(`\b(unabridged|abridged|audiobook|audio book|a novel)\b`)
)

// normalize lower-cases, strips accents, punctuation, bracketed notes and a
// leading article.
func normalize(s string) string {
	s = strings.ToLower(norm.NFD.String(s))
	s = bracketRe.ReplaceAllString(s, " ")
	s = noiseRe.ReplaceAllString(s, " ")
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Mn, r):
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		default:
			b.WriteRune(' ')
		}
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	for _, a := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(out, a) && len(out) > len(a) {
			return out[len(a):]
		}
	}
	return out
}

func titleSimilarity(book, title, subtitle string) float64 {
	b := normalize(book)
	if b == "" {
		return 0
	}
	best := similarity(b, normalize(title))
	if subtitle != "" {
		best = max(best, similarity(b, normalize(title+" "+subtitle)))
	}
	// "Title: Subtitle" on our side vs a bare title on theirs.
	if i := strings.IndexAny(book, ":–—"); i > 0 {
		best = max(best, 0.95*similarity(normalize(book[:i]), normalize(title)))
	}
	return best
}

// nameSimilarity compares people's names, ignoring order ("Lindqvist, Mara").
func nameSimilarity(a, b string) float64 {
	return max(similarity(normalize(a), normalize(b)), tokenSet(normalize(a), normalize(b)))
}

// similarity is 1 - normalized Levenshtein distance, or the token overlap,
// whichever is higher.
func similarity(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 1
	}
	ra, rb := []rune(a), []rune(b)
	d := levenshtein(ra, rb)
	lev := 1 - float64(d)/float64(max(len(ra), len(rb)))
	return max(lev, tokenSet(a, b)*0.95)
}

// tokenSet is the share of words in common (Sørensen–Dice over word sets).
func tokenSet(a, b string) float64 {
	wa, wb := set(strings.Fields(a)), set(strings.Fields(b))
	if len(wa) == 0 || len(wb) == 0 {
		return 0
	}
	n := 0
	for w := range wa {
		if wb[w] {
			n++
		}
	}
	return 2 * float64(n) / float64(len(wa)+len(wb))
}

func set(ws []string) map[string]bool {
	m := make(map[string]bool, len(ws))
	for _, w := range ws {
		m[w] = true
	}
	return m
}

func levenshtein(a, b []rune) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// NormalizeISBN returns a valid ISBN as ISBN-13 digits, or "".
func NormalizeISBN(s string) string {
	var d []byte
	for _, c := range strings.ToUpper(s) {
		if c >= '0' && c <= '9' || c == 'X' {
			d = append(d, byte(c))
		}
	}
	switch len(d) {
	case 13:
		if isbn13Check(d[:12]) == d[12] {
			return string(d)
		}
	case 10:
		sum := 0
		for i, c := range d {
			v := int(c - '0')
			if c == 'X' {
				if i != 9 {
					return ""
				}
				v = 10
			}
			sum += v * (10 - i)
		}
		if sum%11 == 0 {
			b := append([]byte("978"), d[:9]...)
			return string(append(b, isbn13Check(b)))
		}
	}
	return ""
}

func isbn13Check(d []byte) byte {
	sum := 0
	for i, c := range d {
		if c < '0' || c > '9' {
			return 0
		}
		w := 1
		if i%2 == 1 {
			w = 3
		}
		sum += int(c-'0') * w
	}
	return byte('0' + (10-sum%10)%10)
}
