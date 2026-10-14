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
