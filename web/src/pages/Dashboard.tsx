import { useEffect, useMemo, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { Area, AreaChart, CartesianGrid, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import { dash, fmtTokens, short, usd, when } from '../api'
import { useAuth } from '../auth'
import GetStarted from '../components/GetStarted'
import { Badge, Banner, controlCls, ErrorText, PageHeader, Segmented, Skeleton, Sparkline, Table, useLoad } from '../components/ui'
import { fmtBucket, isDay, PER_UNIT, PER_WORD, RANGES, spanOf } from '../range'
import type { Granularity, Range } from '../range'
import type { ApiKey, Group, SpendLog, SpendRow, User } from '../types'

type Spend = { rows: SpendRow[] }
type Metric = 'tokens' | 'requests' | 'cost'

const fmt = new Intl.NumberFormat()
const METRICS: { value: Metric; label: string }[] = [{ value: 'tokens', label: 'Tokens' }, { value: 'requests', label: 'Requests' }, { value: 'cost', label: 'Cost' }]
const SHOW = [10, 25, 50, 100]
const SHOW_KEY = 'tollgate.recent'
const tooltipStyle = { background: 'var(--background)', border: '1px solid var(--border)', color: 'var(--foreground)' }

const ZERO_ROW: SpendRow = { bucket: '', requests: 0, input_tokens: 0, output_tokens: 0, cost_nanos: 0 }

const metricOf = (r: SpendRow, m: Metric) => (m === 'cost' ? r.cost_nanos : m === 'requests' ? r.requests : r.input_tokens + r.output_tokens)
const showMetric = (v: number, m: Metric) => (m === 'cost' ? usd(v) : m === 'tokens' ? fmtTokens(v) : fmt.format(v))
const pct = (v: number, total: number) => `${total ? ((v / total) * 100).toFixed(1) : '0.0'}%`

const storedShow = () => {
  try {
    const n = Number(localStorage.getItem(SHOW_KEY))
    return SHOW.includes(n) ? n : 10
  } catch {
    return 10
  }
}

// isDay, day labels, and the span model (range → window + granularity + bucket labels) live in
// ../range.ts; the buckets must match the SQL labels in core/db/repo/spend.go exactly (tested).

// DayInput: a custom-range date field. Typing a year passes through 0002, 0020 and 0202, so the
// field keeps its own draft and only whole days from 2000 on reach the URL; blur drops a bad draft.
function DayInput({ label, value, min, max, onCommit }: { label: string; value: string; min?: string; max?: string; onCommit: (d: string) => void }) {
  const [draft, setDraft] = useState(value)
  useEffect(() => setDraft(value), [value])
  return (
    <input
      type="date"
      aria-label={label}
      className={controlCls + ' h-9 text-sm'}
      value={draft}
      min={min}
      max={max}
      onBlur={() => setDraft(value)}
      onChange={(e) => {
        setDraft(e.target.value)
        if (isDay(e.target.value)) onCommit(e.target.value)
      }}
    />
  )
}

// Stat: D2 signage label, the range value, and an optional 30-day sparkline
// (primary tone for spend, muted for requests/tokens).
function Stat({ label, value, spark, tone, title }: { label: string; value: string | null; spark?: number[] | null; tone?: 'primary' | 'muted'; title?: string }) {
  return (
    <div className="rounded-lg border border-border bg-card p-4">
      <div className="flex items-start justify-between gap-3">
        <div className="font-mono text-[11px] uppercase tracking-[0.06em] text-muted-foreground">{label}</div>
        {spark && spark.length > 1 && <Sparkline points={spark} tone={tone ?? 'muted'} />}
      </div>
      <div className="mt-1 text-2xl font-semibold tabular-nums" title={title}>{value ?? <Skeleton className="mt-2 h-6 w-24" />}</div>
    </div>
  )
}

function Panel({ title, actions, children }: { title: string; actions?: React.ReactNode; children: React.ReactNode }) {
  return (
    <section className="min-w-0 rounded-lg border border-border bg-card p-4">
      <div className={`${actions ? 'mb-3' : 'mb-2'} flex flex-wrap items-center justify-between gap-3`}>
        <h2 className="font-mono text-[11px] uppercase tracking-[0.06em] text-muted-foreground">{title}</h2>
        {actions}
      </div>
      {children}
    </section>
  )
}

// TopModels: Bars = up to 8 models from group_by=model, bar = share of the leader, % = share of
// the total. Trend = the top 5 as per-bucket lines from group_by=model,<granularity>, fetched
// only when shown. Scope (admins): one dropdown, Everyone or one user — user_id is appended to
// this panel's fetches only; the stat cards and the spend chart stay global.
function TopModels({ rows, emptyText, span, granularity, can, users }: { rows: SpendRow[] | null; emptyText: string; span: ReturnType<typeof spanOf>; granularity: Granularity; can: boolean; users: User[] | null }) {
  const [view, setView] = useState<'bars' | 'trend'>('bars')
  const [metric, setMetric] = useState<Metric>('cost')
  const [userId, setUserId] = useState('') // '' = everyone
  // A user removed from the org while picked: fall back to everyone.
  const scoped = userId !== '' && (users ?? []).some((u) => u.id === userId)
  const extra = scoped ? `&user_id=${userId}` : ''
  const trend = useLoad<Spend>(view === 'trend' ? `/console/api/v1/spend?group_by=${span.modelGroupBy}&${span.q}${extra}` : null)
  // Scoped, the bars (and the trend's top 5) rank that user's models, not the global rows.
  const own = useLoad<Spend>(scoped ? `/console/api/v1/spend?group_by=model&${span.q}${extra}` : null)
  const shown = scoped ? (own.data?.rows ?? (own.error ? [] : null)) : rows
  // Free models that served requests stay listed (at $0.00) under Cost.
  const sorted = useMemo(() => (shown ?? []).filter((r) => r.requests > 0).sort((a, b) => metricOf(b, metric) - metricOf(a, metric)), [shown, metric])
  const total = sorted.reduce((s, r) => s + metricOf(r, metric), 0)
  const leader = sorted.length ? metricOf(sorted[0]!, metric) : 0
  return (
    <Panel
      title="Top models"
      actions={
        <div className="flex flex-wrap gap-2">
          <Segmented label="View" value={view} onChange={setView} options={[{ value: 'bars', label: 'Bars' }, { value: 'trend', label: 'Trend' }]} />
          <Segmented label="Rank by" value={metric} onChange={setMetric} options={METRICS} />
          {/* One always-present, fixed-width control: picking a user never reflows the header. */}
          {can && (
            <select aria-label="Top models scope" className={controlCls + ' h-7 w-40 bg-background text-xs'} value={scoped ? userId : ''} onChange={(e) => setUserId(e.target.value)}>
              <option value="">Everyone</option>
              {(users ?? []).map((u) => (
                <option key={u.id} value={u.id}>{u.email}</option>
              ))}
            </select>
          )}
        </div>
      }
    >
      {scoped && <ErrorText>{own.error}</ErrorText>}
      {shown === null ? (
        <div className="space-y-3">{[0, 1, 2].map((i) => <Skeleton key={i} className="h-4 w-full" />)}</div>
      ) : sorted.length === 0 ? (
        <p className="py-6 text-center text-sm text-muted-foreground">{emptyText}</p>
      ) : view === 'trend' ? (
        <>
          <ErrorText>{trend.error}</ErrorText>
          <Trend top={sorted.slice(0, 5)} total={total} metric={metric} rows={trend.data?.rows ?? (trend.error ? [] : null)} buckets={span.buckets} granularity={granularity} />
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
        {view === 'trend' ? `Top 5 · ${PER_WORD[granularity]} ${metric} · legend = range total` : 'bar = share of the leader · % = share of total'}
      </p>
    </Panel>
  )
}

type Point = { bucket: string; v: number[] }

// Trend: one line per model over every bucket of the range (missing buckets are 0), the highest
// single-bucket value top-left, 4 bucket ticks, then a legend row per model with its range total.
function Trend({ top, total, metric, rows, buckets, granularity }: { top: SpendRow[]; total: number; metric: Metric; rows: SpendRow[] | null; buckets: string[]; granularity: Granularity }) {
  if (rows === null) return <Skeleton className="h-[150px] w-full" />
  const at = new Map(rows.map((r) => [`${r.bucket}\n${r.day}`, metricOf(r, metric)]))
  const data: Point[] = buckets.map((b) => ({ bucket: b, v: top.map((m) => at.get(`${m.bucket}\n${b}`) ?? 0) }))
  const max = Math.max(0, ...data.flatMap((p) => p.v))
  const ceil = (max || 1) / 0.96 // the busiest bucket peaks just under the top gridline
  const ticks = [...new Set([0, 1 / 3, 2 / 3, 1].map((f) => buckets[Math.round(f * (buckets.length - 1))]!))]
  return (
    <div className="flex flex-col gap-1.5">
      <div className="relative h-[150px] border-b border-border">
        <ResponsiveContainer width="100%" height="100%">
          <LineChart data={data} margin={{ top: 0, right: 0, bottom: 0, left: 0 }}>
            <CartesianGrid vertical={false} stroke="var(--border)" strokeDasharray="3 3" />
            <XAxis dataKey="bucket" hide />
            <YAxis hide domain={[0, ceil]} ticks={[0, 1, 2, 3].map((i) => (ceil * i) / 3)} />
            <Tooltip formatter={(v) => showMetric(Number(v ?? 0), metric)} labelFormatter={(b) => fmtBucket(String(b), granularity)} contentStyle={tooltipStyle} />
            {top.map((m, i) => (
              <Line
                key={m.bucket}
                type="monotone"
                name={m.bucket}
                dataKey={(p: Point) => p.v[i]}
                stroke={`var(--chart-${i + 1})`}
                strokeWidth={2}
                dot={buckets.length === 1 ? { r: 3, fill: `var(--chart-${i + 1})` } : false}
              />
            ))}
          </LineChart>
        </ResponsiveContainer>
        <span className="absolute -top-0.5 left-0 bg-card pr-1 text-[11px] text-muted-foreground">{showMetric(max, metric)} / {PER_UNIT[granularity]}</span>
      </div>
      <div className="flex justify-between text-[11px] text-muted-foreground">
        {ticks.map((b) => <span key={b}>{fmtBucket(b, granularity)}</span>)}
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

// TopBoard: the Top users / Top groups board (admins). Same bar semantics as Top models — up to
// 8 rows, bar = share of the leader, % = share of the total — with its own "Rank by". Rows are
// group_by=user|group; ids resolve to an email/group name from the /users and /groups lists
// (short(id) while those load or for an id that no longer exists).
function TopBoard({ title, rows, emptyText, names }: { title: string; rows: SpendRow[] | null; emptyText: string; names: Map<string, string> | null }) {
  const [metric, setMetric] = useState<Metric>('cost')
  const sorted = useMemo(() => (rows ?? []).filter((r) => r.requests > 0).sort((a, b) => metricOf(b, metric) - metricOf(a, metric)), [rows, metric])
  const total = sorted.reduce((s, r) => s + metricOf(r, metric), 0)
  const leader = sorted.length ? metricOf(sorted[0]!, metric) : 0
  return (
    <Panel
      title={title}
      actions={<Segmented label={`${title}: rank by`} value={metric} onChange={setMetric} options={METRICS} />}
    >
      {rows === null ? (
        <div className="space-y-3">{[0, 1, 2].map((i) => <Skeleton key={i} className="h-4 w-full" />)}</div>
      ) : sorted.length === 0 ? (
        <p className="py-6 text-center text-sm text-muted-foreground">{emptyText}</p>
      ) : (
        <ol className="flex flex-col">
          {sorted.slice(0, 8).map((r, i) => {
            const v = metricOf(r, metric)
            return (
              <li key={r.bucket} className="grid grid-cols-[20px_minmax(0,1fr)_96px] items-center gap-2.5 border-b border-border py-[7px]">
                <code className="font-mono text-xs text-muted-foreground">{i + 1}</code>
                <div className="flex min-w-0 flex-col gap-[5px]">
                  <div className="flex justify-between gap-2 text-[13px]">
                    <span className="truncate font-medium" title={r.bucket}>{names?.get(r.bucket) ?? short(r.bucket)}</span>
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
      <p className="mt-3 text-xs text-muted-foreground">bar = share of the leader · % = share of total</p>
    </Panel>
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

  // The range lives in the URL (?range=90d; custom adds from/to days) and drives every panel.
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
    setRange(r === 'custom' ? { range: r, from: span.fromDay, to: span.toDay } : { range: r, from: null, to: null })

  const [show, setShow] = useState(storedShow)
  const daily = useLoad<Spend>(`/console/api/v1/spend?group_by=${span.groupBy}&${span.q}`)
  const byModel = useLoad<Spend>(`/console/api/v1/spend?group_by=model&${span.q}`)
  const recent = useLoad<SpendLog[]>(`/console/api/v1/spend/requests?${span.q}&limit=${show}`)
  // Stat-card sparklines: always the last 30 days by day, whatever range the
  // panels use — one /spend?group_by=day fetch feeds all three series.
  const spark30 = useMemo(() => spanOf('30d', null, null, tz), [tz])
  const spark = useLoad<Spend>(`/console/api/v1/spend?group_by=day&${spark30.q}`)

  // Admin-only: the Top users / Top groups boards and the Top models scope toggle. The users
  // list serves both the boards' name resolution and the toggle's dropdown (contract C5/C6).
  const admin = can('spend:read_all')
  const byUser = useLoad<Spend>(admin ? `/console/api/v1/spend?group_by=user&${span.q}` : null)
  const byGroup = useLoad<Spend>(admin ? `/console/api/v1/spend?group_by=group&${span.q}` : null)
  const users = useLoad<User[]>(admin ? '/console/api/v1/users' : null)
  const groups = useLoad<Group[]>(admin ? '/console/api/v1/groups' : null)
  const userNames = useMemo(() => (users.data ? new Map(users.data.map((u) => [u.id, u.email])) : null), [users.data])
  const groupNames = useMemo(() => (groups.data ? new Map(groups.data.map((g) => [g.id, g.name])) : null), [groups.data])

  const rows = daily.data?.rows ?? null
  const sum = (f: (r: SpendRow) => number) => (rows ? rows.reduce((s, r) => s + f(r), 0) : null)
  const spend = sum((r) => r.cost_nanos)
  const requests = sum((r) => r.requests)
  const tokens = sum((r) => r.input_tokens + r.output_tokens)
  const byBucket = new Map((rows ?? []).map((r) => [r.bucket, r.cost_nanos]))
  const chart = span.buckets.map((b) => ({ bucket: b, usd: (byBucket.get(b) ?? 0) / 1e9 }))
  // Sparkline series, zero-filled for days without spend.
  const sparkByBucket = new Map((spark.data?.rows ?? []).map((r) => [r.bucket, r]))
  const sparkSeries = (f: (r: SpendRow) => number) => (spark.data ? spark30.buckets.map((b) => f(sparkByBucket.get(b) ?? ZERO_ROW)) : null)

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
            <DayInput label="From" value={span.fromDay} max={span.toDay} onCommit={(d) => setRange({ from: d })} />
            <DayInput label="To" value={span.toDay} min={span.fromDay} onCommit={(d) => setRange({ to: d })} />
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
        <ErrorText>{daily.error || byModel.error || recent.error || spark.error || (admin ? byUser.error || byGroup.error : '')}</ErrorText>
      </div>
      <div className="mb-6 grid grid-cols-1 gap-4 sm:grid-cols-3">
        <Stat label="Spend" value={spend == null ? null : usd(spend)} spark={sparkSeries((r) => r.cost_nanos)} tone="primary" />
        <Stat label="Requests" value={requests == null ? null : fmt.format(requests)} spark={sparkSeries((r) => r.requests)} />
        <Stat label="Tokens" value={tokens == null ? null : fmtTokens(tokens)} spark={sparkSeries((r) => r.input_tokens + r.output_tokens)} title={tokens == null ? undefined : fmt.format(tokens)} />
      </div>
      <div className="mb-6 grid grid-cols-1 gap-4 lg:grid-cols-2">
        <Panel title={`Spend per ${span.granularity} (USD)`}>
          <div className="h-[240px]">
            {rows === null ? (
              <Skeleton className="h-full w-full" />
            ) : rows.length === 0 ? (
              <p className="flex h-full items-center justify-center text-sm text-muted-foreground">{neverUsed ? 'No requests yet.' : 'No requests in this period.'}</p>
            ) : (
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart data={chart} margin={{ top: 8, right: 8, bottom: 0, left: 0 }}>
                  <CartesianGrid stroke="var(--border)" strokeDasharray="3 3" />
                  <XAxis dataKey="bucket" tick={{ fill: 'var(--muted-foreground)', fontSize: 12 }} stroke="var(--border)" tickFormatter={(b) => fmtBucket(String(b), span.granularity)} minTickGap={24} />
                  <YAxis tick={{ fill: 'var(--muted-foreground)', fontSize: 12 }} stroke="var(--border)" width={56} />
                  <Tooltip formatter={(v) => usd(Math.round(Number(v ?? 0) * 1e9))} labelFormatter={(b) => fmtBucket(String(b), span.granularity)} contentStyle={tooltipStyle} />
                  {/* A single-bucket range (e.g. a one-day custom) is one point: give it a dot. */}
                  <Area type="monotone" dataKey="usd" stroke="var(--primary)" fill="var(--primary)" fillOpacity={0.14} strokeWidth={2} dot={chart.length === 1 ? { r: 3, fill: 'var(--primary)' } : false} />
                </AreaChart>
              </ResponsiveContainer>
            )}
          </div>
        </Panel>
        <TopModels rows={byModel.data?.rows ?? (byModel.error ? [] : null)} emptyText={neverUsed ? 'No models used yet.' : 'No requests in this period.'} span={span} granularity={span.granularity} can={admin} users={users.data} />
      </div>
      {admin && (
        <div className="mb-6 grid grid-cols-1 gap-4 lg:grid-cols-2">
          <TopBoard title="Top users" rows={byUser.data?.rows ?? (byUser.error ? [] : null)} emptyText={neverUsed ? 'No users yet.' : 'No requests in this period.'} names={userNames} />
          <TopBoard title="Top groups" rows={byGroup.data?.rows ?? (byGroup.error ? [] : null)} emptyText={neverUsed ? 'No groups yet.' : 'No requests in this period.'} names={groupNames} />
        </div>
      )}
      <div className="mb-2 flex flex-wrap items-center justify-between gap-3">
        <h2 className="font-mono text-[11px] uppercase tracking-[0.06em] text-muted-foreground">
          Recent requests <span>· {span.label}</span>
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
          {
            head: 'Tokens in / out',
            cell: (l) => {
              const tin = l.input_tokens + l.cache_read_tokens + l.cache_write_tokens
              const tout = l.output_tokens
              return (
                <span title={`${fmt.format(tin)} / ${fmt.format(tout)}`}>
                  {fmtTokens(tin)} / {fmtTokens(tout)}
                </span>
              )
            },
          },
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
