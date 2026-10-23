package consoleapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"tollgate/core/db/repo"
)

func ptr[T any](v T) *T { return &v }

// countingUpstream is an httptest server that reports max_model_len per entry, the shape
// SGLang serves, and counts how many times the probe hit it. status <= 0 means make the
// request hang until the test ends.
func countingUpstream(t *testing.T, body string, status int) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if status > 0 && status != 200 {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(up.Close)
	return up, &hits
}

// modelBody is the create/update payload for an openai model pointing at apiBase.
func modelBody(name, upstream, apiBase string, cw *int64) map[string]any {
	b := map[string]any{"name": name, "provider": "openai", "upstream_model": upstream,
		"api_base": apiBase, "enabled": true, "price_input_micros": 1, "price_output_micros": 1}
	if cw != nil {
		b["context_window"] = *cw
	}
	return b
}

// TestModels_ContextWindowExceedsUpstreamMax replays the incident: the row declared 1000000
// while the upstream served 32768. The save must be rejected naming both numbers, and
// nothing may be written.
func TestModels_ContextWindowExceedsUpstreamMax(t *testing.T) {
	e := newTenv(t)
	const upstream = "DeepSeek-V4.1-Flash"
	up, hits := countingUpstream(t, fmt.Sprintf(`{"data":[{"id":%q,"max_model_len":32768}]}`, upstream), 200)

	before, err := e.store.ListModels(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	tooBig := int64(1000000)
	w := e.as("admin@x.io", "POST", "/console/api/v1/models", modelBody("deepseek", upstream, up.URL+"/v1", &tooBig))
	if w.Code != 400 {
		t.Fatalf("create: %d %s, want 400", w.Code, w.Body)
	}
	if msg := w.Body.String(); !strings.Contains(msg, "1000000") || !strings.Contains(msg, "32768") {
		t.Fatalf("message must name both numbers: %s", msg)
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("upstream probed %d times, want 1", n)
	}
	after, err := e.store.ListModels(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("row count changed %d -> %d after a rejected create", len(before), len(after))
	}
	for _, m := range after {
		if m.Name == "deepseek" {
			t.Fatalf("rejected model was persisted: %+v", m)
		}
	}

	// The guard also runs on update: seed a row at the real limit, then raise it past it.
	atLimit := int64(32768)
	seed := e.as("admin@x.io", "POST", "/console/api/v1/models",
		modelBody("deepseek", upstream, up.URL+"/v1", &atLimit))
	created := decodeInto[repo.Model](t, seed)
	if seed.Code != 201 || created.ContextWindow == nil || *created.ContextWindow != 32768 {
		t.Fatalf("seed create: %d %+v", seed.Code, created)
	}
	w = e.as("admin@x.io", "PUT", "/console/api/v1/models/"+created.ID,
		modelBody("deepseek", upstream, up.URL+"/v1", &tooBig))
	if w.Code != 400 || !strings.Contains(w.Body.String(), "1000000") || !strings.Contains(w.Body.String(), "32768") {
		t.Fatalf("update: %d %s, want 400 naming both numbers", w.Code, w.Body)
	}
	stored, err := e.store.GetModel(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ContextWindow == nil || *stored.ContextWindow != 32768 {
		t.Fatalf("rejected update altered the row: %+v", stored.ContextWindow)
	}
}

// TestModels_ContextWindowAllowed covers every save that must go through: at or under the
// upstream maximum, no declared value at all (no upstream call), an upstream that reports
// no limit, an upstream error, and an upstream that is down (fail open).
func TestModels_ContextWindowAllowed(t *testing.T) {
	// An unused port: the server is closed, so the probe fails at the transport level.
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	for name, tc := range map[string]struct {
		body      string
		status    int    // upstream status; 0 = hang, 200 = serve body
		cw        *int64 // declared context_window; nil = omitted
		upstream  string // upstream_model to declare
		listed    string // id the upstream lists (defaults to upstream)
		apiBase   string // "" = this test's upstream
		wantCalls int64
	}{
		"exactly at the limit": {body: `{"data":[{"id":"m","max_model_len":32768}]}`, status: 200, cw: ptr(int64(32768)), wantCalls: 1},
		"under the limit":      {body: `{"data":[{"id":"m","max_model_len":32768}]}`, status: 200, cw: ptr(int64(4096)), wantCalls: 1},
		"no declared value":    {body: `{"data":[{"id":"m","max_model_len":32768}]}`, status: 200, cw: nil, wantCalls: 0},
		"no limit in response": {body: `{"data":[{"id":"m"}]}`, status: 200, cw: ptr(int64(1000000)), wantCalls: 1},
		"limit null":           {body: `{"data":[{"id":"m","max_model_len":null}]}`, status: 200, cw: ptr(int64(1000000)), wantCalls: 1},
		"limit not a number":   {body: `{"data":[{"id":"m","max_model_len":"32768"}]}`, status: 200, cw: ptr(int64(1000000)), wantCalls: 1},
		"id mismatch":          {body: `{"data":[{"id":"other","max_model_len":32768}]}`, status: 200, cw: ptr(int64(1000000)), wantCalls: 1},
		"upstream 500":         {status: 500, cw: ptr(int64(1000000)), wantCalls: 1},
		"upstream 401":         {status: 401, cw: ptr(int64(1000000)), wantCalls: 1},
		"non-JSON body":        {body: `<html>nope</html>`, status: 200, cw: ptr(int64(1000000)), wantCalls: 1},
		"upstream unreachable": {cw: ptr(int64(1000000)), apiBase: deadURL + "/v1", wantCalls: 0},
	} {
		t.Run(name, func(t *testing.T) {
			e := newTenv(t)
			base, upstream := tc.apiBase, tc.upstream
			if upstream == "" {
				upstream = "m"
			}
			var hits *atomic.Int64
			if base == "" {
				up, h := countingUpstream(t, tc.body, tc.status)
				hits, base = h, up.URL+"/v1"
			}
			w := e.as("admin@x.io", "POST", "/console/api/v1/models", modelBody("m", upstream, base, tc.cw))
			if w.Code != 201 {
				t.Fatalf("%d %s, want 201 (fail open)", w.Code, w.Body)
			}
			if hits != nil {
				if n := hits.Load(); n != tc.wantCalls {
					t.Fatalf("upstream probed %d times, want %d", n, tc.wantCalls)
				}
			}
			got := decodeInto[repo.Model](t, w)
			if tc.cw == nil {
				if got.ContextWindow != nil {
					t.Fatalf("ContextWindow=%v, want nil", *got.ContextWindow)
				}
			} else if got.ContextWindow == nil || *got.ContextWindow != *tc.cw {
				t.Fatalf("ContextWindow=%v, want %d", got.ContextWindow, *tc.cw)
			}
		})
	}
}

// TestModels_ContextWindowProbeUsesCredential checks the probe resolves the endpoint and
// secret the way the registry does: the credential's api_base wins over the model's, and the
// saved secret is sent. A model whose credential's upstream can't serve the declared limit
// is rejected even though the model carries no api_base of its own.
func TestModels_ContextWindowProbeUsesCredential(t *testing.T) {
	e := newTenv(t)
	const key = "probe-key-0123456789"
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(401)
			w.Write([]byte(`{"error":{"message":"invalid key ` + key + `"}}`))
			return
		}
		w.Write([]byte(`{"data":[{"id":"DeepSeek-V4.1-Flash","max_model_len":32768}]}`))
	}))
	defer up.Close()

	c := decodeInto[repo.Credential](t, e.as("admin@x.io", "POST", "/console/api/v1/credentials",
		map[string]any{"name": "sglang", "provider": "openai", "api_base": up.URL + "/v1", "secret": key}))

	body := map[string]any{"name": "ds", "provider": "openai", "upstream_model": "DeepSeek-V4.1-Flash",
		"credential_id": c.ID, "enabled": true, "context_window": int64(1000000)}
	w := e.as("admin@x.io", "POST", "/console/api/v1/models", body)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "32768") {
		t.Fatalf("credential endpoint: %d %s, want 400 naming 32768", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), key) {
		t.Fatalf("response leaked the secret: %s", w.Body)
	}
	// A value the upstream does serve is accepted, still via the credential's endpoint.
	body["context_window"] = int64(32768)
	if w := e.as("admin@x.io", "POST", "/console/api/v1/models", body); w.Code != 201 {
		t.Fatalf("at the limit via credential: %d %s", w.Code, w.Body)
	}
}

