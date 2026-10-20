package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tollgate/core/db/repo"
)

// chatAs drives the chat path with a hand-built AuthInfo, bypassing the
// Bearer-key auth middleware: the playground will present a key-less
// principal (empty Key.ID, limits and access from the user's group).
func (e *env) chatAs(info *repo.AuthInfo, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	w := httptest.NewRecorder()
	e.srv.ChatAs(w, r, info)
	return w
}

// grouplessInfo builds a key-less AuthInfo for the env's user: no key, the
// user's (default, unlimited) group as billing scope, group model access.
func (e *env) keylessInfo(t *testing.T) *repo.AuthInfo {
	t.Helper()
	g, err := e.store.GetGroupByName(context.Background(), "default")
	if err != nil {
		t.Fatal(err)
	}
	return &repo.AuthInfo{Key: repo.APIKey{UserID: e.user.ID, GroupID: g.ID}, Group: *g, GroupModels: nil}
}

func TestChatAs_NoKey_UsesGroupScopeAndRecordsPlayground(t *testing.T) {
	e := newEnv(t, repo.KeyInput{})
	w := e.chatAs(e.keylessInfo(t), chatBody)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if len(e.records) != 1 {
		t.Fatalf("records=%+v", e.records)
	}
	rec := e.records[0]
	if rec.KeyID != "" || rec.Source != "playground" || rec.CostNanos <= 0 || rec.Status != "success" {
		t.Fatalf("record=%+v", rec)
	}
	if rec.UserID != e.user.ID || rec.GroupID != e.key.GroupID {
		t.Fatalf("record=%+v", rec)
	}
}

func TestChatAs_NoKey_GroupBudgetExceeded(t *testing.T) {
	e := newEnv(t, repo.KeyInput{})
	info := e.keylessInfo(t)
	// daily budget of 1 nano, already spent: the group scope must refuse
	reset := time.Now().Add(24 * time.Hour)
	info.Group.Limits = repo.Limits{BudgetType: "daily", BudgetNanos: 1, SpendNanos: 1, BudgetResetAt: &reset}
	w := e.chatAs(info, chatBody)
	if w.Code != 429 || errCode(t, w) != "budget_exceeded" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if len(e.records) != 0 {
		t.Fatalf("refused request recorded: %+v", e.records)
	}
}

func TestChatAs_NoKey_ModelOutsideGroup403(t *testing.T) {
	e := newEnv(t, repo.KeyInput{})
	info := e.keylessInfo(t)
	info.GroupModels = []string{"slow"}
	w := e.chatAs(info, chatBody) // chatBody asks for "fast"
	if w.Code != 403 || errCode(t, w) != "model_not_allowed" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if len(e.records) != 0 {
		t.Fatalf("refused request recorded: %+v", e.records)
	}
}
