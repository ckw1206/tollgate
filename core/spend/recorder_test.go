package spend_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"tollgate/core/db/dbtest"
	"tollgate/core/db/repo"
	"tollgate/core/spend"
)

func TestRecorder_FlushesOnBatchAndOnShutdown(t *testing.T) {
	d := dbtest.New(t)
	s := repo.New(d)
	ctx := context.Background()
	g, _ := s.GetGroupByName(ctx, "default")
	u, _ := s.CreateUser(ctx, "a@x.io", "A", "hunter22hunter22", "admin", g.ID)
	_, k, _ := s.CreateKey(ctx, u.ID, repo.KeyInput{Alias: "k", GroupID: g.ID, BudgetType: "total", BudgetNanos: 1e12})
	m, _ := s.CreateModel(ctx, repo.ModelInput{Name: "m", Provider: "openai", UpstreamModel: "m", Enabled: true, Extra: json.RawMessage(`{}`)})
	rec := func() repo.SpendRecord {
		return repo.SpendRecord{KeyID: k.ID, UserID: u.ID, GroupID: g.ID, ModelID: m.ID, ModelName: "m", Provider: "openai",
			CostNanos: 10, Status: "success", HTTPStatus: 200, CreatedAt: time.Now()}
	}

	r := spend.NewRecorder(s)
	r.FlushEvery = time.Hour // only batch size or shutdown can flush
	r.BatchSize = 2
	runCtx, cancel := context.WithCancel(ctx)
	go r.Run(runCtx)

	r.Record(rec())
	r.Record(rec()) // hits batch size
	waitFor(t, func() bool { k2, _ := s.GetKey(ctx, k.ID); return k2.SpendNanos == 20 })

	r.Record(rec()) // below batch size; must be flushed by shutdown
	cancel()
	<-r.Done()
	k2, _ := s.GetKey(ctx, k.ID)
	if k2.SpendNanos != 30 {
		t.Fatalf("spend=%d after shutdown drain", k2.SpendNanos)
	}
}

func TestRecorder_BadRecordDoesNotBlockBatch(t *testing.T) {
	s := repo.New(dbtest.New(t))
	ctx := context.Background()
	g, _ := s.GetGroupByName(ctx, "default")
	u, _ := s.CreateUser(ctx, "a@x.io", "A", "hunter22hunter22", "admin", g.ID)
	_, k, _ := s.CreateKey(ctx, u.ID, repo.KeyInput{Alias: "k", GroupID: g.ID, BudgetType: "unlimited"})
	m, _ := s.CreateModel(ctx, repo.ModelInput{Name: "m", Provider: "openai", UpstreamModel: "m", Enabled: true, Extra: json.RawMessage(`{}`)})
	rec := func(modelID string) repo.SpendRecord {
		return repo.SpendRecord{KeyID: k.ID, UserID: u.ID, GroupID: g.ID, ModelID: modelID, ModelName: "m", Provider: "openai",
			CostNanos: 10, Status: "success", HTTPStatus: 200, CreatedAt: time.Now()}
	}

	r := spend.NewRecorder(s)
	r.FlushEvery = time.Hour
	runCtx, cancel := context.WithCancel(ctx)
	go r.Run(runCtx)
	r.Record(rec(m.ID))
	r.Record(rec("00000000-0000-0000-0000-000000000000")) // model no longer exists
	r.Record(rec(m.ID))
	cancel()
	<-r.Done()
	k2, _ := s.GetKey(ctx, k.ID)
	if k2.SpendNanos != 20 {
		t.Fatalf("spend=%d, want the two valid records (20) despite the bad one", k2.SpendNanos)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met")
}
