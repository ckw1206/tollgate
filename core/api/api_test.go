package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"tollgate/core/cache"
	"tollgate/core/db"
	"tollgate/core/db/dbtest"
	"tollgate/core/db/repo"
	"tollgate/core/llm"
)

type fakeProvider struct {
	usage  llm.Usage
	chunks []string
	err    error
}

func (f *fakeProvider) Chat(context.Context, *llm.Model, *llm.ChatRequest) (*llm.Result, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &llm.Result{Body: []byte(`{"id":"x","choices":[]}`), Usage: f.usage}, nil
}

func (f *fakeProvider) ChatStream(_ context.Context, _ *llm.Model, _ *llm.ChatRequest, emit func([]byte) error) (llm.Usage, error) {
	for _, c := range f.chunks {
		emit([]byte(c)) // like real providers, keep going; the server decides what to do with write errors
	}
	return f.usage, f.err
}

func (f *fakeProvider) Embed(context.Context, *llm.Model, []byte) (*llm.Result, error) {
	return &llm.Result{Body: []byte(`{"data":[]}`), Usage: llm.Usage{Input: 3}}, nil
}

type env struct {
	db      *db.DB
	srv     *Server
	h       http.Handler
	store   *repo.Store
	user    *repo.User
	key     *repo.APIKey
	raw     string
	prov    *fakeProvider
	mu      sync.Mutex
	records []repo.SpendRecord
}

func newEnv(t *testing.T, keyIn repo.KeyInput) *env {
	d := dbtest.New(t)
	s := repo.New(d)
	ctx := context.Background()
	g, _ := s.GetGroupByName(ctx, "default")
	u, _ := s.CreateUser(ctx, "a@x.io", "A", "hunter22hunter22", "member", g.ID)
	keyIn.GroupID = g.ID
	if keyIn.BudgetType == "" {
		keyIn.BudgetType = "unlimited"
	}
	raw, k, err := s.CreateKey(ctx, u.ID, keyIn)
	if err != nil {
		t.Fatal(err)
	}
	mr := miniredis.RunT(t)
	e := &env{db: d, store: s, user: u, key: k, raw: raw, prov: &fakeProvider{usage: llm.Usage{Input: 1000, Output: 500}}}
	reg := llm.NewRegistry(map[string]llm.Provider{"openai": e.prov})
	reg.Replace([]*llm.Model{
		{ID: "11111111-1111-1111-1111-111111111111", Name: "fast", Provider: "openai", Prices: llm.Prices{InputMicros: 2_500_000, OutputMicros: 10_000_000}, ContextWindow: 128000},
		{ID: "22222222-2222-2222-2222-222222222222", Name: "slow", Provider: "openai"},
	})
	e.srv = &Server{Store: s, Cache: cache.FromClient(redis.NewClient(&redis.Options{Addr: mr.Addr()})), Registry: reg, Now: time.Now,
		Record: func(r repo.SpendRecord) { e.mu.Lock(); e.records = append(e.records, r); e.mu.Unlock() }}
	mux := http.NewServeMux()
	e.srv.Register(mux)
	e.h = mux
	return e
}

