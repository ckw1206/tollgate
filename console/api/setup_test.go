package consoleapi

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"tollgate/core/cache"
	"tollgate/core/db/repo"
)

type apiErr struct{ Error struct{ Type, Code string } }

func setupBody(token, email string) map[string]string {
	return map[string]string{"token": token, "email": email, "name": "Root", "password": "a-long-password"}
}

func wantErr(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if got := decodeInto[apiErr](t, w).Error.Code; w.Code != status || got != code {
		t.Fatalf("want %d %s, got %d %s", status, code, w.Code, w.Body)
	}
}

func TestSetup_NeededOnlyWhileNoUsers(t *testing.T) {
	e := newEmptyTenv(t)
	if got := decodeInto[struct{ Needed bool }](t, e.raw("", "GET", "/auth/setup", nil)); !got.Needed {
		t.Fatal("empty DB: needed=false")
	}
	e.store.CreateUser(context.Background(), "a@x.io", "A", "hunter22hunter22", "member", e.group.ID)
	if got := decodeInto[struct{ Needed bool }](t, e.raw("", "GET", "/auth/setup", nil)); got.Needed {
		t.Fatal("with a user: needed=true")
	}
}

func TestSetup_RejectedOnceUsersExist(t *testing.T) {
	e := newTenv(t) // users exist, so like main the server has no token
	if got := decodeInto[struct{ Needed bool }](t, e.raw("", "GET", "/auth/setup", nil)); got.Needed {
		t.Fatal("needed=true with users")
	}
	wantErr(t, e.raw("", "POST", "/auth/setup", setupBody("anything", "root@x.io")), 409, "already_set_up")
}

func TestSetup_WrongOrDiscardedTokenIs403(t *testing.T) {
	e := newEmptyTenv(t)
	w := e.raw("", "POST", "/auth/setup", setupBody("wrong", "root@x.io"))
	wantErr(t, w, 403, "invalid_setup_token")
	if typ := decodeInto[apiErr](t, w).Error.Type; typ != "permission_error" {
		t.Fatalf("type %q", typ)
	}
	e.setup.Discard()
	wantErr(t, e.raw("", "POST", "/auth/setup", setupBody(e.token, "root@x.io")), 403, "invalid_setup_token")
	if n, _ := e.store.CountUsers(context.Background()); n != 0 {
		t.Fatalf("users=%d", n)
	}
}

func TestSetup_Validates(t *testing.T) {
	e := newEmptyTenv(t)
	for _, b := range []map[string]string{
		{"token": e.token, "email": "root@x.io", "name": "Root", "password": "short"},
		{"token": e.token, "email": "root@x.io", "name": "  ", "password": "a-long-password"},
		{"token": e.token, "email": "not-an-email", "name": "Root", "password": "a-long-password"},
		{"token": e.token, "email": "", "name": "Root", "password": "a-long-password"},
	} {
		wantErr(t, e.raw("", "POST", "/auth/setup", b), 400, "invalid_request")
	}
	if !e.setup.Valid(e.token) {
		t.Fatal("validation error burned the token")
	}
}

func TestSetup_CreatesAdminAndSignsIn(t *testing.T) {
	e := newEmptyTenv(t)
	w := e.raw("", "POST", "/auth/setup", map[string]string{"token": e.token, "email": " Root@X.io ", "name": " Root ", "password": "a-long-password"})
	if w.Code != 201 || len(w.Result().Cookies()) == 0 {
		t.Fatalf("setup: %d %s", w.Code, w.Body)
	}
	u := decodeInto[repo.User](t, w)
	if u.Email != "root@x.io" || u.Name != "Root" || u.Role != "admin" || u.GroupID != e.group.ID {
		t.Fatalf("user %+v", u)
	}
	e.cookies["root@x.io"] = w.Result().Cookies()[0]
	me := decodeInto[struct{ User repo.User }](t, e.raw("root@x.io", "GET", "/auth/me", nil))
	if me.User.ID != u.ID || me.User.Role != "admin" || me.User.GroupID != e.group.ID {
		t.Fatalf("session: %+v", me)
	}
	entries, _, _ := e.store.ListAudit(context.Background(), repo.AuditFilter{Action: "user.bootstrap", Limit: 10})
	if len(entries) != 1 || entries[0].ActorID == nil || *entries[0].ActorID != u.ID || entries[0].ActorEmail != u.Email ||
		entries[0].ResourceID != u.ID || entries[0].Resource != "users" || entries[0].IP != "192.0.2.1" {
		t.Fatalf("audit %+v", entries)
	}
	if e.setup.Valid(e.token) {
		t.Fatal("token still valid after setup")
	}
	wantErr(t, e.raw("", "POST", "/auth/setup", setupBody(e.token, "second@x.io")), 409, "already_set_up")
}

