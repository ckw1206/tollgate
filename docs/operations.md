# Operations

## Backups

Back up **Postgres and `ENCRYPTION_KEY` together**. Provider credentials are stored encrypted with AES-256-GCM under that key. Everything else in the database is plaintext or hashed and doesn't depend on it.

| Lost | Impact |
|---|---|
| `ENCRYPTION_KEY` | Every provider credential is unreadable. Re-enter them as in the rotation steps below. Users, keys, budgets, and spend are unaffected. |
| Postgres | Everything. Restore from backup. |
| Redis | Active sessions (everyone signs in again), current rate-limit windows, and login throttles. Safe to lose. |

## Changing `ENCRYPTION_KEY`

There is no rotation tool. When the server starts with a different key:
- each credential that fails to decrypt logs `credential failed to decrypt (wrong ENCRYPTION_KEY?)`;
- each model using it logs `skipping model: credential unavailable` and is not served;
- the server still starts.

Credentials can't be edited in place, and one can't be deleted while a model uses it. To rotate, start with the new key, then for each credential:
1. Create a new credential with the same secret.
2. Edit each model that used the old credential to use the new one.
3. Delete the old credential.

Creating or deleting a credential and editing a model all reload the model registry, so no restart is needed.

## Upgrades and migrations

Migrations are embedded in the binary and applied at startup, each in its own transaction, recorded in `schema_migrations`. A failed migration stops startup and leaves the database at the last good version. Back up before upgrading. Rolling back to an older binary after a migration has run is not supported.

## Background jobs

Both run in-process, starting when the server starts.

| Job | Interval | What it does |
|---|---|---|
| `budget_reset` | 1 min | Resets key and group spend whose budget period has ended. Periods are computed in the server timezone (console → Settings). |
| `spend_retention` | 24 h | Deletes `spend_logs` rows older than `SPEND_RETENTION_DAYS` (default 90). Budget counters are unaffected. |

Failures log `job failed` with the job name and are retried on the next tick.

## Log messages worth alerting on

| Message | Level | Meaning |
|---|---|---|
| `spend buffer overflow; dropping oldest records` | error | Postgres has been unwritable long enough to fill the 50,000-record buffer. Spend is being lost. Fix the database. |
| `dropping unrecordable spend record` | error | One record violated a DB constraint and was skipped; the rest of its batch was saved. Usually a model or key deleted mid-flight. |
| `upstream reported no token usage; request billed $0` | warn | A provider returned success without usage. Occasional is fine. Steady for one model means that provider's usage parsing is broken. |
| `job failed` | error | A background job errored (see above). |

On shutdown (SIGTERM), the server waits up to 30 s for in-flight requests (including streams), then flushes the spend buffer. Set the stop grace period above 30 s (`stop_grace_period: 45s` in Compose, `terminationGracePeriodSeconds: 45` in K8s). Docker's default of 10 s kills it mid-flush and loses that spend.

## Reverse proxy

Terminate TLS in front of Tollgate. Then:
- set `BASE_URL` to the public `https://` origin, or console logins fail the CSRF Origin check;
- set `TRUSTED_PROXIES` to the proxy's CIDR, or the audit log and login throttling see the proxy's IP for every user;
- don't buffer `text/event-stream` responses, or streaming arrives all at once (nginx: `proxy_buffering off`).
