package consoleapi

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"tollgate/core/db/repo"
)

// putBody builds a full, valid settings payload so a test exercises exactly one
// thing (RBAC or the webhook-URL rule), not "missing required field".
func putBody(url string) map[string]any {
	return map[string]any{
		"server_name":        "Tollgate",
		"server_icon":        "",
		"timezone":           "UTC",
		"currency":           "USD",
		"landing_headline":   "",
		"landing_tagline":    "",
		"budget_webhook_url": url,
	}
}

// setWebhookURL sets budget_webhook_url and VERIFIES it round-trips via GET,
// retrying with backoff. This matters for two reasons:
//
//   - The settings row is shared across cases in this test. A PUT that silently
//     failed once would let a later case POST to a stale URL and produce a
//     misleading assertion (expecting a 5xx, getting a 2xx from the previous
//     server). Verifying the transition means each case proceeds only when its
//     target URL is confirmed.
//   - Sibling worktrees run `make test` concurrently against the same Postgres,
//     so a single request can transiently fail under load. Retrying the set
//     (bounded, with backoff) absorbs that; a real failure fails loudly at the
//     exact step rather than cascading.
func setWebhookURL(t *testing.T, e *tenv, url string) {
	t.Helper()
	const maxAttempts = 10
	var last string
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		w := e.as("admin@x.io", "PUT", "/console/api/v1/settings", putBody(url))
		switch {
		case w.Code != http.StatusOK:
			last = fmt.Sprintf("PUT → %d: %s", w.Code, w.Body)
		default:
			got := decodeInto[repo.Settings](t, e.as("admin@x.io", "GET", "/console/api/v1/settings", nil))
			if got.BudgetWebhookURL != url {
				last = fmt.Sprintf("round-trip: got %q want %q", got.BudgetWebhookURL, url)
				continue
			}
			return // confirmed set
		}
		if attempt < maxAttempts {
			d := 50 * time.Millisecond * time.Duration(attempt)
			if d > 300*time.Millisecond {
				d = 300 * time.Millisecond
			}
			time.Sleep(d)
		}
	}
	t.Fatalf("could not set budget_webhook_url=%q after %d attempts: %s", url, maxAttempts, last)
}

// permitLoopback temporarily swaps the webhook test's public-host checks for a
// loopback-permitting variant so httptest servers (127.0.0.1) can be probed; it
// returns a restore func. The real guard's rejection behavior is covered
// directly by TestWebhook_PublicHostGuard.
func permitLoopback(t *testing.T) func() {
	t.Helper()
	oldHost, oldCtl := isPublicHostFn, webhookTestControl
	isPublicHostFn = func(host string) bool {
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if ip := net.ParseIP(host); ip != nil {
			return true // any IP literal is fine for the delivery cases
		}
		return host == "localhost"
	}
	webhookTestControl = func(network, address string, _ syscall.RawConn) error { return nil }
	t.Cleanup(func() { isPublicHostFn, webhookTestControl = oldHost, oldCtl })
	return func() { isPublicHostFn, webhookTestControl = oldHost, oldCtl }
}

func TestSettings_BudgetWebhookURL(t *testing.T) {
	e := newTenv(t)

	// Member lacks settings:write → 403, even with a valid body.
	if w := e.as("member@x.io", "PUT", "/console/api/v1/settings", putBody("https://hooks.example.com/budget")); w.Code != http.StatusForbidden {
		t.Fatalf("member PUT: %d %s", w.Code, w.Body)
	}

	// Admin: a non-http(s) URL is rejected before anything is written.
	if w := e.as("admin@x.io", "PUT", "/console/api/v1/settings", putBody("ftp://x")); w.Code != http.StatusBadRequest {
		t.Fatalf("admin ftp: %d %s", w.Code, w.Body)
	}
	// A scheme with no host is also rejected.
	if w := e.as("admin@x.io", "PUT", "/console/api/v1/settings", putBody("https://")); w.Code != http.StatusBadRequest {
		t.Fatalf("admin no-host: %d %s", w.Code, w.Body)
	}
	// An invalid scheme (not http/https).
	if w := e.as("admin@x.io", "PUT", "/console/api/v1/settings", putBody("javascript://alert(1)")); w.Code != http.StatusBadRequest {
		t.Fatalf("admin js: %d %s", w.Code, w.Body)
	}
	// Empty is allowed (disabled).
	setWebhookURL(t, e, "")

	// A valid URL is saved and round-trips through GET.
	setWebhookURL(t, e, "https://hooks.example.com/budget")
	if got := decodeInto[repo.Settings](t, e.as("admin@x.io", "GET", "/console/api/v1/settings", nil)); got.BudgetWebhookURL != "https://hooks.example.com/budget" {
		t.Fatalf("round-trip: %q", got.BudgetWebhookURL)
	}
	// A valid plain-http URL is accepted too (an internal endpoint).
	setWebhookURL(t, e, "http://10.0.0.5:8080/hook")
	if got := decodeInto[repo.Settings](t, e.as("admin@x.io", "GET", "/console/api/v1/settings", nil)); got.BudgetWebhookURL != "http://10.0.0.5:8080/hook" {
		t.Fatalf("round-trip http: %q", got.BudgetWebhookURL)
	}
}

