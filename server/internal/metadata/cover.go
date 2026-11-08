// SPDX-License-Identifier: AGPL-3.0-or-later

package metadata

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // decode PNG covers
	"io"
	"net/http"
	"net/url"
	"strings"
)

// MaxCoverBytes caps a downloaded cover.
const MaxCoverBytes = 10 << 20

// ErrCoverHost means a cover URL isn't from a known provider image host.
var ErrCoverHost = errors.New("cover URL is not from a metadata provider")

// coverHosts are the image hosts the providers link to. Covers are only
// fetched from these (over HTTPS), so a cover URL can't be used to make the
// server request arbitrary addresses.
var coverHosts = []string{"covers.openlibrary.org", "books.google.com", "books.googleusercontent.com",
	"m.media-amazon.com", "images-na.ssl-images-amazon.com"}

// AllowedCoverURL reports whether a cover may be fetched from u. Extra hosts
// (test fakes) may be given.
func AllowedCoverURL(u string, extra ...string) bool {
	p, err := url.Parse(u)
	if err != nil || p.User != nil {
		return false
	}
	host := strings.ToLower(p.Hostname())
	for _, h := range extra {
		if hp, err := url.Parse(h); err == nil && p.Scheme == hp.Scheme && p.Host == hp.Host {
			return true
		}
	}
	if p.Scheme != "https" {
		return false
	}
	for _, h := range coverHosts {
		if host == h {
			return true
		}
	}
	return false
}

// FetchCover downloads a cover from a provider and returns it as JPEG.
func (e Endpoints) FetchCover(ctx context.Context, u string) ([]byte, error) {
	if !AllowedCoverURL(u, e.OpenLibraryCovers, e.GoogleBooks, e.Audnexus) {
		return nil, ErrCoverHost
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	hc := *e.client()
	hc.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		// Open Library redirects to its archive.org image storage.
		archive := r.URL.Scheme == "https" && strings.HasSuffix(strings.ToLower(r.URL.Hostname()), ".archive.org")
		if len(via) > 3 || !(archive || AllowedCoverURL(r.URL.String(), e.OpenLibraryCovers, e.GoogleBooks, e.Audnexus)) {
			return ErrCoverHost
		}
		return nil
	}
	res, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cover: HTTP %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, MaxCoverBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxCoverBytes {
		return nil, errors.New("cover is too large")
	}
	return ToJPEG(data)
}

// ToJPEG checks that data is a JPEG or PNG image of sensible size and
// returns it as JPEG (JPEGs unchanged).
func ToJPEG(data []byte) ([]byte, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("not an image: %w", err)
	}
	if cfg.Width < 32 || cfg.Height < 32 || cfg.Width > 6000 || cfg.Height > 6000 {
		return nil, errors.New("cover has an unusable size")
	}
	if format == "jpeg" {
		return data, nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
