export class ApiError extends Error {
  constructor(public status: number, message: string, public code?: string) {
    super(message)
  }
}

// safePath keeps post-login redirects on this origin: a single leading slash,
// not "//host" or "/\host" (both of which browsers treat as another origin).
export const safePath = (p: string | null | undefined) => (p && /^\/(?![/\\])/.test(p) ? p : '/')

// 401 handling shared by api() and apiStream(): bounce to /login carrying the
// current location. /auth/ endpoints and /health keep their own errors — a
// bad password must surface, not log the user out.
const redirectIf401 = (res: Response, path: string): boolean => {
  if (res.status !== 401 || path.startsWith('/auth/') || path === '/health') return false
  const here = window.location.pathname + window.location.search
  window.location.assign('/login?from=' + encodeURIComponent(here))
  return true
}

// Parse a response body as JSON; null for an empty or non-JSON body (e.g. a
// proxy error page).
const parseBody = async (res: Response): Promise<unknown> => {
  const text = await res.text()
  try {
    return text ? JSON.parse(text) : null
  } catch {
    return null
  }
}

// The ApiError for a non-2xx response, shared by api() and apiStream(): the
// server's { error: { message, code } } body wins, with a stable label for 5xx.
const errorFor = (res: Response, body: unknown): ApiError => {
  const err = (body as { error?: { message?: string; code?: string } } | null)?.error
  const message = res.status >= 500 ? 'internal error' : err?.message ?? res.statusText
  return new ApiError(res.status, message, err?.code)
}

// One of the two fetches in the app (with apiStream). Every request sends the
// JSON content type, DELETE included: the server's CSRF check rejects
// state-changing requests without it.
export async function api<T>(path: string, opts: { method?: string; body?: unknown } = {}): Promise<T> {
  const res = await fetch(path, {
    method: opts.method ?? 'GET',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
  })
  if (redirectIf401(res, path)) throw new ApiError(401, 'signed out')
  const body = await parseBody(res)
  if (!res.ok) throw errorFor(res, body)
  return body as T
}

// parseSSE splits an accumulated text/event-stream buffer into complete data
// payloads. Events are delimited by blank lines; the data: lines of one event
// are joined with \n, mirroring core/llm/sse.go. `rest` is the trailing
// incomplete chunk — prepend it to the next chunk before parsing again.
export const parseSSE = (buffer: string): { events: string[]; rest: string } => {
  const buf = buffer.replace(/\r\n/g, '\n')
  const events: string[] = []
  let start = 0
  let idx = buf.indexOf('\n\n')
  while (idx !== -1) {
    const data = sseData(buf.slice(start, idx))
    if (data !== null) events.push(data)
    start = idx + 2
    idx = buf.indexOf('\n\n', start)
  }
  return { events, rest: buf.slice(start) }
}

// The data payload of one complete event block, or null when it carries none.
const sseData = (block: string): string | null => {
  const data: string[] = []
  for (const line of block.split('\n')) {
    if (line.startsWith('data:')) data.push(line.slice(5).replace(/^ /, ''))
  }
  return data.length ? data.join('\n') : null
}

// Feed complete SSE payloads to onEvent; true on [DONE].
const sseFeed = (events: string[], onEvent: (data: unknown) => void): boolean => {
  for (const ev of events) {
    if (ev === '[DONE]') return true
    try {
      onEvent(JSON.parse(ev))
    } catch {
      // a malformed chunk must not kill the column's stream
    }
  }
  return false
}

// apiStream POSTs a JSON body and reads the response as it arrives: same 401
// handling as api(); non-2xx throws ApiError with the server's message/code;
// 2xx text/event-stream feeds each data: JSON to onEvent until [DONE]; 2xx
// JSON calls onEvent once. Resolves with the status and headers (carrying
// X-Tollgate-Cost on non-stream responses).
export async function apiStream(
  path: string,
  body: unknown,
  onEvent: (data: unknown) => void,
  signal: AbortSignal,
): Promise<{ status: number; headers: Headers }> {
  const res = await fetch(path, {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    signal,
  })
  if (redirectIf401(res, path)) throw new ApiError(401, 'signed out')
  if (!res.ok) throw errorFor(res, await parseBody(res))
  const isSSE = (res.headers.get('content-type') ?? '').includes('text/event-stream')
  if (!isSSE) {
    const text = await res.text()
    let data: unknown = null
    try {
      data = text ? JSON.parse(text) : null
    } catch {
      throw new ApiError(res.status, 'invalid JSON from server')
    }
    if (data !== null) onEvent(data)
    return { status: res.status, headers: res.headers }
  }
  const reader = res.body?.getReader()
  if (!reader) return { status: res.status, headers: res.headers }
  const dec = new TextDecoder()
  let buffer = ''
  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    buffer += dec.decode(value, { stream: true })
    const { events, rest } = parseSSE(buffer)
    buffer = rest
    if (sseFeed(events, onEvent)) {
      void reader.cancel() // [DONE]: stop reading, release the connection
      return { status: res.status, headers: res.headers }
    }
  }
  buffer += dec.decode()
  if (buffer) {
    // A final event may arrive without its trailing blank line.
    sseFeed(parseSSE(buffer + '\n\n').events, onEvent)
  }
  return { status: res.status, headers: res.headers }
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

// Tokens: humanized K/M/B (contract C3). One decimal via toFixed(1), then strip a
// trailing ".0"; thresholds 1e3/1e6/1e9. toFixed carries at the boundaries
// (999999 -> "1000.0" -> "1000K") — the test pins that.
export const fmtTokens = (n: number) => {
  if (n < 1e3) return String(n)
  if (n < 1e6) return (n / 1e3).toFixed(1).replace(/\.0$/, '') + 'K'
  if (n < 1e9) return (n / 1e6).toFixed(1).replace(/\.0$/, '') + 'M'
  return (n / 1e9).toFixed(1).replace(/\.0$/, '') + 'B'
}
export const toNanos = (dollars: string) => Math.round(parseFloat(dollars || '0') * 1e9)
export const fromNanos = (nanos: number) => String(nanos / 1e9)
export const toMicros = (dollars: string) => Math.round(parseFloat(dollars || '0') * 1e6)
export const fromMicros = (micros: number) => String(micros / 1e6)
export const num = (s: string): number | null => (s.trim() === '' ? null : Number(s))
export const when = (iso: string | null | undefined) => (iso ? new Date(iso).toLocaleString() : '—')
export const short = (id: string | null | undefined) => (id ? id.slice(0, 8) : '—')
export const dash = (s: string | null | undefined) => (s ? s : '—')
