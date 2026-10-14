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
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"tollgate/core/api"
	"tollgate/core/db/repo"
	"tollgate/core/webhook"
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
func validWebhookURLStrict(s string) bool { return s != "" && validWebhookURL(s) }

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

// publicHostCheck is the SSRF policy behind the test endpoint: the shared
// guard from core/webhook, which resolves the host up front and requires
// EVERY resolved IP to be public (the same allowedIP the production dialer
// enforces at connect time).
//
// It is a var for exactly ONE seam: permitLoopback in settings_test.go swaps
// it for a loopback-permitting variant so the httptest (127.0.0.1) delivery
// cases can run; the real guard's rejection behavior is pinned in
// core/webhook/dialer_test.go and by TestWebhook_SSRFRejected (seam
// untouched).
var publicHostCheck = webhook.IsPublicHost

// webhookDialControl is the dial-time re-check for the test client's dialer:
// the SAME policy (publicHostCheck) as the pre-flight 400, so a name that
// rebinds to a private IP after the pre-flight is refused at connect time
// (defense in depth against DNS rebinding).
func webhookDialControl(network, address string, _ syscall.RawConn) error {
	if ok, _ := publicHostCheck(address); !ok {
		return fmt.Errorf("webhook host must be public (loopback, private and link-local addresses are not allowed)")
	}
	return nil
}

// isJSONContentType reports whether the request's Content-Type is
// application/json (parameters such as charset are ignored).
func isJSONContentType(r *http.Request) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && strings.EqualFold(mt, "application/json")
}

// testWebhook fires a single synthetic budget crossing at a webhook URL and
// reports the HTTP outcome synchronously. It is a one-shot probe for the console
// "Test" button — it does NOT go through the async sender (which is fire-and-forget
// and can't report back). Gated on SettingsWrite.
//
// The URL is taken from the optional JSON body {"url":"..."} when present and
// non-empty (the console sends the EDITED form value, so an unsaved URL can be
// tested); otherwise it falls back to the stored budget_webhook_url.
//
// Response contract: ALWAYS 200 with {"ok":bool,"status":int,"message":string},
// except 400 (no valid URL / SSRF-rejected host / invalid body URL), 403 (RBAC),
// 405, 415 in the standard error shape. status is the upstream HTTP status, 0
// when no upstream response was received (unreachable or timeout).
//
// The SSRF policy (publicHostCheck → core/webhook.IsPublicHost) is shared with
// the production sender; see permitLoopback in settings_test.go for the single
// test seam that lets the httptest (127.0.0.1) delivery cases run.
func (s *Server) testWebhook(w http.ResponseWriter, r *http.Request) {
	// Read the optional {url} body. It is read whenever Content-Type is JSON
	// (including ContentLength == -1: a chunked-encoding body has no declared
	// length, and skipping it here would silently test the STORED URL instead
	// of the edited one the console sent). The read is capped; an empty body
	// means "test the stored URL", non-empty but invalid JSON is a 400, a
	// valid non-empty url in the body is tested in place of the stored one.
	var in struct {
		URL string `json:"url"`
	}
	if isJSONContentType(r) {
		raw, err := io.ReadAll(io.LimitReader(r.Body, 4<<10))
		if err != nil {
			badRequest(w, "invalid JSON body")
			return
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &in); err != nil {
				badRequest(w, "invalid JSON body")
				return
			}
		}
	}
	u := in.URL
	if u == "" {
		st, err := s.Store.GetSettings(r.Context())
		if err != nil {
			storeErr(w, err)
			return
		}
		u = st.BudgetWebhookURL
	}
	if !validWebhookURLStrict(u) {
		badRequest(w, "set a valid budget_webhook_url (http/https with a host) first")
		return
	}
	// SSRF guard: the probe must only reach public hosts — never loopback,
	// private or link-local (cloud metadata, internal services). Shared with
	// the production dialer (core/webhook): one policy, checked here up front
	// and again at connect time (webhookDialControl).
	parsed, err := url.Parse(u)
	if err != nil || parsed.Host == "" {
		badRequest(w, "invalid webhook URL")
		return
	}
	if ok, _ := publicHostCheck(parsed.Host); !ok {
		badRequest(w, "webhook host must be public (loopback, private and link-local addresses are not allowed)")
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
	payload, err := json.Marshal(crossing)
	if err != nil {
		storeErr(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), webhookTestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(payload))
	if err != nil {
		badRequest(w, "invalid webhook URL")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	// A dedicated client that doesn't follow redirects: the test reaches only the
	// validated URL, never a redirect target. The dialer's Control re-checks every
	// resolved IP (DNS can point a public name at a private one) — the SAME
	// shared policy as the pre-flight 400 above.
	dialer := &net.Dialer{Control: webhookDialControl}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{DialContext: dialer.DialContext, TLSHandshakeTimeout: webhookTestTimeout}}
	resp, err := client.Do(req)
	if err != nil {
		// A delivery failure (connection error, timeout, TLS, SSRF rejection at
		// connect time) is NOT a 5xx: the caller is asking "did the endpoint work?"
		// and the answer is in the body. 200 + ok:false keeps the detail from being
		// replaced by the client's generic "internal error".
		msg := redactErr(err)
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			msg = fmt.Sprintf("timed out after %s", webhookTestTimeout)
		}
		api.WriteJSON(w, 200, map[string]any{"ok": false, "status": 0, "message": msg})
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		api.WriteJSON(w, 200, map[string]any{"ok": true, "status": resp.StatusCode, "message": ""})
		return
	}
	// The endpoint answered but rejected the event: still 200, with the upstream
	// status so the console can show "endpoint returned 404 Not Found".
	api.WriteJSON(w, 200, map[string]any{"ok": false, "status": resp.StatusCode, "message": "endpoint returned " + resp.Status})
}

// redactErr returns a human-readable failure cause from a webhook delivery
// error WITHOUT the request URL. For *url.Error (which is how net/http wraps
// connection/TLS/timeout errors) it surfaces the CAUSE — net/http already
// strips the password from *url.Error, so "x509: …" / "connection refused"
// reach the admin — instead of throwing it away. Anything that is not a
// *url.Error is passed through unchanged (no URL present to leak).
func redactErr(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err.Error()
	}
	return err.Error()
}
