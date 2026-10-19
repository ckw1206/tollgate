import { describe, expect, it } from 'vitest'
import { fmtTokens, parseSSE } from './api'

describe('parseSSE', () => {
  it('extracts a complete event, rest empty', () => {
    expect(parseSSE('data: {"a":1}\n\n')).toEqual({ events: ['{"a":1}'], rest: '' })
  })

  it('holds a payload split across chunk boundaries', () => {
    const first = parseSSE('data: {"a":')
    expect(first).toEqual({ events: [], rest: 'data: {"a":' })
    const second = parseSSE(first.rest + '1}\n\n')
    expect(second).toEqual({ events: ['{"a":1}'], rest: '' })
  })

  it('handles [DONE] and several events in one chunk', () => {
    expect(parseSSE('data: {"a":1}\n\ndata: {"b":2}\n\ndata: [DONE]\n\n')).toEqual({
      events: ['{"a":1}', '{"b":2}', '[DONE]'],
      rest: '',
    })
  })

  it('keeps a trailing partial event for the next chunk', () => {
    expect(parseSSE('data: {"a":1}\n\ndata: {"b":')).toEqual({ events: ['{"a":1}'], rest: 'data: {"b":' })
  })

  it('joins multi-line data, strips one space, skips comments and data-less events', () => {
    expect(parseSSE(': ping\ndata:line1\ndata: line2\n\n\nevent: x\n\n')).toEqual({
      events: ['line1\nline2'],
      rest: '',
    })
  })

  it('accepts CRLF framing', () => {
    expect(parseSSE('data: {"a":1}\r\n\r\n')).toEqual({ events: ['{"a":1}'], rest: '' })
  })
})

// Contract C3 (pinned). The rule is the spec; these values freeze it, including the
// boundary cases where toFixed(1) carries into the next unit (999999 -> "1000K").
describe('fmtTokens', () => {
  it.each([
    [0, '0'],
    [950, '950'],
    [999, '999'],
    [1000, '1K'],
    [1200, '1.2K'],
    [9900, '9.9K'],
    [100000, '100K'],
    [3400000, '3.4M'],
    [999999, '1000K'],
    [1000000, '1M'],
    [999999999, '1000M'],
    [1200000000, '1.2B'],
    [1000000000, '1B'],
  ])('fmtTokens(%i) === %s', (n, want) => {
    expect(fmtTokens(n)).toBe(want)
  })
})
