# Tollgate — agent notes

Self-hosted LLM gateway. Go 1.26, single module `tollgate`, one binary, one port. Postgres (pgx v5) + Redis (go-redis v9). Web console: React + Vite, embedded via `web/embed.go`.

## Hard rules

- Never name the gateway product Tollgate replaces anywhere in the repo: code, comments, docs, commit messages.
- Money is integers only. Model prices are micro-USD per 1M tokens; spend is nano-USD. `cost = tokens * priceMicros / 1000`. No floats in any money path.
- Spend counters (`spend_nanos` on `api_keys` and `groups`) are updated in the same transaction as the `spend_logs` insert. Keep it that way.
- Never log or return secrets: provider credentials, raw API keys (`tg-...`), session tokens, invite tokens. Only hashes are stored.
- Run as a single instance: the model registry and spend buffer live in process. Don't add features that assume more than one replica.
- RBAC is in code (`console/rbac/rbac.go`). Every new console route needs a permission check and a test for the role that must be denied.

## Testing

```bash
docker compose up -d postgres redis
export TOLLGATE_TEST_DATABASE_URL='postgres://tollgate:tollgate@localhost:5432/tollgate?sslmode=disable'
go test -count=1 ./...
```

Without `TOLLGATE_TEST_DATABASE_URL`, the DB-backed tests **skip** and the run still reports `ok`. Never claim the tests pass from a run without it.

## Conventions

- Console mutations require `Content-Type: application/json`, including DELETE, plus an `Origin` equal to `BASE_URL` when the browser sends one (`console/auth/csrf.go`). The SPA must always send the JSON content type.
- Migrations: add a new numbered file `core/db/migrations/NNN_name.sql`. Never edit an applied one. They are tracked in `schema_migrations`, applied at startup, and each runs in a transaction.
- Upstream calls use a context detached from the client, so a client disconnect mid-stream still gets billed. Don't tie upstream requests to `r.Context()`.
- Streaming usage must be exact. For OpenAI, inject `stream_options.include_usage` and strip the usage chunk if the client didn't ask for it. Anthropic usage comes from `message_start`/`message_delta`, Gemini from `usageMetadata`.
- Use stdlib `http.ServeMux` method patterns (`"POST /v1/..."`). No router dependency.
