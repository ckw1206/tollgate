-- Hourly spend rollup (#26): per-hour totals that outlive SPEND_RETENTION_DAYS,
-- so "what did group X spend in March" still has an answer after the per-request
-- spend_logs are pruned. Written in the same transaction as the spend_logs
-- insert (RecordSpend); never pruned.
--
-- Buckets are UTC hours, not server-timezone days: a timezone change can't split
-- or shift a stored bucket, and day/week/month/year labels in any whole-hour
-- zone are exact sums of whole hours. (Zones with a :30/:45 offset get those
-- labels shifted by the offset for spend older than the logs.)
--
-- No foreign keys: users, keys and models may be deleted once their logs are
-- pruned, and their history should stay. key_id is NULL for playground rows.
CREATE TABLE spend_hourly (
    hour_start         TIMESTAMPTZ NOT NULL,
    key_id             UUID,
    user_id            UUID NOT NULL,
    group_id           UUID NOT NULL,
    model_name         TEXT NOT NULL,
    requests           BIGINT NOT NULL,
    input_tokens       BIGINT NOT NULL,
    output_tokens      BIGINT NOT NULL,
    cache_read_tokens  BIGINT NOT NULL,
    cache_write_tokens BIGINT NOT NULL,
    cost_nanos         BIGINT NOT NULL
);

-- One-time backfill from the logs still retained. Spend logged before the
-- previous retention prune is gone and can't be recovered. The lock holds off
-- writers (e.g. an old binary still flushing) until the backfill commits, so no
-- log is left out of the rollup. Cost: roughly 3-4 s per million spend_logs
-- rows, at startup.
LOCK TABLE spend_logs IN SHARE MODE;
INSERT INTO spend_hourly (hour_start, key_id, user_id, group_id, model_name, requests,
    input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_nanos)
SELECT date_trunc('hour', created_at, 'UTC'), key_id, user_id, group_id, model_name, count(*),
    sum(input_tokens), sum(output_tokens), sum(cache_read_tokens), sum(cache_write_tokens), sum(cost_nanos)
FROM spend_logs
GROUP BY 1, 2, 3, 4, 5;

-- The upsert key, built after the backfill (faster than maintaining it row by
-- row). NULLS NOT DISTINCT (Postgres 15+): playground rows have no key. Its
-- leading hour_start also serves QuerySpend's range scans; the user/group/key
-- filters compare as text, so per-column indexes would go unused.
ALTER TABLE spend_hourly ADD CONSTRAINT spend_hourly_bucket
    UNIQUE NULLS NOT DISTINCT (hour_start, key_id, user_id, group_id, model_name);
