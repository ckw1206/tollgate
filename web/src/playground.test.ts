import { describe, expect, it } from 'vitest'
import {
  buildRequest,
  budgetBlockCopy,
  budgetState,
  budgetWarnCopy,
  columnErrorText,
  commitPick,
  costNanos,
  curlFor,
  forSave,
  rawResponseBody,
  reseedSession,
  replyMeta,
  saveBody,
  usageReply,
  type PickState,
} from './playground'
import type { BudgetType, GateBudget, PlaygroundModel, Reply, SessionData } from './types'

// $2.50 input / $10 output per 1M tokens — the same model as core/llm/cost_test.go.
const model: PlaygroundModel = {
  name: 'GPT-4o',
  provider: 'openai',
  input_micros: 2_500_000,
  output_micros: 10_000_000,
  cache_read_micros: null,
  cache_write_micros: null,
}

describe('costNanos', () => {
  // Pinned to core/llm/cost_test.go: 1000 in / 500 out at $2.50/$10 = $0.0075.
  it('matches llm.Cost', () => {
    expect(costNanos({ prompt_tokens: 1000, completion_tokens: 500 }, model)).toBe(7_500_000)
  })

  // core/llm: Cost(Usage{CacheRead: 1000}) == 2_500_000 (cache defaults to input price).
  // prompt_tokens is the total, so the cached 1000 of it are not re-priced as input.
  it('prices cache reads at the input price when unset', () => {
    expect(costNanos({ prompt_tokens: 1000, completion_tokens: 0, cache_read: 1000 }, model)).toBe(2_500_000)
  })

  it('prices cache reads/writes at their own rates, excluded from input', () => {
    const m: PlaygroundModel = { ...model, cache_read_micros: 250_000, cache_write_micros: 500_000 }
    // 1300 prompt = 1000 fresh + 200 read + 100 write; 500 out
    // 1000*2.5M + 500*10M + 200*250k + 100*500k = 2.5M + 5M + 50k + 50k = 7.6M nanos
    expect(costNanos({ prompt_tokens: 1300, completion_tokens: 500, cache_read: 200, cache_write: 100 }, m)).toBe(
      7_600_000,
    )
  })

  it('truncates like Go integer division', () => {
    // 3 * 2_500_001 = 7_500_003; /1000 = 7500.003 -> 7500
    expect(costNanos({ prompt_tokens: 3, completion_tokens: 0 }, { ...model, input_micros: 2_500_001 })).toBe(7500)
  })

  it('clamps the input portion at 0 when cache counts exceed the total', () => {
    // 150 cached of 100 prompt: no negative input, just 150 * 2.5M / 1000.
    expect(costNanos({ prompt_tokens: 100, completion_tokens: 0, cache_read: 150 }, model)).toBe(375_000)
  })
})

describe('replyMeta', () => {
  it('formats the footer exactly', () => {
    expect(replyMeta({ tin: 98, tout: 52, costNanos: 3_000_000, ms: 1940, ttft: 620 })).toBe(
      '98 in · 52 out · $0.003000 · 1,940 ms · TTFT 620 ms',
    )
  })
  it('shows — instead of 0 for unknown tokens/cost', () => {
    expect(replyMeta({ tin: null, tout: null, costNanos: null, ms: 1940, ttft: 620 })).toBe('— in · — out · — · 1,940 ms · TTFT 620 ms')
  })
})

const gate = (type: BudgetType, spend: number, total: number, reset_at: string | null = null): GateBudget => ({
  type,
  spend_nanos: spend,
  budget_nanos: total,
  reset_at,
})

