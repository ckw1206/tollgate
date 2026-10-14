import { useEffect, useMemo, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { CartesianGrid, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import { dash, usd, when } from '../api'
import { useAuth } from '../auth'
import GetStarted from '../components/GetStarted'
import { Badge, Banner, controlCls, ErrorText, PageHeader, Segmented, Skeleton, Table, useLoad } from '../components/ui'
import type { ApiKey, SpendLog, SpendRow } from '../types'

type Spend = { rows: SpendRow[] }
type Metric = 'tokens' | 'requests' | 'cost'

const DAY = 86_400_000
const fmt = new Intl.NumberFormat()
const RANGES = [
  { value: 'today', label: 'Today' },
  { value: '7d', label: 'Last 7 days' },
  { value: '30d', label: 'Last 30 days' },
  { value: '90d', label: 'Last 90 days' },
  { value: 'custom', label: 'Custom…' },
] as const
type Range = (typeof RANGES)[number]['value']
const METRICS: { value: Metric; label: string }[] = [{ value: 'tokens', label: 'Tokens' }, { value: 'requests', label: 'Requests' }, { value: 'cost', label: 'Cost' }]
const SHOW = [10, 25, 50, 100]
const SHOW_KEY = 'tollgate.recent'
const tooltipStyle = { background: 'var(--background)', border: '1px solid var(--border)', color: 'var(--foreground)' }

const metricOf = (r: SpendRow, m: Metric) => (m === 'cost' ? r.cost_nanos : m === 'requests' ? r.requests : r.input_tokens + r.output_tokens)
const showMetric = (v: number, m: Metric) => (m === 'cost' ? usd(v) : fmt.format(v))
const pct = (v: number, total: number) => `${total ? ((v / total) * 100).toFixed(1) : '0.0'}%`

const storedShow = () => {
  try {
    const n = Number(localStorage.getItem(SHOW_KEY))
    return SHOW.includes(n) ? n : 10
  } catch {
    return 10
  }
}

// Calendar days are YYYY-MM-DD in the server timezone, the same buckets as /spend?group_by=day.
const isDay = (s: string | null): s is string => !!s && /^\d{4}-\d{2}-\d{2}$/.test(s)
const dayIn = (t: number, tz: string) => new Intl.DateTimeFormat('en-CA', { timeZone: tz, year: 'numeric', month: '2-digit', day: '2-digit' }).format(t)
const nextDay = (d: string) => new Date(Date.parse(d) + DAY).toISOString().slice(0, 10)
const fmtDay = (d: string) => new Date(Date.parse(d)).toLocaleDateString(undefined, { month: 'short', day: 'numeric', timeZone: 'UTC' })

// midnight: the instant 00:00 starts on day d in tz. The zone's offset is measured at the
// first guess and again at the result, so a DST change on that day still lands on 00:00.
function midnight(d: string, tz: string) {
  const parts = new Intl.DateTimeFormat('en-US', { timeZone: tz, hourCycle: 'h23', year: 'numeric', month: 'numeric', day: 'numeric', hour: 'numeric', minute: 'numeric', second: 'numeric' })
  const offset = (t: number) => {
    const p = Object.fromEntries(parts.formatToParts(t).map((x) => [x.type, x.value]))
    return Date.UTC(+p.year, +p.month - 1, +p.day, +p.hour, +p.minute, +p.second) - Math.floor(t / 1000) * 1000
  }
  const utc = Date.parse(d)
  return utc - offset(utc - offset(utc))
}

// span: what ?range= (and, for custom, ?from=&to=) means right now. Today starts at midnight in the
// server timezone; N days is a rolling N×24h; custom runs from 00:00 on `from` to the end of `to`.
function spanOf(range: Range, pFrom: string | null, pTo: string | null, tz: string) {
  const now = Math.floor(Date.now() / 60_000) * 60_000 + 60_000 // the next whole minute: a stable URL
  const n = parseInt(range)
  let from = now - n * DAY
  let to = now
  let label = `Last ${n} days`
  let phrase = `in the last ${n} days`
  if (range === 'today') {
    from = midnight(dayIn(now, tz), tz)
    label = 'Today'
    phrase = 'today'
  } else if (range === 'custom') {
    let a = isDay(pFrom) ? pFrom : dayIn(now - 30 * DAY, tz)
    let b = isDay(pTo) ? pTo : dayIn(now, tz)
    if (a > b) [a, b] = [b, a]
    from = midnight(a, tz)
    to = midnight(nextDay(b), tz)
    label = a === b ? fmtDay(a) : `${fmtDay(a)} – ${fmtDay(b)}`
    phrase = a === b ? `on ${fmtDay(a)}` : `between ${fmtDay(a)} and ${fmtDay(b)}`
  }
  const days: string[] = []
  for (let d = dayIn(from, tz), last = dayIn(to - 1, tz); d <= last; d = nextDay(d)) days.push(d)
  const q = new URLSearchParams({ from: new Date(from).toISOString(), to: new Date(to).toISOString() }).toString()
  return { q, days, label, phrase }
}

function Stat({ label, value }: { label: string; value: string | null }) {
  return (
    <div className="rounded-lg border border-border bg-card p-4">
      <div className="text-xs uppercase tracking-[0.04em] text-muted-foreground">{label}</div>
      <div className="mt-1 text-2xl font-semibold tabular-nums">{value ?? <Skeleton className="mt-2 h-6 w-24" />}</div>
    </div>
  )
}

function Panel({ title, actions, children }: { title: string; actions?: React.ReactNode; children: React.ReactNode }) {
  return (
    <section className="min-w-0 rounded-lg border border-border bg-card p-4">
      <div className={`${actions ? 'mb-3' : 'mb-2'} flex flex-wrap items-center justify-between gap-3`}>
        <h2 className="text-sm font-medium text-muted-foreground">{title}</h2>
        {actions}
      </div>
      {children}
    </section>
  )
}

// TopModels: Bars = up to 8 models from group_by=model, bar = share of the leader, % = share of
// the total. Trend = the top 5 as daily lines from group_by=model,day, fetched only when shown.
function TopModels({ rows, emptyText, q, days }: { rows: SpendRow[] | null; emptyText: string; q: string; days: string[] }) {
  const [view, setView] = useState<'bars' | 'trend'>('bars')
  const [metric, setMetric] = useState<Metric>('cost')
  const trend = useLoad<Spend>(view === 'trend' ? `/console/api/v1/spend?group_by=model,day&${q}` : null)
  // Free models that served requests stay listed (at $0.00) under Cost.
  const sorted = useMemo(() => (rows ?? []).filter((r) => r.requests > 0).sort((a, b) => metricOf(b, metric) - metricOf(a, metric)), [rows, metric])
  const total = sorted.reduce((s, r) => s + metricOf(r, metric), 0)
  const leader = sorted.length ? metricOf(sorted[0]!, metric) : 0
  return (
    <Panel
      title="Top models"
      actions={
        <div className="flex flex-wrap gap-2">
          <Segmented label="View" value={view} onChange={setView} options={[{ value: 'bars', label: 'Bars' }, { value: 'trend', label: 'Trend' }]} />
          <Segmented label="Rank by" value={metric} onChange={setMetric} options={METRICS} />
        </div>
      }
    >
      {rows === null ? (
        <div className="space-y-3">{[0, 1, 2].map((i) => <Skeleton key={i} className="h-4 w-full" />)}</div>
      ) : sorted.length === 0 ? (
        <p className="py-6 text-center text-sm text-muted-foreground">{emptyText}</p>
      ) : view === 'trend' ? (
        <>
          <ErrorText>{trend.error}</ErrorText>
          <Trend top={sorted.slice(0, 5)} total={total} metric={metric} rows={trend.data?.rows ?? (trend.error ? [] : null)} days={days} />
        </>
      ) : (
        <ol className="flex flex-col">
          {sorted.slice(0, 8).map((r, i) => {
            const v = metricOf(r, metric)
            return (
              <li key={r.bucket} className="grid grid-cols-[20px_minmax(0,1fr)_96px] items-center gap-2.5 border-b border-border py-[7px]">
                <code className="font-mono text-xs text-muted-foreground">{i + 1}</code>
                <div className="flex min-w-0 flex-col gap-[5px]">
                  <div className="flex justify-between gap-2 text-[13px]">
                    <span className="truncate font-medium">{r.bucket}</span>
                    <span className="whitespace-nowrap text-xs text-muted-foreground">{pct(v, total)}</span>
                  </div>
                  <div className="h-1 bg-muted">
                    <div className="h-1 bg-primary" style={{ width: `${leader ? Math.max((v / leader) * 100, v ? 2 : 0) : 0}%` }} />
                  </div>
                </div>
                <span className="text-right text-[13px] tabular-nums">{showMetric(v, metric)}</span>
              </li>
            )
          })}
        </ol>
      )}
      <p className="mt-3 text-xs text-muted-foreground">
        {view === 'trend' ? `Top 5 · daily ${metric} · legend = range total` : 'bar = share of the leader · % = share of total'}
      </p>
    </Panel>
  )
}

type Point = { day: string; v: number[] }

// Trend: one line per model over every day of the range (missing days are 0), the highest
// single-day value top-left, 4 date ticks, then a legend row per model with its range total.
function Trend({ top, total, metric, rows, days }: { top: SpendRow[]; total: number; metric: Metric; rows: SpendRow[] | null; days: string[] }) {
  if (rows === null) return <Skeleton className="h-[150px] w-full" />
  const at = new Map(rows.map((r) => [`${r.bucket}\n${r.day}`, metricOf(r, metric)]))
  const data: Point[] = days.map((d) => ({ day: d, v: top.map((m) => at.get(`${m.bucket}\n${d}`) ?? 0) }))
  const max = Math.max(0, ...data.flatMap((p) => p.v))
  const ceil = (max || 1) / 0.96 // the busiest day peaks just under the top gridline
  const ticks = [...new Set([0, 1 / 3, 2 / 3, 1].map((f) => days[Math.round(f * (days.length - 1))]!))]
  return (
    <div className="flex flex-col gap-1.5">
      <div className="relative h-[150px] border-b border-border">
        <ResponsiveContainer width="100%" height="100%">
          <LineChart data={data} margin={{ top: 0, right: 0, bottom: 0, left: 0 }}>
            <CartesianGrid vertical={false} stroke="var(--border)" strokeDasharray="3 3" />
            <XAxis dataKey="day" hide />
            <YAxis hide domain={[0, ceil]} ticks={[0, 1, 2, 3].map((i) => (ceil * i) / 3)} />
            <Tooltip formatter={(v: number) => showMetric(v, metric)} labelFormatter={fmtDay} contentStyle={tooltipStyle} />
            {top.map((m, i) => (
              <Line
                key={m.bucket}
                type="monotone"
                name={m.bucket}
                dataKey={(p: Point) => p.v[i]}
                stroke={`var(--chart-${i + 1})`}
                strokeWidth={2}
                dot={days.length === 1 ? { r: 3, fill: `var(--chart-${i + 1})` } : false}
              />
            ))}
          </LineChart>
        </ResponsiveContainer>
        <span className="absolute -top-0.5 left-0 bg-card pr-1 text-[11px] text-muted-foreground">{showMetric(max, metric)} / day</span>
      </div>
      <div className="flex justify-between text-[11px] text-muted-foreground">
        {ticks.map((d) => <span key={d}>{fmtDay(d)}</span>)}
      </div>
      <div className="flex flex-col">
        {top.map((m, i) => {
          const v = metricOf(m, metric)
          return (
            <div key={m.bucket} className="grid grid-cols-[10px_minmax(0,1fr)_auto_96px] items-center gap-2.5 border-b border-border py-[5px] text-[13px]">
              <span className="h-2.5 w-2.5" style={{ background: `var(--chart-${i + 1})` }} />
              <span className="truncate font-medium">{m.bucket}</span>
              <span className="text-xs text-muted-foreground">{pct(v, total)}</span>
              <span className="text-right tabular-nums">{showMetric(v, metric)}</span>
            </div>
          )
        })}
      </div>
    </div>
  )
}

export default function Dashboard() {
  const { me, can } = useAuth()
  const [params, setParams] = useSearchParams()
  // ?welcome=1 comes from first-run setup: show the banner once, then drop the flag.
  const [welcome, setWelcome] = useState(() => params.get('welcome') === '1')
  useEffect(() => {
    if (params.has('welcome')) {
      const p = new URLSearchParams(params)
      p.delete('welcome')
      setParams(p, { replace: true })
    }
  }, [params, setParams])

  // The range lives in the URL (?range=30d; custom adds from/to days) and drives every panel.
  const tz = me?.server.timezone || 'UTC'
  const rawRange = params.get('range')
  const range: Range = RANGES.find((r) => r.value === rawRange)?.value ?? '30d'
  const pFrom = params.get('from')
  const pTo = params.get('to')
  const span = useMemo(() => spanOf(range, pFrom, pTo, tz), [range, pFrom, pTo, tz])
  const setRange = (patch: Record<string, string | null>) => {
    const p = new URLSearchParams(params)
    for (const [k, v] of Object.entries(patch)) {
      if (v === null) p.delete(k)
      else p.set(k, v)
    }
    setParams(p, { replace: true })
  }
  const pickRange = (r: Range) =>
    setRange(r === 'custom' ? { range: r, from: span.days[0]!, to: span.days[span.days.length - 1]! } : { range: r, from: null, to: null })

  const [show, setShow] = useState(storedShow)
  const daily = useLoad<Spend>(`/console/api/v1/spend?group_by=day&${span.q}`)
  const byModel = useLoad<Spend>(`/console/api/v1/spend?group_by=model&${span.q}`)
  const recent = useLoad<SpendLog[]>(`/console/api/v1/spend/requests?${span.q}&limit=${show}`)

  const rows = daily.data?.rows ?? null
  const sum = (f: (r: SpendRow) => number) => (rows ? rows.reduce((s, r) => s + f(r), 0) : null)
  const spend = sum((r) => r.cost_nanos)
  const requests = sum((r) => r.requests)
  const tokens = sum((r) => r.input_tokens + r.output_tokens)
  const byDay = new Map((rows ?? []).map((r) => [r.bucket, r.cost_nanos]))
  const chart = span.days.map((d) => ({ day: d, usd: (byDay.get(d) ?? 0) / 1e9 }))

  // Empty dashboard without the checklist (members and viewers): a next step, never a dead end.
  // "Yet" copy only when nothing has ever been recorded; a quiet range says "in this period".
  const ever = useLoad<Spend>(rows !== null && rows.length === 0 ? '/console/api/v1/spend?group_by=model&from=1970-01-01T00%3A00%3A00Z' : null)
  const neverUsed = rows !== null && rows.length === 0 && ever.data !== null && ever.data.rows.length === 0
  // Members and viewers without a key get a next step, never a dead end.
  const myKeys = useLoad<ApiKey[]>(can('models:write') ? null : '/console/api/v1/keys')
  const nothingYet = myKeys.data !== null && myKeys.data.length === 0
  const recentRows = recent.data ?? (recent.error ? [] : null)
  return (
    <>
      {welcome && me && (
        <div className="mb-6">
          <Banner onDismiss={() => setWelcome(false)}>
            <b className="font-semibold">Admin account created.</b> You're signed in as {me.user.email}. The setup token is now invalid.
          </Banner>
        </div>
      )}
      <PageHeader title={can('spend:read_all') ? 'Usage — everyone' : 'Your usage'}>
        {range === 'custom' && (
          <>
            <input type="date" aria-label="From" className={controlCls + ' h-9 text-sm'} value={span.days[0]} max={span.days[span.days.length - 1]} onChange={(e) => isDay(e.target.value) && setRange({ from: e.target.value })} />
            <input type="date" aria-label="To" className={controlCls + ' h-9 text-sm'} value={span.days[span.days.length - 1]} min={span.days[0]} onChange={(e) => isDay(e.target.value) && setRange({ to: e.target.value })} />
          </>
        )}
        <select aria-label="Range" className={controlCls + ' h-9 text-sm'} value={range} onChange={(e) => pickRange(e.target.value as Range)}>
          {RANGES.map((r) => <option key={r.value} value={r.value}>{r.label}</option>)}
        </select>
      </PageHeader>
      {can('models:write') && <GetStarted />}
      {nothingYet && (
        <p className="-mt-2 mb-4 text-sm text-muted-foreground">
          {can('keys:write_own') ? (
            <>
              Create a key on <Link to="/keys" className="text-link-foreground underline underline-offset-2 hover:text-foreground">API keys</Link> to make your first request.
            </>
          ) : (
            'Ask an admin to create an API key for you.'
          )}
        </p>
      )}
      <div className="mb-4">
        <ErrorText>{daily.error || byModel.error || recent.error}</ErrorText>
      </div>
      <div className="mb-6 grid grid-cols-1 gap-4 sm:grid-cols-3">
        <Stat label="Spend" value={spend == null ? null : usd(spend)} />
        <Stat label="Requests" value={requests == null ? null : fmt.format(requests)} />
        <Stat label="Tokens" value={tokens == null ? null : fmt.format(tokens)} />
      </div>
      <div className="mb-6 grid grid-cols-1 gap-4 lg:grid-cols-2">
        <Panel title="Spend per day (USD)">
          <div className="h-[240px]">
            {rows === null ? (
              <Skeleton className="h-full w-full" />
            ) : rows.length === 0 ? (
              <p className="flex h-full items-center justify-center text-sm text-muted-foreground">{neverUsed ? 'No requests yet.' : 'No requests in this period.'}</p>
            ) : (
              <ResponsiveContainer width="100%" height="100%">
                <LineChart data={chart} margin={{ top: 8, right: 8, bottom: 0, left: 0 }}>
                  <CartesianGrid stroke="var(--border)" strokeDasharray="3 3" />
                  <XAxis dataKey="day" tick={{ fill: 'var(--muted-foreground)', fontSize: 12 }} stroke="var(--border)" />
                  <YAxis tick={{ fill: 'var(--muted-foreground)', fontSize: 12 }} stroke="var(--border)" width={56} />
                  <Tooltip formatter={(v: number) => usd(Math.round(v * 1e9))} contentStyle={tooltipStyle} />
                  {/* A one-day range (Today) is a single point: give it a dot so it shows. */}
                  <Line type="monotone" dataKey="usd" stroke="var(--primary)" strokeWidth={2} dot={chart.length === 1 ? { r: 3, fill: 'var(--primary)' } : false} />
                </LineChart>
              </ResponsiveContainer>
            )}
          </div>
        </Panel>
        <TopModels rows={byModel.data?.rows ?? (byModel.error ? [] : null)} emptyText={neverUsed ? 'No models used yet.' : 'No requests in this period.'} q={span.q} days={span.days} />
      </div>
      <div className="mb-2 flex flex-wrap items-center justify-between gap-3">
        <h2 className="text-sm font-medium text-muted-foreground">
          Recent requests <span className="font-normal">· {span.label}</span>
        </h2>
        <label className="flex items-center gap-2 text-[13px] text-muted-foreground">
          Show
          <select
            aria-label="Rows to show"
            className={controlCls + ' h-8 bg-background text-[13px]'}
            value={show}
            onChange={(e) => {
              const n = Number(e.target.value)
              setShow(n)
              try {
                localStorage.setItem(SHOW_KEY, String(n))
              } catch {
                // storage unavailable (private mode): the choice lasts for this visit
              }
            }}
          >
            {SHOW.map((n) => <option key={n} value={n}>{n}</option>)}
          </select>
        </label>
      </div>
      <Table
        rows={recentRows}
        empty="No requests in this period."
        cols={[
          { head: 'When', cell: (l) => when(l.created_at) },
          ...(can('spend:read_all') ? [{ head: 'Owner', cell: (l: SpendLog) => dash(l.owner_email) }] : []),
          {
            head: 'API key',
            cell: (l) => (
              <span className="flex flex-col leading-[1.25]">
                <span className="font-medium">{l.key_alias}</span>
                <code className="font-mono text-[11px] text-muted-foreground">{l.key_prefix}…</code>
              </span>
            ),
          },
          { head: 'Model', cell: (l) => l.model_name },
          // Input counts cached tokens too, matching the stat cards and the Top models board.
          { head: 'Tokens in / out', cell: (l) => `${fmt.format(l.input_tokens + l.cache_read_tokens + l.cache_write_tokens)} / ${fmt.format(l.output_tokens)}` },
          { head: 'Cost', cell: (l) => usd(l.cost_nanos) },
          { head: 'Latency', cell: (l) => `${fmt.format(l.latency_ms)} ms` },
          // A stream that fails after headers were sent is recorded as status "error" with http_status 200.
          { head: 'Status', cell: (l) => (l.status === 'success' ? <Badge tone="success">ok</Badge> : <Badge tone="error">{l.http_status && l.http_status !== 200 ? l.http_status : 'error'}</Badge>) },
        ]}
      />
      {!!recentRows?.length && requests !== null && (
        <p className="mt-2 text-xs text-muted-foreground">
          Latest {recentRows.length} of {fmt.format(requests)} requests {span.phrase} · newest first
        </p>
      )}
    </>
  )
}
