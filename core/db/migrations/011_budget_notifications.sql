-- One-shot per-window budget-crossing notify state (#24).
-- budget_notified_window is the window start (stable within a window; the
-- stored budget_reset_at moves when the reset worker runs, so it can't be the
-- window identity). budget_notified_pct is the highest threshold reported for
-- that window. Both are internal bookkeeping: they must NOT appear in
-- keyCols/groupCols or any API/console response.
ALTER TABLE api_keys ADD COLUMN budget_notified_window TIMESTAMPTZ;
ALTER TABLE api_keys ADD COLUMN budget_notified_pct SMALLINT NOT NULL DEFAULT 0;
ALTER TABLE groups  ADD COLUMN budget_notified_window TIMESTAMPTZ;
ALTER TABLE groups  ADD COLUMN budget_notified_pct SMALLINT NOT NULL DEFAULT 0;
