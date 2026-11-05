package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
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
	during func(ctx context.Context) // runs mid-stream with the upstream ctx
}

func (f *fakeProvider) Chat(ctx context.Context, _ *llm.Model, _ *llm.ChatRequest) (*llm.Result, error) {
	if f.during != nil {
		f.during(ctx)
		if err := ctx.Err(); err != nil {
			return nil, &llm.UpstreamError{Status: 502, Message: err.Error()} // like post(): the cancel is flattened
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	return &llm.Result{Body: []byte(`{"id":"x","choices":[]}`), Usage: f.usage}, nil
}

func (f *fakeProvider) ChatStream(ctx context.Context, _ *llm.Model, _ *llm.ChatRequest, emit func([]byte) error) (llm.Usage, error) {
	if f.during != nil {
		f.during(ctx)
		if err := ctx.Err(); err != nil { // like post(): a cancelled ctx aborts, flattened to an UpstreamError
			return llm.Usage{}, &llm.UpstreamError{Status: 502, Message: err.Error()}
		}
	}
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

const streamBody = `{"model":"fast","stream":true,"messages":[{"role":"user","content":"hi"}]}`

// A slow first token (a long prefill) must not leave the connection silent:
// once Keepalive passes with nothing sent, the stream starts and SSE comments go
// out, so a reverse proxy's read timeout doesn't cut the request.
func TestChat_StreamKeepalive_SlowFirstToken(t *testing.T) {
	e := newEnv(t, repo.KeyInput{})
	e.srv.Keepalive = 10 * time.Millisecond
	e.prov.chunks = []string{`{"a":1}`}
	e.prov.during = func(context.Context) { time.Sleep(60 * time.Millisecond) }
	w := e.do("POST", "/v1/chat/completions", e.raw, streamBody)
	body := w.Body.String()
	if w.Code != 200 || w.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("code=%d ct=%q", w.Code, w.Header().Get("Content-Type"))
	}
	if !strings.HasPrefix(body, ": keepalive\n\n") || strings.Count(body, ": keepalive\n\n") < 2 ||
		!strings.HasSuffix(body, "data: {\"a\":1}\n\ndata: [DONE]\n\n") {
		t.Fatalf("body=%q", body)
	}
	if len(e.records) != 1 || e.records[0].Status != "success" || e.records[0].HTTPStatus != 200 || e.records[0].CostNanos != 7_500_000 {
		t.Fatalf("records=%+v", e.records)
	}
}

// An upstream error after keepalives went out can't change the status line: it
// goes out as an SSE error event and is recorded with HTTP 200, like any other
// mid-stream failure.
func TestChat_StreamKeepalive_ErrorAfterStart(t *testing.T) {
	e := newEnv(t, repo.KeyInput{})
	e.srv.Keepalive = 10 * time.Millisecond
	e.prov.usage = llm.Usage{}
	e.prov.err = &llm.UpstreamError{Status: 503, Message: "overloaded"}
	e.prov.during = func(context.Context) { time.Sleep(40 * time.Millisecond) }
	w := e.do("POST", "/v1/chat/completions", e.raw, streamBody)
	body := w.Body.String()
	if w.Code != 200 || !strings.HasPrefix(body, ": keepalive\n\n") || !strings.Contains(body, `"code":"upstream_error"`) || strings.Contains(body, "[DONE]") {
		t.Fatalf("code=%d body=%q", w.Code, body)
	}
	if len(e.records) != 1 || e.records[0].Status != "error" || e.records[0].HTTPStatus != 200 || !strings.Contains(e.records[0].ErrorMessage, "overloaded") {
		t.Fatalf("records=%+v", e.records)
	}
}

// A fast failure still gets its real status code: nothing is written before
// the first keepalive is due.
func TestChat_StreamFastError_KeepsStatus(t *testing.T) {
	e := newEnv(t, repo.KeyInput{})
	e.prov.err = &llm.UpstreamError{Status: 503, Message: "overloaded"}
	w := e.do("POST", "/v1/chat/completions", e.raw, streamBody)
	if w.Code != 503 || errCode(t, w) != "upstream_error" || strings.Contains(w.Body.String(), "keepalive") {
		t.Fatalf("code=%d body=%q", w.Code, w.Body)
	}
}

// A client that drops while only keepalives have been sent is detected by the
// failed keepalive write (the upstream emits no chunks, so nothing else could
// notice); a priced model still drains and bills.
func TestChat_StreamKeepalive_ClientGone(t *testing.T) {
	e := newEnv(t, repo.KeyInput{})
	e.srv.Keepalive = 10 * time.Millisecond
	e.prov.during = func(context.Context) { time.Sleep(60 * time.Millisecond) }
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(streamBody))
	r.Header.Set("Authorization", "Bearer "+e.raw)
	w := &failingWriter{ResponseRecorder: httptest.NewRecorder()}
	e.h.ServeHTTP(w, r)
	if body := w.Body.String(); body != ": keepalive\n\n" {
		t.Fatalf("body=%q", body)
	}
	if len(e.records) != 1 {
		t.Fatalf("records=%d", len(e.records))
	}
	if got := e.records[0]; got.CostNanos != 7_500_000 || got.Status != "error" || !strings.Contains(got.ErrorMessage, "client disconnected") {
		t.Fatalf("record=%+v", got)
	}
}

// A provider panic must stop the keepalive goroutine as the handler unwinds.
// Otherwise its next tick flushes into a connection net/http has already torn
// down, panicking outside any recover and killing the process (here: the test
// binary).
func TestChat_StreamKeepalive_ProviderPanic(t *testing.T) {
	e := newEnv(t, repo.KeyInput{})
	e.srv.Keepalive = 5 * time.Millisecond
	e.prov.during = func(context.Context) { time.Sleep(30 * time.Millisecond); panic("provider bug") }
	ts := httptest.NewUnstartedServer(e.h)
	ts.Config.ErrorLog = log.New(io.Discard, "", 0) // net/http logs the recovered panic
	ts.Start()
	defer ts.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/v1/chat/completions", strings.NewReader(streamBody))
	req.Header.Set("Authorization", "Bearer "+e.raw)
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, res.Body) // ends when the server drops the connection
	res.Body.Close()
	time.Sleep(50 * time.Millisecond) // many keepalive ticks after the handler unwound
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

// A client that leaves (e.g. during a long prefill, before any byte is written)
// cancels upstream for a $0 model only: recorded 499 and charged its input
// estimate to TPM. Priced models drain and keep main's labelling, including a
// real upstream error that lands after the client left.
func TestChat_ClientGone(t *testing.T) {
	for _, stream := range []bool{true, false} {
		for _, tc := range []struct {
			model      string
			upErr      error
			wantCancel bool
			wantStatus int
		}{
			{"slow", nil, true, 499},
			{"fast", nil, false, 200},
			{"fast", &llm.UpstreamError{Status: 503, Message: "overloaded"}, false, 503},
		} {
			name := fmt.Sprintf("%s/stream=%v/err=%v", tc.model, stream, tc.upErr)
			limit := int32(1_000_000)
			e := newEnv(t, repo.KeyInput{TPMLimit: &limit})
			e.prov.err = tc.upErr
			ctx, leave := context.WithCancel(context.Background())
			var upCtxErr error
			e.prov.during = func(up context.Context) { leave(); upCtxErr = up.Err() }
			body := fmt.Sprintf(`{"model":%q,"stream":%v,"messages":[{"role":"user","content":"hi"}]}`, tc.model, stream)
			r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body)).WithContext(ctx)
			r.Header.Set("Authorization", "Bearer "+e.raw)
			e.h.ServeHTTP(httptest.NewRecorder(), r)
			if (upCtxErr != nil) != tc.wantCancel {
				t.Fatalf("%s: upstream ctx err=%v, want cancelled=%v", name, upCtxErr, tc.wantCancel)
			}
			if len(e.records) != 1 {
				t.Fatalf("%s: records=%d", name, len(e.records))
			}
			got := e.records[0]
			if got.HTTPStatus != tc.wantStatus || tc.wantCancel != strings.Contains(got.ErrorMessage, "client disconnected") {
				t.Fatalf("%s: record=%+v", name, got)
			}
			// An upstream error carries no usage (same as main), so TPM is only asserted otherwise.
			if tpm, _ := e.srv.Cache.TPM(context.Background(), "key", e.key.ID, time.Now()); tc.upErr == nil && tpm <= 0 {
				t.Fatalf("%s: TPM not charged (%d)", name, tpm)
			}
		}
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

// #31: once the server-wide cap is reached, every key is refused with scope
// "global", even a key and group with no budget of their own. Unlimited (the
// default) changes nothing — every other test here runs with it.
func TestChat_GlobalCapRefusesEveryKey(t *testing.T) {
	e := newEnv(t, repo.KeyInput{})
	ctx := context.Background()
	if w := e.do("POST", "/v1/chat/completions", e.raw, chatBody); w.Code != 200 {
		t.Fatalf("unlimited global cap: %d %s", w.Code, w.Body)
	}
	if _, _, err := e.store.SetGlobalBudget(ctx, "daily", 1000); err != nil {
		t.Fatal(err)
	}
	e.db.Pool.Exec(ctx, `UPDATE server_settings SET spend_nanos = 1000`)
	g, _ := e.store.GetGroupByName(ctx, "default")
	u2, _ := e.store.CreateUser(ctx, "b@x.io", "B", "hunter22hunter22", "member", g.ID)
	raw2, _, err := e.store.CreateKey(ctx, u2.ID, repo.KeyInput{GroupID: g.ID, BudgetType: "unlimited"})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{e.raw, raw2} {
		w := e.do("POST", "/v1/chat/completions", raw, chatBody)
		if w.Code != 429 || errCode(t, w) != "budget_exceeded" || !strings.Contains(w.Body.String(), "global budget exceeded") {
			t.Fatalf("over the global cap: %d %s", w.Code, w.Body)
		}
	}
	// Raising the cap admits again.
	if _, _, err := e.store.SetGlobalBudget(ctx, "daily", 1e12); err != nil {
		t.Fatal(err)
	}
	if w := e.do("POST", "/v1/chat/completions", e.raw, chatBody); w.Code != 200 {
		t.Fatalf("under the raised cap: %d %s", w.Code, w.Body)
	}
}
