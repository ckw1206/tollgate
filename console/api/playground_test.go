package consoleapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tollgate/core/db/repo"
)

// TestPlayground_ViewerDenied: the viewer role never has playground:use, so
// every playground route must refuse it (the RBAC gate, not the handler).
func TestPlayground_ViewerDenied(t *testing.T) {
	e := newTenv(t)
	id := "00000000-0000-0000-0000-000000000000"
	routes := []struct{ method, path string }{
		{"GET", "/playground/models"},
		{"POST", "/playground/chat/completions"},
		{"GET", "/playground/sessions"},
		{"POST", "/playground/sessions"},
		{"GET", "/playground/sessions/" + id},
		{"PUT", "/playground/sessions/" + id},
		{"DELETE", "/playground/sessions/" + id},
	}
	for _, rt := range routes {
		if w := e.as("viewer@x.io", rt.method, p+rt.path, map[string]any{}); w.Code != 403 {
			t.Errorf("viewer %s %s: %d, want 403", rt.method, rt.path, w.Code)
		}
	}
}

func TestPlayground_ModelsMemberSeesEffectiveOnly(t *testing.T) {
	e := newTenv(t)
	ctx := context.Background()
	// group allows fast only; the member has slow as an extra grant; off is
	// disabled. The member must see exactly fast (group) and slow (granted).
	e.store.UpdateGroup(ctx, e.group.ID, repo.GroupInput{Name: "default", BudgetType: "unlimited", Models: &[]string{"fast"}})
	e.store.UpdateUser(ctx, e.member.ID, repo.UserUpdate{ExtraModels: &[]string{"slow"}})
	type view struct {
		Name             string `json:"name"`
		Provider         string `json:"provider"`
		InputMicros      int64  `json:"input_micros"`
		OutputMicros     int64  `json:"output_micros"`
		CacheReadMicros  *int64 `json:"cache_read_micros"`
		CacheWriteMicros *int64 `json:"cache_write_micros"`
	}
	got := decodeInto[[]view](t, e.as("member@x.io", "GET", p+"/playground/models", nil))
	if len(got) != 2 || got[0].Name != "fast" || got[1].Name != "slow" {
		t.Fatalf("member: %+v, want [fast slow]", got)
	}
	if got[0].Provider != "openai" || got[0].InputMicros != 2_500_000 || got[0].OutputMicros != 10_000_000 {
		t.Fatalf("fast prices: %+v", got[0])
	}
	// No secret-adjacent model fields in the response.
	if b := e.as("member@x.io", "GET", p+"/playground/models", nil).Body.String(); strings.Contains(b, "api_base") || strings.Contains(b, "credential") || strings.Contains(b, "extra") {
		t.Fatalf("leaks model internals: %s", b)
	}
}

