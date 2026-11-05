-- Server-wide spend cap (#31): a circuit breaker above every group. One row
-- (server_settings, id = 1) carries the same window fields a key or group has,
-- plus the one-shot notify marker (see 011). 'total' is not offered: a cap that
-- never resets would stop the gateway for good once reached.
ALTER TABLE server_settings
    ADD COLUMN budget_type            budget_type NOT NULL DEFAULT 'unlimited'
        CHECK (budget_type IN ('unlimited', 'daily', 'monthly')),
    ADD COLUMN budget_nanos           BIGINT NOT NULL DEFAULT 0 CHECK (budget_nanos >= 0),
    ADD COLUMN spend_nanos            BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN budget_reset_at        TIMESTAMPTZ,
    ADD COLUMN budget_notified_window TIMESTAMPTZ,
    ADD COLUMN budget_notified_pct    SMALLINT NOT NULL DEFAULT 0;
