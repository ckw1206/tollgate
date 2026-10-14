package spend_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"tollgate/core/db/dbtest"
	"tollgate/core/db/repo"
	"tollgate/core/spend"
	"tollgate/core/webhook"
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

func TestRecorder_FinalFlushCrossingIsDeliveredOnShutdown(t *testing.T) {
	// Shutdown simulation: the last flush runs after the signal and fires a
	// crossing into OnCrossing (the webhook Submit). The shutdown path is
	// "cancel, then wait for Done" — exactly like main.go does for the
	// recorder, after which the sender is closed and drained. The crossing
	// must reach the fake endpoint; before the sender got Close/Drain it was
	// enqueued into a channel nobody read and silently lost.
	d := dbtest.New(t)
	s := repo.New(d)
	ctx := context.Background()
	g, _ := s.GetGroupByName(ctx, "default")
	u, _ := s.CreateUser(ctx, "a@x.io", "A", "hunter22hunter22", "admin", g.ID)
	_, k, _ := s.CreateKey(ctx, u.ID, repo.KeyInput{Alias: "k", GroupID: g.ID, BudgetType: "daily", BudgetNanos: 10})
	m, _ := s.CreateModel(ctx, repo.ModelInput{Name: "m", Provider: "openai", UpstreamModel: "m", Enabled: true, Extra: json.RawMessage(`{}`)})

	// Point the webhook at the capturing endpoint.
	st, _ := s.GetSettings(ctx)
	var mu sync.Mutex
	var bodies []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, 0, 1<<16)
		buf := make([]byte, 1<<16)
		for {
			n, err := r.Body.Read(buf)
			body = append(body, buf[:n]...)
			if err != nil {
				break
			}
		}
		mu.Lock()
		bodies = append(bodies, string(body))
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer up.Close()
	st.BudgetWebhookURL = up.URL
	if _, err := s.UpdateSettings(ctx, *st); err != nil {
		t.Fatal(err)
	}

	ws := webhook.NewSender(s, up.Client()) // up.Client() carries the test server's TLS setup
	wsCtx, wsCancel := context.WithCancel(context.Background())
	defer wsCancel()
	go ws.Run(wsCtx)

	r := spend.NewRecorder(s)
	r.FlushEvery = time.Hour // only batch size or shutdown can flush
	r.OnCrossing = func(c repo.BudgetCrossing) { ws.Submit(ctx, c) }
	runCtx, cancel := context.WithCancel(ctx)
	go r.Run(runCtx)

	// One in-window record crossing the 80% threshold (8 of 10): it sits
	// below batch size, so only the shutdown flush commits it and fires the
	// one-shot crossing marker.
	r.Record(repo.SpendRecord{KeyID: k.ID, UserID: u.ID, GroupID: g.ID, ModelID: m.ID, ModelName: "m",
		Provider: "openai", CostNanos: 8, Status: "success", HTTPStatus: 200, CreatedAt: time.Now()})

	// The shutdown path: signal, drain the recorder, close and drain the sender.
	cancel()
	<-r.Done()
	ws.Close()
	<-ws.Done()

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 {
		t.Fatalf("delivered %d webhooks, want exactly 1 (the final-flush crossing); got %v", len(bodies), bodies)
	}
	var c map[string]any
	if err := json.Unmarshal([]byte(bodies[0]), &c); err != nil {
		t.Fatalf("payload not JSON: %v: %s", err, bodies[0])
	}
	if c["scope"] != "key" || c["label"] != "k" || c["threshold"] != 80.0 {
		t.Errorf("wrong crossing delivered: %v", c)
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
