package web

import (
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestHandler(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html":    {Data: []byte("<html>app</html>")},
		"assets/app.js": {Data: []byte("js")},
		"tollgate.png":  {Data: []byte("png")},
	}
	h := Handler(fsys)
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w
	}
	if w := get("/assets/app.js"); w.Body.String() != "js" || !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("asset: %q %v", w.Body, w.Header())
	}
	if w := get("/tollgate.png"); w.Body.String() != "png" || strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("public file: %q %v", w.Body, w.Header())
	}
	if w := get("/keys"); w.Body.String() != "<html>app</html>" || w.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("spa fallback: %q", w.Body)
	}
	if w := get("/invite/abc"); w.Body.String() != "<html>app</html>" {
		t.Fatal("invite page must be the SPA")
	}
	if w := get("/console/api/v1/nope"); w.Code != 404 || strings.Contains(w.Body.String(), "<html>") {
		t.Fatalf("unknown API path served HTML: %d", w.Code)
	}
	rec := httptest.NewRecorder()
	Handler(fstest.MapFS{}).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 503 {
		t.Fatalf("unbuilt console: %d", rec.Code)
	}
}
