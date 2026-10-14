export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message)
  }
}

// safePath keeps post-login redirects on this origin: a single leading slash,
// not "//host" or "/\host" (both of which browsers treat as another origin).
export const safePath = (p: string | null | undefined) => (p && /^\/(?![/\\])/.test(p) ? p : '/')

// The only fetch in the app. Every request sends the JSON content type, DELETE
// included: the server's CSRF check rejects state-changing requests without it.
export async function api<T>(path: string, opts: { method?: string; body?: unknown } = {}): Promise<T> {
  const res = await fetch(path, {
    method: opts.method ?? 'GET',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
  })
  if (res.status === 401 && !path.startsWith('/auth/') && path !== '/health') {
    const here = window.location.pathname + window.location.search
    window.location.assign('/login?from=' + encodeURIComponent(here))
    throw new ApiError(401, 'signed out')
  }
  const text = await res.text()
  let data: { error?: { message?: string } } | null = null
  try {
    data = text ? JSON.parse(text) : null
  } catch {
    // non-JSON body (e.g. a proxy error page): fall through to the status text
  }
  if (!res.ok) throw new ApiError(res.status, res.status >= 500 ? 'internal error' : data?.error?.message ?? res.statusText)
  return data as T
}

// Outcome of a webhook test. The server always answers 200 with this shape
// (status = upstream HTTP status, 0 when unreachable); message is redacted server-side.
export interface WebhookTestResult {
  ok: boolean
  status: number
  message: string
}

// Fire a test event to the budget webhook. With `url`, test that (the value edited in the
// form, which may not be saved yet); without, the server tests the stored setting.
export const sendWebhookTest = (url?: string) =>
  api<WebhookTestResult>('/console/api/v1/settings/webhook/test', { method: 'POST', body: url ? { url } : undefined })

// Money: spend/budgets are nano-USD, prices are micro-USD per 1M tokens.
export const usd = (nanos: number) => {
  const d = nanos / 1e9
  return '$' + (d !== 0 && Math.abs(d) < 0.01 ? d.toFixed(6) : d.toFixed(2))
}
export const toNanos = (dollars: string) => Math.round(parseFloat(dollars || '0') * 1e9)
export const fromNanos = (nanos: number) => String(nanos / 1e9)
export const toMicros = (dollars: string) => Math.round(parseFloat(dollars || '0') * 1e6)
export const fromMicros = (micros: number) => String(micros / 1e6)
export const num = (s: string): number | null => (s.trim() === '' ? null : Number(s))
export const when = (iso: string | null | undefined) => (iso ? new Date(iso).toLocaleString() : '—')
export const short = (id: string | null | undefined) => (id ? id.slice(0, 8) : '—')
export const dash = (s: string | null | undefined) => (s ? s : '—')
