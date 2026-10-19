package consoleapi

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"tollgate/core/db/repo"
)

func TestLogin_FailsGenericallyAndThrottles(t *testing.T) {
	e := newTenv(t)
	for i := 0; i < 10; i++ {
		w := e.raw("", "POST", "/auth/login", map[string]string{"email": "member@x.io", "password": "wrong"})
		if w.Code != 401 || !strings.Contains(w.Body.String(), "invalid email or password") {
			t.Fatalf("attempt %d: %d %s", i, w.Code, w.Body)
		}
	}
	w := e.raw("", "POST", "/auth/login", map[string]string{"email": "member@x.io", "password": "hunter22hunter22"})
	if w.Code != 429 {
		t.Fatalf("locked account accepted correct password: %d", w.Code)
	}
	if w := e.raw("", "POST", "/auth/login", map[string]string{"email": "ghost@x.io", "password": "x"}); w.Code != 429 && w.Code != 401 {
		t.Fatalf("unknown user: %d", w.Code)
	}
}

func TestMe_ReturnsPermissions(t *testing.T) {
	e := newTenv(t)
	me := decodeInto[struct {
		User        repo.User
		Permissions []string
	}](t, e.as("viewer@x.io", "GET", "/auth/me", nil))
	if me.User.Email != "viewer@x.io" || len(me.Permissions) != 4 {
		t.Fatalf("%+v", me)
	}
}

func TestInvite_EndToEnd(t *testing.T) {
	e := newTenv(t)
	team, _ := e.store.CreateGroup(context.Background(), repo.GroupInput{Name: "team", BudgetType: "unlimited"})
	w := e.as("admin@x.io", "POST", "/console/api/v1/invites", map[string]any{"role": "member", "group_id": team.ID, "ttl_hours": 24})
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	out := decodeInto[struct{ URL string }](t, w)
	token, ok := strings.CutPrefix(out.URL, base+"/invite/")
	if !ok {
		t.Fatalf("url %q", out.URL)
	}
	info := decodeInto[struct{ Role, GroupName, Status string }](t, e.raw("", "GET", "/auth/invite/"+token, nil))
	if info.Role != "member" || info.Status != "active" {
		t.Fatalf("info %+v", info)
	}
	w = e.raw("", "POST", "/auth/invite/"+token, map[string]string{"email": "new@x.io", "name": "New", "password": "short"})
	if w.Code != 400 {
		t.Fatalf("short password accepted: %d", w.Code)
	}
	w = e.raw("", "POST", "/auth/invite/"+token, map[string]string{"email": "new@x.io", "name": "New", "password": "a-long-password"})
	if w.Code != 201 || len(w.Result().Cookies()) == 0 {
		t.Fatalf("accept: %d %s", w.Code, w.Body)
	}
	if w := e.raw("", "POST", "/auth/invite/"+token, map[string]string{"email": "n2@x.io", "name": "N", "password": "a-long-password"}); w.Code != 410 {
		t.Fatalf("reuse: %d", w.Code)
	}
}

func TestInvites_MemberForbidden(t *testing.T) {
	e := newTenv(t)
	if w := e.as("member@x.io", "POST", "/console/api/v1/invites", map[string]any{"role": "member", "group_id": e.group.ID}); w.Code != 403 {
		t.Fatalf("%d", w.Code)
	}
}

func TestUsers_AdminCannotLockThemselvesOut(t *testing.T) {
	e := newTenv(t)
	if w := e.as("admin@x.io", "PUT", "/console/api/v1/users/"+e.admin.ID, map[string]any{"role": "member"}); w.Code != 400 {
		t.Fatalf("self-demote: %d", w.Code)
	}
	for _, id := range []string{e.admin.ID, strings.ToUpper(e.admin.ID)} {
		if w := e.as("admin@x.io", "DELETE", "/console/api/v1/users/"+id, nil); w.Code != 400 {
			t.Fatalf("self-disable %s: %d", id, w.Code)
		}
	}
	if w := e.as("admin@x.io", "PUT", "/console/api/v1/users/"+e.member.ID, map[string]any{"role": "viewer"}); w.Code != 200 {
		t.Fatalf("demote other: %d %s", w.Code, w.Body)
	}
}

