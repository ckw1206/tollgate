package consoleapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

func TestSettings_WebhookTestEvent(t *testing.T) {
	e := newTenv(t)
	const path = "/console/api/v1/settings/webhook/test"

	// Member lacks settings:write → 403.
	if w := e.as("member@x.io", "POST", path, nil); w.Code != http.StatusForbidden {
		t.Fatalf("member test: %d %s", w.Code, w.Body)
	}
	// Viewer too.
	if w := e.as("viewer@x.io", "POST", path, nil); w.Code != http.StatusForbidden {
		t.Fatalf("viewer test: %d %s", w.Code, w.Body)
	}
	// Admin, no URL configured → 400 (nothing to deliver to).
	if w := e.as("admin@x.io", "POST", path, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("admin no-url: %d %s", w.Code, w.Body)
	}

	// Each delivery case sets + verifies its own target URL immediately before the
	// POST, so it never relies on a prior case's state (the settings row is shared).

	// A reachable 2xx endpoint → 200 ok, and the probe carries the no-secret payload.
	// gotBody is written by the httptest handler (its own goroutine) and read here,
	// so a mutex orders the two.
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
	wOk := e.as("admin@x.io", "POST", path, nil)
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

	// An upstream 5xx is reported as a delivery failure with a 5xx status.
	// Each case gets its own response variable (assigned outside the if) so the
	// body assertion below can't accidentally decode a prior case's response.
	up500 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer up500.Close()
	setWebhookURL(t, e, up500.URL) // verified: the POST below is guaranteed to hit up500
	w500 := e.as("admin@x.io", "POST", path, nil)
	if w500.Code != http.StatusBadGateway {
		t.Fatalf("admin 500: %d %s", w500.Code, w500.Body)
	}
	if out := decodeInto[map[string]any](t, w500); out["ok"] != false || out["status"] != 500.0 {
		t.Fatalf("500: %v", out)
	}

	// An unreachable endpoint is a delivery failure (5xx, ok:false).
	setWebhookURL(t, e, "http://127.0.0.1:1/hook")
	wUnreach := e.as("admin@x.io", "POST", path, nil)
	if wUnreach.Code != http.StatusBadGateway {
		t.Fatalf("unreachable code: %d %s", wUnreach.Code, wUnreach.Body)
	}
	if out := decodeInto[map[string]any](t, wUnreach); out["ok"] != false {
		t.Fatalf("unreachable: %v", out)
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
