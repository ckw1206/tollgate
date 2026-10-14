-- Optional total context budget in tokens. NULL = unknown/unset; /v1/models omits the
-- field when NULL, so existing clients are unaffected. The CHECK fires only for
-- non-NULL values, so NULL rows are always valid.
ALTER TABLE models ADD COLUMN IF NOT EXISTS context_window BIGINT CHECK (context_window > 0);
