// Package web embeds the built console SPA.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"tollgate/core/api"
)

//go:embed all:dist
var dist embed.FS

func Dist() fs.FS {
	sub, _ := fs.Sub(dist, "dist")
	return sub
}

// Handler serves static files and falls back to index.html for client-side routes.
func Handler(fsys fs.FS) http.Handler {
	files := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")

		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p != "" && p != "index.html" {
			if st, err := fs.Stat(fsys, p); err == nil && !st.IsDir() {
				if strings.HasPrefix(p, "assets/") {
					h.Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		for _, prefix := range []string{"/v1/", "/auth/", "/console/"} {
			if strings.HasPrefix(r.URL.Path, prefix) {
				api.WriteError(w, 404, "invalid_request_error", "not_found", "no such endpoint")
				return
			}
		}
		index, err := fs.ReadFile(fsys, "index.html")
		if err != nil {
			http.Error(w, "console not built: run `make web`", http.StatusServiceUnavailable)
			return
		}
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Cache-Control", "no-cache")
		w.Write(index)
	})
}