// C1: /auth/me is returned to EVERY authenticated role, so it must not carry
// the webhook URL (its path token may be the credential, or it may embed
// user:pass@host). The full row — webhook URL included — is admin-only via
// GET /console/api/v1/settings.
func TestMe_SettingsNotLeaked(t *testing.T) {
	e := newTenv(t)
	const secret = "https://hooks.example.com/services/T0000/B0000/token-SECRET"
	setWebhookURL(t, e, secret)

	// Member and viewer: no usable budget_webhook_url in /auth/me.
	for _, email := range []string{"member@x.io", "viewer@x.io"} {
		type meServer struct {
			ServerName       string `json:"server_name"`
			BudgetWebhookURL string `json:"budget_webhook_url"`
			Version          string `json:"version"`
		}
		me := decodeInto[struct {
			User   repo.User `json:"user"`
			Server meServer  `json:"server"`
		}](t, e.as(email, "GET", "/auth/me", nil))
		if me.Server.BudgetWebhookURL != "" {
			t.Fatalf("%s /auth/me leaks budget_webhook_url: %q", email, me.Server.BudgetWebhookURL)
		}
		if me.Server.ServerName == "" || me.Server.Version == "" {
			t.Fatalf("%s /auth/me server blob incomplete: %+v", email, me.Server)
		}
	}

	// Non-admin GET settings is denied (the settings:read role test).
	for _, email := range []string{"member@x.io", "viewer@x.io"} {
		if w := e.as(email, "GET", "/console/api/v1/settings", nil); w.Code != http.StatusForbidden {
			t.Fatalf("%s GET settings: %d %s", email, w.Code, w.Body)
		}
	}

	// Admin GET settings returns the full row, webhook URL included.
	if got := decodeInto[repo.Settings](t, e.as("admin@x.io", "GET", "/console/api/v1/settings", nil)); got.BudgetWebhookURL != secret {
		t.Fatalf("admin GET settings: %q", got.BudgetWebhookURL)
	}
}