func TestPlayground_ChatRecordsPlaygroundSpend(t *testing.T) {
	e := newTenv(t)
	w := e.as("member@x.io", "POST", p+"/playground/chat/completions", map[string]any{
		"model": "fast", "messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if len(e.records) != 1 {
		t.Fatalf("records=%d, want 1", len(e.records))
	}
	rec := e.records[0]
	// Key-less principal: no key id, source marks the row, user+group carry the charge.
	if rec.Source != "playground" || rec.KeyID != "" || rec.UserID != e.member.ID || rec.GroupID != e.group.ID {
		t.Fatalf("record=%+v", rec)
	}
	// 1000 in / 500 out at fast's seeded prices: (1000·2.5M + 500·10M)/1000.
	if rec.CostNanos != 7_500_000 || rec.ModelName != "fast" || rec.Status != "success" {
		t.Fatalf("record=%+v", rec)
	}
}

func TestPlayground_ChatOutsideSet403(t *testing.T) {
	e := newTenv(t)
	ctx := context.Background()
	e.store.UpdateGroup(ctx, e.group.ID, repo.GroupInput{Name: "default", BudgetType: "unlimited", Models: &[]string{"fast"}})
	w := e.as("member@x.io", "POST", p+"/playground/chat/completions", map[string]any{
		"model": "slow", "messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if w.Code != 403 || errCode(t, w) != "model_not_allowed" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if len(e.records) != 0 {
		t.Fatalf("refused request recorded: %+v", e.records)
	}
}

// TestPlayground_ChatNeedsJSONContentType: the CSRF guard applies to the
// playground chat route like any other cookie-authenticated mutation.
func TestPlayground_ChatNeedsJSONContentType(t *testing.T) {
	e := newTenv(t)
	e.as("member@x.io", "GET", "/auth/me", nil) // log in to hold the session cookie
	r := httptest.NewRequest("POST", p+"/playground/chat/completions",
		strings.NewReader(`{"model":"fast","messages":[{"role":"user","content":"hi"}]}`))
	r.Header.Set("Content-Type", "text/plain")
	r.AddCookie(e.cookies["member@x.io"])
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	if w.Code != 403 || errCode(t, w) != "csrf" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if len(e.records) != 0 {
		t.Fatalf("csrf refusal recorded: %+v", e.records)
	}
}

func TestPlaygroundSessions_CRUDAndIsolation(t *testing.T) {
	e := newTenv(t)
	ctx := context.Background()
	data := map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hi"}}, "models": []string{"fast"}}

	w := e.as("member@x.io", "POST", p+"/playground/sessions", map[string]any{"name": "first", "data": data})
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	created := decodeInto[repo.PlaygroundSession](t, w)
	if created.ID == "" || created.UserID != e.member.ID || created.Name != "first" || !json.Valid(created.Data) {
		t.Fatalf("created=%+v", created)
	}

	list := decodeInto[[]struct {
		ID        string    `json:"id"`
		Name      string    `json:"name"`
		UpdatedAt time.Time `json:"updated_at"`
	}](t, e.as("member@x.io", "GET", p+"/playground/sessions", nil))
	if len(list) != 1 || list[0].ID != created.ID || list[0].Name != "first" {
		t.Fatalf("list=%+v", list)
	}

	got := decodeInto[repo.PlaygroundSession](t, e.as("member@x.io", "GET", p+"/playground/sessions/"+created.ID, nil))
	if got.ID != created.ID || got.Name != "first" || !json.Valid(got.Data) {
		t.Fatalf("get=%+v", got)
	}

	wd := e.as("member@x.io", "PUT", p+"/playground/sessions/"+created.ID, map[string]any{"name": "second", "data": map[string]any{"x": 1}})
	if wd.Code != 200 {
		t.Fatalf("update: %d %s", wd.Code, wd.Body)
	}
	updated := decodeInto[repo.PlaygroundSession](t, wd)
	if updated.Name != "second" || updated.UpdatedAt.Before(created.UpdatedAt) {
		t.Fatalf("updated=%+v", updated)
	}

	// Owner-scoped: the admin (who has playground:use) gets 404 on the
	// member's session, exactly like a missing id — no cross-user reads.
	for _, rt := range []struct{ method, path string }{
		{"GET", "/playground/sessions/" + created.ID},
		{"PUT", "/playground/sessions/" + created.ID},
		{"DELETE", "/playground/sessions/" + created.ID},
	} {
		body := map[string]any{}
		if rt.method == "PUT" {
			body = map[string]any{"name": "pwn", "data": map[string]any{}}
		}
		if w := e.as("admin@x.io", rt.method, p+rt.path, body); w.Code != 404 {
			t.Errorf("admin %s: %d, want 404", rt.method, w.Code)
		}
	}
	if ls := decodeInto[[]any](t, e.as("admin@x.io", "GET", p+"/playground/sessions", nil)); len(ls) != 0 {
		t.Fatalf("admin lists the member's sessions: %+v", ls)
	}

	// Malformed (non-UUID) ids are 404, never a Postgres cast error (500).
	for _, bad := range []string{"not-a-uuid", "00000000-0000-0000-0000-00000000000g", strings.Repeat("z", 36), strings.ToUpper(created.ID) + "x"} {
		if w := e.as("member@x.io", "GET", p+"/playground/sessions/"+bad, nil); w.Code != 404 {
			t.Errorf("GET %q: %d, want 404", bad, w.Code)
		}
	}
	// A well-formed UUID nobody owns is 404 too.
	if w := e.as("member@x.io", "GET", p+"/playground/sessions/11111111-1111-1111-1111-111111111111", nil); w.Code != 404 {
		t.Fatalf("foreign uuid: %d", w.Code)
	}

	// data must be a JSON object; a missing name is a 400 as well.
	for _, bad := range []any{[]any{1}, "str", 5} {
		if w := e.as("member@x.io", "POST", p+"/playground/sessions", map[string]any{"name": "x", "data": bad}); w.Code != 400 {
			t.Errorf("data %v: %d, want 400", bad, w.Code)
		}
	}
	if w := e.as("member@x.io", "POST", p+"/playground/sessions", map[string]any{"name": "", "data": map[string]any{}}); w.Code != 400 {
		t.Fatalf("empty name: %d, want 400", w.Code)
	}

	if w := e.as("member@x.io", "DELETE", p+"/playground/sessions/"+created.ID, nil); w.Code != 204 {
		t.Fatalf("delete: %d", w.Code)
	}
	if _, err := e.store.GetPlaygroundSession(ctx, e.member.ID, created.ID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("still stored: %v", err)
	}
	if w := e.as("member@x.io", "DELETE", p+"/playground/sessions/"+created.ID, nil); w.Code != 404 {
		t.Fatalf("re-delete: %d, want 404", w.Code)
	}
}

// TestPlaygroundSessions_NameLengthCountsCharacters: the 1-120 check counts
// characters like the DB CHECK (length()), not bytes — a 60-character CJK
// name is 180 bytes and must be accepted, 121 characters must not.
func TestPlaygroundSessions_NameLengthCountsCharacters(t *testing.T) {
	e := newTenv(t)
	cjk60 := strings.Repeat("测", 60)
	w := e.as("member@x.io", "POST", p+"/playground/sessions", map[string]any{"name": cjk60, "data": map[string]any{}})
	if w.Code != 201 {
		t.Fatalf("create 60-char (180-byte) CJK name: %d %s, want 201", w.Code, w.Body)
	}
	created := decodeInto[repo.PlaygroundSession](t, w)
	if w := e.as("member@x.io", "PUT", p+"/playground/sessions/"+created.ID, map[string]any{"name": cjk60, "data": map[string]any{}}); w.Code != 200 {
		t.Fatalf("update 60-char CJK name: %d %s, want 200", w.Code, w.Body)
	}
	cjk121 := strings.Repeat("测", 121)
	if w := e.as("member@x.io", "POST", p+"/playground/sessions", map[string]any{"name": cjk121, "data": map[string]any{}}); w.Code != 400 {
		t.Fatalf("create 121-char CJK name: %d, want 400", w.Code)
	}
}

func errCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var b struct{ Error struct{ Code string } }
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatalf("decode error body %q: %v", w.Body, err)
	}
	return b.Error.Code
}

