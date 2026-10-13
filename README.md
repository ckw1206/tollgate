# Tollgate

Self-hosted LLM gateway: one OpenAI-compatible endpoint in front of OpenAI-compatible, Anthropic, and Google models, with per-key and per-group budgets, rate limits, spend tracking, an audit log, and a web console.

> **Status:** backend and console API are done. The web console UI and Docker image are in progress. Until they land, run from source and drive the console API directly.

## Run it (from source)

Requires Go 1.26 and Docker.

```bash
cp .env.example .env
sed -i '' "s/^ENCRYPTION_KEY=.*/ENCRYPTION_KEY=$(openssl rand -hex 32)/" .env   # Linux: sed -i
docker compose up -d postgres redis
set -a; . ./.env; set +a
go run ./cmd/tollgate admin create --email you@example.com --name You   # prompts for a password (min 12 chars)
go run ./cmd/tollgate
```

Over plain `http`, browsers drop the `Secure` session cookie and login appears to do nothing. Set `COOKIE_INSECURE=true` in `.env` for local testing only. In production, serve over HTTPS and leave it `false`.

Then, as admin:

1. **Credentials**: add a provider API key, or a GCP service-account JSON for Vertex.
2. **Models**: add a model. Give it the name clients will send, the provider's model ID, the credential, and prices in USD per 1M tokens.
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

Console requests that change state must send `Content-Type: application/json` and, if the request has an `Origin` header, it must equal `BASE_URL`. Anything else gets a 403 (CSRF protection).

## Configuration

See [.env.example](.env.example). `DATABASE_URL`, `REDIS_URL`, `ENCRYPTION_KEY` (32 bytes hex), and `BASE_URL` are required.

## Production notes

- Put it behind a TLS-terminating reverse proxy and set `BASE_URL` to the public `https://` URL. Set `TRUSTED_PROXIES` to the proxy's CIDR so client IPs in the audit log and login throttling are real.
- Back up Postgres **and** `ENCRYPTION_KEY`. Losing the key means re-entering provider credentials. Nothing else is affected.
- Single instance only: the model registry and spend buffer live in process.

## Limits (v1)

- Tool/function calling is passed through for OpenAI-compatible models only. Anthropic and Google models return 400 for requests with `tools`.
- No `/v1/messages`, `/v1/responses`, images, or audio endpoints.

## Develop

```bash
docker compose up -d postgres redis
export TOLLGATE_TEST_DATABASE_URL='postgres://tollgate:tollgate@localhost:5432/tollgate?sslmode=disable'
go test -count=1 ./...
```

Without `TOLLGATE_TEST_DATABASE_URL`, the database-backed tests **skip** and the run still reports `ok`.

Design and implementation plan: [docs/superpowers/](docs/superpowers/).

## License

MIT