describe('usageReply', () => {
  it('stream: tokens from the usage chunk, cost via costNanos with cached tokens', () => {
    const r = usageReply({
      stream: true,
      usage: { prompt_tokens: 1300, completion_tokens: 500, prompt_tokens_details: { cached_tokens: 200 } },
      costHeader: null,
      model,
    })
    // input 1100 @ $2.50 + out 500 @ $10 + cache read 200 @ $2.50 = 8.25M micros → 8,250,000 nanos
    expect(r).toEqual({ tin: 1300, tout: 500, costNanos: 8_250_000 })
  })

  it('stream: keeps the tokens when price info is missing, cost unknown', () => {
    expect(usageReply({ stream: true, usage: { prompt_tokens: 10, completion_tokens: 5 }, costHeader: null, model: null })).toEqual({
      tin: 10,
      tout: 5,
      costNanos: null,
    })
  })

  it('no usage chunk: everything unknown, header irrelevant', () => {
    expect(usageReply({ stream: false, usage: null, costHeader: '9.99', model })).toEqual({ tin: null, tout: null, costNanos: null })
  })

  it('non-stream: cost from the X-Tollgate-Cost header via toNanos', () => {
    const r = usageReply({ stream: false, usage: { prompt_tokens: 1000, completion_tokens: 500 }, costHeader: '0.0075', model: null })
    expect(r).toEqual({ tin: 1000, tout: 500, costNanos: 7_500_000 })
  })

  it('non-stream: a missing header is unknown, not $0', () => {
    const r = usageReply({ stream: false, usage: { prompt_tokens: 1000, completion_tokens: 500 }, costHeader: null, model: null })
    expect(r).toEqual({ tin: 1000, tout: 500, costNanos: null })
  })
})

describe('columnErrorText', () => {
  it('no failure, no message', () => {
    expect(columnErrorText(null, 'BUDGET')).toBeNull()
  })
  it('renders {status} · {message} for an upstream 502', () => {
    expect(columnErrorText({ status: 502, code: 'upstream_error', message: 'upstream exploded' }, 'BUDGET')).toBe('502 · upstream exploded')
  })
  it('keeps the block copy for the budget_exceeded code and for 402', () => {
    expect(columnErrorText({ status: 429, code: 'budget_exceeded', message: 'group budget exceeded' }, 'BUDGET')).toBe('BUDGET')
    expect(columnErrorText({ status: 402, code: 'payment_required', message: 'no credit' }, 'BUDGET')).toBe('BUDGET')
  })
  it('an in-stream error chunk (no status) shows just the message', () => {
    expect(columnErrorText({ code: 'upstream_error', message: 'upstream exploded' }, 'BUDGET')).toBe('upstream exploded')
  })
  it('other 4xx keep the status and the server message', () => {
    expect(columnErrorText({ status: 403, code: 'model_not_allowed', message: 'model "fast" isn’t allowed' }, 'BUDGET')).toBe(
      '403 · model "fast" isn’t allowed',
    )
  })
})

describe('budgetWarnCopy', () => {
  it('daily: floored pct, today’s budget', () => {
    expect(budgetWarnCopy(gate('daily', 809_000_000, 1_000_000_000), 80.9)).toBe(
      "You've used 80% of today's budget ($0.81 of $1.00). Replies keep working until you reach it.",
    )
  })
  it('non-daily: your budget', () => {
    expect(budgetWarnCopy(gate('monthly', 904_000_000, 1_000_000_000), 90.4)).toBe(
      "You've used 90% of your budget ($0.90 of $1.00). Replies keep working until you reach it.",
    )
  })
})

describe('budgetBlockCopy', () => {
  it('daily with a reset time', () => {
    const c = budgetBlockCopy(gate('daily', 1_000_000_000, 1_000_000_000, '2026-10-20T00:00:00Z'))
    expect(c.startsWith('Daily budget reached ($1.00). Resets at ')).toBe(true)
    expect(c.endsWith('— ask an admin to raise it.')).toBe(true)
  })
  it('non-daily without a reset time', () => {
    expect(budgetBlockCopy(gate('total', 5_000_000_000, 5_000_000_000, null))).toBe('Budget reached ($5.00). Ask an admin to raise it.')
  })
})

