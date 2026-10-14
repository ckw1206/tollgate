package consoleapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

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

// validWebhookURL accepts "" (disabled) or an http/https URL with a host.
func validWebhookURL(s string) bool {
	if s == "" {
		return true
	}
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// validWebhookURLStrict is validWebhookURL with no "" exception: the test-event
// endpoint needs an actual destination, so an empty/invalid value is a 400 there.
func validWebhookURLStrict(s string) bool {
	if s == "" {
		return false
	}
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
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
	if utf8.RuneCountInString(in.LandingHeadline) > 80 || utf8.RuneCountInString(in.LandingTagline) > 160 {
		badRequest(w, "landing_headline is limited to 80 characters and landing_tagline to 160")
		return
	}
	if !validWebhookURL(in.BudgetWebhookURL) {
		badRequest(w, "budget_webhook_url must be empty or an http(s) URL with a host")
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

// webhookTestTimeout bounds the test event; a var so tests can shorten it.
var webhookTestTimeout = 5 * time.Second

// testWebhook fires a single synthetic budget crossing at the configured URL and
// reports the HTTP outcome synchronously. It is a one-shot probe for the console
// "Test" button — it does NOT go through the async sender (which is fire-and-forget
// and can't report back). Gated on SettingsWrite.
func (s *Server) testWebhook(w http.ResponseWriter, r *http.Request) {
	st, err := s.Store.GetSettings(r.Context())
	if err != nil {
		storeErr(w, err)
		return
	}
	u := st.BudgetWebhookURL
	if !validWebhookURLStrict(u) {
		badRequest(w, "set a valid budget_webhook_url (http/https with a host) first")
		return
	}
	crossing := repo.BudgetCrossing{
		Scope:       "key",
		ID:          "00000000-0000-4000-8000-000000000000",
		Label:       "test",
		Threshold:   80,
		SpendNanos:  0,
		BudgetNanos: 0,
		BudgetType:  "daily",
	}
	body, err := json.Marshal(crossing)
	if err != nil {
		storeErr(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), webhookTestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		badRequest(w, "invalid webhook URL")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	// A dedicated client that doesn't follow redirects: the test reaches only the
	// validated URL, never a redirect target.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		// A delivery failure (connection error or timeout) is a 5xx: the webhook is
		// down or unreachable. status 0 means no upstream response was received.
		msg := redactErr(err)
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			msg = fmt.Sprintf("timed out after %s", webhookTestTimeout)
		}
		api.WriteJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "status": 0, "message": msg})
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		api.WriteJSON(w, 200, map[string]any{"ok": true, "status": resp.StatusCode, "message": ""})
		return
	}
	// The endpoint answered but rejected the event: 5xx-style, with the upstream
	// status so the console can show it.
	api.WriteJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "status": resp.StatusCode, "message": "endpoint returned " + resp.Status})
}

// redactErr strips a request URL from an error string before echoing it, so a
// URL with an embedded credential (user:pass@host) can't leak into the response.
func redactErr(err error) string {
	msg := err.Error()
	if i := strings.Index(msg, "http://"); i >= 0 {
		msg = msg[:i] + "[url redacted]"
	}
	if i := strings.Index(msg, "https://"); i >= 0 {
		msg = msg[:i] + "[url redacted]"
	}
	return msg
}
