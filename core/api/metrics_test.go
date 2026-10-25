package api

import (
	"encoding/json"
	"strings"
	"testing"

	"tollgate/core/db/repo"
	"tollgate/core/metrics"
)

// Tests for the admit-path instrumentation endpoint and its wiring (#33).

// metricsDoc fetches GET /metrics and decodes it.
func metricsDoc(t *testing.T, e *env) map[string]any {
	t.Helper()
	w := e.do("GET", "/metrics", "", "")
	if w.Code != 200 {
		t.Fatalf("GET /metrics = %d body=%s", w.Code, w.Body)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode %s: %v", w.Body, err)
	}
	return m
}

// TestMetricsEndpoint_UnauthenticatedAndAggregateOnly: the endpoint needs no
// credential (justified in core/api/metrics.go) and must carry no secret and no
// per-tenant identifier in any label or key.
//
// The leak check here is STRUCTURAL, not a search for this test's own fixture
// strings. Grepping for e.raw / e.key.ID only proves the handler does not echo
// what this test happened to create; a newly added field carrying some OTHER
// identifier would pass. metrics.ValidateShape walks the decoded document
// against an allow-list of keys and shapes and rejects any scalar that looks
// like a UUID, hash, tg- key or JWT — see core/metrics/shape.go. Its teeth are
// demonstrated by TestMetrics_ShapeValidatorCatchesInjectedLeak.
func TestMetricsEndpoint_UnauthenticatedAndAggregateOnly(t *testing.T) {
	e := newEnv(t, repo.KeyInput{})
	// One authenticated call so the auth path has actually run.
	if w := e.do("POST", "/v1/chat/completions", e.raw, chatBody); w.Code != 200 {
		t.Fatalf("chat = %d %s", w.Code, w.Body)
	}
	w := e.do("GET", "/metrics", "", "")
	body := w.Body.String()

	// The documented cache behaviour: a stale copy would answer the wrong
	// question, so the handler must forbid caching.
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}

	var doc map[string]any
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	// The structural guarantee, applied to the live response.
	if summary := metrics.ValidateShapeSummary(doc); summary != "" {
		t.Errorf("metrics document violates its allow-list:\n  %s", summary)
	}
	// Belt and braces on this test's own credential, cheap and explicit.
	if strings.Contains(body, e.raw) {
		t.Error("metrics response contains the raw API key")
	}
	if strings.Contains(body, repo.HashToken(e.raw)) {
		t.Error("metrics response contains the key hash")
	}

	ops := doc["ops"].(map[string]any)
	auth, ok := ops["auth_lookup"].(map[string]any)
	if !ok {
		t.Fatalf("ops has no auth_lookup: %v", ops)
	}
	if got := auth["count"].(float64); got < 1 {
		t.Errorf("auth_lookup count = %v, want >= 1 after one authenticated request", got)
	}
	authed := doc["auth"].(map[string]any)
	if authed["trigger_p99_ms"].(float64) != 5 || authed["trigger_qps"].(float64) != 100 {
		t.Errorf("thresholds = %v/%v, want 5/100", authed["trigger_p99_ms"], authed["trigger_qps"])
	}
	// The reservation ops must be present and separate, even at zero.
	for _, op := range []string{"reserve_add", "reserve_release", "reserve_settle", "reserved_read"} {
		if _, ok := ops[op]; !ok {
			t.Errorf("ops is missing %s; the reservation round-trips must be measured separately", op)
		}
	}
	// qps_since_start was dropped: it divided lifetime calls by lifetime uptime
	// and read as a huge number on a fresh server.
	if _, present := authed["qps_since_start"]; present {
		t.Error("auth.qps_since_start is back; it is a misleading signal (see Report)")
	}
}

// TestMetricsEndpoint_CountsEveryAuthCall: a known number of /v1 calls produces
// exactly that many auth lookups. This is the counter the issue's trigger reads.
func TestMetricsEndpoint_CountsEveryAuthCall(t *testing.T) {
	e := newEnv(t, repo.KeyInput{})
	metricsReset(t)

	const calls = 4
	for i := 0; i < calls; i++ {
		if w := e.do("POST", "/v1/chat/completions", e.raw, chatBody); w.Code != 200 {
			t.Fatalf("call %d = %d %s", i, w.Code, w.Body)
		}
	}
	// A rejected key also runs the lookup, so it must be counted too — the
	// trigger is about query volume, not about successful requests.
	if w := e.do("POST", "/v1/chat/completions", "tg-not-a-real-key", chatBody); w.Code != 401 {
		t.Fatalf("bad key = %d %s", w.Code, w.Body)
	}

	doc := metricsDoc(t, e)
	ops := doc["ops"].(map[string]any)["auth_lookup"].(map[string]any)
	if got := ops["count"].(float64); got != calls+1 {
		t.Errorf("auth_lookup count = %v, want %d (%d ok + 1 rejected)", got, calls+1, calls)
	}
}

