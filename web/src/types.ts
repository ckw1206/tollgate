export type Role = 'admin' | 'member' | 'viewer'
export type BudgetType = 'unlimited' | 'daily' | 'monthly' | 'total'

export interface Limits {
  budget_type: BudgetType
  budget_nanos: number
  spend_nanos: number
  budget_reset_at: string | null
  rpm_limit: number | null
  tpm_limit: number | null
}

export interface User {
  id: string
  email: string
  name: string
  role: Role
  group_id: string
  disabled: boolean
  extra_models: string[] // granted on top of the group's; empty = none
  created_at: string
}

export interface Group extends Limits {
  id: string
  name: string
  models: string[] // empty = every enabled model
  members: number
  created_at: string
}

// PUT /groups/{id} and PUT /users/{id}: live keys whose list now holds a model the owner can't use.
export type Affected<T> = T & { affected_keys: number }

export interface ApiKey extends Limits {
  id: string
  key_prefix: string
  alias: string
  user_id: string
  group_id: string
  models: string[]
  expires_at: string | null
  revoked_at: string | null
  rotated_at: string | null
  last_used_at: string | null
  created_at: string
}

export interface Model {
  id: string
  name: string
  provider: 'openai' | 'anthropic' | 'google'
  upstream_model: string
  api_base: string
  credential_id: string | null
  enabled: boolean
  price_input_micros: number
  price_output_micros: number
  price_cache_read_micros: number | null
  price_cache_write_micros: number | null
  context_window: number | null
  extra: Record<string, unknown> | null
  access?: 'group' | 'granted' // only without models:write ("group" wins when both)
  created_at: string
}

export interface Credential {
  id: string
  name: string
  kind: 'api_key' | 'gcp_service_account'
  provider: Model['provider']
  api_base: string // '' = provider default
  vertex_project: string
  vertex_location: string
  used_by: string[] // model names
  secret_updated_at: string
  created_at: string
}

export interface CredentialTest {
  ok: boolean
  status: number // 0 = unreachable or timed out
  model_count: number
  latency_ms: number
  message: string
}

export interface Invite {
  id: string
  role: Role
  group_id: string
  email: string
  created_by: string
  expires_at: string
  used_at: string | null
  revoked_at: string | null
  created_at: string
  status: 'active' | 'used' | 'revoked' | 'expired'
}

export interface AuditEntry {
  id: number
  actor_id: string | null
  actor_email: string
  action: string
  resource: string
  resource_id: string
  ip: string
  details: unknown
  created_at: string
}

export interface SpendRow {
  bucket: string
  day?: string // YYYY-MM-DD, group_by=model,day only (bucket is then the model)
  requests: number
  input_tokens: number
  output_tokens: number
  cost_nanos: number
}

export interface SpendLog {
  id: number
  key_id: string
  user_id: string
  model_name: string
  input_tokens: number
  output_tokens: number
  cache_read_tokens: number
  cache_write_tokens: number
  cost_nanos: number
  latency_ms: number
  status: string
  http_status: number
  error_message: string
  created_at: string
  key_alias: string
  key_prefix: string
  owner_email: string
}

export interface Settings {
  server_name: string
  server_icon: string
  timezone: string
  currency: string
  landing_headline: string // ≤ 80 characters; '' = default copy
  landing_tagline: string // ≤ 160 characters; '' = default copy
  budget_webhook_url: string // '' = no budget alerts
}

// GET /auth/landing: public, cached for 60s.
export interface LandingInfo {
  server_name: string
  server_icon: string
  landing_headline: string
  landing_tagline: string
  version: string
  commit: string
}

// /auth/me adds the build version to the settings row (read-only).
export interface ServerInfo extends Settings {
  version: string
  commit: string
}

export interface Me {
  user: User
  permissions: string[]
  server: ServerInfo
}

// Playground (session-scoped, no API key). The enabled models the caller may
// try, with prices for client-side cost (micro-USD per 1M tokens; spend is
// nano-USD).
export interface PlaygroundModel {
  name: string
  provider: Model['provider']
  input_micros: number
  output_micros: number
  cache_read_micros: number | null
  cache_write_micros: number | null
}

// The caller's tightest budget = their group's limits.
export interface GateBudget {
  type: BudgetType
  budget_nanos: number
  spend_nanos: number
  reset_at: string | null
}

// GET /console/api/v1/gate, polled every 15 s. providers_up of
// providers_total: distinct providers of the enabled models; a provider is
// down when its last >= 3 requests in the past 5 minutes all came back >= 500.
// rpm is requests in the last 60 s across all users; only for spend:read_all,
// else null; p50_ms the recent latency.
export interface GateInfo {
  providers_up: number
  providers_total: number
  rpm: number | null
  p50_ms: number | null
  budget: GateBudget | null
}

// One column of one playground turn. replies[i] lines up with models[i];
// null = not run (or still running). error holds the display text when the
// run failed (status · message, or the budget block copy). tin/tout/costNanos
// are null when no usage was reported; request is the exact body that was
// sent (for the Raw view and Copy as cURL); id/finishReason come from the
// response (success replies only).
export interface Reply {
  model: string
  text: string
  tin: number | null
  tout: number | null
  costNanos: number | null
  ms: number
  ttft: number
  error?: string
  id?: string
  finishReason?: string
  request?: object // the exact /playground/chat/completions body that was sent
}

export interface Turn {
  user: string
  replies: (Reply | null)[]
}

// A playground session: up to 3 model columns, one prompt per turn.
export interface SessionData {
  models: string[]
  system: string
  temperature: number
  top_p: number
  max_tokens: number
  stream: boolean
  turns: Turn[]
}
