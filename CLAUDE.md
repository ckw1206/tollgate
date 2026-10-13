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
docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d postgres redis
make test                      # Go: sets TOLLGATE_TEST_DATABASE_URL for you
cd web && npm run build        # console: strict type-check + Vite build
```

A bare `go test ./...` without `TOLLGATE_TEST_DATABASE_URL` **skips** the DB-backed tests and still reports `ok`. Never claim the tests pass from a run without it.

## Conventions

- Console mutations require `Content-Type: application/json`, including DELETE, plus an `Origin` equal to `BASE_URL` when the browser sends one (`console/auth/csrf.go`). The SPA must always send the JSON content type.
- Migrations: add a new numbered file `core/db/migrations/NNN_name.sql`. Never edit an applied one. They are tracked in `schema_migrations`, applied at startup, and each runs in a transaction.
- Upstream calls use a context detached from the client, so a client disconnect mid-stream still gets billed. Don't tie upstream requests to `r.Context()`.
- Streaming usage must be exact. For OpenAI, inject `stream_options.include_usage` and strip the usage chunk if the client didn't ask for it. Anthropic usage comes from `message_start`/`message_delta`, Gemini from `usageMetadata`.
- Use stdlib `http.ServeMux` method patterns (`"POST /v1/..."`). No router dependency.
- The server enforces what the console hides: a UI-only restriction (e.g. "rotated keys can't be edited") needs a handler check and a test too.
- `GET /console/api/v1/models` is readable by every role; anything secret-adjacent on a model (`extra`, `api_base`, `credential_id`) is redacted for callers without `models:write`.

## Web console (`web/`)

- React 18 + react-router 7 + Tailwind v4 + Recharts, TypeScript strict. Built into `web/dist` and embedded by `web/embed.go`.
- Only `api()` in `web/src/api.ts` calls `fetch`. Gate every nav item, column, button and field with `can(permission)` from `/auth/me`; never branch on `user.role`.
- Use only `web/src/components/ui.tsx` primitives. Colors, radii and fonts come from Alison UI tokens (`web/src/styles/alison.css`, mapped in `index.css`); no hex, `rgba()`, palette classes (`zinc-*`, `red-*`…) or `rounded-full` in `.ts/.tsx`, and no `alert()`/`confirm()`.
- Tailwind v4 orders conflicting utilities by value, not by class-string position: don't narrow `inputCls` by appending (`+ ' mt-0'`); compose `controlCls` with the sizes you need.
- Raw API keys and invite URLs appear once in `SecretDialog` and are dropped from state when it closes.