// Contract for stream D: the response is ALWAYS 200 with
// {"ok":bool,"status":int,"message":string} except 400 (no valid URL) / 403
// (RBAC) / 405 / 415 in the standard error shape.
func TestSettings_WebhookTestEvent(t *testing.T) {
	e := newTenv(t)
	const path = "/console/api/v1/settings/webhook/test"
	permitLoopback(t)

	// Member lacks settings:write → 403.
	if w := e.as("member@x.io", "POST", path, nil); w.Code != http.StatusForbidden {
		t.Fatalf("member test: %d %s", w.Code, w.Body)
	}
	// Viewer too.
	if w := e.as("viewer@x.io", "POST", path, nil); w.Code != http.StatusForbidden {
		t.Fatalf("viewer test: %d %s", w.Code, w.Body)
	}
	// Admin, no URL saved → 400 (nothing to deliver to).
	if w := e.as("admin@x.io", "POST", path, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("admin no-url: %d %s", w.Code, w.Body)
	}

	// SSRF: a body URL on a non-public host is a 400 — covered with the REAL
	// guard in TestWebhook_SSRFRejected (permitLoopback above would allow it).

	// Each delivery case sets + verifies its own target URL immediately before the
	// POST, so it never relies on a prior case's state (the settings row is shared).

	// A reachable 2xx endpoint → 200 ok:true, and the probe carries the
	// no-secret payload. gotBody is written by the httptest handler (its own
	// goroutine) and read here, so a mutex orders the two.
	var (
		bodyMu  sync.Mutex
		gotBody string
	)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyMu.Lock()
		gotBody = readBody(r)
		bodyMu.Unlock()
		w.WriteHeader(200)
	}))
	defer up.Close()
	setWebhookURL(t, e, up.URL)
	wOk := e.as("admin@x.io", "POST", path, nil) // empty body → the STORED url
	if wOk.Code != http.StatusOK {
		t.Fatalf("admin ok: %d %s", wOk.Code, wOk.Body)
	}
	if out := decodeInto[map[string]any](t, wOk); out["ok"] != true || out["status"] != 200.0 {
		t.Fatalf("ok: %v", out)
	}
	bodyMu.Lock()
	got := gotBody
	bodyMu.Unlock()
	for _, want := range []string{`"scope":"key"`, `"label":"test"`, `"threshold":80`, `"budget_type":"daily"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("test payload missing %s: %s", want, got)
		}
	}

	// C6: a body URL is tested INSTEAD of the stored one — the two fake
	// endpoints below are distinct, so which one fired proves the routing.
	up404 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer up404.Close()
	// Stored stays up (200); the body overrides with up404 (404). If the stored
	// URL were hit instead, the probe would have gotten 200/ok:true.
	w404 := e.as("admin@x.io", "POST", path, map[string]any{"url": up404.URL + "/nope"})
	if w404.Code != http.StatusOK {
		t.Fatalf("body-404 code: %d %s", w404.Code, w404.Body)
	}
	out404 := decodeInto[map[string]any](t, w404)
	if out404["ok"] != false || out404["status"] != 404.0 {
		t.Fatalf("body-404: %v", out404)
	}
	if msg, _ := out404["message"].(string); !strings.Contains(msg, "404") {
		t.Fatalf("body-404 message: %q", msg)
	}

	// An invalid body URL is a 400 (not a delivery attempt).
	if w := e.as("admin@x.io", "POST", path, map[string]any{"url": "ftp://x/hook"}); w.Code != http.StatusBadRequest {
		t.Fatalf("body invalid: %d %s", w.Code, w.Body)
	}

	// An upstream 5xx is reported as 200 ok:false with the upstream status.
	up500 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer up500.Close()
	setWebhookURL(t, e, up500.URL) // verified: the POST below is guaranteed to hit up500
	w500 := e.as("admin@x.io", "POST", path, nil)
	if w500.Code != http.StatusOK {
		t.Fatalf("admin 500: %d %s", w500.Code, w500.Body)
	}
	if out := decodeInto[map[string]any](t, w500); out["ok"] != false || out["status"] != 500.0 {
		t.Fatalf("500: %v", out)
	}

	// An unreachable endpoint → 200 ok:false status:0, message keeps the cause
	// (C10) — "connection refused" style, not "internal error".
	setWebhookURL(t, e, "http://127.0.0.1:1/hook")
	wUnreach := e.as("admin@x.io", "POST", path, nil)
	if wUnreach.Code != http.StatusOK {
		t.Fatalf("unreachable code: %d %s", wUnreach.Code, wUnreach.Body)
	}
	outU := decodeInto[map[string]any](t, wUnreach)
	if outU["ok"] != false || outU["status"] != 0.0 {
		t.Fatalf("unreachable: %v", outU)
	}
	if msg, _ := outU["message"].(string); msg == "" || strings.Contains(msg, "internal error") {
		t.Fatalf("unreachable message: %q", msg)
	}

	// C10: a TLS failure surfaces the CAUSE (x509 …) with no host/path/token.
	// The body URL points at the TLS test server (self-signed) while the stored
	// URL is the plain 200 server, so this also proves body routing.
	upTLS := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer upTLS.Close()
	setWebhookURL(t, e, up.URL) // verified: stored is the plain 200 server
	wTLS := e.as("admin@x.io", "POST", path, map[string]any{"url": upTLS.URL + "/hook"})
	if wTLS.Code != http.StatusOK {
		t.Fatalf("tls code: %d %s", wTLS.Code, wTLS.Body)
	}
	outTLS := decodeInto[map[string]any](t, wTLS)
	if outTLS["ok"] != false || outTLS["status"] != 0.0 {
		t.Fatalf("tls: %v", outTLS)
	}
	msgTLS, _ := outTLS["message"].(string)
	if !strings.Contains(msgTLS, "x509") {
		t.Fatalf("tls message missing cause: %q", msgTLS)
	}
	for _, secret := range []string{"127.0.0.1", "/hook", "token"} {
		if strings.Contains(msgTLS, secret) {
			t.Fatalf("tls message leaks %q: %q", secret, msgTLS)
		}
	}
}

// C12: the pre-flight public-host check (the dialer's Control re-checks the
// SAME function at connect time).
func TestWebhook_PublicHostGuard(t *testing.T) {
	rejected := []string{
		"127.0.0.1", "127.0.0.1:8080", // loopback
		"169.254.169.254", "169.254.169.254:80", // cloud metadata
		"10.0.0.5", "192.168.1.10:8443", // RFC1918
		"172.16.0.1", "127.1", // 172.16/12, shorthand loopback
		"::1", "fc00::1", "fe80::1", // IPv6 loopback / ULA / link-local
		"0.0.0.0", "::", // unspecified
		"224.0.0.1", "ff02::1", // multicast
		"nonexistent-host-tollgate-xyz.invalid", // unresolvable
	}
	for _, h := range rejected {
		if isPublicHost(h) {
			t.Fatalf("isPublicHost(%q) = true, want false", h)
		}
	}
	public := []string{"8.8.8.8", "8.8.8.8:443", "93.184.216.34", "2001:4860:4860::8888"}
	for _, h := range public {
		if !isPublicHost(h) {
			t.Fatalf("isPublicHost(%q) = false, want true", h)
		}
	}
}

// C12: rejected hosts get a 400 through the endpoint, and the dialer's Control
// hook refuses the connection at dial time (defense in depth against DNS
// rebinding — same function, second layer).
func TestWebhook_SSRFRejected(t *testing.T) {
	e := newTenv(t)
	const path = "/console/api/v1/settings/webhook/test"
	for _, url := range []string{"http://127.0.0.1:9/x", "http://169.254.169.254/latest", "http://10.0.0.5/hook", "http://[::1]/x"} {
		if w := e.as("admin@x.io", "POST", path, map[string]any{"url": url}); w.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", url, w.Code, w.Body)
		} else if !strings.Contains(w.Body.String(), "public") {
			t.Fatalf("%s: message should say public: %s", url, w.Body)
		}
	}
	// The dialer Control itself: public passes, private fails.
	if err := webhookTestControl("tcp", "8.8.8.8:443", nil); err != nil {
		t.Fatalf("control public: %v", err)
	}
	if err := webhookTestControl("tcp", "127.0.0.1:443", nil); err == nil {
		t.Fatal("control loopback: expected error")
	}
	if err := webhookTestControl("tcp", "169.254.169.254:80", nil); err == nil {
		t.Fatal("control metadata: expected error")
	}
}

// C10: redactErr surfaces the CAUSE of a *url.Error — net/http already strips
// the URL's password from the error it builds, so the cause is safe to surface
// and the URL (with any token in the path) is not.
func TestRedactErr_KeepsCause(t *testing.T) {
	err := &url.Error{Op: "Post", URL: "https://user:supersecretpw@hooks.example.com/services/TOKEN/hook",
		Err: fmt.Errorf("x509: certificate signed by unknown authority")}
	msg := redactErr(err)
	if !strings.Contains(msg, "x509") || !strings.Contains(msg, "signed by unknown authority") {
		t.Fatalf("cause lost: %q", msg)
	}
	for _, leak := range []string{"hooks.example.com", "/services/", "TOKEN", "supersecretpw"} {
		if strings.Contains(msg, leak) {
			t.Fatalf("leaks %q: %q", leak, msg)
		}
	}
	// A non-url.Error passes through (nothing to redact).
	if got := redactErr(fmt.Errorf("plain failure")); got != "plain failure" {
		t.Fatalf("passthrough: %q", got)
	}
}

func readBody(r *http.Request) string {
	buf := make([]byte, 0, 1<<16)
	tmp := make([]byte, 1<<16)
	for {
		n, err := r.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	return string(buf)
}