func TestUsers_CreateAndSignin(t *testing.T) {
	e := newTenv(t)
	team, _ := e.store.CreateGroup(context.Background(), repo.GroupInput{Name: "team", BudgetType: "unlimited"})
	w := e.as("admin@x.io", "POST", "/console/api/v1/users", map[string]any{
		"email": "New@X.io", "name": "New", "role": "member", "group_id": team.ID, "password": "a-long-password"})
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	u := decodeInto[repo.User](t, w)
	if u.Email != "new@x.io" || u.Role != "member" || u.GroupID != team.ID {
		t.Fatalf("got %+v", u)
	}
	if b := w.Body.String(); strings.Contains(b, "a-long-password") || strings.Contains(b, "$2") {
		t.Fatalf("response leaks password: %s", b)
	}
	if w := e.raw("", "POST", "/auth/login", map[string]string{"email": "new@x.io", "password": "a-long-password"}); w.Code != 200 {
		t.Fatalf("sign in with typed password: %d %s", w.Code, w.Body)
	}
	entries, _, _ := e.store.ListAudit(context.Background(), repo.AuditFilter{Action: "user.create", Limit: 50})
	if len(entries) != 1 {
		t.Fatalf("audit entries: %+v", entries)
	}
	a := entries[0]
	if a.ActorEmail != "admin@x.io" || a.Resource != "users" {
		t.Fatalf("audit: %+v", a)
	}
	if d := string(a.Details); !strings.Contains(d, "new@x.io") || strings.Contains(d, "a-long-password") || strings.Contains(d, "$2") {
		t.Fatalf("audit details: %s", d)
	}
}

func TestUsers_CreateValidation(t *testing.T) {
	e := newTenv(t)
	team, _ := e.store.CreateGroup(context.Background(), repo.GroupInput{Name: "team", BudgetType: "unlimited"})
	check := func(body map[string]any, want string) {
		w := e.as("admin@x.io", "POST", "/console/api/v1/users", body)
		if w.Code != 400 || !strings.Contains(w.Body.String(), want) {
			t.Fatalf("%v: %d %s", body, w.Code, w.Body)
		}
	}
	check(map[string]any{}, "email is required")
	check(map[string]any{"email": "a@x.io", "role": "superuser", "group_id": team.ID, "password": "a-long-password"}, "role must be admin, member, or viewer")
	check(map[string]any{"email": "a@x.io", "role": "member", "group_id": team.ID, "password": "short"}, "password must be at least 12 characters")
	check(map[string]any{"email": "a@x.io", "role": "member", "group_id": "00000000-0000-0000-0000-000000000000", "password": "a-long-password"}, "unknown group")
	body := map[string]any{"email": "new@x.io", "name": "New", "role": "member", "group_id": team.ID, "password": "a-long-password"}
	if w := e.as("admin@x.io", "POST", "/console/api/v1/users", body); w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	if w := e.as("admin@x.io", "POST", "/console/api/v1/users", body); w.Code != 409 || !strings.Contains(w.Body.String(), "a user with this email already exists") {
		t.Fatalf("duplicate: %d %s", w.Code, w.Body)
	}
}

func TestUsers_ResetPassword(t *testing.T) {
	e := newTenv(t)
	path := "/console/api/v1/users/" + e.member.ID
	login := func(pw string) int {
		return e.raw("", "POST", "/auth/login", map[string]string{"email": "member@x.io", "password": pw}).Code
	}
	if w := e.as("admin@x.io", "PUT", path, map[string]any{"password": "another-long-pw"}); w.Code != 200 {
		t.Fatalf("reset: %d %s", w.Code, w.Body)
	}
	if got := login("hunter22hunter22"); got != 401 {
		t.Fatalf("old password still works: %d", got)
	}
	if got := login("another-long-pw"); got != 200 {
		t.Fatalf("new password: %d", got)
	}
	if w := e.as("admin@x.io", "PUT", path, map[string]any{"name": "M2"}); w.Code != 200 {
		t.Fatalf("name-only save: %d %s", w.Code, w.Body)
	}
	if got := login("another-long-pw"); got != 200 {
		t.Fatalf("password changed by absent field: %d", got)
	}
	if w := e.as("admin@x.io", "PUT", path, map[string]any{"password": ""}); w.Code != 200 {
		t.Fatalf("empty password: %d %s", w.Code, w.Body)
	}
	if got := login("another-long-pw"); got != 200 {
		t.Fatalf("password changed by empty string: %d", got)
	}
	if w := e.as("admin@x.io", "PUT", path, map[string]any{"password": "short"}); w.Code != 400 || !strings.Contains(w.Body.String(), "password must be at least 12 characters") {
		t.Fatalf("short: %d %s", w.Code, w.Body)
	}
	entries, _, _ := e.store.ListAudit(context.Background(), repo.AuditFilter{Action: "user.update", Limit: 50})
	if len(entries) == 0 {
		t.Fatal("no user.update audit entries")
	}
	if d := string(entries[0].Details); strings.Contains(d, "another-long-pw") || strings.Contains(d, "hunter22hunter22") || strings.Contains(d, "$2") {
		t.Fatalf("audit leaks password: %s", d)
	}
}

