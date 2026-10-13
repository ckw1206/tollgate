package consoleapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"tollgate/core/api"
	"tollgate/core/db/repo"
)

// maxIconBytes caps the decoded server_icon image. The console downscales uploads to a
// 256×256 PNG, well under this; the cap only bounds what a direct API call can store
// (it's returned by /auth/me on every page load).
const maxIconBytes = 256 << 10

// validIcon accepts "" (the default icon) or a base64 data URL of a PNG, JPEG or WebP
// whose bytes match the declared type. No SVG, no remote URLs.
func validIcon(s string) bool {
	if s == "" {
		return true
	}
	rest, ok := strings.CutPrefix(s, "data:image/")
	if !ok {
		return false
	}
	kind, b64, ok := strings.Cut(rest, ";base64,")
	if !ok || base64.StdEncoding.DecodedLen(len(b64)) > maxIconBytes+2 {
		return false
	}
	b, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(b) > maxIconBytes {
		return false
	}
	switch kind {
	case "png":
		return bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n"))
	case "jpeg":
		return bytes.HasPrefix(b, []byte{0xFF, 0xD8, 0xFF})
	case "webp":
		return len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP"
	}
	return false
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.Store.GetSettings(r.Context())
	if err != nil {
		storeErr(w, err)
		return
	}
	api.WriteJSON(w, 200, st)
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var in repo.Settings
	if !decode(w, r, &in) {
		return
	}
	if in.ServerName == "" || in.Currency == "" {
		badRequest(w, "server_name and currency are required")
		return
	}
	if !validIcon(in.ServerIcon) {
		badRequest(w, "server_icon must be empty or a PNG, JPEG or WebP data URL of at most 256 KB")
		return
	}
	before, _ := s.Store.GetSettings(r.Context())
	after, err := s.Store.UpdateSettings(r.Context(), in)
	if err != nil {
		badRequest(w, err.Error()) // only failure mode besides DB down is an invalid timezone
		return
	}
	s.audit(r, "settings.update", "server_settings", "1", auditSettings(before), auditSettings(after))
	api.WriteJSON(w, 200, after)
}

// auditSettings replaces the icon data URL (up to ~350 KB) with a short digest, so every
// settings.update entry doesn't carry the image twice; the digest still shows a change.
func auditSettings(st *repo.Settings) *repo.Settings {
	if st == nil || st.ServerIcon == "" {
		return st
	}
	c := *st
	c.ServerIcon = fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(st.ServerIcon)))[:19]
	return &c
}
