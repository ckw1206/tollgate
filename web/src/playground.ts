import { toNanos, usd, when } from './api'
import type { GateBudget, PlaygroundModel, Reply, SessionData } from './types'

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
// tin/tout/costNanos are null when no usage was reported: show —, not 0.
export const replyMeta = (r: { tin: number | null; tout: number | null; costNanos: number | null; ms: number; ttft: number }): string =>
  `${r.tin ?? '—'} in · ${r.tout ?? '—'} out · ${r.costNanos != null ? usd(r.costNanos) : '—'} · ${r.ms.toLocaleString('en-US')} ms · TTFT ${r.ttft.toLocaleString(
    'en-US',
  )} ms`

// The usage chunk of a stream (OpenAI-shaped, prompt_tokens is the total) or
// the usage object of a non-stream body.
export interface UsageInfo {
  prompt_tokens?: number
  completion_tokens?: number
  prompt_tokens_details?: { cached_tokens?: number }
}

// Reduce one column's outcome to {tin, tout, costNanos}.
// - Tokens always come from the usage chunk; no chunk -> all unknown (null).
// - Stream: streams carry no X-Tollgate-Cost, so the cost is computed
//   client-side with the same integer formula as the server (costNanos,
//   cached tokens at their own rate). No price info -> cost unknown (null),
//   the tokens are still set.
// - Non-stream: the server's X-Tollgate-Cost header, decimal USD like
//   "0.0075", converted with toNanos (never float math on money).
export const usageReply = (args: {
  stream: boolean
  usage: UsageInfo | null
  costHeader: string | null
  model: PlaygroundModel | null
}): { tin: number | null; tout: number | null; costNanos: number | null } => {
  const u = args.usage
  if (!u) return { tin: null, tout: null, costNanos: null }
  const tin = u.prompt_tokens ?? null
  const tout = u.completion_tokens ?? null
  if (!args.stream) {
    // A missing header means the cost is unknown, not $0.
    if (args.costHeader == null || args.costHeader === '') return { tin, tout, costNanos: null }
    return { tin, tout, costNanos: toNanos(args.costHeader) }
  }
  if (!args.model) return { tin, tout, costNanos: null }
  return {
    tin,
    tout,
    costNanos: costNanos(
      { prompt_tokens: u.prompt_tokens ?? 0, completion_tokens: u.completion_tokens ?? 0, cache_read: u.prompt_tokens_details?.cached_tokens },
      args.model,
    ),
  }
}

// The column message for a failed run: the budget block copy for 402 or the
// budget_exceeded code (any transport: pre-stream status or in-stream
// error chunk); otherwise "{status} · {message}" — the in-stream error chunk
// has no status (the stream already went out as 200), so just the message.
export const columnErrorText = (fail: { message: string; code?: string; status?: number } | null, budgetText: string): string | null => {
  if (!fail) return null
  if (fail.status === 402 || fail.code === 'budget_exceeded') return budgetText
  return fail.status ? `${fail.status} · ${fail.message}` : fail.message
}

// Composer banner for >= 80% of a finite budget: "You've used 80% of today's
// budget ($0.81 of $1.00). Replies keep working until you reach it."
// ("your budget" for non-daily; percentage floored.)
export const budgetWarnCopy = (b: GateBudget, pct: number): string =>
  `You've used ${Math.floor(pct)}% of ${b.type === 'daily' ? "today's budget" : 'your budget'} (${usd(b.spend_nanos)} of ${usd(
    b.budget_nanos,
  )}). Replies keep working until you reach it.`

// Composer banner and the column message at >= 100%: "Daily budget reached
// ($1.00). Resets at … — ask an admin to raise it." Non-daily budgets say
// "Budget reached" and only mention the reset time when there is one.
export const budgetBlockCopy = (b: GateBudget): string => {
  const head = b.type === 'daily' ? 'Daily budget reached' : 'Budget reached'
  const tail = b.reset_at ? ` Resets at ${when(b.reset_at)} — ask an admin to raise it.` : ' Ask an admin to raise it.'
  return `${head} (${usd(b.budget_nanos)}).${tail}`
}

