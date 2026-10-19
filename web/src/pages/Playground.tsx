import { useEffect, useRef, useState } from 'react'
import { ApiError, api, apiStream, usd } from '../api'
import { useAuth } from '../auth'
import { Banner, Button, CheckButton, CopyButton, controlCls, ErrorText, inputCls, PageHeader, Segmented, Skeleton, useLoad } from '../components/ui'
import {
  buildRequest,
  budgetBlockCopy,
  budgetState,
  budgetWarnCopy,
  columnErrorText,
  curlFor,
  rawResponseBody,
  replyMeta,
  usageReply,
} from '../playground'
import type { GateBudget, GateInfo, PlaygroundModel, Reply, SessionData, Turn } from '../types'

// The playground runs through the console session (no API key): one POST per
// column, key-less principal, the user's group as billing scope.
const CHAT = '/console/api/v1/playground/chat/completions'
const SESSIONS = '/console/api/v1/playground/sessions'
const MAX_MODELS = 3

// D2: panel labels — Geist Mono 11px, uppercase.
const labelCls = 'font-mono text-[11px] uppercase tracking-[0.06em] text-muted-foreground'

const emptySession = (): SessionData => ({ models: [], system: '', temperature: 0.7, top_p: 1, max_tokens: 512, stream: true, turns: [] })

interface SessionMeta {
  id: string
  name: string
  updated_at: string
}

interface SessionRow {
  id: string
  name: string
  data: SessionData
}

// One SSE data payload (or the single non-stream JSON body): OpenAI-shaped,
// normalised by the gateway for all three providers.
interface Chunk {
  id?: string
  choices?: { delta?: { content?: string }; message?: { content?: string }; finish_reason?: string }[]
  usage?: { prompt_tokens: number; completion_tokens: number; prompt_tokens_details?: { cached_tokens?: number } }
  error?: { message?: string; code?: string } // in-stream failure (sent after the stream started)
}

// A saved session may predate the current model set or an older page version:
// accept the known shape, drop the rest, keep defaults (unknown usage = null).
const DEFAULT_REPLY = { model: '', text: '', tin: null, tout: null, costNanos: null, ms: 0, ttft: 0 }
const normalize = (d: Partial<SessionData> | null | undefined): SessionData => ({
  models: Array.isArray(d?.models) ? d.models.slice(0, MAX_MODELS).filter((m): m is string => typeof m === 'string') : [],
  system: typeof d?.system === 'string' ? d.system : '',
  temperature: typeof d?.temperature === 'number' ? d.temperature : 0.7,
  top_p: typeof d?.top_p === 'number' ? d.top_p : 1,
  max_tokens: typeof d?.max_tokens === 'number' && d.max_tokens >= 1 ? Math.floor(d.max_tokens) : 512,
  stream: d?.stream !== false,
  turns: (Array.isArray(d?.turns) ? d.turns : [])
    .filter((t) => t && typeof t.user === 'string')
    .map((t) => ({
      user: t.user,
      replies: (Array.isArray(t.replies) ? t.replies : []).map((r) => (r ? { ...DEFAULT_REPLY, ...r } : null)),
    })),
})

// MaxTokensInput: draft like DayInput — only whole numbers >= 1 reach state,
// blur resets a partial draft.
function MaxTokensInput({ value, onCommit }: { value: number; onCommit: (n: number) => void }) {
  const [draft, setDraft] = useState(String(value))
  useEffect(() => setDraft(String(value)), [value])
  return (
    <input
      type="number"
      min={1}
      aria-label="Max tokens"
      className={controlCls + ' mt-1 block h-9 w-full text-sm'}
      value={draft}
      onChange={(e) => {
        setDraft(e.target.value)
        const n = Number(e.target.value)
        if (e.target.value.trim() !== '' && Number.isFinite(n)) onCommit(Math.max(1, Math.floor(n)))
      }}
      onBlur={() => setDraft(String(value))}
    />
  )
}

