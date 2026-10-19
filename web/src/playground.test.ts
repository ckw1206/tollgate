import { describe, expect, it } from 'vitest'
import { buildRequest, budgetState, costNanos, curlFor, replyMeta } from './playground'
import type { GateBudget, PlaygroundModel, SessionData } from './types'

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
