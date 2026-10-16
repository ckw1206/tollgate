package consoleapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	pngenc "image/png"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"tollgate/console/rbac"
	"tollgate/core/db/repo"
)

func TestCredentials_SecretNeverReturnedOrAudited(t *testing.T) {
	e := newTenv(t)
	w := e.as("admin@x.io", "POST", "/console/api/v1/credentials", map[string]string{"name": "openai-prod", "provider": "openai", "kind": "api_key", "secret": "test-very-secret-value"})
	if w.Code != 201 || strings.Contains(w.Body.String(), "very-secret") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if list := e.as("admin@x.io", "GET", "/console/api/v1/credentials", nil).Body.String(); strings.Contains(list, "very-secret") || strings.Contains(list, "ciphertext") {
		t.Fatalf("list leaks: %s", list)
	}
	entries, _, _ := e.store.ListAudit(context.Background(), repo.AuditFilter{Limit: 50})
	for _, a := range entries {
		if strings.Contains(string(a.Details), "very-secret") {
			t.Fatal("secret in audit log")
		}
	}
	if w := e.as("member@x.io", "GET", "/console/api/v1/credentials", nil); w.Code != 403 {
		t.Fatalf("member: %d", w.Code)
	}
}

func TestModels_WriteReloadsRegistryAndValidates(t *testing.T) {
	e := newTenv(t)
	body := map[string]any{"name": "gpt", "provider": "openai", "upstream_model": "gpt-4o", "api_base": "http://10.0.0.5:8000/v1", "enabled": true,
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
		map[string]any{"name": "m", "provider": "openai", "upstream_model": "m", "api_base": "http://10.0.0.5:8000/v1", "enabled": true}))
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
	type out struct{ Rows []repo.SpendRow }
	// member tries to widen the scope with user_id; it must be ignored
	got := decodeInto[out](t, e.as("member@x.io", "GET", "/console/api/v1/spend?group_by=user&user_id="+e.admin.ID, nil))
	if len(got.Rows) != 1 || got.Rows[0].Bucket != e.member.ID {
		t.Fatalf("member: %+v", got)
	}
	all := decodeInto[out](t, e.as("admin@x.io", "GET", "/console/api/v1/spend?group_by=user", nil))
	if len(all.Rows) != 2 {
		t.Fatalf("admin: %+v", all)
	}
	md := decodeInto[out](t, e.as("member@x.io", "GET", "/console/api/v1/spend?group_by=model,day", nil))
	if len(md.Rows) != 1 || md.Rows[0].Bucket != "m" || md.Rows[0].Day != time.Now().UTC().Format("2006-01-02") || md.Rows[0].CostNanos != 100 {
		t.Fatalf("model,day: %+v", md)
	}
	if w := e.as("member@x.io", "GET", "/console/api/v1/spend?group_by=hourly", nil); w.Code != 400 {
		t.Fatalf("bad group_by: %d", w.Code)
	}
}