describe('rawResponseBody', () => {
  it('streaming and no-reply notes', () => {
    expect(rawResponseBody(null, true)).toBe('// streaming…')
    expect(rawResponseBody(null, false)).toBe('// no response yet')
  })
  it('an error reply shows the error text', () => {
    const r: Reply = { model: 'm', text: '', tin: null, tout: null, costNanos: null, ms: 1, ttft: 0, error: '502 · upstream exploded' }
    expect(rawResponseBody(r, false)).toBe('// error\n502 · upstream exploded')
  })
  it('a success: status/cost line, trimmed content, usage', () => {
    const r: Reply = {
      model: 'gpt-4o',
      text: 'x'.repeat(100),
      tin: 10,
      tout: 5,
      costNanos: 7_500_000,
      ms: 100,
      ttft: 40,
      id: 'chatcmpl-1',
      finishReason: 'stop',
    }
    const out = rawResponseBody(r, false)
    const [head, ...rest] = out.split('\n')
    expect(head).toBe('// 200 · cost (nanos): 7500000')
    const body = JSON.parse(rest.join('\n'))
    expect(body.id).toBe('chatcmpl-1')
    expect(body.model).toBe('gpt-4o')
    expect(body.choices).toEqual([
      { index: 0, message: { role: 'assistant', content: 'x'.repeat(64) + '…' }, finish_reason: 'stop' },
    ])
    expect(body.usage).toEqual({ prompt_tokens: 10, completion_tokens: 5, total_tokens: 15 })
  })
  it('unknown usage: no usage object, cost dashed (—, like the footer)', () => {
    const r: Reply = { model: 'gpt-4o', text: 'hi', tin: null, tout: null, costNanos: null, ms: 1, ttft: 0 }
    const out = rawResponseBody(r, false)
    expect(out.startsWith('// 200 · cost (nanos): —')).toBe(true)
    expect(JSON.parse(out.split('\n').slice(1).join('\n')).usage).toBeUndefined()
  })
})

const budget = (spend: number, total: number): GateBudget => ({
  type: 'daily',
  budget_nanos: total,
  spend_nanos: spend,
  reset_at: null,
})

describe('budgetState', () => {
  it('ok below 80%', () => {
    expect(budgetState(budget(799, 1000))).toEqual({ pct: 79.9, level: 'ok' })
  })
  it('warn at 80%', () => {
    expect(budgetState(budget(80, 100))).toEqual({ pct: 80, level: 'warn' })
  })
  it('block at 100%', () => {
    expect(budgetState(budget(100, 100))).toEqual({ pct: 100, level: 'block' })
  })
  it('null budget is ok at 0', () => {
    expect(budgetState(null)).toEqual({ pct: 0, level: 'ok' })
  })
  it('unlimited (budget_nanos 0) is ok at 0', () => {
    expect(budgetState(budget(5, 0))).toEqual({ pct: 0, level: 'ok' })
  })
})

const session = (): SessionData => ({
  models: ['gpt-4o', 'claude-sonnet'],
  system: 'Be terse.',
  temperature: 0.7,
  top_p: 1,
  max_tokens: 512,
  stream: true,
  turns: [
    {
      user: 'Hello',
      replies: [
        { model: 'gpt-4o', text: 'Hi there', tin: 10, tout: 5, costNanos: 0, ms: 100, ttft: 40 },
        null,
      ],
    },
    { user: 'And you?', replies: [null, null] },
  ],
})