export default function Playground() {
  const { can } = useAuth()
  const models = useLoad<PlaygroundModel[]>('/console/api/v1/playground/models')
  const gate = useLoad<GateInfo>('/console/api/v1/gate')
  const list = useLoad<SessionMeta[]>(SESSIONS)

  const [session, setSession] = useState<SessionData>(emptySession)
  const [name, setName] = useState('')
  const [sessionId, setSessionId] = useState<string | null>(null)
  // The last saved (or loaded) snapshot; anything else is dirty. A fresh,
  // never-saved session is dirty only once there is something to save.
  const [saved, setSaved] = useState<{ name: string; data: SessionData } | null>(null)
  const dirty = saved
    ? JSON.stringify({ name, data: session }) !== JSON.stringify(saved)
    : name.trim() !== '' || session.turns.length > 0

  const [view, setView] = useState<'chat' | 'raw'>('chat')
  const [prompt, setPrompt] = useState('')
  const [running, setRunning] = useState(false)
  // In-flight text per slot, plus the exact request sent for that column.
  const [live, setLive] = useState<Record<number, { text: string; request: object }>>({})
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState('')
  const [loadError, setLoadError] = useState('')
  const [bannerGone, setBannerGone] = useState<'warn' | 'block' | null>(null)

  // seq invalidates in-flight finishers (clear/new/switch bump it); pending
  // counts the columns of the current send so the UI unlocks exactly once;
  // pickSeq keeps a slower session GET from overwriting a newer pick.
  const seqRef = useRef(0)
  const pendingRef = useRef(0)
  const pickSeqRef = useRef(0)
  const controllersRef = useRef(new Map<number, AbortController>())

  const abortAll = () => {
    for (const c of controllersRef.current.values()) c.abort()
    controllersRef.current.clear()
  }
  useEffect(() => abortAll, [])

  // Whenever the session has no models but some are usable, take the first
  // (initial load, "New", a saved empty session).
  useEffect(() => {
    const m = models.data
    if (!m?.length || session.models.length) return
    setSession((s) => (s.models.length ? s : { ...s, models: [m[0]!.name] }))
  }, [models.data, session.models.length])

  const patch = (p: Partial<SessionData>) => setSession((s) => ({ ...s, ...p }))
  const setSlot = (i: number, model: string) =>
    setSession((s) => {
      if (s.models[i] === model || s.models.includes(model)) return s // no duplicate columns
      const arr = [...s.models]
      arr[i] = model
      return { ...s, models: arr }
    })
  const addSlot = () =>
    setSession((s) => {
      if (s.models.length >= MAX_MODELS) return s
      // Preselect the first allowed model not already in a column. A new
      // column has no reply for the earlier turns: they render as
      // "Not run — model added after this message."
      const next = (models.data ?? []).find((m) => !s.models.includes(m.name))?.name ?? ''
      return { ...s, models: [...s.models, next], turns: s.turns.map((t) => ({ ...t, replies: [...t.replies, null] })) }
    })
  const removeSlot = (i: number) =>
    setSession((s) => ({
      ...s,
      models: s.models.filter((_, j) => j !== i),
      turns: s.turns.map((t) => ({ ...t, replies: t.replies.filter((_, j) => j !== i) })),
    }))

  const budget: GateBudget | null = gate.data?.budget ?? null
  const { pct, level } = budgetState(budget)
  const budgetText = budget ? budgetBlockCopy(budget) : 'Daily budget reached.'

  const runColumn = async (seq: number, s: SessionData, slot: number, req: object, signal: AbortSignal) => {
    const model = s.models[slot]
    const info = models.data?.find((m) => m.name === model)
    const t0 = performance.now()
    let ttft = 0
    let text = ''
    // Written from onEvent (a closure): an object keeps them readable after
    // the stream without fighting the narrowing on closure-assigned lets.
    const st = {
      usage: null as NonNullable<Chunk['usage']> | null,
      err: null as { message: string; code?: string } | null,
      id: '',
      finishReason: '',
    }
    const onEvent = (d: unknown) => {
      const c = d as Chunk
      if (c.id) st.id = c.id
      const fr = c.choices?.[0]?.finish_reason
      if (fr) st.finishReason = fr
      if (c.error) {
        st.err = { message: c.error.message ?? 'error', code: c.error.code }
        return
      }
      const delta = c.choices?.[0]?.delta?.content
      if (delta) {
        if (!ttft) ttft = performance.now() - t0 // TTFT = first content chunk
        text += delta
        setLive((l) => ({ ...l, [slot]: { text, request: req } }))
      }
      const msg = c.choices?.[0]?.message?.content
      if (typeof msg === 'string') {
        if (!ttft) ttft = performance.now() - t0
        text = msg
        setLive((l) => ({ ...l, [slot]: { text, request: req } }))
      }
      if (c.usage) st.usage = c.usage
    }
    let fail: { message: string; code?: string; status?: number } | null = null
    let costHeader: string | null = null
    try {
      const { headers } = await apiStream(CHAT, req, onEvent, signal)
      costHeader = headers.get('X-Tollgate-Cost')
      if (st.err) fail = { message: st.err.message, code: st.err.code }
    } catch (err) {
      if (err instanceof ApiError) fail = { message: err.message, code: err.code, status: err.status }
      else fail = { message: (err as Error).message }
    }
    const ms = Math.round(performance.now() - t0)
    if (--pendingRef.current === 0) {
      // Every column of this send finished: unlock, then let the gate bar and
      // the budget banners below pick up the new spend.
      setRunning(false)
      setLive({})
      window.dispatchEvent(new Event('tollgate:gate-refresh'))
      void gate.reload()
    }
    if (seq !== seqRef.current || signal.aborted) return // cleared/switched/unmounted: stale
    if (fail?.status === 401) return // signed out mid-run: the redirect owns the page
    const u = fail ? null : usageReply({ stream: s.stream, usage: st.usage, costHeader, model: info ?? null })
    const reply: Reply = {
      model,
      text: fail ? '' : text,
      tin: u?.tin ?? null,
      tout: u?.tout ?? null,
      costNanos: u?.costNanos ?? null,
      ms,
      ttft: Math.round(ttft),
      error: fail ? columnErrorText(fail, budgetText) ?? undefined : undefined,
      request: req,
      ...(fail ? {} : { id: st.id || undefined, finishReason: st.finishReason || 'stop' }),
    }
    setSession((cur) => {
      const turns = cur.turns
      const li = turns.length - 1
      const last = turns[li]
      if (li < 0 || !last || last.replies[slot] !== null) return cur
      const replies = [...last.replies]
      replies[slot] = reply
      return { ...cur, turns: turns.map((t, i) => (i === li ? { ...t, replies } : t)) }
    })
  }

  const send = () => {
    const text = prompt.trim()
    const slots = session.models
    if (!text || running || !slots.length || level === 'block') return
    const turn: Turn = { user: text, replies: slots.map(() => null) }
    const next: SessionData = { ...session, turns: [...session.turns, turn] }
    const seq = ++seqRef.current
    // The exact body each column will send, kept on the reply and shown in
    // the Raw view (and used by Copy as cURL).
    const reqs = slots.map((_, i) => buildRequest(next, i))
    setSession(next)
    setPrompt('')
    setRunning(true)
    // Every column is live from t=0 (empty text until the first chunk), so a
    // slow TTFT never flashes the "not run" note.
    setLive(Object.fromEntries(slots.map((_, i) => [i, { text: '', request: reqs[i]! }])))
    pendingRef.current = slots.length
    // One request per column, in parallel, each with its own AbortController.
    slots.forEach((_, i) => {
      const ctrl = new AbortController()
      controllersRef.current.set(i, ctrl)
      void runColumn(seq, next, i, reqs[i]!, ctrl.signal)
    })
  }

  const pickSession = async (id: string) => {
    if (!id || id === sessionId) return
    const n = ++pickSeqRef.current
    seqRef.current += 1
    abortAll()
    setLoadError('')
    setSessionId(id) // the select shows the pick immediately, not after the GET
    try {
      const row = await api<SessionRow>(`${SESSIONS}/${id}`)
      if (n !== pickSeqRef.current) return // a newer pick (or New) superseded this GET
      const data = normalize(row.data)
      setSession(data)
      setName(row.name)
      setSessionId(row.id)
      setSaved({ name: row.name, data })
      setLive({})
      setBannerGone(null)
    } catch (e) {
      if (n === pickSeqRef.current) setLoadError((e as Error).message)
    }
  }

  const newSession = () => {
    pickSeqRef.current += 1
    seqRef.current += 1
    abortAll()
    // Start on the first usable model when some are known; the preselect
    // effect picks one up later if the list still hasn't loaded.
    const first = models.data?.[0]?.name
    setSession(first ? { ...emptySession(), models: [first] } : emptySession())
    setName('')
    setSessionId(null)
    setSaved(null)
    setLive({})
    setBannerGone(null)
    setSaveError('')
    setLoadError('')
  }

  const clear = () => {
    seqRef.current += 1
    abortAll()
    setLive({})
    setSession((s) => ({ ...s, turns: [] }))
  }

  const save = async () => {
    const n = name.trim()
    if (!n || saving) return
    const data = session
    setSaving(true)
    setSaveError('')
    try {
      const row = sessionId
        ? await api<SessionRow>(`${SESSIONS}/${sessionId}`, { method: 'PUT', body: { name: n, data } })
        : await api<SessionRow>(SESSIONS, { method: 'POST', body: { name: n, data } })
      setSessionId(row.id)
      setName(row.name)
      setSaved({ name: row.name, data })
      void list.reload()
    } catch (e) {
      setSaveError((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  const modelById = new Map((models.data ?? []).map((m) => [m.name, m]))
  const colTotal = (slot: number) => session.turns.reduce((sum, t) => sum + (t.replies[slot]?.costNanos ?? 0), 0)
  const usable = models.data ?? []
  const usableNote = usable.length ? (can('models:write') ? `${usable.length} enabled models.` : `${usable.length} models you can use.`) : ''
  const canSend = prompt.trim() !== '' && session.models.length > 0 && !running && level !== 'block'
  const placeholder =
    level === 'block'
      ? budget?.type === 'daily'
        ? 'Daily budget reached'
        : 'Budget reached'
      : `Message ${session.models.length === 1 ? session.models[0] : `${session.models.length} models`}… ⌘↵ to send`

  const columnHeader = (slot: number) => (
    <div className="flex items-center justify-between gap-2 border-b border-border px-3 py-2">
      <div className="min-w-0">
        <div className="truncate text-[13px] font-medium">{session.models[slot]}</div>
        <div className="font-mono text-[11px] text-muted-foreground">{modelById.get(session.models[slot])?.provider ?? '—'}</div>
      </div>
      <div className="shrink-0 font-mono text-[11px] tabular-nums text-muted-foreground" title="Total for this column">
        {usd(colTotal(slot))}
      </div>
    </div>
  )

  const chatTurn = (slot: number, t: Turn, i: number) => {
    const r = t.replies[slot] ?? null
    const lv = live[slot]
    // Live text belongs to the in-flight (last) turn only: a column added
    // later must read "Not run" for the earlier turns, never the new reply.
    const streaming = !r && lv !== undefined && i === session.turns.length - 1
    return (
      <div key={i}>
        <div className="mb-1.5 flex justify-end">
          <span className="max-w-[90%] whitespace-pre-wrap break-words rounded-md bg-secondary px-2.5 py-1.5 text-[13px] text-secondary-foreground">
            You {t.user}
          </span>
        </div>
        {streaming ? (
          <div className="whitespace-pre-wrap break-words text-[13px]">
            {lv.text}
            <span aria-hidden className="ml-0.5 inline-block h-3.5 w-[2px] animate-pulse bg-foreground align-[-2px]" />
          </div>
        ) : !r ? (
          <p className="text-[13px] text-muted-foreground">Not run — model added after this message.</p>
        ) : r.error ? (
          <ErrorText>{r.error}</ErrorText>
        ) : (
          <div>
            <div className="whitespace-pre-wrap break-words text-[13px]">{r.text || <span className="text-muted-foreground">(empty response)</span>}</div>
            <div className="mt-1.5 font-mono text-[11px] text-muted-foreground">{replyMeta(r)}</div>
          </div>
        )}
      </div>
    )
  }

  return (
    <>
      <PageHeader
        title="Playground"
        description="Try the models you're allowed to use — no API key needed. Runs as you and counts toward your budget."
      />
      <div className="mb-4">
        <ErrorText>{models.error || list.error || gate.error || loadError}</ErrorText>
      </div>
      <div className="grid items-start gap-4 md:grid-cols-[272px_minmax(0,1fr)]">
        <aside className="flex flex-col gap-5 rounded-lg border border-border bg-card p-4">
          <section>
            <h2 className={labelCls}>Session</h2>
            <select
              aria-label="Saved sessions"
              className={inputCls + ' bg-background'}
              value={sessionId ?? ''}
              onChange={(e) => {
                if (e.target.value === '') {
                  if (sessionId !== null) newSession()
                  return
                }
                void pickSession(e.target.value)
              }}
              disabled={running}
            >
              <option value="">New session</option>
              {(list.data ?? []).map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
            </select>
            <input className={inputCls} aria-label="Session name" placeholder="Untitled" maxLength={120} value={name} onChange={(e) => setName(e.target.value)} />
            <div className="mt-2 flex items-center gap-2">
              {/* Disabled while running: saving mid-run would persist the
                  in-flight turn's null replies as "Not run". */}
              <Button size="sm" onClick={() => void save()} disabled={saving || running || !dirty || !name.trim()}>
                {dirty ? 'Save' : 'Saved'}
              </Button>
              <Button size="sm" variant="ghost" onClick={newSession} disabled={running}>
                New
              </Button>
              <span className="text-xs text-muted-foreground">Private to you.</span>
            </div>
            <ErrorText>{saveError}</ErrorText>
          </section>
          <section className="border-t border-border pt-4">
            <h2 className={labelCls}>
              Models {session.models.length} / {MAX_MODELS}
            </h2>
            {session.models.map((m, i) => (
              <div key={i} className="mt-1.5 flex items-center gap-1">
                <select
                  aria-label={`Model ${i + 1}`}
                  className={controlCls + ' min-h-8 w-full bg-background px-2 py-1 text-[13px]'}
                  value={m}
                  onChange={(e) => setSlot(i, e.target.value)}
                  disabled={running}
                >
                  {!modelById.has(m) && (
                    <option value={m} disabled>
                      {m}
                    </option>
                  )}
                  {(models.data ?? []).map((x) => (
                    <option key={x.name} value={x.name}>
                      {x.name} · {x.provider}
                    </option>
                  ))}
                </select>
                {session.models.length > 1 && (
                  <button
                    type="button"
                    aria-label={`Remove ${m}`}
                    disabled={running}
                    onClick={() => removeSlot(i)}
                    className="shrink-0 px-1 text-lg leading-none text-muted-foreground hover:opacity-70 disabled:opacity-30"
                  >
                    ×
                  </button>
                )}
              </div>
            ))}
            {session.models.length < MAX_MODELS && (
              <Button type="button" size="sm" variant="ghost" className="mt-2 w-full" onClick={addSlot} disabled={running || !models.data?.length}>
                + Compare another model
              </Button>
            )}
            {usableNote && <p className="mt-1.5 text-xs text-muted-foreground">{usableNote}</p>}
          </section>
          <section className="border-t border-border pt-4">
            <h2 className={labelCls}>System prompt</h2>
            <textarea
              rows={3}
              aria-label="System prompt"
              className={inputCls + ' resize-y'}
              value={session.system}
              onChange={(e) => patch({ system: e.target.value })}
            />
          </section>
          <section className="border-t border-border pt-4">
            <h2 className={labelCls}>Parameters</h2>
            <div className="mt-2 space-y-3">
              <label className="block text-sm">
                <span className="flex items-center justify-between">
                  <span className="font-medium">Temperature</span>
                  <span className="font-mono text-xs text-muted-foreground">{session.temperature.toFixed(1)}</span>
                </span>
                <input
                  type="range"
                  min={0}
                  max={2}
                  step={0.1}
                  aria-label="Temperature"
                  value={session.temperature}
                  onChange={(e) => patch({ temperature: Number(e.target.value) })}
                  className="mt-1 w-full accent-primary"
                />
              </label>
              <label className="block text-sm">
                <span className="flex items-center justify-between">
                  <span className="font-medium">Top P</span>
                  <span className="font-mono text-xs text-muted-foreground">{session.top_p.toFixed(2)}</span>
                </span>
                <input
                  type="range"
                  min={0}
                  max={1}
                  step={0.05}
                  aria-label="Top P"
                  value={session.top_p}
                  onChange={(e) => patch({ top_p: Number(e.target.value) })}
                  className="mt-1 w-full accent-primary"
                />
              </label>
              <label className="block text-sm">
                <span className="font-medium">Max tokens</span>
                <MaxTokensInput value={session.max_tokens} onCommit={(n) => patch({ max_tokens: n })} />
              </label>
              <div className="flex items-center justify-between text-sm">
                <span className="font-medium">Stream</span>
                <CheckButton on={session.stream} aria-label="Stream" onClick={() => patch({ stream: !session.stream })} />
              </div>
            </div>
          </section>
        </aside>
        <section className="flex min-w-0 flex-col rounded-lg border border-border bg-card">
          <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border p-4">
            <Segmented label="View" value={view} onChange={setView} options={[{ value: 'chat', label: 'Chat' }, { value: 'raw', label: 'Raw' }]} />
            <span className="text-xs text-muted-foreground">Runs as you · no API key</span>
            <Button variant="ghost" size="sm" onClick={clear} disabled={running || session.turns.length === 0}>
              Clear
            </Button>
          </div>
          <div className="min-h-[280px] flex-1 p-4">
            {view === 'chat' && session.turns.length === 0 ? (
              models.data === null || session.models.length === 0 ? (
                // Models still loading, or "New" waiting for the preselect
                // effect: a skeleton, never "no models available".
                <div className="flex flex-col gap-3 py-12">
                  {[0, 1, 2].map((i) => (
                    <Skeleton key={i} className="h-4 w-2/3 self-center" />
                  ))}
                </div>
              ) : usable.length === 0 ? (
                <p className="py-12 text-center text-sm text-muted-foreground">No models available — ask an admin to enable one.</p>
              ) : (
                <div className="flex flex-col items-center gap-1 py-12 text-center">
                  <p className="text-sm">
                    Send a prompt to run it on {session.models.length === 1 ? '1 model' : `${session.models.length} models at once`}.
                  </p>
                  <p className="text-[13px] text-muted-foreground">Each column streams on its own. Cost and latency appear under every reply.</p>
                </div>
              )
            ) : (
              <div className="grid gap-3" style={{ gridTemplateColumns: `repeat(${session.models.length || 1}, minmax(0, 1fr))` }}>
                {session.models.map((_, slot) => (
                  <div key={slot} className="flex min-w-0 flex-col rounded-md border border-border">
                    {columnHeader(slot)}
                    {view === 'chat' ? (
                      <div className="flex flex-col gap-4 p-3">{session.turns.map((t, i) => chatTurn(slot, t, i))}</div>
                    ) : (
                      (() => {
                        const last = session.turns[session.turns.length - 1]
                        const r = last?.replies[slot] ?? null
                        const lv = live[slot]
                        const inflight = !!last && !r && lv !== undefined
                        // The exact body that was sent (or the one that would
                        // go out for a new turn); Copy as cURL uses it in full.
                        const req: object = inflight ? lv.request : (r?.request ?? buildRequest(session, slot))
                        return (
                          <div className="flex flex-col gap-4 p-3">
                            <div>
                              <h3 className={labelCls}>Request</h3>
                              <pre className="mt-1.5 overflow-x-auto rounded-md bg-muted p-2.5 font-mono text-[11px] leading-relaxed">
                                {`// POST /playground/chat/completions\n${JSON.stringify(req, null, 2)}`}
                              </pre>
                            </div>
                            <div>
                              <h3 className={labelCls}>Response</h3>
                              <pre className="mt-1.5 max-h-64 overflow-auto whitespace-pre-wrap break-words rounded-md bg-muted p-2.5 font-mono text-[11px] leading-relaxed">
                                {rawResponseBody(r, inflight)}
                              </pre>
                            </div>
                            <div className="flex justify-end">
                              <CopyButton value={curlFor(req)} label="Copy as cURL" size="row" />
                            </div>
                          </div>
                        )
                      })()
                    )}
                  </div>
                ))}
              </div>
            )}
          </div>
          <div className="border-t border-border p-4">
            {level !== 'ok' && budget && bannerGone !== level && (
              <div className="mb-2">
                <Banner onDismiss={() => setBannerGone(level)}>{level === 'warn' ? budgetWarnCopy(budget, pct) : budgetBlockCopy(budget)}</Banner>
              </div>
            )}
            <div className="flex items-end gap-2">
              <textarea
                rows={2}
                aria-label="Message"
                className={controlCls + ' w-full resize-y text-sm'}
                placeholder={placeholder}
                value={prompt}
                onChange={(e) => setPrompt(e.target.value)}
                onKeyDown={(e) => {
                  if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
                    e.preventDefault()
                    send()
                  }
                }}
              />
              <Button onClick={send} disabled={!canSend}>
                {running ? 'Running…' : 'Send'}
              </Button>
            </div>
          </div>
        </section>
      </div>
    </>
  )
}
