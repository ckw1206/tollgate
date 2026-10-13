# Tollgate — Design Spec

## Intent

A self-hosted LLM gateway that proxies OpenAI-format requests to multiple LLM providers (OpenAI-compatible, Google Gemini/Vertex, Anthropic), with:

- An OpenAI-compatible API surface (`/v1/chat/completions`, `/v1/embeddings`, `/v1/models`) for clients
- A web console with two views:
  - **Admin view** — models, provider credentials, users, groups, invites, usage dashboard, settings, audit log
  - **User view** — own keys, own spend, model list; no access to settings or other users
- Per-key and per-group rate limits and budgets, with spend attribution
- Redis for rate-limit counters, sessions, and login throttling
- Multi-tenant data model from day one, serving a small personal circle today

## Why

Replace the current gateway setup with something leaner (Go, single binary), with a built-in dashboard, invite-only onboarding, and encrypted provider credentials — and own the whole stack.

---

## Goals

1. **OpenAI-compatible API** at `/v1/*`
2. **Multi-provider routing** — OpenAI-compatible, Google (AI Studio API key or Vertex service account), Anthropic (API key)
3. **RBAC** — 3 built-in roles (`admin`, `member`, `viewer`), permission set defined in code
4. **Groups** — a group is a cost center with its own budget and rate limits; keys bill to a group
5. **Invite-only onboarding** — admins create invite links that pre-assign role + group; no self-signup
6. **Encrypted credential vault** — provider secrets stored as AES-256-GCM ciphertext in Postgres
7. **Budgets and rate limits** — per key and per group: spend cap (`unlimited`/`daily`/`monthly`/`total`), RPM, TPM
8. **Model management** — add/edit/remove models, per-model pricing, enable/disable
9. **Usage dashboard** — spend over time, request volume, cost by model/user/key; admin sees all, users see their own
10. **Server settings** — server name, icon, timezone, currency label
11. **Audit log** — every console mutation: who, what, when, from where, before/after
12. **Structured layout** — `core/` (gateway engine, no console knowledge) + `console/` (admin API + UI, imports core)

## Non-Goals (v1)

- SSO / external identity providers
- Multi-organization hierarchy
- Fine-tuning, image, audio endpoints
- `/v1/messages` (Anthropic-native) and `/v1/responses` passthrough — candidates for v2
- Tool/function-calling translation for non-OpenAI providers (requests with `tools` to Anthropic/Google models return 400; OpenAI-compatible models pass tools through untouched)
- Load balancing / fallback across deployments
- LLM response caching
- Server-side API key auto-rotation (use `expires_at`)
- Horizontal scaling (single instance; in-process caches assume it)

## Anti-Goals

- Provider secrets in plain text anywhere (DB, logs, responses)
- Requests that bypass rate limits or budgets
- A dashboard that needs external Grafana to be useful
- A console exposed without HTTPS

## Constraints

- **Go 1.26**, single Go module `tollgate`, single binary
- **PostgreSQL 16+**, **Redis 7+**
- Router: Go stdlib `net/http` ServeMux (method + `{param}` patterns) — no router library
- React SPA built with Vite, embedded via `embed.FS`
- Secrets via env only: `DATABASE_URL`, `REDIS_URL`, `ENCRYPTION_KEY`
- TLS terminated at the reverse proxy (Caddy/nginx/Cloudflare)
- One HTTP port serves `/v1/*`, `/health`, `/auth/*`, `/console/api/*`, and the SPA

---

## API Design

### Client endpoints

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `POST` | `/v1/chat/completions` | `Authorization: Bearer <key>` | Chat completions (streaming via SSE) |
| `POST` | `/v1/embeddings` | `Authorization: Bearer <key>` | Embeddings (OpenAI-compatible models only) |
| `GET` | `/v1/models` | `Authorization: Bearer <key>` | Enabled models the key may use |
| `GET` | `/health` | none | Pings Postgres + Redis; 200 or 503 |

Errors use OpenAI's shape — `code` is a string or null:

```json
{"error": {"message": "Rate limit exceeded", "type": "rate_limit_error", "code": "rate_limit_exceeded"}}
```