describe('buildRequest', () => {
  it('replays only turns that column has a reply for', () => {
    const req = buildRequest(session(), 0)
    expect(req.model).toBe('gpt-4o')
    expect(req.messages).toEqual([
      { role: 'system', content: 'Be terse.' },
      { role: 'user', content: 'Hello' },
      { role: 'assistant', content: 'Hi there' },
      { role: 'user', content: 'And you?' },
    ])
    expect(req.temperature).toBe(0.7)
    expect(req.top_p).toBe(1)
    expect(req.max_tokens).toBe(512)
    expect(req.stream).toBe(true)
    expect(req.stream_options).toEqual({ include_usage: true })
  })

  it('a turn without a reply for this column contributes neither message', () => {
    const req = buildRequest(session(), 1) // turn 0 has no reply for column 1
    expect(req.model).toBe('claude-sonnet')
    expect(req.messages).toEqual([
      { role: 'system', content: 'Be terse.' },
      { role: 'user', content: 'And you?' },
    ])
  })

  it('treats a missing reply slot as not run (model added after the message)', () => {
    const s = session()
    s.turns[0].replies = s.turns[0].replies.slice(0, 1) // the second column did not exist yet
    const req = buildRequest(s, 1)
    expect(req.messages).toEqual([
      { role: 'system', content: 'Be terse.' },
      { role: 'user', content: 'And you?' },
    ])
  })

  it('does not replay an errored reply (or its user message)', () => {
    const s = session()
    s.turns[0].replies[0] = {
      model: 'gpt-4o',
      text: '',
      tin: 10,
      tout: 0,
      costNanos: 0,
      ms: 300,
      ttft: 0,
      error: 'upstream 503: service unavailable',
    }
    const req = buildRequest(s, 0)
    expect(req.messages).toEqual([
      { role: 'system', content: 'Be terse.' },
      { role: 'user', content: 'And you?' },
    ])
  })

  it('with no turns sends only the system message', () => {
    const s = session()
    s.turns = []
    const req = buildRequest(s, 0)
    expect(req.model).toBe('gpt-4o')
    expect(req.messages).toEqual([{ role: 'system', content: 'Be terse.' }])
    expect(req.stream_options).toEqual({ include_usage: true })
  })

  it('omits the system message when blank and stream_options when not streaming', () => {
    const s = session()
    s.system = '   '
    s.stream = false
    const req = buildRequest(s, 0)
    expect(req.messages[0]).toEqual({ role: 'user', content: 'Hello' })
    expect(req.stream).toBe(false)
    expect(req.stream_options).toBeUndefined()
  })
})

describe('forSave', () => {
  it('keeps request only on the last turn, strips it from earlier turns', () => {
    const req = { model: 'gpt-4o', messages: [{ role: 'user' as const, content: 'hi' }], temperature: 0.7, top_p: 1, max_tokens: 512, stream: true }
    const s = session()
    s.turns[0].replies[0] = { ...s.turns[0].replies[0]!, request: req }
    s.turns[1].replies = [
      { model: 'gpt-4o', text: 'yo', tin: 3, tout: 2, costNanos: 100, ms: 10, ttft: 5, request: req },
      { model: 'claude-sonnet', text: 'hey', tin: 4, tout: 1, costNanos: 200, ms: 20, ttft: 6, request: req },
    ]
    const out = forSave(s)
    expect(out.turns[0].replies[0]?.request).toBeUndefined()
    expect(out.turns[1].replies[0]?.request).toBe(req)
    expect(out.turns[1].replies[1]?.request).toBe(req)
    // The persisted payload has no request keys before the last turn.
    expect(JSON.stringify(out.turns.slice(0, -1))).not.toContain('request')
    // The input is not mutated.
    expect(s.turns[0].replies[0]?.request).toBe(req)
  })

  it('leaves a session with no turns unchanged', () => {
    const s = session()
    s.turns = []
    expect(forSave(s)).toEqual(s)
  })

  it('keeps null (not-run) slots as-is', () => {
    const s = session()
    const out = forSave(s)
    expect(out.turns[0].replies[1]).toBeNull()
  })
})

describe('saveBody', () => {
  it('sends the trimmed name and forSave’s payload', () => {
    const req = { model: 'gpt-4o', messages: [{ role: 'user' as const, content: 'hi' }], temperature: 0.7, top_p: 1, max_tokens: 512, stream: true }
    const s = session()
    s.turns[0].replies[0] = { ...s.turns[0].replies[0]!, request: req }
    s.turns[1].replies[0] = { model: 'gpt-4o', text: 'yo', tin: 3, tout: 2, costNanos: 100, ms: 10, ttft: 5, request: req }
    const body = saveBody('  My session  ', s)
    expect(body.name).toBe('My session')
    // forSave's strip is applied: the last turn keeps its request, the earlier
    // turns don't (so the persisted payload can't grow quadratically).
    expect(body.data.turns[1].replies[0]?.request).toBe(req)
    expect(JSON.stringify(body.data.turns.slice(0, -1))).not.toContain('request')
  })
})

