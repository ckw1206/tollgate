package consoleapi

import (
	"encoding/json"
	"strings"
	"testing"

	"tollgate/core/metrics"
)

// Tests for the console read of the admit-path instrumentation (#33).
//
// The gateway port already serves GET /metrics unauthenticated (see
// core/api/metrics.go). This is the same document behind metrics:read, for a
// console that cannot reach the gateway port. Both call metrics.Report().

// TestMetrics_MemberAndViewerDenied: metrics:read is admin-only, so the two
// other roles must be refused by the RBAC gate. This is the denial test
// CLAUDE.md requires for a new permissioned console route — it is not optional
// because the gateway also exposes the data openly; the console route carries
// the permission and must honour it.
func TestMetrics_MemberAndViewerDenied(t *testing.T) {
	e := newTenv(t)
	for _, who := range []string{"member@x.io", "viewer@x.io"} {
		w := e.as(who, "GET", p+"/metrics", nil)
		if w.Code != 403 {
			t.Errorf("%s GET /metrics = %d body=%s, want 403", who, w.Code, w.Body)
		}
	}
	// Anonymous is refused too: the route is inside Sessions.Require, so an
	// unauthenticated caller gets 401 rather than the fleet numbers.
	if w := e.as("", "GET", p+"/metrics", nil); w.Code != 401 {
		t.Errorf("anonymous GET /metrics = %d, want 401", w.Code)
	}
}

// TestMetrics_AdminAllowed: the role that holds the permission gets the
// document, in the shape the gateway endpoint serves.
func TestMetrics_AdminAllowed(t *testing.T) {
	e := newTenv(t)
	w := e.as("admin@x.io", "GET", p+"/metrics", nil)
	if w.Code != 200 {
		t.Fatalf("admin GET /metrics = %d body=%s", w.Code, w.Body)
	}
	var doc struct {
		BucketEdges []float64 `json:"bucket_edges_ms"`
		Ops         map[string]struct {
			Count   int64     `json:"count"`
			Buckets []float64 `json:"buckets"`
		} `json:"ops"`
		Auth struct {
			TriggerP99 float64 `json:"trigger_p99_ms"`
			TriggerQPS float64 `json:"trigger_qps"`
		} `json:"auth"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode %s: %v", w.Body, err)
	}
	if len(doc.BucketEdges) == 0 {
		t.Error("bucket_edges_ms missing")
	}
	auth, ok := doc.Ops["auth_lookup"]
	if !ok {
		t.Fatalf("no auth_lookup entry: %v", doc.Ops)
	}
	if len(auth.Buckets) != len(doc.BucketEdges)+1 {
		t.Errorf("auth_lookup buckets = %d, want edges+1 = %d", len(auth.Buckets), len(doc.BucketEdges)+1)
	}
	if doc.Auth.TriggerP99 != 5 || doc.Auth.TriggerQPS != 100 {
		t.Errorf("thresholds = %v/%v, want 5/100", doc.Auth.TriggerP99, doc.Auth.TriggerQPS)
	}
}

// TestMetrics_MatchesGatewayEndpoint: the console read and the gateway read are
// the same document, so they cannot drift into two different truths.
func TestMetrics_MatchesGatewayEndpoint(t *testing.T) {
	e := newTenv(t)
	w := e.as("admin@x.io", "GET", p+"/metrics", nil)
	if w.Code != 200 {
		t.Fatalf("admin GET /metrics = %d", w.Code)
	}
	// ...except for counts accumulated between the two calls, so compare shape
	// and the static fields rather than byte equality.
	var viaconsole, viaGateway map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &viaconsole); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(mustJSON(t, metrics.Report())), &viaGateway); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"bucket_edges_ms", "NOTE"} {
		if !equalJSON(viaconsole[k], viaGateway[k]) {
			t.Errorf("field %q differs between console and gateway reads:\n console=%v\n gateway=%v", k, viaconsole[k], viaGateway[k])
		}
	}
	co, _ := viaconsole["ops"].(map[string]any)
	go_, _ := viaGateway["ops"].(map[string]any)
	if len(co) != len(go_) {
		t.Errorf("console reports %d ops, gateway reports %d", len(co), len(go_))
	}
}

// TestMetrics_NoSecretsInConsoleRead: the console document must stay aggregate.
// The console is authenticated, but this payload still describes the whole
// fleet, so it must not grow into per-tenant data.
//
// Like the gateway-side test, this uses the SHARED structural allow-list rather
// than grepping for this test's own fixture IDs (which only proves the handler
// does not echo what this test created) or for substrings like "tg-" (which a
// leaked bare hex hash or UUID would not match at all). It also confirms the
// console read and the gateway read are held to the same shape, so the two
// cannot diverge into one strict and one lax.
func TestMetrics_NoSecretsInConsoleRead(t *testing.T) {
	e := newTenv(t)
	// Exercise an authenticated console read that runs a Held lookup, so if
	// reserved_read ever started carrying a scope ID it would be in this body.
	if w := e.as("admin@x.io", "GET", p+"/gate", nil); w.Code != 200 {
		t.Fatalf("gate = %d %s", w.Code, w.Body)
	}
	w := e.as("admin@x.io", "GET", p+"/metrics", nil)
	if w.Code != 200 {
		t.Fatalf("GET /metrics = %d %s", w.Code, w.Body)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	var doc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode %s: %v", w.Body, err)
	}
	if summary := metrics.ValidateShapeSummary(doc); summary != "" {
		t.Errorf("console metrics document violates its allow-list:\n  %s", summary)
	}
	// The shared validator must accept the gateway's document too, so a document
	// that passes one route passes the other.
	viaGateway := metrics.Report()
	if summary := metrics.ValidateShape(viaGateway); len(summary) != 0 {
		t.Errorf("gateway document fails the shared allow-list:\n  %s", strings.Join(summary, "\n  "))
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func equalJSON(a, b any) bool { return mustJSONEq(a) == mustJSONEq(b) }

func mustJSONEq(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
