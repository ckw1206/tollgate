package webhook

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestNewWebhookClient_FollowsNoRedirects pins the production client
// (NewWebhookClient — what main.go hands to NewSender) to no redirect
// following. The existing TestSender_RedirectIsAFailureNotDelivery injects its
// OWN noRedirect client, so it would pass even if CheckRedirect were dropped
// from the production wiring; this test builds the client via the real
// constructor, so a regression there is caught here.
//
// The 302 must come back un-followed: the request returns the 3xx and the
// redirect TARGET is never contacted.
//
// One documented test seam: the production client's SafeTransport refuses
// loopback, so the httptest server (127.0.0.1) would be blocked before any
// 302 is seen. We swap ONLY the Transport to a loopback-permitting one; the
// CheckRedirect field under test comes verbatim from NewWebhookClient. (The
// guard itself is pinned separately by TestNewWebhookClient_CarriesTheGuard.)
func TestNewWebhookClient_FollowsNoRedirects(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1) // must never be reached
		w.WriteHeader(200)
	}))
	defer target.Close()

	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound) // 302
	}))
	defer up.Close()

	c := NewWebhookClient(5 * time.Second)
	c.Transport = http.DefaultTransport // loopback-only test seam; CheckRedirect untouched
	resp, err := c.Post(up.URL, "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("request errored instead of returning the 3xx response: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302 (un-followed)", resp.StatusCode)
	}
	if n := targetHits.Load(); n != 0 {
		t.Fatalf("redirect target hit %d times; NewWebhookClient must not follow redirects", n)
	}
}

// TestNewWebhookClient_CarriesTheGuard: the client's transport refuses an
// internal destination the same way main.go's does — the constructor must keep
// SafeTransport, not just the no-redirect behavior.
func TestNewWebhookClient_CarriesTheGuard(t *testing.T) {
	c := NewWebhookClient(5 * time.Second)
	resp, err := c.Post("http://127.0.0.1:1/x", "application/json", strings.NewReader(`{}`))
	if err == nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		t.Fatal("dial to loopback succeeded; NewWebhookClient lost the SSRF guard")
	}
	if !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("error = %v, want the Control refusal", err)
	}
}