func TestSetup_ThrottlesWrongTokensByIPOnly(t *testing.T) {
	e := newEmptyTenv(t)
	for i := range cache.SetupMaxFailures {
		wantErr(t, e.raw("", "POST", "/auth/setup", setupBody("wrong", fmt.Sprintf("u%d@x.io", i))), 403, "invalid_setup_token")
	}
	wantErr(t, e.raw("", "POST", "/auth/setup", setupBody(e.token, "root@x.io")), 429, "too_many_attempts")
	if n, _ := e.store.CountUsers(context.Background()); n != 0 {
		t.Fatalf("users=%d", n)
	}
	// The setup form must never touch the sign-in counters, or anyone could lock an
	// email (or an office IP) out of signing in.
	for _, k := range e.redis.Keys() {
		if strings.HasPrefix(k, "loginfail:") {
			t.Fatalf("setup wrote a sign-in throttle key: %s", k)
		}
	}
}

func TestSetup_InertOnceUsersExistEvenWhenThrottled(t *testing.T) {
	e := newEmptyTenv(t)
	for i := range cache.SetupMaxFailures {
		wantErr(t, e.raw("", "POST", "/auth/setup", setupBody("wrong", fmt.Sprintf("u%d@x.io", i))), 403, "invalid_setup_token")
	}
	if _, err := e.store.CreateUser(context.Background(), "cli@x.io", "CLI", "hunter22hunter22", "admin", e.group.ID); err != nil {
		t.Fatal(err)
	}
	// 409 comes before the throttle: after setup the endpoint does nothing at all.
	wantErr(t, e.raw("", "POST", "/auth/setup", setupBody(e.token, "late@x.io")), 409, "already_set_up")
	before := len(e.redis.Keys())
	wantErr(t, e.raw("", "POST", "/auth/setup", setupBody("wrong", "late@x.io")), 409, "already_set_up")
	if after := len(e.redis.Keys()); after != before {
		t.Fatalf("a post-setup request wrote to Redis: %d -> %d keys", before, after)
	}
}

func TestSetup_ConcurrentSingleWinner(t *testing.T) {
	e := newEmptyTenv(t)
	res := make([]*httptest.ResponseRecorder, 8)
	var wg sync.WaitGroup
	for i := range res {
		wg.Go(func() { res[i] = e.raw("", "POST", "/auth/setup", setupBody(e.token, fmt.Sprintf("root%d@x.io", i))) })
	}
	wg.Wait()
	wins := 0
	for _, w := range res {
		if w.Code == 201 {
			wins++
		} else {
			wantErr(t, w, 409, "already_set_up")
		}
	}
	if n, _ := e.store.CountUsers(context.Background()); wins != 1 || n != 1 {
		t.Fatalf("wins=%d users=%d", wins, n)
	}
}

func TestLanding_PublicAndCacheable(t *testing.T) {
	e := newEmptyTenv(t)
	w := e.raw("", "GET", "/auth/landing", nil)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "public, max-age=60" {
		t.Fatalf("%d %q", w.Code, w.Header().Get("Cache-Control"))
	}
	got := decodeInto[map[string]string](t, w)
	want := map[string]string{"server_name": "Tollgate", "server_icon": "", "landing_headline": "", "landing_tagline": "",
		"version": "v0.0.0-test", "commit": "0123456789abcdef0123456789abcdef01234567"}
	if fmt.Sprint(got) != fmt.Sprint(want) { // exact key set: nothing else leaks to anonymous callers
		t.Fatalf("got %v", got)
	}
}