// TestPlaygroundSessions_CapPerUser: create past the cap is a 409; updates
// still work, the cap is per user, and deleting one frees a slot.
func TestPlaygroundSessions_CapPerUser(t *testing.T) {
	e := newTenv(t)
	ctx := context.Background()
	var last *repo.PlaygroundSession
	for i := 0; i < repo.MaxPlaygroundSessions; i++ {
		ps, err := e.store.CreatePlaygroundSession(ctx, e.member.ID, "s", nil)
		if err != nil {
			t.Fatal(err)
		}
		last = ps
	}
	body := map[string]any{"name": "one more", "data": map[string]any{}}
	w := e.as("member@x.io", "POST", p+"/playground/sessions", body)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "session_limit") {
		t.Fatalf("create past cap: %d %s", w.Code, w.Body)
	}
	if w := e.as("member@x.io", "PUT", p+"/playground/sessions/"+last.ID, body); w.Code != 200 {
		t.Fatalf("update at cap: %d %s", w.Code, w.Body)
	}
	if w := e.as("admin@x.io", "POST", p+"/playground/sessions", body); w.Code != 201 {
		t.Fatalf("another user at the member's cap: %d %s", w.Code, w.Body)
	}
	if w := e.as("member@x.io", "DELETE", p+"/playground/sessions/"+last.ID, nil); w.Code != 204 {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if w := e.as("member@x.io", "POST", p+"/playground/sessions", body); w.Code != 201 {
		t.Fatalf("create after delete: %d %s", w.Code, w.Body)
	}
}