func TestSpendRequests_ScopedNewestFirstAndCapped(t *testing.T) {
	e := newTenv(t)
	ctx := context.Background()
	m, _ := e.store.CreateModel(ctx, repo.ModelInput{Name: "m", Provider: "openai", UpstreamModel: "m", Enabled: true, Extra: json.RawMessage(`{}`)})
	var recs []repo.SpendRecord
	var raws []string
	t0 := time.Now().Add(-time.Hour)
	for _, u := range []*repo.User{e.admin, e.member, e.viewer} {
		raw, k, _ := e.store.CreateKey(ctx, u.ID, repo.KeyInput{Alias: "key-" + u.Role, GroupID: e.group.ID, BudgetType: "unlimited"})
		raws = append(raws, raw)
		n := 1
		if u == e.admin {
			n = 120 // more than the 100 cap
		}
		for range n { // one second apart, in insertion order
			recs = append(recs, repo.SpendRecord{KeyID: k.ID, UserID: u.ID, GroupID: e.group.ID, ModelID: m.ID, ModelName: "m",
				Provider: "openai", CostNanos: 100, Status: "success", HTTPStatus: 200, CreatedAt: t0.Add(time.Duration(len(recs)) * time.Second)})
		}
	}
	if _, err := e.store.RecordSpend(ctx, recs); err != nil {
		t.Fatal(err)
	}
	// member and viewer try to widen the scope with user_id; they only ever see their own rows
	for _, u := range []*repo.User{e.member, e.viewer} {
		got := decodeInto[[]repo.SpendLog](t, e.as(u.Email, "GET", "/console/api/v1/spend/requests?limit=100&user_id="+e.admin.ID, nil))
		if len(got) != 1 || got[0].UserID != u.ID || got[0].OwnerEmail != u.Email || got[0].KeyAlias != "key-"+u.Role {
			t.Fatalf("%s: %+v", u.Email, got)
		}
	}
	w := e.as("admin@x.io", "GET", "/console/api/v1/spend/requests?limit=100", nil)
	for _, s := range append(raws, "key_hash") {
		if strings.Contains(w.Body.String(), s) {
			t.Fatalf("leaks key material %q", s)
		}
	}
	owners := map[string]bool{}
	for _, l := range decodeInto[[]repo.SpendLog](t, w) {
		if l.KeyPrefix == "" || l.KeyAlias != "key-"+strings.TrimSuffix(l.OwnerEmail, "@x.io") {
			t.Fatalf("row %+v", l)
		}
		owners[l.OwnerEmail] = true
	}
	if len(owners) != 3 {
		t.Fatalf("admin sees owners %v", owners)
	}
	def := decodeInto[[]repo.SpendLog](t, e.as("admin@x.io", "GET", "/console/api/v1/spend/requests", nil))
	if len(def) != 10 || def[0].OwnerEmail != "viewer@x.io" || def[1].OwnerEmail != "member@x.io" || !def[1].CreatedAt.After(def[2].CreatedAt) {
		t.Fatalf("admin default: %d rows, first %+v", len(def), def[0])
	}
	for q, want := range map[string]int{"500": 100, "0": 1, "-3": 1} {
		if got := decodeInto[[]repo.SpendLog](t, e.as("admin@x.io", "GET", "/console/api/v1/spend/requests?limit="+q, nil)); len(got) != want {
			t.Fatalf("limit=%s returned %d rows, want %d", q, len(got), want)
		}
	}
	if w := e.as("admin@x.io", "GET", "/console/api/v1/spend/requests?from=yesterday", nil); w.Code != 400 {
		t.Fatalf("bad from: %d", w.Code)
	}
	if w := e.raw("", "GET", "/console/api/v1/spend/requests", nil); w.Code != 401 {
		t.Fatalf("anonymous: %d", w.Code)
	}
}