| Condition | HTTP | type | code |
|-----------|------|------|------|
| Missing/unknown key | 401 | `authentication_error` | `invalid_api_key` |
| Blocked/expired key | 403 | `permission_error` | `key_blocked` / `key_expired` |
| Model not allowed for key | 403 | `permission_error` | `model_not_allowed` |
| Unknown/disabled model | 404 | `invalid_request_error` | `model_not_found` |
| RPM/TPM exceeded | 429 | `rate_limit_error` | `rate_limit_exceeded` (+ `Retry-After`) |
| Budget exceeded | 429 | `insufficient_quota` | `budget_exceeded` |
| Upstream error | upstream status (or 502) | `api_error` | `upstream_error` |

Non-streaming responses carry `X-Tollgate-Cost: <usd>`. Streaming responses don't (headers are sent before usage is known).

### Console endpoints

Session cookie auth. All under `/console/api/v1/`, except `/auth/*`. The SPA owns every other path (including the `/invite/{token}` page), with an `index.html` fallback for client-side routes.

| Path | Methods | Permission |
|------|---------|------------|
| `/auth/login` | POST | public (rate limited) |
| `/auth/logout` | POST | session |
| `/auth/me` | GET | session |
| `/auth/invite/{token}` | GET (validate), POST (accept) | public |
| `/console/api/v1/invites` | GET, POST, `DELETE /{id}` | `invites:*` |
| `/console/api/v1/users` | GET, `PUT /{id}`, `DELETE /{id}` | `users:*` |
| `/console/api/v1/groups` | GET, POST, `PUT /{id}` | `groups:*` |
| `/console/api/v1/keys` | GET, POST, `PUT /{id}`, `DELETE /{id}` | `keys:*` |
| `/console/api/v1/credentials` | GET, POST, `DELETE /{id}` | `models:write` |
| `/console/api/v1/models` | GET, POST, `PUT /{id}`, `DELETE /{id}` | `models:*` |
| `/console/api/v1/spend` | GET | `spend:*` |
| `/console/api/v1/audit` | GET | `audit:read` |
| `/console/api/v1/settings` | GET, PUT | `settings:*` |

CSRF: state-changing console requests must have `Content-Type: application/json` and, when an `Origin` header is present, it must match `BASE_URL`. Cookie: `HttpOnly`, `SameSite=Lax`, `Secure` unless `COOKIE_INSECURE=true` (local dev).

The first admin is created with the CLI: `tollgate admin create --email <e> --name <n>` (prompts for the password on stdin).

---

## Roles and Permissions

Defined in `console/rbac/roles.go`. "Own" means `api_keys.user_id = me` for keys, `spend_logs.user_id = me` for spend.

| Permission | admin | member | viewer |
|------------|:-----:|:------:|:------:|
| `models:read` | ✅ | ✅ | ✅ |
| `models:write` | ✅ | | |
| `keys:read_own` | ✅ | ✅ | ✅ |
| `keys:write_own` | ✅ | ✅ | |
| `keys:read_all` / `keys:write_all` | ✅ | | |
| `users:read` / `users:write` | ✅ | | |
| `groups:read_own` | ✅ | ✅ | ✅ |
| `groups:read_all` / `groups:write` | ✅ | | |
| `invites:read` / `invites:write` | ✅ | | |
| `spend:read_own` | ✅ | ✅ | ✅ |
| `spend:read_all` | ✅ | | |
| `audit:read` | ✅ | | |
| `settings:read` / `settings:write` | ✅ | | |

---

## Data Model

Money is stored as integers to avoid float/decimal handling:

- **Prices**: `BIGINT` micro-USD per 1M tokens (`$2.50/M` → `2500000`)
- **Spend and budgets**: `BIGINT` nano-USD (`$1` → `1_000_000_000`)
- **Cost of a request**: `tokens × price_micros / 1000` nano-USD (rounding loss < 1 nano-USD per request)

`currency` in settings is a display label only; no conversion happens.

### groups

