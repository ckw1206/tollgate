package consoleapi

import (
	"context"
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
	if w := e.as("admin@x.io", "DELETE", "/console/api/v1/users/"+e.admin.ID, nil); w.Code != 400 {
		t.Fatalf("self-disable: %d", w.Code)
	}
	if w := e.as("admin@x.io", "PUT", "/console/api/v1/users/"+e.member.ID, map[string]any{"role": "viewer"}); w.Code != 200 {
		t.Fatalf("demote other: %d %s", w.Code, w.Body)
	}
}

func TestGroups_MemberSeesOnlyOwn(t *testing.T) {
	e := newTenv(t)
	e.store.CreateGroup(context.Background(), repo.GroupInput{Name: "other", BudgetType: "unlimited"})
	gs := decodeInto[[]repo.Group](t, e.as("member@x.io", "GET", "/console/api/v1/groups", nil))
	if len(gs) != 1 || gs[0].ID != e.group.ID {
		t.Fatalf("%+v", gs)
	}
	all := decodeInto[[]repo.Group](t, e.as("admin@x.io", "GET", "/console/api/v1/groups", nil))
	if len(all) != 2 {
		t.Fatalf("admin sees %d", len(all))
	}
}