func TestSettingsAndAudit_AdminOnly(t *testing.T) {
	e := newTenv(t)
	if w := e.as("member@x.io", "GET", "/console/api/v1/settings", nil); w.Code != 403 {
		t.Fatalf("member settings: %d", w.Code)
	}
	for _, who := range []string{"member@x.io", "viewer@x.io"} {
		if w := e.as(who, "PUT", "/console/api/v1/settings", map[string]string{"server_name": "Pwned", "timezone": "UTC", "currency": "USD", "server_icon": ""}); w.Code != 403 {
			t.Fatalf("%s PUT settings: %d", who, w.Code)
		}
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
	if w := e.as("admin@x.io", "GET", "/console/api/v1/audit?limit=500&offset=-1", nil); w.Code != 200 {
		t.Fatalf("out-of-range paging: %d %s", w.Code, w.Body)
	}
	e.store.InsertAudit(context.Background(), repo.AuditEntry{ActorEmail: "admin@x.io", Action: "key.create", Resource: "key"})
	if z := decodeInto[struct {
		Entries []repo.AuditEntry
		Total   int
	}](t, e.as("admin@x.io", "GET", "/console/api/v1/audit?limit=0", nil)); len(z.Entries) != 1 || z.Total < 2 {
		t.Fatalf("limit=0: %d entries of %d", len(z.Entries), z.Total)
	}
	if w := e.as("member@x.io", "GET", "/console/api/v1/audit", nil); w.Code != 403 {
		t.Fatalf("member audit: %d", w.Code)
	}
}

func TestModels_NonWritersDontSeeUpstreamDetails(t *testing.T) {
	e := newTenv(t)
	body := map[string]any{"name": "azure", "provider": "openai", "upstream_model": "gpt-4o", "enabled": true,
		"api_base": "https://corp.example.com/openai", "extra": map[string]any{"headers": map[string]string{"api-key": "upstream-secret"}}}
	if w := e.as("admin@x.io", "POST", "/console/api/v1/models", body); w.Code != 201 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if b := e.as("admin@x.io", "GET", "/console/api/v1/models", nil).Body.String(); !strings.Contains(b, "upstream-secret") {
		t.Fatal("admin should see extra")
	}
	for _, who := range []string{"member@x.io", "viewer@x.io"} {
		b := e.as(who, "GET", "/console/api/v1/models", nil).Body.String()
		if strings.Contains(b, "upstream-secret") || strings.Contains(b, "corp.example.com") || !strings.Contains(b, `"name":"azure"`) {
			t.Fatalf("%s sees upstream details: %s", who, b)
		}
	}
}

func TestModels_NonWritersSeeEffectiveSet(t *testing.T) {
	e := newTenv(t)
	ctx := context.Background()
	accessModels(e) // a, b enabled; c disabled
	e.store.UpdateGroup(ctx, e.group.ID, repo.GroupInput{Name: "default", BudgetType: "unlimited", Models: &[]string{"a"}})
	e.store.UpdateUser(ctx, e.member.ID, repo.UserUpdate{ExtraModels: &[]string{"a", "b", "c"}})
	seen := func(who string) (out []string) {
		w := e.as(who, "GET", "/console/api/v1/models", nil)
		if strings.Contains(w.Body.String(), "corp.example.com") {
			t.Fatalf("%s sees api_base: %s", who, w.Body)
		}
		for _, m := range decodeInto[[]struct{ Name, Access string }](t, w) {
			out = append(out, m.Name+":"+m.Access)
		}
		return out
	}
	// "group" wins over "granted"; disabled models stay hidden even when granted
	if got := seen("member@x.io"); !slices.Equal(got, []string{"a:group", "b:granted"}) {
		t.Fatalf("member: %v", got)
	}
	if got := seen("viewer@x.io"); !slices.Equal(got, []string{"a:group"}) {
		t.Fatalf("viewer: %v", got)
	}
	w := e.as("admin@x.io", "GET", "/console/api/v1/models", nil)
	if b := w.Body.String(); len(decodeInto[[]repo.Model](t, w)) != 3 || strings.Contains(b, `"access"`) || !strings.Contains(b, "corp.example.com") {
		t.Fatalf("admin: %s", b)
	}
}

// TestRoutes_DeniedRoles: every permissioned console route refuses the roles
// that lack its permission. Paths use the caller's own user and group, so this
// also covers members granting themselves models or moving groups.
func TestRoutes_DeniedRoles(t *testing.T) {
	e := newTenv(t)
	id := "00000000-0000-0000-0000-000000000000"
	routes := []struct {
		method, path string
		perm         rbac.Permission
	}{
		{"GET", "/invites", rbac.InvitesRead},
		{"POST", "/invites", rbac.InvitesWrite},
		{"DELETE", "/invites/" + id, rbac.InvitesWrite},
		{"GET", "/users", rbac.UsersRead},
		{"POST", "/users", rbac.UsersWrite},
		{"PUT", "/users/" + e.member.ID, rbac.UsersWrite},
		{"PUT", "/users/" + e.viewer.ID, rbac.UsersWrite},
		{"DELETE", "/users/" + e.admin.ID, rbac.UsersWrite},
		{"GET", "/groups", rbac.GroupsReadOwn},
		{"POST", "/groups", rbac.GroupsWrite},
		{"PUT", "/groups/" + e.group.ID, rbac.GroupsWrite},
		{"GET", "/keys", rbac.KeysReadOwn},
		{"POST", "/keys", rbac.KeysWriteOwn},
		{"PUT", "/keys/" + id, rbac.KeysWriteOwn},
		{"DELETE", "/keys/" + id, rbac.KeysWriteOwn},
		{"POST", "/keys/" + id + "/rotate", rbac.KeysWriteOwn},
		{"GET", "/credentials", rbac.ModelsWrite},
		{"POST", "/credentials", rbac.ModelsWrite},
		{"PUT", "/credentials/" + id, rbac.ModelsWrite},
		{"DELETE", "/credentials/" + id, rbac.ModelsWrite},
		{"POST", "/credentials/test", rbac.ModelsWrite},
		{"GET", "/models", rbac.ModelsRead},
		{"POST", "/models", rbac.ModelsWrite},
		{"PUT", "/models/" + id, rbac.ModelsWrite},
		{"DELETE", "/models/" + id, rbac.ModelsWrite},
		{"GET", "/spend", rbac.SpendReadOwn},
		{"GET", "/audit", rbac.AuditRead},
		{"GET", "/settings", rbac.SettingsRead},
		{"PUT", "/settings", rbac.SettingsWrite},
	}
	body := map[string]any{"name": "default", "budget_type": "unlimited", "models": []string{}, "extra_models": []string{}, "group_id": e.group.ID}
	denied := 0
	for _, u := range []*repo.User{e.member, e.viewer} {
		for _, rt := range routes {
			if rbac.Has(u.Role, rt.perm) {
				continue
			}
			denied++
			if w := e.as(u.Email, rt.method, p+rt.path, body); w.Code != 403 {
				t.Errorf("%s %s %s: %d, want 403", u.Role, rt.method, rt.path, w.Code)
			}
		}
	}
	if denied < 40 {
		t.Fatalf("only %d denied cases; role permissions changed?", denied)
	}
}

func TestModels_ContextWindowRoundTrip(t *testing.T) {
	e := newTenv(t)
	ctx := context.Background()
	cw := int64(128000)
	// (i) create with a context_window: 201, reads back pointing at 128000
	created := decodeInto[repo.Model](t, e.as("admin@x.io", "POST", "/console/api/v1/models", map[string]any{
		"name": "cw", "provider": "openai", "upstream_model": "gpt-4o", "api_base": "http://10.0.0.5:8000/v1",
		"enabled": true, "price_input_micros": 1, "price_output_micros": 1, "context_window": cw}))
	if created.ContextWindow == nil || *created.ContextWindow != 128000 {
		t.Fatalf("create: ContextWindow=%v, want 128000", created.ContextWindow)
	}
	// (ii) update it: 200, reads back 64000
	newCW := int64(64000)
	updated := decodeInto[repo.Model](t, e.as("admin@x.io", "PUT", "/console/api/v1/models/"+created.ID, map[string]any{
		"name": "cw", "provider": "openai", "upstream_model": "gpt-4o", "api_base": "http://10.0.0.5:8000/v1",
		"enabled": true, "price_input_micros": 1, "price_output_micros": 1, "context_window": newCW}))
	if updated.ContextWindow == nil || *updated.ContextWindow != 64000 {
		t.Fatalf("update: ContextWindow=%v, want 64000", updated.ContextWindow)
	}
	// (iii) update omitting it: full-row replace clears it back to nil
	omitted := decodeInto[repo.Model](t, e.as("admin@x.io", "PUT", "/console/api/v1/models/"+created.ID, map[string]any{
		"name": "cw", "provider": "openai", "upstream_model": "gpt-4o", "api_base": "http://10.0.0.5:8000/v1",
		"enabled": true, "price_input_micros": 1, "price_output_micros": 1}))
	if omitted.ContextWindow != nil {
		t.Fatalf("omit: ContextWindow=%v, want nil", *omitted.ContextWindow)
	}
	// The non-writer list must still carry the field (user-facing metadata, not redacted).
	// Re-set it, then check member sees it.
	e.store.UpdateModel(ctx, created.ID, repo.ModelInput{Name: "cw", Provider: "openai", UpstreamModel: "gpt-4o",
		APIBase: "http://10.0.0.5:8000/v1", Enabled: true, PriceInputMicros: 1, PriceOutputMicros: 1, ContextWindow: &cw})
	if b := e.as("member@x.io", "GET", "/console/api/v1/models", nil).Body.String(); !strings.Contains(b, `"context_window":128000`) {
		t.Fatalf("member does not see context_window: %s", b)
	}
	// (iv) a non-positive value is rejected
	for _, bad := range []any{0, -5} {
		body := map[string]any{"name": "cw-bad", "provider": "openai", "upstream_model": "gpt-4o",
			"api_base": "http://10.0.0.5:8000/v1", "enabled": true, "context_window": bad}
		if w := e.as("admin@x.io", "POST", "/console/api/v1/models", body); w.Code != 400 {
			t.Fatalf("context_window=%v: %d, want 400", bad, w.Code)
		}
	}
}

func TestModels_RejectsExtraTheRegistryCantLoad(t *testing.T) {
	e := newTenv(t)
	for _, extra := range []any{[]any{}, "x", map[string]any{"headers": map[string]any{"X": 1}}} {
		body := map[string]any{"name": "m", "provider": "openai", "upstream_model": "m", "api_base": "http://10.0.0.5:8000/v1", "extra": extra}
		if w := e.as("admin@x.io", "POST", "/console/api/v1/models", body); w.Code != 400 {
			t.Fatalf("extra %v: %d", extra, w.Code)
		}
	}
}

func TestSettings_IconMustBeImageDataURL(t *testing.T) {
	e := newTenv(t)
	var png bytes.Buffer
	if err := pngenc.Encode(&png, image.NewRGBA(image.Rect(0, 0, 256, 256))); err != nil {
		t.Fatal(err)
	}
	dataURL := func(kind string, b []byte) string {
		return "data:image/" + kind + ";base64," + base64.StdEncoding.EncodeToString(b)
	}
	pngOfSize := func(n int) []byte { return append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, n-8)...) }
	put := func(icon string) *httptest.ResponseRecorder {
		return e.as("admin@x.io", "PUT", "/console/api/v1/settings",
			map[string]string{"server_name": "Mine", "timezone": "UTC", "currency": "USD", "server_icon": icon})
	}
	for _, ok := range []string{"", dataURL("png", png.Bytes()), dataURL("png", pngOfSize(256<<10)), dataURL("jpeg", []byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0}), dataURL("webp", []byte("RIFF\x00\x00\x00\x00WEBPVP8 "))} {
		if w := put(ok); w.Code != 200 {
			t.Fatalf("valid icon %.40q: %d %s", ok, w.Code, w.Body)
		}
	}
	bad := []string{
		"🚦", "https://example.com/icon.png", "/tollgate.png",
		dataURL("svg+xml", []byte("<svg/>")),              // SVG not allowed
		dataURL("png", []byte("GIF89a not a png at all")), // bytes don't match the declared type
		"data:image/png;base64,%%%not-base64",
		"data:image/png," + string(png.Bytes()), // not base64
		dataURL("png", pngOfSize(256<<10+1)),    // one byte over the cap
	}
	for _, b := range bad {
		w := put(b)
		code := decodeInto[struct{ Error struct{ Code string } }](t, w).Error.Code
		if w.Code != 400 || code != "invalid_request" {
			t.Fatalf("invalid icon %.40q: %d %s", b, w.Code, code)
		}
	}
	st := decodeInto[repo.Settings](t, e.as("admin@x.io", "GET", "/console/api/v1/settings", nil))
	if !strings.HasPrefix(st.ServerIcon, "data:image/webp;base64,") {
		t.Fatalf("last valid icon not stored: %.40q", st.ServerIcon)
	}
	// The audit log records that the icon changed, not the image itself.
	entries, _, _ := e.store.ListAudit(context.Background(), repo.AuditFilter{Action: "settings.update", Limit: 50})
	for _, a := range entries {
		if strings.Contains(string(a.Details), "base64") {
			t.Fatalf("audit entry carries the icon: %.120s", a.Details)
		}
	}
	if len(entries) == 0 || !strings.Contains(string(entries[0].Details), "sha256:") {
		t.Fatalf("audit entry should show an icon digest: %+v", entries)
	}
}