| Field | Type | Notes |
|-------|------|-------|
| `id` | UUID PK | `gen_random_uuid()` |
| `name` | TEXT UNIQUE | |
| `budget_type` | ENUM | `unlimited`, `daily`, `monthly`, `total` |
| `budget_nanos` | BIGINT | 0 when unlimited |
| `spend_nanos` | BIGINT | current window's spend |
| `budget_reset_at` | TIMESTAMPTZ | next reset; null for unlimited/total |
| `rpm_limit` / `tpm_limit` | INT NULL | null = no limit |
| `created_at` / `updated_at` | TIMESTAMPTZ | |

Every user, admins included, belongs to a group. Migration seeds a `default` group.

### users

| Field | Type | Notes |
|-------|------|-------|
| `id` | UUID PK | |
| `email` | TEXT UNIQUE | stored lowercased |
| `name` | TEXT | |
| `password_hash` | TEXT | bcrypt, cost 12 |
| `role` | ENUM | `admin`, `member`, `viewer` |
| `group_id` | UUID FK → groups | NOT NULL |
| `disabled` | BOOLEAN | soft delete; disabled users can't log in and their keys stop working |
| `created_at` / `updated_at` | TIMESTAMPTZ | |

### invites

| Field | Type | Notes |
|-------|------|-------|
| `id` | UUID PK | |
| `token_hash` | TEXT UNIQUE | sha256 of the 32-byte random token in the link |
| `role` | ENUM | role the invitee gets |
| `group_id` | UUID FK → groups | group the invitee joins |
| `email` | TEXT NULL | if set, invitee must register with this email |
| `created_by` | UUID FK → users | |
| `expires_at` | TIMESTAMPTZ | default now + 7 days |
| `used_by` / `used_at` | NULL until accepted | |
| `revoked_at` | TIMESTAMPTZ NULL | |

Link: `{BASE_URL}/invite/{token}` (an SPA page that calls `/auth/invite/{token}`). Accepting is a single `UPDATE … WHERE used_at IS NULL AND revoked_at IS NULL AND expires_at > now()` in the same transaction as the user insert, so a token can't be used twice.

### provider_credentials

| Field | Type | Notes |
|-------|------|-------|
| `id` | UUID PK | |
| `name` | TEXT UNIQUE | e.g. `openai-prod` |
| `kind` | TEXT | `api_key` or `gcp_service_account` |
| `ciphertext` | BYTEA | AES-256-GCM |
| `nonce` | BYTEA | 12 random bytes per encryption |
| `created_at` | TIMESTAMPTZ | |

AAD = credential `name` (unique, and never renamed), so ciphertext copied to another row fails to decrypt. Plaintext is never returned by the API; the list shows name/kind/created_at only. To rotate, create a new credential and repoint models.

### models

| Field | Type | Notes |
|-------|------|-------|
| `id` | UUID PK | |
| `name` | TEXT UNIQUE | what clients send as `model` and see in `/v1/models` |
| `provider` | ENUM | `openai`, `google`, `anthropic` |
| `upstream_model` | TEXT | provider's model ID |
| `api_base` | TEXT NULL | override (self-hosted OpenAI-compatible, Azure, Vertex region) |
| `credential_id` | UUID FK → provider_credentials NULL | null allowed for keyless self-hosted endpoints |
| `enabled` | BOOLEAN | |
| `price_input_micros` / `price_output_micros` | BIGINT | per 1M tokens |
| `price_cache_read_micros` / `price_cache_write_micros` | BIGINT NULL | null → billed at input price |
| `extra` | JSONB | `{"vertex_project": "...", "vertex_location": "...", "headers": {...}}` |
| `created_at` / `updated_at` | TIMESTAMPTZ | |

Deleting a model sets `enabled=false` and renames nothing; hard delete is refused while spend logs reference it (`ON DELETE RESTRICT`), and the console offers disable instead.

### api_keys

