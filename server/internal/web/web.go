// SPDX-License-Identifier: AGPL-3.0-or-later

// Package web serves the embedded web app (player + admin panel). The SPA is
// built from /web and copied into dist/ before `go build` (the Dockerfile and
// `npm run build:embed` do this), so the server ships as one binary.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Handler serves static files, falling back to index.html for app routes.
func Handler() http.Handler {
	root, _ := fs.Sub(dist, "dist")
	index, err := fs.ReadFile(root, "index.html")
	if err != nil {
		index = []byte(placeholder)
	}
	files := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		securityHeaders(w.Header())
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p != "" && p != "index.html" {
			if st, err := fs.Stat(root, p); err == nil && !st.IsDir() {
				if strings.HasPrefix(p, "assets/") {
					// Content-hashed file names: cache forever.
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "public, max-age=3600")
				}
				files.ServeHTTP(w, r)
				return
			}
			if path.Ext(p) != "" {
				http.NotFound(w, r) // a missing asset is a 404, not the app
				return
			}
		}
		// App route: always revalidate index.html so new deploys take effect.
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	})
}

// securityHeaders: no inline scripts, nothing from other origins, no framing.
func securityHeaders(h http.Header) {
	h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
		"img-src 'self' data: blob:; media-src 'self' blob:; connect-src 'self' ws: wss:; font-src 'self'; "+
		"object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "same-origin")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
}

const placeholder = `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Simple APB</title></head>
<body style="font-family:system-ui;padding:2rem"><h1>Simple APB</h1>
<p>The web app was not built into this binary. Run <code>npm run build:embed</code> in <code>web/</code>, then rebuild the server.
The API is available under <code>/api/v1</code>.</p></body></html>`
