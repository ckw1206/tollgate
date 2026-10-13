package consoleapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"tollgate/console/auth"
	"tollgate/core/cache"
	"tollgate/core/db/dbtest"
	"tollgate/core/db/repo"
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
}

func newTenv(t *testing.T) *tenv {
	s := repo.New(dbtest.New(t))
	ctx := context.Background()
	g, _ := s.GetGroupByName(ctx, "default")
	e := &tenv{t: t, store: s, group: g, cookies: map[string]*http.Cookie{}}
	e.admin, _ = s.CreateUser(ctx, "admin@x.io", "Admin", "hunter22hunter22", "admin", g.ID)
	e.member, _ = s.CreateUser(ctx, "member@x.io", "Member", "hunter22hunter22", "member", g.ID)
	e.viewer, _ = s.CreateUser(ctx, "viewer@x.io", "Viewer", "hunter22hunter22", "viewer", g.ID)
	mr := miniredis.RunT(t)
	c := cache.FromClient(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	v, _ := vault.New(bytes.Repeat([]byte{3}, 32))
	srv := &Server{Store: s, Cache: c, Vault: v, BaseURL: base,
		Version: "v0.0.0-test", Commit: "0123456789abcdef0123456789abcdef01234567",
		Sessions: &auth.Sessions{Cache: c, Store: s, TTL: time.Hour},
		Reload:   func(context.Context) error { e.reloads++; return nil }}
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