| Field | Type | Notes |
|-------|------|-------|
| `id` | UUID PK | |
| `key_hash` | TEXT UNIQUE | hex sha256 of the raw key |
| `key_prefix` | TEXT | first 12 chars (`tg-` + 9) for display |
| `alias` | TEXT | |
| `user_id` | UUID FK → users | owner |
| `group_id` | UUID FK → groups | cost center (defaults to owner's group) |
| `models` | TEXT[] | allowed model names; empty = all |
| `budget_type` / `budget_nanos` / `spend_nanos` / `budget_reset_at` | | same semantics as groups |
| `rpm_limit` / `tpm_limit` | INT NULL | |
| `expires_at` | TIMESTAMPTZ NULL | |
| `revoked_at` | TIMESTAMPTZ NULL | revoke = set this; rows are never deleted |
| `last_used_at` | TIMESTAMPTZ NULL | updated by the spend recorder |
| `created_at` | TIMESTAMPTZ | |

Raw key format: `tg-` + 43 chars base64url of 32 random bytes. Returned once at creation.

### spend_logs

Plain table (no partitioning), index on `(created_at)`, `(key_id, created_at)`, `(user_id, created_at)`, `(group_id, created_at)`.

| Field | Type |
|-------|------|
| `id` | BIGSERIAL PK |
| `key_id` / `user_id` / `group_id` / `model_id` | UUID FKs (`ON DELETE RESTRICT`) |
| `model_name` | TEXT (denormalized) |
| `provider` | TEXT |
| `input_tokens` / `output_tokens` / `cache_read_tokens` / `cache_write_tokens` | INT |
| `cost_nanos` | BIGINT |
| `latency_ms` | INT |
| `status` | TEXT (`success`, `error`) |
| `http_status` | INT |
| `error_message` | TEXT NULL |
| `created_at` | TIMESTAMPTZ |

Retention: a daily job deletes rows older than `SPEND_RETENTION_DAYS` (default 90). Budgets use the `spend_nanos` counters, so `total` budgets survive retention.

Rejected requests (401/403/404/429) are not written to spend_logs.

### audit_logs

| Field | Type | Notes |
|-------|------|-------|
| `id` | BIGSERIAL PK | |
| `actor_id` | UUID NULL FK → users `ON DELETE SET NULL` | null for CLI actions |
| `actor_email` | TEXT | denormalized, survives user removal |
| `action` | TEXT | `key.create`, `user.update`, `model.delete`, … |
| `resource` | TEXT | `api_keys`, `models`, … |
| `resource_id` | TEXT | |
| `ip` | TEXT | resolved via trusted proxies |
| `details` | JSONB | `{"before": …, "after": …}`; secrets and hashes stripped |
| `created_at` | TIMESTAMPTZ | |

### server_settings

Single row, `id = 1`: `server_name` (default `Tollgate`), `server_icon`, `timezone` (IANA, default `UTC`), `currency` (label, default `USD`), `updated_at`. Base URL is env-only (`BASE_URL`).

---

## Request Flow (`/v1/chat/completions`)

1. **Auth** — hash bearer token, look up key joined with user + group (single indexed query, no cache). Reject if unknown, revoked, expired, or owner disabled.
2. **Model** — resolve `model` against the in-memory registry; 404 if unknown/disabled; 403 if not in the key's `models` list.
3. **Rate limit** — fixed one-minute windows in Redis: `rl:rpm:{scope}:{id}:{unix_minute}` incremented per request; `rl:tpm:{scope}:{id}:{unix_minute}` read and compared. Scope is `key` and `group`. Each counter key gets `EXPIRE 120` on first increment; because the minute is part of the key name, windows can't stick.
4. **Budget** — reject if `spend_nanos >= budget_nanos` for key or group (non-unlimited). Spend counters lag by up to the recorder's 2s flush plus in-flight requests, so a burst can overshoot a budget by roughly that much traffic; accepted at this scale.
5. **Proxy** — call provider with decrypted credential. For OpenAI-compatible streams, inject `stream_options.include_usage = true` and strip the final usage-only chunk if the client didn't request it. If the client disconnects mid-stream, the gateway stops writing but keeps reading the upstream stream to the end (bounded by a 10-minute request timeout) so it gets exact usage and bills it — OpenAI only reports usage in the final chunk.
6. **Record** — usage normalized to `{input, output, cache_read, cache_write}` (input excludes cached tokens); cost computed; TPM counters incremented by `input+output`; a spend record is enqueued to the in-process recorder.

The recorder batches records (flush every 2s or 200 records) and, in one transaction, inserts spend_logs and increments `spend_nanos` + `last_used_at` on keys and `spend_nanos` on groups. On shutdown it drains. A crash loses at most ~2s of records — accepted. If a batch hits an integrity error (e.g. a model deleted while its records were buffered), the recorder writes records one by one and drops only the bad ones, so one record can't block all billing.

### Usage normalization

| Provider | input | cache_read | cache_write | output |
|----------|-------|-----------|-------------|--------|
| OpenAI | `prompt_tokens − prompt_tokens_details.cached_tokens` | `cached_tokens` | 0 | `completion_tokens` |
| Anthropic | `input_tokens` | `cache_read_input_tokens` | `cache_creation_input_tokens` | `output_tokens` |
| Google | `promptTokenCount − cachedContentTokenCount` | `cachedContentTokenCount` | 0 | `candidatesTokenCount + thoughtsTokenCount` |

Responses to clients report OpenAI-shaped usage: `prompt_tokens = input + cache_read + cache_write`.

### Provider endpoints

- **OpenAI-compatible**: `{api_base or https://api.openai.com/v1}/chat/completions`, `/embeddings`; `Authorization: Bearer`; body forwarded as-is except `model` → `upstream_model`. Extra headers from `extra.headers`.
- **Anthropic**: `{api_base or https://api.anthropic.com}/v1/messages`; headers `x-api-key`, `anthropic-version: 2023-06-01`. System messages → `system`; `max_tokens` defaults to 4096 when absent.
- **Google AI Studio** (credential kind `api_key`): `https://generativelanguage.googleapis.com/v1beta/models/{model}:generateContent` / `:streamGenerateContent?alt=sse`; header `x-goog-api-key`.
- **Google Vertex** (credential kind `gcp_service_account`): `https://{loc}-aiplatform.googleapis.com/v1/projects/{project}/locations/{loc}/publishers/google/models/{model}:generateContent` (host `aiplatform.googleapis.com` when `loc = global`); OAuth token from `golang.org/x/oauth2/google`.

Message translation (Anthropic/Google) covers `system`/`user`/`assistant` roles, string content, and content-part arrays with `text` and `image_url` (data URLs only for Google/Anthropic). `temperature`, `top_p`, `max_tokens`/`max_completion_tokens`, `stop` are mapped. Anything else is dropped.

---

## Security Design

| Threat | Mitigation |
|--------|------------|
| Credential stuffing | Redis counters `loginfail:ip:{ip}` and `loginfail:email:{email}`: 10 failures in 15 min → 15-min lockout (429). bcrypt cost 12. Same error for unknown user and bad password. |
| Key theft | Keys stored as sha256; never logged; shown once. Revocation is immediate (no key cache). |
| Spend exhaustion | Per-key + per-group budgets and RPM/TPM checked before every upstream call |
| Provider secret exposure | AES-256-GCM with per-row nonce and credential name as AAD; key from `ENCRYPTION_KEY` env (32 bytes hex); plaintext only in process memory |
| Privilege escalation | Every console route declares a permission; middleware enforces; handlers additionally scope "own" queries by session user |
| CSRF | JSON content-type requirement + Origin check + SameSite=Lax |
| X-Forwarded-For spoofing | `TRUSTED_PROXIES` CIDR list; XFF only honored when the peer is in it |
| Invite token guessing | 256-bit random token, stored hashed, single-use, expiring |

Losing `ENCRYPTION_KEY` means re-entering all provider credentials; nothing else is affected. Admin access never depends on it.

---

## Redis Usage

| Key | Purpose | TTL |
|-----|---------|-----|
| `rl:rpm:{key\|group}:{id}:{minute}` | RPM counter | 120s |
| `rl:tpm:{key\|group}:{id}:{minute}` | TPM counter | 120s |
| `sess:{sha256(session_token)}` | Session JSON `{user_id}` | 24h |
| `loginfail:ip:{ip}` / `loginfail:email:{email}` | Failed logins | 15min |

Redis loss: everyone is logged out and current-minute rate counters reset. No money data lives in Redis.

---

## Background Jobs (in-process tickers)

| Job | Interval | Work |
|-----|----------|------|
| `budget_reset` | 1 min | One `UPDATE` per table: zero `spend_nanos` and advance `budget_reset_at` (next local midnight / 1st of month in server timezone) where it's due |
| `spend_retention` | 24h | Delete spend_logs older than retention |

The model registry reloads after every console write to models/credentials (same process) and on startup. No polling.

---

## Project Structure

```
tollgate/
├── cmd/tollgate/main.go          # `serve` (default) and `admin create`
├── core/                         # gateway engine; never imports console/
│   ├── config/                   # env → Config
│   ├── db/                       # pgxpool, embedded migrations, repositories
│   │   ├── migrations/001_initial.sql
│   │   └── repo/                 # users, groups, invites, keys, credentials, models, spend, audit, settings
│   ├── vault/                    # AES-256-GCM seal/open
│   ├── cache/                    # Redis client + rate limiter
│   ├── llm/                      # types, registry, cost, providers/{openai,anthropic,google}
│   ├── spend/                    # batching recorder
│   ├── worker/                   # budget reset, retention
│   └── api/                      # /v1/* and /health handlers + middleware
├── console/                      # imports core
│   ├── rbac/                     # permissions
│   ├── auth/                     # sessions, login, CSRF, client IP
│   └── api/                      # /auth, /invite, /console/api/v1 handlers + audit helper
├── web/                          # React SPA source; web/embed.go embeds web/dist
├── .env.example
├── docker-compose.yml
└── Dockerfile
```

---

## Decisions Log

Changes from the original draft, and why:

| Decision | Reason |
|----------|--------|
| `ENCRYPTION_KEY` only; no `MASTER_KEY` | One secret per job. Admin access via users + CLI bootstrap; rotating an admin credential can't brick the vault |
| Vault ciphertext in `provider_credentials` table | Original stored it in an in-memory map — lost on restart |
| No spend_logs partitioning | First partition was hardcoded to 2026-01, which breaks inserts today; plain table + index is plenty at this scale |
| Tracked migrations (`schema_migrations`) | Re-running `001` fails on `CREATE TYPE` |
| `spend_nanos` counters on keys + groups, updated in the spend transaction | Original had no spend column; Redis-only counters lose money data on flush |
| Integer money (micro-USD prices, nano-USD spend) | No decimal library; original cost formula was missing the `/1e6` and `DECIMAL(10,8)` overflowed at $100/M |
| Every user (incl. admins) has a group | Original NULL admin group broke key creation |
| Key ownership by `user_id`, billing by `group_id` | Group-based ownership let members revoke each other's keys |
| Minute in rate-limit key names | Prevents the EXPIRE-on-every-INCR sticky-window bug |
| No Redis key cache | Revocation must be immediate; indexed hash lookup is sub-millisecond |
| In-process spend recorder instead of Redis spend queue | Same crash-loss window, less code, no SCAN |
| Exact streaming usage (`include_usage`, Anthropic/Gemini usage events) | Chunk-count approximation produces wrong bills |
| Unique `models.name` alias | `(provider, model_id)` was ambiguous for clients |
| Invites carry role + group (+ optional email), token stored hashed | Invitee's group was undefined; raw UUID in DB was a bearer secret |
| CLI first-admin bootstrap | Invite-only had no way to create the first user |
| One port | Draft spec and plan disagreed (one vs two ports) |
| Permissions in code | Draft said DB but plan hardcoded; code is simpler |
| `BASE_URL` env only | Was in both env and settings table |
| Single Go module | "Two modules" would need `go.work` for no benefit |
| `web/embed.go` lives in `web/` | `//go:embed ../web/dist` is invalid |
| One layout with permission-filtered nav (not separate Admin/User layouts) | Same two views, half the code; the server enforces permissions either way |
| Public invite API at `/auth/invite/{token}` | The SPA page owns `/invite/{token}` |
| Cut: auto-rotation, response cache, fallback/load balancing, model poll, `/v1/messages`, `/v1/responses` | Not needed for a small circle; rotation broke clients; prompt-hash cache leaked across tenants |