func TestGroups_MemberSeesOnlyOwn(t *testing.T) {
	e := newTenv(t)
	e.store.CreateGroup(context.Background(), repo.GroupInput{Name: "other", BudgetType: "unlimited"})
	disabled := true
	e.store.UpdateUser(context.Background(), e.viewer.ID, repo.UserUpdate{Disabled: &disabled})
	gs := decodeInto[[]repo.Group](t, e.as("member@x.io", "GET", "/console/api/v1/groups", nil))
	if len(gs) != 1 || gs[0].ID != e.group.ID || gs[0].Members != 3 || gs[0].Models == nil {
		t.Fatalf("%+v", gs)
	}
	all := decodeInto[[]repo.Group](t, e.as("admin@x.io", "GET", "/console/api/v1/groups", nil))
	if len(all) != 2 {
		t.Fatalf("admin sees %d", len(all))
	}
}

// groupWithKeys is a PUT /groups/{id} response.
type groupWithKeys struct {
	repo.Group
	AffectedKeys int `json:"affected_keys"`
}

type userWithKeys struct {
	repo.User
	AffectedKeys int `json:"affected_keys"`
}

// accessModels creates enabled models a and b and disabled model c.
func accessModels(e *tenv) {
	for _, n := range []string{"a", "b", "c"} {
		if _, err := e.store.CreateModel(context.Background(), repo.ModelInput{Name: n, Provider: "openai", UpstreamModel: n,
			APIBase: "https://corp.example.com", Enabled: n != "c", Extra: json.RawMessage(`{}`)}); err != nil {
			e.t.Fatal(err)
		}
	}
}

func TestGroups_ModelsReturnAffectedKeysAndAudit(t *testing.T) {
	e := newTenv(t)
	ctx := context.Background()
	accessModels(e)
	key := func(models ...string) *repo.APIKey {
		_, k, _ := e.store.CreateKey(ctx, e.member.ID, repo.KeyInput{Alias: "k", GroupID: e.group.ID, BudgetType: "unlimited", Models: models})
		return k
	}
	key("a")                                 // counted once the group drops a
	key()                                    // [] = whatever the owner may use: never counted
	key("c")                                 // c is disabled: not counted
	e.store.RevokeKey(ctx, key("a", "b").ID) // revoked: not counted
	put := func(models ...string) groupWithKeys {
		w := e.as("admin@x.io", "PUT", "/console/api/v1/groups/"+e.group.ID, map[string]any{"name": "default", "budget_type": "unlimited", "models": models})
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		return decodeInto[groupWithKeys](t, w)
	}
	if g := put("b", "b"); !slices.Equal(g.Models, []string{"b"}) || g.AffectedKeys != 1 || g.Members != 3 {
		t.Fatalf("narrow: %+v", g)
	}
	entries, _, _ := e.store.ListAudit(ctx, repo.AuditFilter{Action: "group.update", Limit: 50})
	var d struct{ Before, After struct{ Models []string } }
	if len(entries) != 1 || json.Unmarshal(entries[0].Details, &d) != nil || d.Before.Models == nil || len(d.Before.Models) != 0 || !slices.Equal(d.After.Models, []string{"b"}) {
		t.Fatalf("audit: %+v", entries)
	}
	// the path id may be any spelling Postgres accepts; the count must not depend on it
	upper := e.as("admin@x.io", "PUT", "/console/api/v1/groups/"+strings.ToUpper(e.group.ID), map[string]any{"name": "default", "budget_type": "unlimited", "models": []string{"b"}})
	if g := decodeInto[groupWithKeys](t, upper); g.AffectedKeys != 1 {
		t.Fatalf("uppercase id: %s", upper.Body)
	}
	// an extra grant covers the key again
	w := e.as("admin@x.io", "PUT", "/console/api/v1/users/"+e.member.ID, map[string]any{"extra_models": []string{"a"}})
	if u := decodeInto[userWithKeys](t, w); w.Code != 200 || u.AffectedKeys != 0 {
		t.Fatalf("grant: %d %s", w.Code, w.Body)
	}
	if g := put("b"); g.AffectedKeys != 0 {
		t.Fatalf("with grant: %+v", g)
	}
	// key lists are never rewritten
	ks, _ := e.store.ListKeys(ctx, e.member.ID)
	for _, k := range ks {
		if k.Alias == "k" && len(k.Models) == 1 && k.Models[0] == "a" {
			return
		}
	}
	t.Fatalf("key list rewritten: %+v", ks)
}