// TestModels_ContextWindowProbeTimeout pins the fail-open timeout path: a hung upstream
// delays the save by the timeout but must not block it. The upstream handler records that
// the probe arrived and blocks until the test releases it, so the assertions below
// distinguish "waited for the probe and gave up at the deadline" from "never probed".
func TestModels_ContextWindowProbeTimeout(t *testing.T) {
	e := newTenv(t)
	// The handler signals each arrival, then blocks well past the shortened timeout below.
	arrived := make(chan struct{}, 1)
	release := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case arrived <- struct{}{}:
		default:
		}
		<-release
	}))
	defer func() { close(release); up.Close() }()

	old := contextWindowTimeout
	contextWindowTimeout = 50 * time.Millisecond
	defer func() { contextWindowTimeout = old }()

	cw := int64(1000000)
	start := time.Now()
	w := e.as("admin@x.io", "POST", "/console/api/v1/models", modelBody("hang", "m", up.URL+"/v1", &cw))
	elapsed := time.Since(start)

	if w.Code != 201 {
		t.Fatalf("hung upstream: %d %s, want 201 (fail open)", w.Code, w.Body)
	}
	// Evidence the timeout path was actually taken: the probe really was issued...
	select {
	case <-arrived:
	default:
		t.Fatal("upstream was never probed; the save did not exercise the timeout path")
	}
	// ...and the save waited on it, then gave up at the deadline rather than hanging. The
	// handler holds the connection for far longer, so an elapsed time near the 50ms deadline
	// can only come from the bound context. The lower bound also proves the probe was awaited
	// instead of being skipped for an unrelated reason (e.g. an unparsable api_base).
	if elapsed < 40*time.Millisecond || elapsed > 5*time.Second {
		t.Fatalf("save returned after %s; want ~50ms (probe awaited and bounded)", elapsed)
	}
}