describe('commitPick', () => {
  const state: PickState = {
    sessionId: 'old',
    session: session(),
    name: 'Old',
    saved: { name: 'Old', data: session() },
    pendingId: 'pick-1',
  }
  it('sets the id and the data together and clears the pending flag', () => {
    const data = session()
    const next = commitPick(state, { id: 'pick-1', name: 'Picked', data })
    // The id and the data land in the same state object — a commit can never
    // hold one session's data under another session's id.
    expect(next.sessionId).toBe('pick-1')
    expect(next.session).toBe(data)
    expect(next.saved).toEqual({ name: 'Picked', data })
    expect(next.name).toBe('Picked')
    expect(next.pendingId).toBeNull()
  })
  it('replaces the previous state’s values wholesale', () => {
    const data = session()
    const next = commitPick(state, { id: 'pick-2', name: 'New', data })
    expect(next.sessionId).toBe('pick-2')
    expect(next.session).toBe(data)
    expect(next.name).toBe('New')
    expect(next.saved?.name).toBe('New')
    expect(next.saved?.data).toBe(data)
    expect(next.pendingId).toBeNull()
  })
})

describe('reseedSession', () => {
  const usable: PlaygroundModel[] = [
    model,
    { name: 'claude-sonnet', provider: 'anthropic', input_micros: 3_000_000, output_micros: 15_000_000, cache_read_micros: null, cache_write_micros: null },
  ]
  it('falls back to the first usable model when none of the saved ones remain', () => {
    const s = session()
    s.models = ['old-a', 'old-b']
    expect(reseedSession(s, usable).models).toEqual(['GPT-4o'])
  })
  it('re-seeding resets every turn’s replies to the new model list', () => {
    const s = session()
    s.models = ['old-a', 'old-b']
    const out = reseedSession(s, usable)
    // Each turn's replies line up with the (single) re-seeded model and are
    // all null, so no old column's answer can show under the new one.
    for (const t of out.turns) {
      expect(t.replies).toHaveLength(out.models.length)
      for (const r of t.replies) expect(r).toBeNull()
    }
    // The old columns' answers are dropped, not replayed as history.
    expect(JSON.stringify(out.turns)).not.toContain('Hi there')
    // The input is not mutated.
    expect(s.turns[0].replies[0]?.text).toBe('Hi there')
  })
  it('seeds an empty model list the same way', () => {
    const s = session()
    s.models = []
    expect(reseedSession(s, usable).models).toEqual(['GPT-4o'])
  })
  it('keeps a list that still has a usable model (stale entries stay disabled)', () => {
    const s = session()
    s.models = ['old-a', 'GPT-4o']
    expect(reseedSession(s, usable)).toBe(s)
  })
  it('leaves the session alone when nothing is usable', () => {
    const s = session()
    s.models = ['old-a']
    expect(reseedSession(s, [])).toBe(s)
  })
})

describe('curlFor', () => {
  it('embeds the request with shell variables, never a real key', () => {
    const s = session()
    s.stream = false
    const curl = curlFor(buildRequest(s, 0))
    expect(curl).toContain('$TOLLGATE_URL/v1/chat/completions')
    expect(curl).toContain('-H "Authorization: Bearer $TOLLGATE_KEY"')
    expect(curl).toContain('-H "Content-Type: application/json"')
    expect(curl).not.toContain('tg-')
  })

  it('single-quotes the JSON payload and escapes embedded quotes', () => {
    const curl = curlFor({ model: 'm', messages: [{ role: 'user', content: "it's" }] })
    expect(curl).toBe(
      `curl $TOLLGATE_URL/v1/chat/completions -H "Authorization: Bearer $TOLLGATE_KEY" -H "Content-Type: application/json" -d '{"model":"m","messages":[{"role":"user","content":"it'\\''s"}]}'`,
    )
  })
})
