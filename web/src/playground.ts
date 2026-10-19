import { usd } from './api'
import type { GateBudget, PlaygroundModel, SessionData } from './types'

// costNanos mirrors core/llm/cost.go: nano-USD = tokens * micros / 1000,
// integer math only. prompt_tokens is the total (input + cache, as the
// adapters report it); cache tokens are priced at their own rate — the input
// rate when unset — and excluded from the input portion, exactly like Go's
// Usage.Input vs CacheRead/CacheWrite split.
export const costNanos = (
  u: { prompt_tokens: number; completion_tokens: number; cache_read?: number; cache_write?: number },
  p: PlaygroundModel,
): number => {
  const cr = p.cache_read_micros ?? p.input_micros
  const cw = p.cache_write_micros ?? p.input_micros
  const cacheRead = u.cache_read ?? 0
  const cacheWrite = u.cache_write ?? 0
  const input = Math.max(0, u.prompt_tokens - cacheRead - cacheWrite)
  const total =
    input * p.input_micros +
    u.completion_tokens * p.output_micros +
    cacheRead * cr +
    cacheWrite * cw
  return Math.trunc(total / 1000)
}

// Reply footer: "98 in · 52 out · $0.003000 · 1,940 ms · TTFT 620 ms".
export const replyMeta = (r: { tin: number; tout: number; costNanos: number; ms: number; ttft: number }): string =>
  `${r.tin} in · ${r.tout} out · ${usd(r.costNanos)} · ${r.ms.toLocaleString('en-US')} ms · TTFT ${r.ttft.toLocaleString(
    'en-US',
  )} ms`

// >= 80% warns, >= 100% blocks; no budget (null or unlimited) is ok at 0.
export const budgetState = (b: GateBudget | null): { pct: number; level: 'ok' | 'warn' | 'block' } => {
  if (!b || b.budget_nanos <= 0) return { pct: 0, level: 'ok' }
  const pct = (b.spend_nanos / b.budget_nanos) * 100
  return { pct, level: pct >= 100 ? 'block' : pct >= 80 ? 'warn' : 'ok' }
}

export interface PlaygroundChatRequest {
  model: string
  messages: { role: 'system' | 'user' | 'assistant'; content: string }[]
  temperature: number
  top_p: number
  max_tokens: number
  stream: boolean
  stream_options?: { include_usage: boolean }
}

// The /v1/chat/completions body for one column. A past turn contributes its
// user + assistant pair only when this column has a clean reply for it: a
// null/missing slot (not run, or the model was added after that message) and
// an errored reply contribute nothing, so a failed answer is never replayed
// (an empty assistant message would be rejected upstream). The last turn's
// prompt is the new user message; with no turns yet, only the system message
// is sent. include_usage is set only when streaming: all three adapters then
// emit the usage chunk, and the cost is computed client-side (streams carry
// no X-Tollgate-Cost).
export const buildRequest = (s: SessionData, slot: number): PlaygroundChatRequest => {
  const messages: PlaygroundChatRequest['messages'] = []
  if (s.system.trim() !== '') messages.push({ role: 'system', content: s.system })
  const turns = s.turns
  for (let i = 0; i + 1 < turns.length; i++) {
    const reply = turns[i].replies[slot]
    if (!reply || reply.error) continue
    messages.push({ role: 'user', content: turns[i].user })
    messages.push({ role: 'assistant', content: reply.text })
  }
  if (turns.length) messages.push({ role: 'user', content: turns[turns.length - 1].user })
  const req: PlaygroundChatRequest = {
    model: s.models[slot],
    messages,
    temperature: s.temperature,
    top_p: s.top_p,
    max_tokens: s.max_tokens,
    stream: s.stream,
  }
  if (s.stream) req.stream_options = { include_usage: true }
  return req
}

// A copy-paste curl of the same request. Never a real key: $TOLLGATE_URL and
// $TOLLGATE_KEY are the shell variables the user exports.
export const curlFor = (req: object): string =>
  `curl $TOLLGATE_URL/v1/chat/completions -H "Authorization: Bearer $TOLLGATE_KEY" -H "Content-Type: application/json" -d ${shellQuote(
    JSON.stringify(req),
  )}`

// Single-quote for the shell: an embedded ' becomes '\''.
const shellQuote = (s: string) => "'" + s.replaceAll("'", `'\\''`) + "'"