// TestModels_ContextWindowUnchangedSkipsProbe pins the escape hatch that keeps a row whose
// stored context_window is already too big from becoming unmanageable. The console's
// Enable/Disable toggle re-sends the whole form (web/src/pages/Models.tsx), so guarding the
// unchanged value would make that row impossible to switch off — exactly when an operator
// most needs to. An unchanged value must pass with NO upstream call; a real edit still probes.
func TestModels_ContextWindowUnchangedSkipsProbe(t *testing.T) {
	e := newTenv(t)
	const upstream = "DeepSeek-V4.1-Flash"
	up, hits := countingUpstream(t, fmt.Sprintf(`{"data":[{"id":%q,"max_model_len":32768}]}`, upstream), 200)

	// Seed the prod state directly: already over the upstream's limit.
	tooBig := int64(1000000)
	seeded, err := e.store.CreateModel(t.Context(), repo.ModelInput{
		Name: "deepseek", Provider: "openai", UpstreamModel: upstream,
		APIBase: up.URL + "/v1", Enabled: true, PriceInputMicros: 1, PriceOutputMicros: 1,
		ContextWindow: &tooBig})
	if err != nil {
		t.Fatal(err)
	}

	// What the toggle sends: the whole form with the stored window and enabled flipped.
	body := modelBody("deepseek", upstream, up.URL+"/v1", &tooBig)
	body["enabled"] = false
	w := e.as("admin@x.io", "PUT", "/console/api/v1/models/"+seeded.ID, body)
	if w.Code != 200 {
		t.Fatalf("disable with an unchanged window: %d %s, want 200", w.Code, w.Body)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("unchanged window probed the upstream %d times, want 0", n)
	}
	stored, err := e.store.GetModel(t.Context(), seeded.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Enabled {
		t.Fatal("model is still enabled after the toggle")
	}

	// A genuine edit — a NEW value still over the limit — is still rejected, and probed.
	newCW := int64(2000000)
	body = modelBody("deepseek", upstream, up.URL+"/v1", &newCW)
	body["enabled"] = false
	if w := e.as("admin@x.io", "PUT", "/console/api/v1/models/"+seeded.ID, body); w.Code != 400 {
		t.Fatalf("changed value over the limit: %d %s, want 400", w.Code, w.Body)
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("a changed window probed %d times, want 1", n)
	}
	// And lowering it to a served value is accepted.
	ok := int64(32768)
	body = modelBody("deepseek", upstream, up.URL+"/v1", &ok)
	body["enabled"] = true
	if w := e.as("admin@x.io", "PUT", "/console/api/v1/models/"+seeded.ID, body); w.Code != 200 {
		t.Fatalf("lowering to the served limit: %d %s, want 200", w.Code, w.Body)
	}
}

// TestModels_ContextWindowRepointedUpstreamIsProbed guards the no-op shortcut in
// contextWindowFits: it must require the served model's identity to be unchanged, not just
// the context_window. Repointing upstream_model at a server with a smaller limit is a NEW
// claim about the limit even when the window is byte-identical, and honouring the shortcut
// there would let a row be rewritten straight back into the incident this guards against.
func TestModels_ContextWindowRepointedUpstreamIsProbed(t *testing.T) {
	e := newTenv(t)
	big, _ := countingUpstream(t, `{"data":[{"id":"BIG","max_model_len":32768}]}`, 200)
	small, hits := countingUpstream(t, `{"data":[{"id":"SMALL","max_model_len":4096}]}`, 200)

	cw := int64(1000000)
	seeded, err := e.store.CreateModel(t.Context(), repo.ModelInput{
		Name: "ds", Provider: "openai", UpstreamModel: "BIG", APIBase: big.URL + "/v1",
		Enabled: true, PriceInputMicros: 1, PriceOutputMicros: 1, ContextWindow: &cw})
	if err != nil {
		t.Fatal(err)
	}

	// Same context_window, new upstream_model: must probe and reject, not short-circuit.
	body := modelBody("ds", "SMALL", small.URL+"/v1", &cw)
	w := e.as("admin@x.io", "PUT", "/console/api/v1/models/"+seeded.ID, body)
	if w.Code != 400 {
		t.Fatalf("repointed upstream: %d %s, want 400", w.Code, w.Body)
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("repointed upstream probed %d times, want 1", n)
	}
	stored, err := e.store.GetModel(t.Context(), seeded.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.UpstreamModel != "BIG" {
		t.Fatalf("rejected repoint altered the row: upstream_model=%s", stored.UpstreamModel)
	}
}

// TestModels_ContextWindowProviderChangeIsProbed pins the other half of the no-op shortcut:
// `provider` must be part of the upstream-identity comparison. probeRequest switches on it
// (different path, different auth header), so the two shapes can report different limits and
// a provider change is a new claim about the limit even at an identical context_window.
func TestModels_ContextWindowProviderChangeIsProbed(t *testing.T) {
	e := newTenv(t)
	// google is reached with x-goog-api-key on /models?pageSize=1000; openai with Bearer on
	// /models. Serve a smaller limit on the google shape so the switch is observable.
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lim := 32768
		if r.Header.Get("x-goog-api-key") != "" {
			lim = 4096
		}
		fmt.Fprintf(w, `{"data":[{"id":"m","max_model_len":%d}]}`, lim)
	}))
	defer up.Close()

	// A google credential, so credentialFits accepts either provider on the row.
	cred := decodeInto[repo.Credential](t, e.as("admin@x.io", "POST", "/console/api/v1/credentials",
		map[string]any{"name": "cg", "provider": "google", "api_base": up.URL + "/v1", "secret": "k"}))

	cw := int64(32768)
	seeded, err := e.store.CreateModel(t.Context(), repo.ModelInput{
		Name: "m", Provider: "openai", UpstreamModel: "m", CredentialID: &cred.ID,
		Enabled: true, PriceInputMicros: 1, PriceOutputMicros: 1, ContextWindow: &cw})
	if err != nil {
		t.Fatal(err)
	}

	// Flip ONLY the provider; upstream_model, credential_id and context_window are identical.
	body := map[string]any{"name": "m", "provider": "google", "upstream_model": "m",
		"credential_id": cred.ID, "enabled": true, "price_input_micros": 1,
		"price_output_micros": 1, "context_window": cw}
	w := e.as("admin@x.io", "PUT", "/console/api/v1/models/"+seeded.ID, body)
	if w.Code != 400 {
		t.Fatalf("provider flip at a constant window: %d %s, want 400", w.Code, w.Body)
	}
	stored, err := e.store.GetModel(t.Context(), seeded.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Provider != "openai" {
		t.Fatalf("rejected provider flip altered the row: provider=%s", stored.Provider)
	}
}

