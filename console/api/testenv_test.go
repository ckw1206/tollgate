package consoleapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"tollgate/console/auth"
	"tollgate/core/api"
	"tollgate/core/cache"
	"tollgate/core/db/dbtest"
	"tollgate/core/db/repo"
	"tollgate/core/llm"
	"tollgate/core/vault"
)

const base = "https://gw.example.com"

type tenv struct {
	t       *testing.T
	h       http.Handler
	store   *repo.Store
	group   *repo.Group
	admin   *repo.User
	member  *repo.User
	viewer  *repo.User
	reloads int
	cookies map[string]*http.Cookie
	setup   *SetupToken // non-nil only from newEmptyTenv, like main
	token   string      // setup's raw token
	redis   *miniredis.Miniredis
	cache   *cache.Cache // the same instance the gateway and console server use
	gateway *api.Server
	prov    *fakeProvider
	mu      sync.Mutex
	records []repo.SpendRecord // captured by the gateway's Record
}

// fakeProvider answers every chat with fixed usage (1000 in / 500 out), so
// playground spend is deterministic against the seeded model prices.
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
		emit([]byte(c))
	}
	return f.usage, f.err
}

func (f *fakeProvider) Embed(context.Context, *llm.Model, []byte) (*llm.Result, error) {
	return &llm.Result{Body: []byte(`{"data":[]}`), Usage: llm.Usage{Input: 3}}, nil
}

// modelByName returns a seeded model by name (the fixtures are fast, slow, off).
func modelByName(t *testing.T, e *tenv, name string) repo.Model {
	t.Helper()
	ms, err := e.store.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		if m.Name == name {
			return m
		}
	}
	t.Fatalf("model %s not seeded", name)
	return repo.Model{}
}

func newTenv(t *testing.T) *tenv { return buildTenv(t, true) }

// newEmptyTenv starts with no users, so the server holds a first-run setup token.
func newEmptyTenv(t *testing.T) *tenv { return buildTenv(t, false) }

func buildTenv(t *testing.T, withUsers bool) *tenv {
	s := repo.New(dbtest.New(t))
	ctx := context.Background()
	g, _ := s.GetGroupByName(ctx, "default")
	e := &tenv{t: t, store: s, group: g, cookies: map[string]*http.Cookie{}}
	if withUsers {
		e.admin, _ = s.CreateUser(ctx, "admin@x.io", "Admin", "hunter22hunter22", "admin", g.ID)
		e.member, _ = s.CreateUser(ctx, "member@x.io", "Member", "hunter22hunter22", "member", g.ID)
		e.viewer, _ = s.CreateUser(ctx, "viewer@x.io", "Viewer", "hunter22hunter22", "viewer", g.ID)
	} else {
		e.setup, e.token = NewSetupToken()
	}
	mr := miniredis.RunT(t)
	e.redis = mr
	c := cache.FromClient(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	e.cache = c
	v, _ := vault.New(bytes.Repeat([]byte{3}, 32))
	// Seed the models the playground tests need, and build the gateway
	// registry from the very same rows so GET /playground/models (DB) and the
	// chat path (registry) agree: fast and slow enabled, off disabled.
	fast, err := s.CreateModel(ctx, repo.ModelInput{Name: "fast", Provider: "openai", UpstreamModel: "fast-1",
		APIBase: "https://api.openai.com/v1", Enabled: true, PriceInputMicros: 2_500_000, PriceOutputMicros: 10_000_000})
	if err != nil {
		t.Fatal(err)
	}
	slow, err := s.CreateModel(ctx, repo.ModelInput{Name: "slow", Provider: "openai", UpstreamModel: "slow-1",
		APIBase: "https://api.openai.com/v1", Enabled: true, PriceInputMicros: 100_000, PriceOutputMicros: 1_000_000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateModel(ctx, repo.ModelInput{Name: "off", Provider: "openai", UpstreamModel: "off-1",
		APIBase: "https://api.openai.com/v1", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	prov := &fakeProvider{usage: llm.Usage{Input: 1000, Output: 500}}
	reg := llm.NewRegistry(map[string]llm.Provider{"openai": prov})
	reg.Replace([]*llm.Model{
		{ID: fast.ID, Name: fast.Name, Provider: fast.Provider,
			Prices: llm.Prices{InputMicros: fast.PriceInputMicros, OutputMicros: fast.PriceOutputMicros,
				CacheReadMicros: fast.PriceCacheReadMicros, CacheWriteMicros: fast.PriceCacheWriteMicros}},
		{ID: slow.ID, Name: slow.Name, Provider: slow.Provider,
			Prices: llm.Prices{InputMicros: slow.PriceInputMicros, OutputMicros: slow.PriceOutputMicros,
				CacheReadMicros: slow.PriceCacheReadMicros, CacheWriteMicros: slow.PriceCacheWriteMicros}},
	})
	e.prov = prov
	gw := &api.Server{Store: s, Cache: c, Registry: reg, Now: time.Now,
		Record: func(r repo.SpendRecord) { e.mu.Lock(); e.records = append(e.records, r); e.mu.Unlock() }}
	srv := &Server{Store: s, Cache: c, Vault: v, BaseURL: base, Setup: e.setup,
		Version: "v0.0.0-test", Commit: "0123456789abcdef0123456789abcdef01234567",
		Sessions: &auth.Sessions{Cache: c, Store: s, TTL: time.Hour},
		Reload:   func(context.Context) error { e.reloads++; return nil },
		Gateway:  gw}
	e.gateway = gw
	mux := http.NewServeMux()
	srv.Register(mux)
	e.h = auth.CSRF(base, mux)
	return e
}

// as performs a request as the given email ("" = anonymous), logging in on first use.
func (e *tenv) as(email, method, path string, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	if email != "" && e.cookies[email] == nil {
		w := e.raw("", "POST", "/auth/login", map[string]string{"email": email, "password": "hunter22hunter22"})
		if w.Code != 200 {
			e.t.Fatalf("login %s: %d %s", email, w.Code, w.Body)
		}
		e.cookies[email] = w.Result().Cookies()[0]
	}
	return e.raw(email, method, path, body)
}

func (e *tenv) raw(email, method, path string, body any) *httptest.ResponseRecorder {
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, path, rdr)
	r.Header.Set("Content-Type", "application/json")
	if c := e.cookies[email]; email != "" && c != nil {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

func decodeInto[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %s: %v", w.Body, err)
	}
	return v
}
