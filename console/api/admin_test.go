package consoleapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	pngenc "image/png"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	if w := e.as("member@x.io", "GET", "/console/api/v1/spend?group_by=week", nil); w.Code != 400 {
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
	if err := e.store.RecordSpend(ctx, recs); err != nil {
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
	if got := decodeInto[[]repo.SpendLog](t, e.as("admin@x.io", "GET", "/console/api/v1/spend/requests?limit=500", nil)); len(got) != 100 {
		t.Fatalf("limit=500 returned %d rows", len(got))
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