// TestMetricsEndpoint_ReservationRoundTripsAreSeparate: a request against a
// budgeted key reserves, settles and (with a TPM limit) reads TPM, while the
// auth lookup stays its own measurement. This is the acceptance criterion that
// the PR #47 Redis round-trips do not contaminate the auth numbers.
func TestMetricsEndpoint_ReservationRoundTripsAreSeparate(t *testing.T) {
	e := newEnv(t, repo.KeyInput{BudgetType: "total", BudgetNanos: 1_000_000_000, TPMLimit: ptr(int32(1_000_000))})
	metricsReset(t)

	if w := e.do("POST", "/v1/chat/completions", e.raw, chatBody); w.Code != 200 {
		t.Fatalf("chat = %d %s", w.Code, w.Body)
	}
	doc := metricsDoc(t, e)
	ops := doc["ops"].(map[string]any)
	get := func(name string) float64 {
		m, ok := ops[name].(map[string]any)
		if !ok {
			t.Fatalf("ops has no %q: %v", name, ops)
		}
		return m["count"].(float64)
	}
	if get("auth_lookup") != 1 {
		t.Errorf("auth_lookup = %v, want 1", get("auth_lookup"))
	}
	// Key scope + group scope each hold a budget reservation, so Reserve runs
	// twice for cost plus twice for TPM (key and group both have tpm_limit).
	if got := get("reserve_add"); got < 1 {
		t.Errorf("reserve_add = %v, want >= 1; the budget reservation was not measured", got)
	}
	if got := get("reserve_settle"); got < 1 {
		t.Errorf("reserve_settle = %v, want >= 1; the settle was not measured", got)
	}
	if got := get("tpm_read"); got < 1 {
		t.Errorf("tpm_read = %v, want >= 1 with a TPM limit set", got)
	}
	// The two measurements must not be the same series.
	if get("auth_lookup") == get("reserve_add") && get("reserve_add") != 0 {
		t.Error("auth_lookup and reserve_add report the same count; they may be conflated")
	}
}

// TestMetricsEndpoint_RefundedRequestCountsRelease: a request refused after a
// reservation was taken must release it, and the release must be measured.
func TestMetricsEndpoint_RefundedRequestCountsRelease(t *testing.T) {
	// Budget of 1 nano-USD cannot cover any estimate, so admit refuses after
	// reserving; the release path then runs.
	e := newEnv(t, repo.KeyInput{BudgetType: "total", BudgetNanos: 1})
	metricsReset(t)

	if w := e.do("POST", "/v1/chat/completions", e.raw, chatBody); w.Code != 429 {
		t.Fatalf("chat = %d %s, want 429", w.Code, w.Body)
	}
	doc := metricsDoc(t, e)
	ops := doc["ops"].(map[string]any)
	if got := ops["reserve_release"].(map[string]any)["count"].(float64); got < 1 {
		t.Errorf("reserve_release = %v, want >= 1 after a refused request released its estimate", got)
	}
}

// TestMetricsEndpoint_NoAuthRequiredAndWrongMethod: the route is deliberately
// open (like /health) and is GET-only.
func TestMetricsEndpoint_NoAuthRequiredAndWrongMethod(t *testing.T) {
	e := newEnv(t, repo.KeyInput{})
	if w := e.do("GET", "/metrics", "", ""); w.Code != 200 {
		t.Errorf("anonymous GET /metrics = %d, want 200 (documented as unauthenticated)", w.Code)
	}
	if w := e.do("POST", "/metrics", e.raw, ""); w.Code != 405 {
		t.Errorf("POST /metrics = %d, want 405", w.Code)
	}
}

func ptr[T any](v T) *T { return &v }

// metricsReset zeroes the process-wide counters so a test can assert exact
// counts. Package-level metrics are shared by every test in this binary
// (including other api tests that make /v1 calls), so any assertion on a count
// must reset first.
func metricsReset(t *testing.T) {
	t.Helper()
	metrics.ResetForTest()
}

