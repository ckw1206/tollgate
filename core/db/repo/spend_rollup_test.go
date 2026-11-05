package repo_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"tollgate/core/db/repo"
)

// rollupFixture seeds spend across two keys, two models, a playground
// (key-less) row and three days, at minutes that never sit on an hour.
func rollupFixture(t *testing.T) (fixture, time.Time) {
	t.Helper()
	f := newFixture(t, "unlimited")
	ctx := context.Background()
	m2, err := f.s.CreateModel(ctx, repo.ModelInput{Name: "m2", Provider: "openai", UpstreamModel: "m2", Enabled: true, Extra: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	_, k2, err := f.s.CreateKey(ctx, f.u.ID, repo.KeyInput{Alias: "k2", GroupID: f.g.ID, BudgetType: "unlimited"})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	var recs []repo.SpendRecord
	for i := 0; i < 72; i++ {
		at := base.Add(time.Duration(i)*time.Hour + time.Duration(7+i%50)*time.Minute)
		r := f.rec(int64(1000+i), at)
		r.InputTokens, r.OutputTokens, r.CacheReadTokens, r.CacheWriteTokens = int64(i), int64(2*i), 3, 1
		switch i % 4 {
		case 1:
			r.KeyID = k2.ID
		case 2:
			r.ModelID, r.ModelName = m2.ID, "m2"
		case 3:
			r.KeyID, r.Source = "", "playground"
		}
		recs = append(recs, r, r) // two per hour: the rollup must sum within a bucket
	}
	// Two batches split inside an hour's pair (71 is odd), so the second
	// batch's first upsert lands on a row the first batch created.
	if _, err := f.s.RecordSpend(ctx, recs[:71]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.RecordSpend(ctx, recs[71:]); err != nil {
		t.Fatal(err)
	}
	return f, base
}

var rollupGroupBys = []string{"hour", "day", "week", "month", "year", "model", "key", "user", "group",
	"model,hour", "model,day", "model,week", "model,month", "model,year"}

func querySpendAll(t *testing.T, s *repo.Store, from, to time.Time, scope repo.SpendFilter) map[string][]repo.SpendRow {
	t.Helper()
	out := map[string][]repo.SpendRow{}
	for _, gb := range rollupGroupBys {
		f := scope
		f.From, f.To, f.GroupBy = from, to, gb
		rows, err := s.QuerySpend(context.Background(), f)
		if err != nil {
			t.Fatalf("%s: %v", gb, err)
		}
		out[gb] = rows
	}
	return out
}

// #26: the rollup is written with the logs, in the same transaction, and its
// totals equal the logs' for the same rows.
func TestSpendHourly_MatchesLogs(t *testing.T) {
	f, _ := rollupFixture(t)
	ctx := context.Background()
	for _, col := range []string{"count(*)", "sum(cost_nanos)", "sum(input_tokens)", "sum(output_tokens)", "sum(cache_read_tokens)", "sum(cache_write_tokens)"} {
		rollCol := col
		if col == "count(*)" {
			rollCol = "sum(requests)"
		}
		eq, err := f.s.QueryForTest(ctx, `SELECT (SELECT `+col+` FROM spend_logs)::bigint = (SELECT `+rollCol+` FROM spend_hourly)::bigint`)
		if err != nil || eq != true {
			t.Fatalf("%s: logs and rollup disagree (%v, %v)", col, eq, err)
		}
	}
	// One row per (hour, key, user, group, model): 72 hours, two records each.
	n, err := f.s.QueryForTest(ctx, `SELECT count(*) FROM spend_hourly`)
	if err != nil || n != int64(72) {
		t.Fatalf("rollup rows = %v (%v), want 72", n, err)
	}
	// A key-less row is stored with a NULL key and still buckets as "playground".
	pg, err := f.s.QueryForTest(ctx, `SELECT count(*) FROM spend_hourly WHERE key_id IS NULL`)
	if err != nil || pg != int64(18) {
		t.Fatalf("playground rollup rows = %v (%v), want 18", pg, err)
	}
}

// #26: QuerySpend returns the same rows after the matching spend_logs are
// pruned, for every group_by, for scoped queries, and with the prune cutoff in
// the middle of an hour (that hour comes from the rollup, the next from logs).
func TestQuerySpend_SurvivesRetention(t *testing.T) {
	f, base := rollupFixture(t)
	ctx := context.Background()
	from, to := base.Add(-24*time.Hour), base.Add(96*time.Hour)
	scopes := map[string]repo.SpendFilter{
		"all":   {},
		"user":  {UserID: f.u.ID},
		"group": {GroupID: f.g.ID},
		"key":   {KeyID: f.k.ID},
	}
	before := map[string]map[string][]repo.SpendRow{}
	for name, sc := range scopes {
		before[name] = querySpendAll(t, f.s, from, to, sc)
	}
	// Totals over a sub-range that starts before and ends after the cutoff.
	subFrom, subTo := base.Add(10*time.Hour), base.Add(50*time.Hour)
	subBefore := querySpendAll(t, f.s, subFrom, subTo, repo.SpendFilter{})

	cutoff := base.Add(30*time.Hour + 40*time.Minute)
	if n, err := f.s.DeleteSpendBefore(ctx, cutoff); err != nil || n == 0 {
		t.Fatalf("prune: %d rows, %v", n, err)
	}
	for name, sc := range scopes {
		after := querySpendAll(t, f.s, from, to, sc)
		for _, gb := range rollupGroupBys {
			if !reflect.DeepEqual(before[name][gb], after[gb]) {
				t.Errorf("%s/%s after prune:\n got  %+v\n want %+v", name, gb, after[gb], before[name][gb])
			}
		}
	}
	subAfter := querySpendAll(t, f.s, subFrom, subTo, repo.SpendFilter{})
	for _, gb := range rollupGroupBys {
		if !reflect.DeepEqual(subBefore[gb], subAfter[gb]) {
			t.Errorf("sub-range %s after prune:\n got  %+v\n want %+v", gb, subAfter[gb], subBefore[gb])
		}
	}
	// With every log pruned, the rollup alone still answers.
	if _, err := f.s.DeleteSpendBefore(ctx, to); err != nil {
		t.Fatal(err)
	}
	all := querySpendAll(t, f.s, from, to, repo.SpendFilter{})
	for _, gb := range rollupGroupBys {
		if !reflect.DeepEqual(before["all"][gb], all[gb]) {
			t.Errorf("%s with no logs left:\n got  %+v\n want %+v", gb, all[gb], before["all"][gb])
		}
	}
}

// Day buckets of rolled-up spend follow the server timezone, like the logs'.
func TestQuerySpend_RollupUsesServerTimezone(t *testing.T) {
	f, base := rollupFixture(t)
	ctx := context.Background()
	st, _ := f.s.GetSettings(ctx)
	st.Timezone = "Asia/Taipei"
	if _, err := f.s.UpdateSettings(ctx, *st); err != nil {
		t.Fatal(err)
	}
	from, to := base.Add(-24*time.Hour), base.Add(96*time.Hour)
	before := querySpendAll(t, f.s, from, to, repo.SpendFilter{})
	if _, err := f.s.DeleteSpendBefore(ctx, to); err != nil {
		t.Fatal(err)
	}
	after := querySpendAll(t, f.s, from, to, repo.SpendFilter{})
	if !reflect.DeepEqual(before["day"], after["day"]) || len(after["day"]) != 4 {
		t.Fatalf("Taipei days from the rollup:\n got  %+v\n want %+v (4 local days)", after["day"], before["day"])
	}
}

// Separate batches into the same hour add up, for a keyed bucket and for a
// key-less (NULL key) one: the upsert must sum, and NULL keys must collide.
func TestSpendHourly_UpsertSumsAcrossBatches(t *testing.T) {
	f := newFixture(t, "unlimited")
	ctx := context.Background()
	at := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	keyed := f.rec(100, at.Add(5*time.Minute))
	keyless := f.rec(7, at.Add(6*time.Minute))
	keyless.KeyID, keyless.Source = "", "playground"
	for i := 0; i < 3; i++ {
		if _, err := f.s.RecordSpend(ctx, []repo.SpendRecord{keyed, keyless}); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		where       string
		rows, n, ct int64
	}{{"key_id IS NOT NULL", 1, 3, 300}, {"key_id IS NULL", 1, 3, 21}} {
		got, err := f.s.QueryForTest(ctx, `SELECT array[count(*), sum(requests), sum(cost_nanos)]::bigint[]::text FROM spend_hourly WHERE `+c.where)
		want := fmt.Sprintf("{%d,%d,%d}", c.rows, c.n, c.ct)
		if err != nil || got != want {
			t.Fatalf("%s: rows,requests,cost = %v (%v), want %s", c.where, got, err, want)
		}
	}
}
