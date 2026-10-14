package repo_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"tollgate/core/db/repo"
)

// TestSpendBuckets pins the C1/C2 bucket-label contract: hour/week/month/year
// buckets and the model,<granularity> sub-bucket, plus group group_by, all
// computed in the SERVER's timezone (a subtest flips the server tz to prove
// the labels are not hardcoded to UTC). Each subtest builds its own fixture so
// its seeded records never leak into a sibling's rows — no narrow-window
// bookkeeping needed.
func TestSpendBuckets(t *testing.T) {
	ctx := context.Background()

	t.Run("hour", func(t *testing.T) {
		f := newFixture(t, "unlimited")
		f.s.RecordSpend(ctx, []repo.SpendRecord{
			f.rec(10, time.Date(2026, 10, 14, 9, 5, 0, 0, time.UTC)),
			f.rec(10, time.Date(2026, 10, 14, 10, 5, 0, 0, time.UTC)),
		})
		rows, err := f.s.QuerySpend(ctx, repo.SpendFilter{
			From:    time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC),
			To:      time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC),
			GroupBy: "hour",
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 2 || rows[0].Bucket != "2026-10-14T09" || rows[1].Bucket != "2026-10-14T10" {
			t.Fatalf("hour: %+v, want 2026-10-14T09 then 2026-10-14T10", rows)
		}
		if rows[0].Day != "" || rows[1].Day != "" {
			t.Fatalf("hour: Day must be empty, got %+v", rows)
		}
	})

	// The same two records land in a different hour bucket once the server
	// timezone flips: 09:05/10:05 UTC is 17:05/18:05 in Asia/Taipei (UTC+8),
	// so the buckets move from the UTC T09/T10 (above) to T17/T18 — proving the
	// label follows the server tz rather than being hardcoded to UTC.
	t.Run("hour in server tz", func(t *testing.T) {
		f := newFixture(t, "unlimited")
		st, _ := f.s.GetSettings(ctx)
		st.Timezone = "Asia/Taipei"
		f.s.UpdateSettings(ctx, *st)
		f.s.RecordSpend(ctx, []repo.SpendRecord{
			f.rec(10, time.Date(2026, 10, 14, 9, 5, 0, 0, time.UTC)),
			f.rec(10, time.Date(2026, 10, 14, 10, 5, 0, 0, time.UTC)),
		})
		rows, err := f.s.QuerySpend(ctx, repo.SpendFilter{
			From:    time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC),
			To:      time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC),
			GroupBy: "hour",
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 2 || rows[0].Bucket != "2026-10-14T17" || rows[1].Bucket != "2026-10-14T18" {
			t.Fatalf("hour (Asia/Taipei): %+v, want 2026-10-14T17 then 2026-10-14T18", rows)
		}
	})

	t.Run("week", func(t *testing.T) {
		f := newFixture(t, "unlimited")
		f.s.RecordSpend(ctx, []repo.SpendRecord{
			f.rec(10, time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)),   // 2026-W01
			f.rec(10, time.Date(2026, 5, 15, 9, 0, 0, 0, time.UTC)),   // 2026-W20
			f.rec(10, time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)), // 2026-W42
			f.rec(10, time.Date(2027, 1, 1, 0, 30, 0, 0, time.UTC)),   // 2026-W53
		})
		rows, err := f.s.QuerySpend(ctx, repo.SpendFilter{
			From:    time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC),
			To:      time.Date(2027, 1, 2, 0, 0, 0, 0, time.UTC),
			GroupBy: "week",
		})
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"2026-W01", "2026-W20", "2026-W42", "2026-W53"}
		if len(rows) != len(want) {
			t.Fatalf("week: got %+v, want buckets %v", rows, want)
		}
		for i, w := range want {
			if rows[i].Bucket != w {
				t.Fatalf("week[%d] = %s, want %s", i, rows[i].Bucket, w)
			}
		}
	})

	t.Run("month", func(t *testing.T) {
		f := newFixture(t, "unlimited")
		f.s.RecordSpend(ctx, []repo.SpendRecord{
			f.rec(10, time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)),
		})
		rows, err := f.s.QuerySpend(ctx, repo.SpendFilter{
			From:    time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC),
			To:      time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
			GroupBy: "month",
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].Bucket != "2026-10" {
			t.Fatalf("month: %+v, want one row 2026-10", rows)
		}
	})

	t.Run("year", func(t *testing.T) {
		f := newFixture(t, "unlimited")
		f.s.RecordSpend(ctx, []repo.SpendRecord{
			f.rec(10, time.Date(2026, 12, 31, 12, 0, 0, 0, time.UTC)),
			f.rec(10, time.Date(2027, 1, 1, 0, 30, 0, 0, time.UTC)),
		})
		rows, err := f.s.QuerySpend(ctx, repo.SpendFilter{
			From:    time.Date(2026, 12, 30, 0, 0, 0, 0, time.UTC),
			To:      time.Date(2027, 1, 2, 0, 0, 0, 0, time.UTC),
			GroupBy: "year",
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 2 || rows[0].Bucket != "2026" || rows[1].Bucket != "2027" {
			t.Fatalf("year: %+v, want 2026 then 2027", rows)
		}
	})

	t.Run("model,week", func(t *testing.T) {
		f := newFixture(t, "unlimited")
		m2, err := f.s.CreateModel(ctx, repo.ModelInput{Name: "m2", Provider: "openai", UpstreamModel: "m2", Enabled: true, Extra: json.RawMessage(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		other := f.rec(10, time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC))
		other.ModelID, other.ModelName = m2.ID, "m2"
		f.s.RecordSpend(ctx, []repo.SpendRecord{
			f.rec(10, time.Date(2026, 5, 15, 9, 0, 0, 0, time.UTC)),
			other,
		})
		rows, err := f.s.QuerySpend(ctx, repo.SpendFilter{
			From:    time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC),
			To:      time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC),
			GroupBy: "model,week",
		})
		if err != nil {
			t.Fatal(err)
		}
		want := []repo.SpendRow{
			{Bucket: "m1", Day: "2026-W20", Requests: 1},
			{Bucket: "m2", Day: "2026-W20", Requests: 1},
		}
		if len(rows) != 2 {
			t.Fatalf("model,week: %+v, want two rows", rows)
		}
		for i, w := range want {
			if rows[i].Bucket != w.Bucket || rows[i].Day != w.Day || rows[i].Requests != w.Requests {
				t.Fatalf("model,week[%d] = %+v, want %+v", i, rows[i], w)
			}
		}
	})

	t.Run("group", func(t *testing.T) {
		f := newFixture(t, "unlimited")
		g2, err := f.s.CreateGroup(ctx, repo.GroupInput{Name: "g2", BudgetType: "unlimited"})
		if err != nil {
			t.Fatal(err)
		}
		other := f.rec(10, time.Date(2026, 8, 1, 1, 0, 0, 0, time.UTC))
		other.GroupID = g2.ID
		f.s.RecordSpend(ctx, []repo.SpendRecord{
			f.rec(10, time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)),
			other,
		})
		rows, err := f.s.QuerySpend(ctx, repo.SpendFilter{
			From:    time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC),
			To:      time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC),
			GroupBy: "group",
		})
		if err != nil {
			t.Fatal(err)
		}
		// Two buckets, one per group id (ORDER BY b sorts by the uuid, so assert
		// membership rather than a fixed order).
		if len(rows) != 2 {
			t.Fatalf("group: %+v, want two group rows", rows)
		}
		seen := map[string]int{}
		for _, r := range rows {
			seen[r.Bucket]++
			if r.Bucket != f.g.ID && r.Bucket != g2.ID {
				t.Fatalf("group: unexpected bucket %s (want %s or %s)", r.Bucket, f.g.ID, g2.ID)
			}
		}
		if seen[f.g.ID] != 1 || seen[g2.ID] != 1 {
			t.Fatalf("group: buckets %v, want one row each for %s and %s", seen, f.g.ID, g2.ID)
		}
	})
}