// TestMetrics_ShapeValidatorCatchesInjectedLeak is the mutation check for the
// leak assertion above. It takes a real, passing document and injects exactly
// the kind of addition the old string-grep tests could not see — a new series
// keyed by a hardcoded scope ID — then asserts the validator REJECTS it.
//
// Without this, TestMetricsEndpoint_UnauthenticatedAndAggregateOnly could be
// vacuous again tomorrow (a refactor that stops calling ValidateShape would
// leave it green). With it, the validator's teeth are themselves pinned.
func TestMetrics_ShapeValidatorCatchesInjectedLeak(t *testing.T) {
	e := newEnv(t, repo.KeyInput{})
	w := e.do("GET", "/metrics", "", "")
	var doc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if summary := metrics.ValidateShapeSummary(doc); summary != "" {
		t.Fatalf("baseline document should be clean, got:\n  %s", summary)
	}

	// Mutation 1: a new top-level field carrying a hardcoded UUID (the exact
	// string the reviewer injected; both old tests passed with it present).
	m1 := cloneDoc(t, doc)
	m1["leaked_scope"] = "3f2504e0-4f89-11d3-9a0c-0305e82c3301"
	if got := metrics.ValidateShape(m1); len(got) == 0 {
		t.Error("validator accepted an injected UUID under a new top-level key")
	}

	// Mutation 2: a new series inside ops, keyed by a scope ID rather than an Op.
	m2 := cloneDoc(t, doc)
	m2["ops"].(map[string]any)["key:3f2504e0-4f89-11d3-9a0c-0305e82c3301"] = map[string]any{}
	if got := metrics.ValidateShape(m2); len(got) == 0 {
		t.Error("validator accepted an unknown series name inside ops")
	}

	// Mutation 3: a raw API key smuggled into the existing free-text NOTE, which
	// a structural-only check that ignored prose would miss.
	m3 := cloneDoc(t, doc)
	m3["NOTE"] = "admit-path instrumentation (key tg-abcdef0123456789)"
	if got := metrics.ValidateShape(m3); len(got) == 0 {
		t.Error("validator accepted a tg- token inside the NOTE string")
	}

	// Mutation 4: a bare hex hash in a new field.
	m4 := cloneDoc(t, doc)
	m4["debug_key_hash"] = "9f2c1ab4de7f0c3b58a6d1e0f4b7c2a9"
	if got := metrics.ValidateShape(m4); len(got) == 0 {
		t.Error("validator accepted a bare hex hash in a new field")
	}
}

// cloneDoc deep-copies a decoded document via JSON so each mutation is
// independent of the others.
func cloneDoc(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestMetrics_ShapeValidatorRejectsMissingSeriesAndImplausibleNumbers covers
// the two gaps the round-2 review found in the validator: it checked the
// series that were PRESENT but never that all known series WERE present, and
// numeric fields were exempt from the identifier check with no backstop.
//
// Both matter for the same reason: each would let a real regression pass a
// green test. Dropping auth_lookup is exactly the series this change exists to
// publish; and a leak encoded as a number bypasses a string-shaped check.
func TestMetrics_ShapeValidatorRejectsMissingSeriesAndImplausibleNumbers(t *testing.T) {
	e := newEnv(t, repo.KeyInput{})
	var doc map[string]any
	if err := json.Unmarshal(e.do("GET", "/metrics", "", "").Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := metrics.ValidateShape(doc); len(got) != 0 {
		t.Fatalf("baseline should be clean, got %v", got)
	}

	// 1. Deleting the series this whole issue is about must be a violation.
	m := cloneDoc(t, doc)
	delete(m["ops"].(map[string]any), "auth_lookup")
	if got := metrics.ValidateShape(m); len(got) == 0 {
		t.Error("validator accepted a document with ops.auth_lookup deleted")
	}

	// 2. Same for the rate block, which feeds the qps trigger.
	m = cloneDoc(t, doc)
	delete(m["auth"].(map[string]any)["per_op_qps_60s"].(map[string]any), "auth_lookup")
	if got := metrics.ValidateShape(m); len(got) == 0 {
		t.Error("validator accepted a document missing auth.per_op_qps_60s.auth_lookup")
	}

	// 3. Dropping an arbitrary other series is caught too, not just auth_lookup.
	m = cloneDoc(t, doc)
	delete(m["ops"].(map[string]any), "reserve_settle")
	if got := metrics.ValidateShape(m); len(got) == 0 {
		t.Error("validator accepted a document with a known series deleted")
	}

	// 4. The exact numeric leaks the reviewer injected must now be rejected.
	for _, inject := range []struct {
		field string
		value float64
	}{
		{"count", 3060532441000},
		{"sum_ms", 123456789012345},
	} {
		m = cloneDoc(t, doc)
		m["ops"].(map[string]any)["auth_lookup"].(map[string]any)[inject.field] = inject.value
		if got := metrics.ValidateShape(m); len(got) == 0 {
			t.Errorf("validator accepted %s = %v, an identifier-shaped number", inject.field, inject.value)
		}
	}

	// 5. ...but legitimate large-ish counters must still pass, or the ceiling
	//    would fire on real traffic and the check would be useless.
	m = cloneDoc(t, doc)
	entry := m["ops"].(map[string]any)["auth_lookup"].(map[string]any)
	entry["count"] = 50_000_000.0
	entry["sum_ms"] = 900_000_000.0
	if got := metrics.ValidateShape(m); len(got) != 0 {
		t.Errorf("validator rejected plausible counters: %v", got)
	}
}
