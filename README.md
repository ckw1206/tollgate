# Tollgate

Self-hosted LLM gateway: one OpenAI-compatible endpoint in front of OpenAI-compatible, Anthropic, and Google models, with per-key and per-group budgets, rate limits, spend tracking, an audit log, and a web console.

## Run it

Requires Docker.

```bash
cp .env.example .env
sed -i.bak "s/^ENCRYPTION_KEY=.*/ENCRYPTION_KEY=$(openssl rand -hex 32)/" .env && rm .env.bak   # GNU and BSD/macOS sed
VERSION=dev COMMIT=$(git rev-parse HEAD) docker compose up -d --build
until curl -sf localhost:4000/health >/dev/null; do sleep 1; done
docker compose logs tollgate | grep 'First-run setup'   # one-time link to create the first admin
```

Open that link (it pre-fills a setup token that changes on every restart until an admin exists) and create the first admin. Headless? `docker compose exec tollgate /tollgate admin create --email you@example.com --name You` does the same and prompts for a password (min 12 chars).

Over plain `http` on anything other than `localhost` (and in Safari even on localhost), browsers drop the `Secure` session cookie and login appears to do nothing. Set `COOKIE_INSECURE=true` in `.env` for local testing only. In production, serve over HTTPS and leave it `false`.

`make image` builds a tagged image (`tollgate:<tag or dev>`) with the version and commit shown in the console footer. Without Docker: `make build` (needs Go 1.26 and Node 24), then `set -a; . ./.env; set +a; ./tollgate`.

Then, as admin:

1. **Models → Credentials**: pick the provider, check the API endpoint (prefilled with the provider default; point it at any OpenAI-compatible server), and add its API key, or a GCP service-account JSON for Vertex. **Test connection** lists the upstream's models without saving.
2. **Models**: add a model. Give it the name clients will send, the provider's model ID, the credential (it sets the provider and endpoint; for a keyless OpenAI-compatible server pick None and enter its API base), and prices in USD per 1M tokens.
3. **API keys**: create a key and copy it.

```bash
curl $BASE_URL/v1/chat/completions -H "Authorization: Bearer tg-..." -H 'Content-Type: application/json' \
  -d '{"model":"<model name>","messages":[{"role":"user","content":"hello"}]}'
```

Any OpenAI SDK works with `base_url=$BASE_URL/v1`.

## API

| Endpoint | Auth |
|---|---|
| `POST /v1/chat/completions` (streaming supported) | API key |
| `POST /v1/embeddings` | API key |
| `GET /v1/models` | API key |
| `GET /health` | none |
| `/auth/*`, `/console/api/v1/*` | session cookie |
| everything else | the web console (single-page app) |

Console requests that change state must send `Content-Type: application/json` and, if the request has an `Origin` header, it must equal `BASE_URL`. Anything else gets a 403 (CSRF protection).

## Configuration

See [.env.example](.env.example). `DATABASE_URL`, `REDIS_URL`, `ENCRYPTION_KEY` (32 bytes hex), and `BASE_URL` are required.

## Production notes

- Put it behind a TLS-terminating reverse proxy and set `BASE_URL` to the public `https://` URL. Set `TRUSTED_PROXIES` to the proxy's CIDR so client IPs in the audit log and login throttling are real.
- Back up Postgres **and** `ENCRYPTION_KEY`. Losing the key means re-entering provider credentials. Nothing else is affected.
- Single instance only: the model registry and spend buffer live in process.
- Backups, encryption-key rotation, API key rotation, upgrades, and log alerts: [docs/operations.md](docs/operations.md).

## Limits (v1)

- Tool/function calling is passed through for OpenAI-compatible models only. Anthropic and Google models return 400 for requests with `tools`.
- No `/v1/messages`, `/v1/responses`, images, or audio endpoints.

## Develop

```bash
docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d postgres redis
make test   # sets TOLLGATE_TEST_DATABASE_URL, so DB-backed tests fail loudly if Postgres is down
```

Plain `go test ./...` without `TOLLGATE_TEST_DATABASE_URL` **skips** the database-backed tests and still reports `ok`.

Console development with hot reload:

```bash
set -a; . ./.env; set +a
BASE_URL=http://localhost:5173 COOKIE_INSECURE=true go run ./cmd/tollgate &   # BASE_URL must match the page origin (CSRF check)
cd web && npm ci && npm run dev    # console on :5173, API proxied to :4000
```

The console follows the UI spec's rules: every request goes through `api()` in `web/src/api.ts`, controls are gated with `can(permission)`, colors and radii come from the Alison UI tokens in `web/src/styles/`, and there is no `alert()`/`confirm()`.

## License

MIT