func TestSettings_LandingCopy(t *testing.T) {
	e := newTenv(t)
	put := func(headline, tagline string) *httptest.ResponseRecorder {
		return e.as("admin@x.io", "PUT", "/console/api/v1/settings", map[string]string{"server_name": "Mine", "timezone": "UTC",
			"currency": "USD", "landing_headline": headline, "landing_tagline": tagline})
	}
	for _, bad := range [][2]string{{strings.Repeat("a", 81), ""}, {"", strings.Repeat("a", 161)}, {strings.Repeat("閘", 81), ""}} {
		w := put(bad[0], bad[1])
		if code := decodeInto[struct{ Error struct{ Code string } }](t, w).Error.Code; w.Code != 400 || code != "invalid_request" {
			t.Fatalf("too long accepted: %d %s", w.Code, w.Body)
		}
	}
	// Limits count characters, not bytes: 80 and 160 three-byte runes fit.
	h, tg := strings.Repeat("閘", 80), strings.Repeat("門", 160)
	if w := put(h, tg); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	st := decodeInto[repo.Settings](t, e.as("admin@x.io", "GET", "/console/api/v1/settings", nil))
	l := decodeInto[map[string]string](t, e.raw("", "GET", "/auth/landing", nil))
	if st.LandingHeadline != h || st.LandingTagline != tg || l["landing_headline"] != h || l["landing_tagline"] != tg || l["server_name"] != "Mine" {
		t.Fatalf("round trip: %+v %v", st, l)
	}
}
