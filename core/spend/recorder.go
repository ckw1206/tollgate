// Package spend batches spend records into Postgres off the request path.
package spend

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"tollgate/core/db/repo"
)

// ponytail: in-process buffer; a crash loses up to FlushEvery of records.
// Move to a durable queue only if that window ever matters.
type Recorder struct {
	FlushEvery time.Duration
	BatchSize  int

	// OnCrossing is called for each budget crossing fired by a successful
	// RecordSpend. Nil discards them. (Wired to a webhook in a later task.)
	OnCrossing func(repo.BudgetCrossing)

	store *repo.Store
	ch    chan repo.SpendRecord
	done  chan struct{}
}

const maxBuffered = 50_000

func NewRecorder(store *repo.Store) *Recorder {
	return &Recorder{FlushEvery: 2 * time.Second, BatchSize: 200, store: store,
		ch: make(chan repo.SpendRecord, 10_000), done: make(chan struct{})}
}

// Record blocks if the buffer is full rather than dropping money data.
func (r *Recorder) Record(rec repo.SpendRecord) { r.ch <- rec }

func (r *Recorder) Done() <-chan struct{} { return r.done }

func (r *Recorder) Run(ctx context.Context) {
	defer close(r.done)
	t := time.NewTicker(r.FlushEvery)
	defer t.Stop()
	var buf []repo.SpendRecord
	flush := func(ctx context.Context) {
		if len(buf) == 0 {
			return
		}
		crossings, err := r.store.RecordSpend(ctx, buf)
		var pg *pgconn.PgError
		if errors.As(err, &pg) && strings.HasPrefix(pg.Code, "23") {
			// Integrity error (e.g. a model deleted while its records sat in the buffer):
			// retrying the batch would fail forever and block all billing. Write records
			// one by one and drop only the ones that can't be stored.
			for _, rec := range buf {
				if cs, err := r.store.RecordSpend(ctx, []repo.SpendRecord{rec}); err != nil {
					slog.Error("dropping unrecordable spend record", "key", rec.KeyID, "model", rec.ModelName, "cost_nanos", rec.CostNanos, "err", err)
				} else if r.OnCrossing != nil {
					for _, c := range cs {
						r.OnCrossing(c)
					}
				}
			}
			buf = nil
			return
		}
		if err != nil {
			slog.Error("spend flush failed; will retry", "records", len(buf), "err", err)
			if len(buf) > maxBuffered {
				slog.Error("spend buffer overflow; dropping oldest records", "dropped", len(buf)-maxBuffered)
				buf = buf[len(buf)-maxBuffered:]
			}
			return
		}
		if r.OnCrossing != nil {
			for _, c := range crossings {
				r.OnCrossing(c)
			}
		}
		buf = nil
	}
	for {
		select {
		case rec := <-r.ch:
			buf = append(buf, rec)
			if len(buf) >= r.BatchSize {
				flush(ctx)
			}
		case <-t.C:
			flush(ctx)
		case <-ctx.Done():
			for {
				select {
				case rec := <-r.ch:
					buf = append(buf, rec)
					continue
				default:
				}
				break
			}
			fctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			flush(fctx)
			cancel()
			return
		}
	}
}