// TestModels_ContextWindowExtraChangeIsProbed covers the third half of the no-op shortcut:
// extra.headers ride on every real chat request (each provider copies them in), so a
// deployment that routes on a header serves a different model when that header changes —
// while probeRequest would still reach the old one. An extra change must force a probe.
//
// It also pins the opposite direction, which is easy to get wrong: Postgres hands extra back
// with its own spacing ({"a": 1} vs {"a":1}), so comparing the raw bytes would treat every
// unchanged multi-key extra as a change and probe on every save, breaking the Enable/Disable
// toggle the shortcut exists to protect. The comparison is by value, and NULL == {}.
func TestModels_ContextWindowExtraChangeIsProbed(t *testing.T) {
	e := newTenv(t)
	up, hits := countingUpstream(t, `{"data":[{"id":"m","max_model_len":32768}]}`, 200)

	cw := int64(1000000)
	multi, err := json.Marshal(map[string]any{"vertex_location": "us-central1",
		"headers": map[string]string{"X-Route": "big"}})
	if err != nil {
		t.Fatal(err)
	}
	seeded, err := e.store.CreateModel(t.Context(), repo.ModelInput{
		Name: "m", Provider: "openai", UpstreamModel: "m", APIBase: up.URL + "/v1",
		Enabled: true, PriceInputMicros: 1, PriceOutputMicros: 1, ContextWindow: &cw, Extra: multi})
	if err != nil {
		t.Fatal(err)
	}

	// Changing only the routing header must probe and reject.
	changed := modelBody("m", "m", up.URL+"/v1", &cw)
	changed["extra"] = map[string]any{"vertex_location": "us-central1",
		"headers": map[string]string{"X-Route": "small"}}
	hits.Store(0)
	if w := e.as("admin@x.io", "PUT", "/console/api/v1/models/"+seeded.ID, changed); w.Code != 400 {
		t.Fatalf("extra change at a constant window: %d %s, want 400", w.Code, w.Body)
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("extra change probed %d times, want 1", n)
	}

	// Re-sending the SAME extra in a different key order is not a change: the toggle must
	// still short-circuit with no probe.
	same := modelBody("m", "m", up.URL+"/v1", &cw)
	same["extra"] = map[string]any{"headers": map[string]string{"X-Route": "big"},
		"vertex_location": "us-central1"}
	same["enabled"] = false
	hits.Store(0)
	if w := e.as("admin@x.io", "PUT", "/console/api/v1/models/"+seeded.ID, same); w.Code != 200 {
		t.Fatalf("identical extra reordered: %d %s, want 200", w.Code, w.Body)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("identical extra probed %d times, want 0", n)
	}
	stored, err := e.store.GetModel(t.Context(), seeded.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Enabled {
		t.Fatal("toggle did not disable the row")
	}

	// A row with no extra (NULL) saved with {} is not a change either.
	noExtra, err := e.store.CreateModel(t.Context(), repo.ModelInput{
		Name: "n", Provider: "openai", UpstreamModel: "m", APIBase: up.URL + "/v1",
		Enabled: true, PriceInputMicros: 1, PriceOutputMicros: 1, ContextWindow: &cw})
	if err != nil {
		t.Fatal(err)
	}
	empty := modelBody("n", "m", up.URL+"/v1", &cw)
	empty["extra"] = map[string]any{}
	empty["enabled"] = false
	hits.Store(0)
	if w := e.as("admin@x.io", "PUT", "/console/api/v1/models/"+noExtra.ID, empty); w.Code != 200 {
		t.Fatalf("NULL extra re-sent as {}: %d %s, want 200", w.Code, w.Body)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("NULL extra re-sent as {} probed %d times, want 0", n)
	}
}

// TestModels_ContextWindowMutationGuards keeps the console mutation convention intact on the
// routes this change touches: a session is required, a foreign Origin is refused, and the
// endpoint stays private-host reachable (no publicHostCheck).
func TestModels_ContextWindowMutationGuards(t *testing.T) {
	e := newTenv(t)
	cw := int64(1000000)
	// A private upstream must remain saveable: the guard deliberately does not apply
	// publicHostCheck, or a self-hosted model on an RFC1918 address could not be saved. The
	// probe just fails open here because nothing serves that address.
	body := modelBody("private", "m", "http://10.0.0.7:8080/v1", &cw)
	if w := e.as("admin@x.io", "POST", "/console/api/v1/models", body); w.Code != 201 {
		t.Fatalf("private upstream: %d %s, want 201", w.Code, w.Body)
	}
	// Anonymous is refused.
	if w := e.as("", "POST", "/console/api/v1/models", body); w.Code != 401 {
		t.Fatalf("anonymous create: %d, want 401", w.Code)
	}
	// A foreign Origin is rejected by the CSRF guard before any handler runs.
	r := httptest.NewRequest("POST", "/console/api/v1/models", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	if rec.Code != 403 {
		t.Fatalf("foreign origin: %d, want 403", rec.Code)
	}
}