func TestGroups_ModelsKeptWhenAbsentAndValidated(t *testing.T) {
	e := newTenv(t)
	accessModels(e)
	path := "/console/api/v1/groups/" + e.group.ID
	if w := e.as("admin@x.io", "PUT", path, map[string]any{"name": "default", "budget_type": "unlimited", "models": []string{"c"}}); w.Code != 200 {
		t.Fatalf("disabled models can be granted: %d %s", w.Code, w.Body)
	}
	for _, body := range []map[string]any{
		{"name": "default", "budget_type": "daily", "budget_nanos": 5}, // absent
		{"name": "default", "budget_type": "daily", "models": nil},     // null
	} {
		if g := decodeInto[repo.Group](t, e.as("admin@x.io", "PUT", path, body)); !slices.Equal(g.Models, []string{"c"}) {
			t.Fatalf("%v dropped models: %+v", body, g)
		}
	}
	for _, bad := range []any{[]string{"ghost"}, []string{""}} {
		if w := e.as("admin@x.io", "PUT", path, map[string]any{"name": "default", "budget_type": "unlimited", "models": bad}); w.Code != 400 {
			t.Fatalf("PUT %v: %d", bad, w.Code)
		}
		if w := e.as("admin@x.io", "POST", "/console/api/v1/groups", map[string]any{"name": "x", "budget_type": "unlimited", "models": bad}); w.Code != 400 {
			t.Fatalf("POST %v: %d", bad, w.Code)
		}
	}
	w := e.as("admin@x.io", "POST", "/console/api/v1/groups", map[string]any{"name": "team", "budget_type": "unlimited", "models": []string{"a", "b"}})
	if g := decodeInto[repo.Group](t, w); w.Code != 201 || !slices.Equal(g.Models, []string{"a", "b"}) {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
}

func TestUsers_ExtraModels(t *testing.T) {
	e := newTenv(t)
	ctx := context.Background()
	accessModels(e)
	path := "/console/api/v1/users/" + e.member.ID
	w := e.as("admin@x.io", "PUT", path, map[string]any{"extra_models": []string{"c", "c"}})
	if u := decodeInto[userWithKeys](t, w); w.Code != 200 || !slices.Equal(u.ExtraModels, []string{"c"}) || u.AffectedKeys != 0 {
		t.Fatalf("grant: %d %s", w.Code, w.Body)
	}
	if u := decodeInto[repo.User](t, e.as("admin@x.io", "PUT", path, map[string]any{"name": "M2"})); !slices.Equal(u.ExtraModels, []string{"c"}) {
		t.Fatalf("absent extra_models dropped: %+v", u)
	}
	if w := e.as("admin@x.io", "PUT", path, map[string]any{"extra_models": []string{"ghost"}}); w.Code != 400 {
		t.Fatalf("unknown: %d", w.Code)
	}
	// moving the owner to a narrower group counts their keys too
	narrow, _ := e.store.CreateGroup(ctx, repo.GroupInput{Name: "narrow", BudgetType: "unlimited", Models: &[]string{"b"}})
	e.store.CreateKey(ctx, e.member.ID, repo.KeyInput{Alias: "k", GroupID: e.group.ID, BudgetType: "unlimited", Models: []string{"a"}})
	if u := decodeInto[userWithKeys](t, e.as("admin@x.io", "PUT", path, map[string]any{"group_id": narrow.ID})); u.AffectedKeys != 1 {
		t.Fatalf("group move: %+v", u)
	}
	entries, _, _ := e.store.ListAudit(ctx, repo.AuditFilter{Action: "user.update", Limit: 50})
	var d struct {
		Before, After struct {
			ExtraModels []string `json:"extra_models"`
		}
	}
	if len(entries) == 0 || json.Unmarshal(entries[len(entries)-1].Details, &d) != nil || len(d.Before.ExtraModels) != 0 || !slices.Equal(d.After.ExtraModels, []string{"c"}) {
		t.Fatalf("audit: %+v", entries)
	}
}

// A model deleted (or renamed) after it was granted stays on the stored list; resending that
// list with an unrelated change must still save, while a newly added unknown name is refused.
func TestModelAccess_StoredStaleNamesDontBlockSave(t *testing.T) {
	e := newTenv(t)
	ctx := context.Background()
	accessModels(e)
	group := "/console/api/v1/groups/" + e.group.ID
	user := "/console/api/v1/users/" + e.member.ID
	if w := e.as("admin@x.io", "PUT", group, map[string]any{"name": "default", "budget_type": "unlimited", "models": []string{"a", "b"}}); w.Code != 200 {
		t.Fatalf("group: %d %s", w.Code, w.Body)
	}
	if w := e.as("admin@x.io", "PUT", user, map[string]any{"extra_models": []string{"c"}}); w.Code != 200 {
		t.Fatalf("user: %d %s", w.Code, w.Body)
	}
	ms, _ := e.store.ListModels(ctx)
	for _, m := range ms {
		if m.Name == "a" || m.Name == "c" {
			if err := e.store.DeleteModel(ctx, m.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	w := e.as("admin@x.io", "PUT", group, map[string]any{"name": "default", "budget_type": "daily", "budget_nanos": 5, "models": []string{"a", "b"}})
	if g := decodeInto[repo.Group](t, w); w.Code != 200 || !slices.Equal(g.Models, []string{"a", "b"}) || g.BudgetNanos != 5 {
		t.Fatalf("group resave: %d %s", w.Code, w.Body)
	}
	if w := e.as("admin@x.io", "PUT", user, map[string]any{"name": "M2", "extra_models": []string{"c"}}); w.Code != 200 {
		t.Fatalf("user resave: %d %s", w.Code, w.Body)
	}
	for path, body := range map[string]map[string]any{
		group: {"name": "default", "budget_type": "unlimited", "models": []string{"a", "ghost"}},
		user:  {"extra_models": []string{"c", "ghost"}},
	} {
		if w := e.as("admin@x.io", "PUT", path, body); w.Code != 400 {
			t.Fatalf("new unknown name on %s: %d", path, w.Code)
		}
	}
}

func TestUsers_UpdateGroupMustExist(t *testing.T) {
	e := newTenv(t)
	path := "/console/api/v1/users/" + e.member.ID
	if w := e.as("admin@x.io", "PUT", path, map[string]any{"group_id": "00000000-0000-0000-0000-000000000000"}); w.Code != 400 || !strings.Contains(w.Body.String(), "unknown group") {
		t.Fatalf("unknown group: %d %s", w.Code, w.Body)
	}
	// a resolvable group (even the unchanged one) still saves
	if w := e.as("admin@x.io", "PUT", path, map[string]any{"group_id": e.group.ID}); w.Code != 200 {
		t.Fatalf("valid group: %d %s", w.Code, w.Body)
	}
}

func TestUsers_CannotChangeOwnModelAccess(t *testing.T) {
	e := newTenv(t)
	accessModels(e)
	other, _ := e.store.CreateGroup(context.Background(), repo.GroupInput{Name: "other", BudgetType: "unlimited"})
	self := "/console/api/v1/users/" + e.admin.ID
	for _, path := range []string{self, "/console/api/v1/users/" + strings.ToUpper(e.admin.ID)} { // any id spelling Postgres accepts is still you
		for _, body := range []map[string]any{{"extra_models": []string{"a"}}, {"group_id": other.ID}, {"role": "viewer"}} {
			if w := e.as("admin@x.io", "PUT", path, body); w.Code != 400 {
				t.Fatalf("self %s %v: %d", path, body, w.Code)
			}
		}
	}
	// unchanged values are fine, e.g. a full row save
	if w := e.as("admin@x.io", "PUT", self, map[string]any{"name": "Root", "group_id": e.group.ID, "extra_models": []string{}}); w.Code != 200 {
		t.Fatalf("no-op self save: %d %s", w.Code, w.Body)
	}
}
