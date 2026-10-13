import { useEffect, useMemo, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { CartesianGrid, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import { usd, when } from '../api'
import { useAuth } from '../auth'
import GetStarted from '../components/GetStarted'
import { Badge, Banner, controlCls, ErrorText, PageHeader, Segmented, Skeleton, Table, useLoad } from '../components/ui'
import type { ApiKey, SpendLog, SpendRow } from '../types'

type Spend = { rows: SpendRow[]; recent: SpendLog[] }
type Metric = 'tokens' | 'requests' | 'cost'

const DAY = 86_400_000
const fmt = new Intl.NumberFormat()

const metricOf = (r: SpendRow, m: Metric) => (m === 'cost' ? r.cost_nanos : m === 'requests' ? r.requests : r.input_tokens + r.output_tokens)
const showMetric = (v: number, m: Metric) => (m === 'cost' ? usd(v) : fmt.format(v))

function Stat({ label, value }: { label: string; value: string | null }) {
  return (
    <div className="rounded-md border border-border bg-card p-4">
      <div className="text-xs uppercase tracking-[0.04em] text-muted-foreground">{label}</div>
      <div className="mt-1 text-2xl font-semibold tabular-nums">{value ?? <Skeleton className="mt-2 h-6 w-24" />}</div>
    </div>
  )
}

function Panel({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="min-w-0 rounded-md border border-border bg-card p-4">
      <h2 className="mb-3 text-sm font-medium">{title}</h2>
      {children}
    </section>
  )
}

// TopModels: up to 8 rows from group_by=model, sorted by the chosen metric.
// Bar = share of the leader; % = share of the total.
function TopModels({ rows, emptyText }: { rows: SpendRow[] | null; emptyText: string }) {
  const [metric, setMetric] = useState<Metric>('cost')
  const sorted = useMemo(() => (rows ?? []).filter((r) => metricOf(r, metric) > 0).sort((a, b) => metricOf(b, metric) - metricOf(a, metric)), [rows, metric])
  const top = sorted.slice(0, 8)
  const total = sorted.reduce((s, r) => s + metricOf(r, metric), 0)
  const leader = top.length ? metricOf(top[0]!, metric) : 0
  return (
    <Panel title="Top models">
      <div className="mb-3">
        <Segmented label="Top models metric" value={metric} onChange={setMetric} options={[{ value: 'tokens', label: 'Tokens' }, { value: 'requests', label: 'Requests' }, { value: 'cost', label: 'Cost' }]} />
      </div>
      {rows === null ? (
        <div className="space-y-3">{[0, 1, 2].map((i) => <Skeleton key={i} className="h-4 w-full" />)}</div>
      ) : top.length === 0 ? (
        <p className="py-6 text-center text-sm text-muted-foreground">{emptyText}</p>
      ) : (
        <ol className="space-y-3">
          {top.map((r, i) => {
            const v = metricOf(r, metric)
            return (
              <li key={r.bucket} className="grid grid-cols-[20px_1fr_96px] items-center gap-2">
                <span className="font-mono text-xs text-muted-foreground">{i + 1}</span>
                <div className="min-w-0">
                  <div className="flex items-baseline gap-2">
                    <span className="truncate text-[13px] font-medium">{r.bucket}</span>
                    <span className="text-xs text-muted-foreground">{total ? Math.round((v / total) * 100) : 0}%</span>
                  </div>
                  <div className="mt-1 h-1 bg-muted">
                    <div className="h-1 bg-primary" style={{ width: `${leader ? (v / leader) * 100 : 0}%` }} />
                  </div>
                </div>
                <span className="text-right text-sm tabular-nums">{showMetric(v, metric)}</span>
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
  const [days, setDays] = useState(30)
  // Round "from" to the minute so the URL (and the fetch) is stable across re-renders.
  const from = useMemo(() => new Date(Math.floor((Date.now() - days * DAY) / 60_000) * 60_000).toISOString(), [days])
  const daily = useLoad<Spend>(`/console/api/v1/spend?group_by=day&from=${encodeURIComponent(from)}`)
  const byModel = useLoad<Spend>(`/console/api/v1/spend?group_by=model&from=${encodeURIComponent(from)}`)

  const rows = daily.data?.rows ?? null
  const sum = (f: (r: SpendRow) => number) => (rows ? rows.reduce((s, r) => s + f(r), 0) : null)
  const spend = sum((r) => r.cost_nanos)
  const requests = sum((r) => r.requests)
  const tokens = sum((r) => r.input_tokens + r.output_tokens)
  const chart = (rows ?? []).map((r) => ({ day: r.bucket, usd: r.cost_nanos / 1e9 }))

  // Empty dashboard without the checklist (members and viewers): a next step, never a dead end.
  // "Yet" copy only when nothing has ever been recorded; a quiet range says "in this period".
  const ever = useLoad<Spend>(rows !== null && rows.length === 0 ? '/console/api/v1/spend?group_by=model&from=1970-01-01T00%3A00%3A00Z' : null)
  const neverUsed = rows !== null && rows.length === 0 && ever.data !== null && ever.data.recent.length === 0
  // Members and viewers without a key get a next step, never a dead end.
  const myKeys = useLoad<ApiKey[]>(can('models:write') ? null : '/console/api/v1/keys')
  const nothingYet = myKeys.data !== null && myKeys.data.length === 0
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
        <select aria-label="Range" className={controlCls + ' h-9 text-sm'} value={days} onChange={(e) => setDays(Number(e.target.value))}>
          <option value={7}>Last 7 days</option>
          <option value={30}>Last 30 days</option>
          <option value={90}>Last 90 days</option>
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
      <div className="mb-4 space-y-1">
        <ErrorText>{daily.error}</ErrorText>
        <ErrorText>{daily.error ? '' : byModel.error}</ErrorText>
      </div>
      <div className="mb-4 grid grid-cols-1 gap-4 sm:grid-cols-3">
        <Stat label="Spend" value={spend == null ? null : usd(spend)} />
        <Stat label="Requests" value={requests == null ? null : fmt.format(requests)} />
        <Stat label="Tokens" value={tokens == null ? null : fmt.format(tokens)} />
      </div>
      <div className="mb-4 grid grid-cols-1 gap-4 lg:grid-cols-2">
        <Panel title="Spend per day (USD)">
          <div className="h-[240px]">
            {rows === null ? (
              <Skeleton className="h-full w-full" />
            ) : chart.length === 0 ? (
              <p className="flex h-full items-center justify-center text-sm text-muted-foreground">{neverUsed ? 'No requests yet.' : 'No requests in this period.'}</p>
            ) : (
              <ResponsiveContainer width="100%" height="100%">
                <LineChart data={chart} margin={{ top: 8, right: 8, bottom: 0, left: 0 }}>
                  <CartesianGrid stroke="var(--border)" strokeDasharray="3 3" />
                  <XAxis dataKey="day" tick={{ fill: 'var(--muted-foreground)', fontSize: 12 }} stroke="var(--border)" />
                  <YAxis tick={{ fill: 'var(--muted-foreground)', fontSize: 12 }} stroke="var(--border)" width={56} />
                  <Tooltip
                    formatter={(v: number) => usd(Math.round(v * 1e9))}
                    contentStyle={{ background: 'var(--background)', border: '1px solid var(--border)', color: 'var(--foreground)' }}
                  />
                  <Line type="monotone" dataKey="usd" stroke="var(--primary)" strokeWidth={2} dot={false} />
                </LineChart>
              </ResponsiveContainer>
            )}
          </div>
        </Panel>
        <TopModels rows={byModel.data?.rows ?? (byModel.error ? [] : null)} emptyText={neverUsed ? 'No models used yet.' : 'No requests in this period.'} />
      </div>
      <h2 className="mb-3 text-sm font-medium">Recent requests</h2>
      <Table
        rows={daily.data?.recent ?? (daily.error ? [] : null)}
        empty="No requests in this period."
        cols={[
          { head: 'When', cell: (l) => when(l.created_at) },
          { head: 'Model', cell: (l) => l.model_name },
          // Input counts cached tokens too, matching the stat cards and the Top models board.
          { head: 'Tokens in / out', cell: (l) => `${fmt.format(l.input_tokens + l.cache_read_tokens + l.cache_write_tokens)} / ${fmt.format(l.output_tokens)}` },
          { head: 'Cost', cell: (l) => usd(l.cost_nanos) },
          { head: 'Latency', cell: (l) => `${fmt.format(l.latency_ms)} ms` },
          // A stream that fails after headers were sent is recorded as status "error" with http_status 200.
          { head: 'Status', cell: (l) => (l.status === 'success' ? <Badge tone="success">ok</Badge> : <Badge tone="error">{l.http_status && l.http_status !== 200 ? l.http_status : 'error'}</Badge>) },
        ]}
      />
    </>
  )
}
