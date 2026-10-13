package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"tollgate/console/rbac"
	"tollgate/core/cache"
	"tollgate/core/db/dbtest"
	"tollgate/core/db/repo"
)

func TestClientIP(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.1.1.1:5555"
	r.Header.Set("X-Forwarded-For", "6.6.6.6, 1.2.3.4, 10.2.2.2")
	if ip := ClientIP(r, trusted); ip != "1.2.3.4" {
		t.Fatalf("got %s (must skip trusted hops from the right, not trust the leftmost)", ip)
	}
	r.RemoteAddr = "8.8.8.8:5555" // untrusted peer: ignore XFF entirely
	if ip := ClientIP(r, trusted); ip != "8.8.8.8" {
		t.Fatalf("got %s", ip)
	}
}

func TestCSRF(t *testing.T) {
	h := CSRF("https://gw.example.com", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	cases := []struct {
		method, path, ct, origin string
		want                     int
	}{
		{"POST", "/console/api/v1/keys", "application/json", "https://gw.example.com", 204},
		{"POST", "/console/api/v1/keys", "application/json", "", 204},
		{"POST", "/console/api/v1/keys", "application/x-www-form-urlencoded", "", 403},
		{"DELETE", "/console/api/v1/keys/1", "application/json", "https://evil.example", 403},
		{"POST", "/auth/login", "text/plain", "", 403},
		{"GET", "/console/api/v1/keys", "", "https://evil.example", 204},
		{"POST", "/v1/chat/completions", "text/plain", "https://evil.example", 204}, // API keys, not cookies
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, c.path, strings.NewReader("{}"))
		if c.ct != "" {
			r.Header.Set("Content-Type", c.ct)
		}
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.want {
			t.Errorf("%s %s ct=%q origin=%q: got %d want %d", c.method, c.path, c.ct, c.origin, w.Code, c.want)
		}
	}
}

func TestSessions_StartLoadRequireEnd(t *testing.T) {
	s := repo.New(dbtest.New(t))
	ctx := context.Background()
	g, _ := s.GetGroupByName(ctx, "default")
	u, _ := s.CreateUser(ctx, "m@x.io", "M", "hunter22hunter22", "member", g.ID)
	mr := miniredis.RunT(t)
	sess := &Sessions{Cache: cache.FromClient(redis.NewClient(&redis.Options{Addr: mr.Addr()})), Store: s, TTL: time.Hour}

	w := httptest.NewRecorder()
	if err := sess.Start(ctx, w, u.ID); err != nil {
		t.Fatal(err)
	}
	cookie := w.Result().Cookies()[0]
	if cookie.Name != CookieName || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie flags: %+v", cookie)
	}

	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	call := func(h http.Handler, withCookie bool) int {
		r := httptest.NewRequest("GET", "/", nil)
		if withCookie {
			r.AddCookie(cookie)
		}
		rw := httptest.NewRecorder()
		sess.Load(h).ServeHTTP(rw, r)
		return rw.Code
	}
	if c := call(sess.Require(rbac.KeysReadOwn, ok), true); c != 200 {
		t.Fatalf("member reading own keys: %d", c)
	}
	if c := call(sess.Require(rbac.UsersRead, ok), true); c != 403 {
		t.Fatalf("member reading users: %d", c)
	}
	if c := call(sess.Require(rbac.KeysReadOwn, ok), false); c != 401 {
		t.Fatalf("anonymous: %d", c)
	}

	disabled := true
	s.UpdateUser(ctx, u.ID, repo.UserUpdate{Disabled: &disabled})
	if c := call(sess.Require("", ok), true); c != 401 {
		t.Fatalf("disabled user kept session: %d", c)
	}
}
