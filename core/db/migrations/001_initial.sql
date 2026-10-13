CREATE TYPE user_role   AS ENUM ('admin', 'member', 'viewer');
CREATE TYPE budget_type AS ENUM ('unlimited', 'daily', 'monthly', 'total');
CREATE TYPE provider    AS ENUM ('openai', 'google', 'anthropic');

CREATE TABLE server_settings (
    id          INT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    server_name TEXT NOT NULL DEFAULT 'Tollgate',
    server_icon TEXT NOT NULL DEFAULT '',
    timezone    TEXT NOT NULL DEFAULT 'UTC',
    currency    TEXT NOT NULL DEFAULT 'USD',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO server_settings (id) VALUES (1);

-- Next reset instant for a budget window, in the server's timezone. NULL for unlimited/total.
CREATE FUNCTION next_budget_reset(bt budget_type) RETURNS TIMESTAMPTZ
LANGUAGE sql STABLE AS $$
    SELECT CASE bt
        WHEN 'daily'   THEN (date_trunc('day',   now() AT TIME ZONE s.timezone) + interval '1 day')   AT TIME ZONE s.timezone
        WHEN 'monthly' THEN (date_trunc('month', now() AT TIME ZONE s.timezone) + interval '1 month') AT TIME ZONE s.timezone
    END
    FROM server_settings s WHERE s.id = 1
$$;

CREATE TABLE groups (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL UNIQUE,
    budget_type     budget_type NOT NULL DEFAULT 'unlimited',
    budget_nanos    BIGINT NOT NULL DEFAULT 0 CHECK (budget_nanos >= 0),
    spend_nanos     BIGINT NOT NULL DEFAULT 0,
    budget_reset_at TIMESTAMPTZ,
    rpm_limit       INT CHECK (rpm_limit > 0),
    tpm_limit       INT CHECK (tpm_limit > 0),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO groups (name) VALUES ('default');

CREATE TABLE users (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email         TEXT NOT NULL UNIQUE CHECK (email = lower(email)),
    name          TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    role          user_role NOT NULL DEFAULT 'member',
    group_id      UUID NOT NULL REFERENCES groups(id),
    disabled      BOOLEAN NOT NULL DEFAULT false,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE invites (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash TEXT NOT NULL UNIQUE,
    role       user_role NOT NULL DEFAULT 'member',
    group_id   UUID NOT NULL REFERENCES groups(id),
    email      TEXT CHECK (email = lower(email)),
    created_by UUID NOT NULL REFERENCES users(id),
    expires_at TIMESTAMPTZ NOT NULL DEFAULT now() + interval '7 days',
    used_by    UUID REFERENCES users(id),
    used_at    TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE provider_credentials (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT NOT NULL UNIQUE,
    kind       TEXT NOT NULL CHECK (kind IN ('api_key', 'gcp_service_account')),
    ciphertext BYTEA NOT NULL,
    nonce      BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE models (
    id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                     TEXT NOT NULL UNIQUE,
    provider                 provider NOT NULL,
    upstream_model           TEXT NOT NULL,
    api_base                 TEXT NOT NULL DEFAULT '',
    credential_id            UUID REFERENCES provider_credentials(id) ON DELETE RESTRICT,
    enabled                  BOOLEAN NOT NULL DEFAULT true,
    price_input_micros       BIGINT NOT NULL DEFAULT 0 CHECK (price_input_micros >= 0),
    price_output_micros      BIGINT NOT NULL DEFAULT 0 CHECK (price_output_micros >= 0),
    price_cache_read_micros  BIGINT CHECK (price_cache_read_micros >= 0),
    price_cache_write_micros BIGINT CHECK (price_cache_write_micros >= 0),
    extra                    JSONB NOT NULL DEFAULT '{}',
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE api_keys (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    key_hash        TEXT NOT NULL UNIQUE,
    key_prefix      TEXT NOT NULL,
    alias           TEXT NOT NULL,
    user_id         UUID NOT NULL REFERENCES users(id),
    group_id        UUID NOT NULL REFERENCES groups(id),
    models          TEXT[] NOT NULL DEFAULT '{}',
    budget_type     budget_type NOT NULL DEFAULT 'unlimited',
    budget_nanos    BIGINT NOT NULL DEFAULT 0 CHECK (budget_nanos >= 0),
    spend_nanos     BIGINT NOT NULL DEFAULT 0,
    budget_reset_at TIMESTAMPTZ,
    rpm_limit       INT CHECK (rpm_limit > 0),
    tpm_limit       INT CHECK (tpm_limit > 0),
    expires_at      TIMESTAMPTZ,
    revoked_at      TIMESTAMPTZ,
    last_used_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX api_keys_user_idx ON api_keys(user_id);

CREATE TABLE spend_logs (
    id                 BIGSERIAL PRIMARY KEY,
    key_id             UUID NOT NULL REFERENCES api_keys(id) ON DELETE RESTRICT,
    user_id            UUID NOT NULL REFERENCES users(id)    ON DELETE RESTRICT,
    group_id           UUID NOT NULL REFERENCES groups(id)   ON DELETE RESTRICT,
    model_id           UUID NOT NULL REFERENCES models(id)   ON DELETE RESTRICT,
    model_name         TEXT NOT NULL,
    provider           TEXT NOT NULL,
    input_tokens       INT NOT NULL DEFAULT 0,
    output_tokens      INT NOT NULL DEFAULT 0,
    cache_read_tokens  INT NOT NULL DEFAULT 0,
    cache_write_tokens INT NOT NULL DEFAULT 0,
    cost_nanos         BIGINT NOT NULL DEFAULT 0,
    latency_ms         INT NOT NULL DEFAULT 0,
    status             TEXT NOT NULL,
    http_status        INT NOT NULL,
    error_message      TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX spend_logs_created_idx ON spend_logs(created_at);
CREATE INDEX spend_logs_key_idx     ON spend_logs(key_id, created_at);
CREATE INDEX spend_logs_user_idx    ON spend_logs(user_id, created_at);
CREATE INDEX spend_logs_group_idx   ON spend_logs(group_id, created_at);

CREATE TABLE audit_logs (
    id          BIGSERIAL PRIMARY KEY,
    actor_id    UUID REFERENCES users(id) ON DELETE SET NULL,
    actor_email TEXT NOT NULL DEFAULT '',
    action      TEXT NOT NULL,
    resource    TEXT NOT NULL,
    resource_id TEXT NOT NULL DEFAULT '',
    ip          TEXT NOT NULL DEFAULT '',
    details     JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX audit_logs_created_idx ON audit_logs(created_at);