// The Raw view's response pane for one column: a status + cost header line,
// then the normalized body with the content trimmed to ~64 characters — or
// a plain note while the column is streaming or has no reply yet.
export const rawResponseBody = (r: Reply | null, streaming: boolean): string => {
  if (streaming) return '// streaming…'
  if (!r) return '// no response yet'
  if (r.error) return `// error\n${r.error}`
  const body: Record<string, unknown> = {}
  if (r.id) body.id = r.id
  body.model = r.model
  body.choices = [
    {
      index: 0,
      message: { role: 'assistant', content: r.text.length > 64 ? `${r.text.slice(0, 64)}…` : r.text },
      finish_reason: r.finishReason ?? 'stop',
    },
  ]
  if (r.tin != null && r.tout != null) {
    body.usage = { prompt_tokens: r.tin, completion_tokens: r.tout, total_tokens: r.tin + r.tout }
  }
  return `// 200 · cost (nanos): ${r.costNanos ?? '—'}\n${JSON.stringify(body, null, 2)}`
}

// A saved session may predate the current model set: if none of its models
// are usable any more, fall back to the first usable one. A partially stale
// list keeps its valid models (the stale ones render as disabled options),
// and an empty list is seeded the same way.
export const reseedSession = (data: SessionData, usable: PlaygroundModel[]): SessionData => {
  if (!usable.length) return data
  if (data.models.some((m) => usable.some((x) => x.name === m))) return data
  const models = [usable[0]!.name]
  return {
    ...data,
    models,
    // The saved replies no longer line up with the re-seeded model: kept as
    // they were, the new column would show the old model's replies/costs and
    // buildRequest would replay the old answers as history. Reset every turn
    // so it renders "Not run — model added after this message."
    turns: data.turns.map((t) => ({ ...t, replies: models.map(() => null) })),
  }
}

// The session payload for save. The Raw view and Copy as cURL only read the
// last turn's stored request, so persisting it on every reply would make the
// payload grow quadratically (each request holds the whole history) and hit
// the 1 MiB body cap; keep it only on the last turn and strip it from the
// rest. Earlier turns fall back to buildRequest.
export const forSave = (data: SessionData): SessionData => {
  const last = data.turns.length - 1
  return {
    ...data,
    turns: data.turns.map((t, i) =>
      i === last
        ? t
        : {
            ...t,
            replies: t.replies.map((r) => (r && r.request ? { ...r, request: undefined } : r)),
          },
    ),
  }
}

// The exact JSON body the page PUTs/POSTs when saving a session: the trimmed
// name and forSave's payload (only the last turn keeps its stored request, so
// the body stays linear in the number of turns).
export const saveBody = (name: string, session: SessionData): { name: string; data: SessionData } => ({
  name: name.trim(),
  data: forSave(session),
})

// The page state a session pick commits into.
export interface PickState {
  sessionId: string | null
  session: SessionData
  name: string
  saved: { name: string; data: SessionData } | null
  pendingId: string | null
}

// The commit of a session pick: the loaded session's id and data are set
// together and the pending flag is cleared, so the editor and the id can
// never disagree (Save could then PUT session A's data to session B). Pure,
// so the atomicity is testable without a DOM.
export const commitPick = (state: PickState, loaded: { id: string; name: string; data: SessionData }): PickState => ({
  ...state,
  sessionId: loaded.id,
  session: loaded.data,
  name: loaded.name,
  saved: { name: loaded.name, data: loaded.data },
  pendingId: null,
})

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

// A fresh session's title when the user leaves the name blank, e.g. "Brisk Heron".
const ADJECTIVES = ['Amber', 'Brisk', 'Calm', 'Clever', 'Dapper', 'Eager', 'Gentle', 'Golden', 'Lively', 'Lucky', 'Mellow', 'Nimble', 'Quiet', 'Rapid', 'Sunny', 'Swift']
const NOUNS = ['Badger', 'Comet', 'Falcon', 'Fox', 'Harbor', 'Heron', 'Lantern', 'Maple', 'Meadow', 'Otter', 'Pine', 'Raven', 'River', 'Sparrow', 'Summit', 'Willow']
export const randomName = (rand: () => number = Math.random): string => {
  const pick = (a: string[]) => a[Math.floor(rand() * a.length)]!
  return `${pick(ADJECTIVES)} ${pick(NOUNS)}`
}

// Enter sends, Shift+Enter is a newline; never while an IME is composing
// (keyCode 229 covers Safari, which ends composition before keydown).
export const isSendKey = (e: { key: string; shiftKey: boolean; isComposing?: boolean; keyCode?: number }): boolean =>
  e.key === 'Enter' && !e.shiftKey && !e.isComposing && e.keyCode !== 229