func (e *env) do(method, path, auth, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if auth != "" {
		r.Header.Set("Authorization", "Bearer "+auth)
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

const chatBody = `{"model":"fast","messages":[{"role":"user","content":"hi"}]}`

func errCode(t *testing.T, w *httptest.ResponseRecorder) string {
	var b struct{ Error struct{ Code string } }
	json.Unmarshal(w.Body.Bytes(), &b)
	return b.Error.Code
}

func TestAuth_MissingAndUnknown401(t *testing.T) {
	e := newEnv(t, repo.KeyInput{})
	for _, auth := range []string{"", "tg-nope"} {
		if w := e.do("POST", "/v1/chat/completions", auth, chatBody); w.Code != 401 || errCode(t, w) != "invalid_api_key" {
			t.Fatalf("auth=%q code=%d body=%s", auth, w.Code, w.Body)
		}
	}
}

func TestAuth_RevokedAndDisabledRejectedImmediately(t *testing.T) {
	e := newEnv(t, repo.KeyInput{})
	ctx := context.Background()
	if w := e.do("POST", "/v1/chat/completions", e.raw, chatBody); w.Code != 200 {
		t.Fatalf("baseline %d %s", w.Code, w.Body)
	}
	disabled := true
	e.store.UpdateUser(ctx, e.user.ID, repo.UserUpdate{Disabled: &disabled})
	if w := e.do("POST", "/v1/chat/completions", e.raw, chatBody); w.Code != 403 {
		t.Fatalf("disabled user: %d", w.Code)
	}
	enabled := false
	e.store.UpdateUser(ctx, e.user.ID, repo.UserUpdate{Disabled: &enabled})
	e.store.RevokeKey(ctx, e.key.ID)
	if w := e.do("POST", "/v1/chat/completions", e.raw, chatBody); w.Code != 403 || errCode(t, w) != "key_blocked" {
		t.Fatalf("revoked: %d %s", w.Code, w.Body)
	}
}

func TestAuth_Expired403(t *testing.T) {
	past := time.Now().Add(-time.Minute)
	e := newEnv(t, repo.KeyInput{ExpiresAt: &past})
	if w := e.do("POST", "/v1/chat/completions", e.raw, chatBody); w.Code != 403 || errCode(t, w) != "key_expired" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestChat_NonStream_RecordsCostAndHeader(t *testing.T) {
	e := newEnv(t, repo.KeyInput{})
	w := e.do("POST", "/v1/chat/completions", e.raw, chatBody)
	if w.Code != 200 || w.Header().Get("X-Tollgate-Cost") != "0.0075" {
		t.Fatalf("%d cost=%q", w.Code, w.Header().Get("X-Tollgate-Cost"))
	}
	if len(e.records) != 1 || e.records[0].CostNanos != 7_500_000 || e.records[0].Status != "success" || e.records[0].KeyID != e.key.ID {
		t.Fatalf("records=%+v", e.records)
	}
}

func TestChat_ModelChecks(t *testing.T) {
	e := newEnv(t, repo.KeyInput{Models: []string{"slow"}})
	if w := e.do("POST", "/v1/chat/completions", e.raw, chatBody); w.Code != 403 || errCode(t, w) != "model_not_allowed" {
		t.Fatalf("not allowed: %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/v1/chat/completions", e.raw, `{"model":"ghost","messages":[{"role":"user","content":"x"}]}`); w.Code != 404 {
		t.Fatalf("unknown: %d", w.Code)
	}
	if len(e.records) != 0 {
		t.Fatal("rejected requests must not be recorded")
	}
}

func TestChat_RPMLimit(t *testing.T) {
	one := int32(1)
	e := newEnv(t, repo.KeyInput{RPMLimit: &one})
	e.do("POST", "/v1/chat/completions", e.raw, chatBody)
	w := e.do("POST", "/v1/chat/completions", e.raw, chatBody)
	if w.Code != 429 || w.Header().Get("Retry-After") == "" || errCode(t, w) != "rate_limit_exceeded" {
		t.Fatalf("%d %v %s", w.Code, w.Header(), w.Body)
	}
}

func TestChat_TPMLimit(t *testing.T) {
	limit := int32(1000)
	e := newEnv(t, repo.KeyInput{TPMLimit: &limit})
	e.do("POST", "/v1/chat/completions", e.raw, chatBody) // uses 1500 tokens
	if w := e.do("POST", "/v1/chat/completions", e.raw, chatBody); w.Code != 429 {
		t.Fatalf("%d", w.Code)
	}
}

func TestChat_BudgetExceeded(t *testing.T) {
	e := newEnv(t, repo.KeyInput{BudgetType: "total", BudgetNanos: 1})
	e.db.Pool.Exec(context.Background(), `UPDATE api_keys SET spend_nanos = 1`)
	if w := e.do("POST", "/v1/chat/completions", e.raw, chatBody); w.Code != 429 || errCode(t, w) != "budget_exceeded" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestChat_UpstreamErrorRecorded(t *testing.T) {
	e := newEnv(t, repo.KeyInput{})
	e.prov.err = &llm.UpstreamError{Status: 503, Message: "overloaded"}
	w := e.do("POST", "/v1/chat/completions", e.raw, chatBody)
	if w.Code != 503 || errCode(t, w) != "upstream_error" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if len(e.records) != 1 || e.records[0].Status != "error" || e.records[0].HTTPStatus != 503 {
		t.Fatalf("records=%+v", e.records)
	}
}

func TestChat_BadRequestFromProvider400NotRecorded(t *testing.T) {
	e := newEnv(t, repo.KeyInput{})
	e.prov.err = llm.ErrBadRequest
	if w := e.do("POST", "/v1/chat/completions", e.raw, chatBody); w.Code != 400 {
		t.Fatalf("%d", w.Code)
	}
	if len(e.records) != 0 {
		t.Fatal("recorded a 400")
	}
}

func TestChat_Stream(t *testing.T) {
	e := newEnv(t, repo.KeyInput{})
	e.prov.chunks = []string{`{"a":1}`, `{"a":2}`}
	w := e.do("POST", "/v1/chat/completions", e.raw, `{"model":"fast","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if w.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("ct=%q", w.Header().Get("Content-Type"))
	}
	if w.Body.String() != "data: {\"a\":1}\n\ndata: {\"a\":2}\n\ndata: [DONE]\n\n" {
		t.Fatalf("body=%q", w.Body)
	}
	if len(e.records) != 1 || e.records[0].CostNanos != 7_500_000 {
		t.Fatalf("records=%+v", e.records)
	}
}

// failingWriter accepts the first write, then behaves like a disconnected client.
type failingWriter struct {
	*httptest.ResponseRecorder
	writes int
}

func (f *failingWriter) Write(b []byte) (int, error) {
	f.writes++
	if f.writes > 1 {
		return 0, errors.New("client gone")
	}
	return f.ResponseRecorder.Write(b)
}

func TestChat_StreamClientDisconnect_StillRecords(t *testing.T) {
	e := newEnv(t, repo.KeyInput{})
	e.prov.chunks = []string{`{"a":1}`, `{"a":2}`, `{"a":3}`}
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"fast","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	r.Header.Set("Authorization", "Bearer "+e.raw)
	w := &failingWriter{ResponseRecorder: httptest.NewRecorder()}
	e.h.ServeHTTP(w, r)
	if len(e.records) != 1 {
		t.Fatalf("records=%d", len(e.records))
	}
	got := e.records[0]
	if got.CostNanos != 7_500_000 || got.Status != "error" || !strings.Contains(got.ErrorMessage, "client disconnected") {
		t.Fatalf("record=%+v", got)
	}
}

func TestModels_FiltersByAllowList(t *testing.T) {
	e := newEnv(t, repo.KeyInput{Models: []string{"slow"}})
	w := e.do("GET", "/v1/models", e.raw, "")
	var out struct{ Data []struct{ ID string } }
	json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != 200 || len(out.Data) != 1 || out.Data[0].ID != "slow" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

// TestModels_ContextWindow: /v1/models emits context_window only for models that have one.
// The unset model must omit the key entirely (never null, never 0).
func TestModels_ContextWindow(t *testing.T) {
	e := newEnv(t, repo.KeyInput{})
	w := e.do("GET", "/v1/models", e.raw, "")
	var out struct {
		Data []struct {
			ID            string
			ContextWindow *int64 `json:"context_window"`
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("%d %s: %v", w.Code, w.Body, err)
	}
	if w.Code != 200 || len(out.Data) != 2 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	byID := map[string]*struct {
		ID            string
		ContextWindow *int64 `json:"context_window"`
	}{}
	for i := range out.Data {
		byID[out.Data[i].ID] = &out.Data[i]
	}
	fast, slow := byID["fast"], byID["slow"]
	if fast == nil || slow == nil {
		t.Fatalf("missing entries: %s", w.Body)
	}
	if fast.ContextWindow == nil || *fast.ContextWindow != 128000 {
		t.Fatalf("fast context_window=%v, want 128000", fast.ContextWindow)
	}
	if slow.ContextWindow != nil {
		t.Fatalf("slow context_window=%v, want omitted (nil)", *slow.ContextWindow)
	}
	// Raw-body sanity: fast carries the value and is the only entry that does — the
	// key appears exactly once, so the unset model (slow) omits it entirely (never null/0).
	body := w.Body.String()
	if !strings.Contains(body, `"context_window":128000`) {
		t.Fatalf("fast raw missing value: %s", body)
	}
	if n := strings.Count(body, `"context_window"`); n != 1 {
		t.Fatalf("context_window appears %d times, want 1 (only fast): %s", n, body)
	}
}

func TestModelAccess_GroupUnionExtrasThenKey(t *testing.T) {
	e := newEnv(t, repo.KeyInput{})
	ctx := context.Background()
	list := func(names ...string) *[]string { l := append([]string{}, names...); return &l }
	setGroup := func(names ...string) {
		if _, err := e.store.UpdateGroup(ctx, e.user.GroupID, repo.GroupInput{Name: "default", BudgetType: "unlimited", Models: list(names...)}); err != nil {
			t.Fatal(err)
		}
	}
	setExtra := func(names ...string) {
		if _, err := e.store.UpdateUser(ctx, e.user.ID, repo.UserUpdate{ExtraModels: list(names...)}); err != nil {
			t.Fatal(err)
		}
	}
	chat := func(raw, model string) *httptest.ResponseRecorder {
		return e.do("POST", "/v1/chat/completions", raw, `{"model":"`+model+`","messages":[{"role":"user","content":"hi"}]}`)
	}
	listed := func(raw string) (ids []string) {
		var out struct{ Data []struct{ ID string } }
		json.Unmarshal(e.do("GET", "/v1/models", raw, "").Body.Bytes(), &out)
		for _, m := range out.Data {
			ids = append(ids, m.ID)
		}
		return ids
	}

	// group [] = all, extras [] = none
	if w := chat(e.raw, "fast"); w.Code != 200 || len(listed(e.raw)) != 2 {
		t.Fatalf("all: %d %v", w.Code, listed(e.raw))
	}
	n := len(e.records)
	setGroup("slow")
	w := chat(e.raw, "fast")
	if w.Code != 403 || errCode(t, w) != "model_not_allowed" || !strings.Contains(w.Body.String(), "its owner or the owner's group") {
		t.Fatalf("group excludes fast: %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/v1/embeddings", e.raw, `{"model":"fast","input":"hi"}`); w.Code != 403 || errCode(t, w) != "model_not_allowed" {
		t.Fatalf("embeddings: %d %s", w.Code, w.Body)
	}
	if got := listed(e.raw); len(got) != 1 || got[0] != "slow" {
		t.Fatalf("/v1/models: %v", got)
	}
	if len(e.records) != n {
		t.Fatal("refused requests must not be recorded")
	}
	// an extra grant applies on the next request: the per-request lookup has no cache
	setExtra("fast")
	if w := chat(e.raw, "fast"); w.Code != 200 || len(listed(e.raw)) != 2 {
		t.Fatalf("granted: %d %v", w.Code, listed(e.raw))
	}

	// a key narrows but can't widen
	setExtra()
	narrow, _, _ := e.store.CreateKey(ctx, e.user.ID, repo.KeyInput{Alias: "n", GroupID: e.user.GroupID, BudgetType: "unlimited", Models: []string{"fast"}})
	if w := chat(narrow, "fast"); w.Code != 403 {
		t.Fatalf("key widened past the group: %d", w.Code)
	}
	if w := chat(narrow, "slow"); w.Code != 403 || len(listed(narrow)) != 0 {
		t.Fatalf("key narrowing: %d %v", w.Code, listed(narrow))
	}

	// unknown or disabled (not in the registry) stays 404, even when granted
	setGroup("ghost")
	setExtra("ghost")
	if w := chat(e.raw, "ghost"); w.Code != 404 || errCode(t, w) != "model_not_found" || len(listed(e.raw)) != 0 {
		t.Fatalf("disabled: %d %s %v", w.Code, w.Body, listed(e.raw))
	}

	// the owner's group governs, not the key's billing group
	team, _ := e.store.CreateGroup(ctx, repo.GroupInput{Name: "team", BudgetType: "unlimited", Models: list("slow")})
	setGroup()
	e.store.UpdateUser(ctx, e.user.ID, repo.UserUpdate{GroupID: &team.ID})
	if w := chat(e.raw, "fast"); w.Code != 403 {
		t.Fatalf("billing group granted access: %d", w.Code)
	}
}

func TestEmbeddings(t *testing.T) {
	e := newEnv(t, repo.KeyInput{})
	w := e.do("POST", "/v1/embeddings", e.raw, `{"model":"fast","input":"hi"}`)
	if w.Code != 200 || len(e.records) != 1 || e.records[0].InputTokens != 3 {
		t.Fatalf("%d %s %+v", w.Code, w.Body, e.records)
	}
}

func TestHealth(t *testing.T) {
	e := newEnv(t, repo.KeyInput{})
	w := e.do("GET", "/health", "", "")
	b, _ := io.ReadAll(w.Body)
	if w.Code != 200 || !strings.Contains(string(b), "ok") {
		t.Fatalf("%d %s", w.Code, b)
	}
}
