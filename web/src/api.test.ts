import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, apiStream, fmtTokens, parseSSE } from './api'

// A text/event-stream Response whose body arrives in the given chunks.
const sseResponse = (chunks: string[]) =>
  new Response(
    new ReadableStream<Uint8Array>({
      start(controller) {
        const enc = new TextEncoder()
        for (const c of chunks) controller.enqueue(enc.encode(c))
        controller.close()
      },
    }),
    { status: 200, headers: { 'content-type': 'text/event-stream' } },
  )

describe('apiStream', () => {
  afterEach(() => vi.unstubAllGlobals())

  const signal = () => new AbortController().signal

  it('throws ApiError with the server message and code on non-2xx JSON', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({ error: { message: 'daily budget exceeded', type: 'api_error', code: 'budget_exceeded' } }),
          { status: 429, headers: { 'content-type': 'application/json' } },
        ),
      ),
    )
    const promise = apiStream('/console/api/v1/playground/chat/completions', { model: 'm' }, () => {}, signal())
    await expect(promise).rejects.toBeInstanceOf(ApiError)
    await expect(promise).rejects.toMatchObject({
      status: 429,
      message: 'daily budget exceeded',
      code: 'budget_exceeded',
    })
  })

  it('keeps the server message for a 5xx that carries a gateway error code', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ error: { message: 'upstream exploded', type: 'api_error', code: 'upstream_error' } }), {
          status: 502,
          headers: { 'content-type': 'application/json' },
        }),
      ),
    )
    await expect(apiStream('/console/api/v1/playground/chat/completions', { model: 'm' }, () => {}, signal())).rejects.toMatchObject({
      status: 502,
      message: 'upstream exploded',
      code: 'upstream_error',
    })
  })

  it('still labels a plain 5xx without a code as internal error', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ error: { message: 'boom' } }), {
          status: 500,
          headers: { 'content-type': 'application/json' },
        }),
      ),
    )
    await expect(apiStream('/console/api/v1/playground/chat/completions', { model: 'm' }, () => {}, signal())).rejects.toMatchObject({
      status: 500,
      message: 'internal error',
    })
  })

  it('feeds each SSE event as it completes and stops at [DONE]', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        sseResponse([
          'data: {"n":1}', // incomplete: held in rest until the next chunk
          '\n\ndata: {"n":2}\n\ndata: [DONE]\n\n',
          'data: {"late":true}\n\n', // after [DONE]: never delivered
        ]),
      ),
    )
    const seen: unknown[] = []
    const out = await apiStream('/console/api/v1/playground/chat/completions', { model: 'm' }, (d) => seen.push(d), signal())
    expect(out.status).toBe(200)
    expect(seen).toEqual([{ n: 1 }, { n: 2 }])
  })

  it('calls onEvent once for a 2xx JSON body', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ id: 'cmpl-1', choices: [] }), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      }),
    )
    vi.stubGlobal('fetch', fetchMock)
    const seen: unknown[] = []
    const out = await apiStream(
      '/console/api/v1/playground/chat/completions',
      { model: 'm', stream: false },
      (d) => seen.push(d),
      signal(),
    )
    expect(out.status).toBe(200)
    expect(seen).toEqual([{ id: 'cmpl-1', choices: [] }])
    expect(fetchMock).toHaveBeenCalledWith(
      '/console/api/v1/playground/chat/completions',
      expect.objectContaining({
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ model: 'm', stream: false }),
      }),
    )
  })

  it('flushes a final event that lacks its trailing blank line', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(sseResponse(['data: {"n":1}\n\n', 'data: {"n":2}'])))
    const seen: unknown[] = []
    await apiStream('/console/api/v1/playground/chat/completions', { model: 'm' }, (d) => seen.push(d), signal())
    expect(seen).toEqual([{ n: 1 }, { n: 2 }])
  })

  it('throws ApiError on a 2xx body that is not valid JSON', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response('<html>proxy error</html>', { status: 200, headers: { 'content-type': 'application/json' } }),
      ),
    )
    await expect(apiStream('/console/api/v1/playground/chat/completions', {}, () => {}, signal())).rejects.toMatchObject({
      status: 200,
      message: 'invalid JSON from server',
    })
  })

  it('redirects to /login on 401, same as api()', async () => {
    const assign = vi.fn()
    vi.stubGlobal('window', { location: { pathname: '/playground', search: '?a=b', assign } })
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('', { status: 401 })))
    await expect(apiStream('/console/api/v1/playground/chat/completions', {}, () => {}, signal())).rejects.toMatchObject({
      status: 401,
      message: 'signed out',
    })
    expect(assign).toHaveBeenCalledWith('/login?from=%2Fplayground%3Fa%3Db')
  })
})

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
