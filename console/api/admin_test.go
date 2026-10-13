package consoleapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"tollgate/core/db/repo"
)

func TestCredentials_SecretNeverReturnedOrAudited(t *testing.T) {
	e := newTenv(t)
	w := e.as("admin@x.io", "POST", "/console/api/v1/credentials", map[string]string{"name": "openai-prod", "kind": "api_key", "secret": "«redacted:sk-…»"})
	if w.Code != 201 || strings.Contains(w.Body.String(), "«redacted:sk-…»") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if list := e.as("admin@x.io", "GET", "/console/api/v1/credentials", nil).Body.String(); strings.Contains(list, "sk-very") || strings.Contains(list, "ciphertext") {
		t.Fatalf("list leaks: %s", list)
	}
	entries, _, _ := e.store.ListAudit(context.Background(), repo.AuditFilter{Limit: 50})
	for _, a := range entries {
		if strings.Contains(string(a.Details), "sk-very") {
			t.Fatal("secret in audit log")
		}
	}
	if w := e.as("member@x.io", "GET", "/console/api/v1/credentials", nil); w.Code != 403 {
		t.Fatalf("member: %d", w.Code)
	}
}

func TestModels_WriteReloadsRegistryAndValidates(t *testing.T) {
	e := newTenv(t)
	body := map[string]any{"name": "gpt", "provider": "openai", "upstream_model": "gpt-4o", "enabled": true,
		"price_input_micros": 2500000, "price_output_micros": 10000000}
	w := e.as("admin@x.io", "POST", "/console/api/v1/models", body)
	if w.Code != 201 || e.reloads != 1 {
		t.Fatalf("%d reloads=%d %s", w.Code, e.reloads, w.Body)
	}
	body["provider"] = "cohere"
	body["name"] = "x"
	if w := e.as("admin@x.io", "POST", "/console/api/v1/models", body); w.Code != 400 {
		t.Fatalf("bad provider: %d", w.Code)
	}
	if w := e.as("viewer@x.io", "GET", "/console/api/v1/models", nil); w.Code != 200 {
		t.Fatalf("viewer list: %d", w.Code)
	}
	if w := e.as("member@x.io", "POST", "/console/api/v1/models", body); w.Code != 403 {
		t.Fatalf("member write: %d", w.Code)
	}
}

func TestModels_DeleteWithSpendHistoryIs409(t *testing.T) {
	e := newTenv(t)
	ctx := context.Background()
	m := decodeInto[repo.Model](t, e.as("admin@x.io", "POST", "/console/api/v1/models",
		map[string]any{"name": "m", "provider": "openai", "upstream_model": "m", "enabled": true}))
	_, k, _ := e.store.CreateKey(ctx, e.admin.ID, repo.KeyInput{Alias: "k", GroupID: e.group.ID, BudgetType: "unlimited"})
	e.store.RecordSpend(ctx, []repo.SpendRecord{{KeyID: k.ID, UserID: e.admin.ID, GroupID: e.group.ID, ModelID: m.ID, ModelName: "m",
		Provider: "openai", Status: "success", HTTPStatus: 200, CreatedAt: time.Now()}})
	if w := e.as("admin@x.io", "DELETE", "/console/api/v1/models/"+m.ID, nil); w.Code != 409 {
		t.Fatalf("%d", w.Code)
	}
}

func TestSpend_MemberSeesOnlyOwn(t *testing.T) {
	e := newTenv(t)
	ctx := context.Background()
	m, _ := e.store.CreateModel(ctx, repo.ModelInput{Name: "m", Provider: "openai", UpstreamModel: "m", Enabled: true, Extra: json.RawMessage(`{}`)})
	var recs []repo.SpendRecord
	for _, u := range []*repo.User{e.admin, e.member} {
		_, k, _ := e.store.CreateKey(ctx, u.ID, repo.KeyInput{Alias: "k", GroupID: e.group.ID, BudgetType: "unlimited"})
		recs = append(recs, repo.SpendRecord{KeyID: k.ID, UserID: u.ID, GroupID: e.group.ID, ModelID: m.ID, ModelName: "m",
			Provider: "openai", CostNanos: 100, Status: "success", HTTPStatus: 200, CreatedAt: time.Now()})
	}
	e.store.RecordSpend(ctx, recs)
	type out struct {
		Rows   []repo.SpendRow
		Recent []repo.SpendLog
	}
	// member tries to widen the scope with user_id; it must be ignored
	got := decodeInto[out](t, e.as("member@x.io", "GET", "/console/api/v1/spend?group_by=user&user_id="+e.admin.ID, nil))
	if len(got.Rows) != 1 || got.Rows[0].Bucket != e.member.ID || len(got.Recent) != 1 {
		t.Fatalf("member: %+v", got)
	}
	all := decodeInto[out](t, e.as("admin@x.io", "GET", "/console/api/v1/spend?group_by=user", nil))
	if len(all.Rows) != 2 {
		t.Fatalf("admin: %+v", all)
	}
}

func TestSettingsAndAudit_AdminOnly(t *testing.T) {
	e := newTenv(t)
	if w := e.as("member@x.io", "GET", "/console/api/v1/settings", nil); w.Code != 403 {
		t.Fatalf("member settings: %d", w.Code)
	}
	w := e.as("admin@x.io", "PUT", "/console/api/v1/settings", map[string]string{"server_name": "Mine", "timezone": "Asia/Taipei", "currency": "USD"})
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := e.as("admin@x.io", "PUT", "/console/api/v1/settings", map[string]string{"server_name": "Mine", "timezone": "Nowhere/Land", "currency": "USD"}); w.Code != 400 {
		t.Fatalf("bad tz: %d", w.Code)
	}
	a := decodeInto[struct {
		Entries []repo.AuditEntry
		Total   int
	}](t, e.as("admin@x.io", "GET", "/console/api/v1/audit?action=settings.update", nil))
	if a.Total != 1 {
		t.Fatalf("audit total=%d", a.Total)
	}
	if w := e.as("member@x.io", "GET", "/console/api/v1/audit", nil); w.Code != 403 {
		t.Fatalf("member audit: %d", w.Code)
	}
}
