import { useCallback, useEffect, useRef, useState } from 'react'
import { api, usd } from '../api'
import type { GateInfo } from '../types'

// The one-line status bar above every console page header: gateway health,
// provider uptime, request rate, latency and the caller's budget meter.
// useGate polls /health + /console/api/v1/gate every 15 s, re-polls on the
// tollgate:gate-refresh window event (the Playground dispatches it after each
// send) and immediately when a hidden tab becomes visible again, so stale
// budget state — a block that has since reset — clears without a reload.

const POLL_MS = 15_000

export function useGate() {
  // null = still loading; /health is 503 (api throws) when a dependency is down.
  const [health, setHealth] = useState<boolean | null>(null)
  const [gate, setGate] = useState<GateInfo | null>(null)
  // Only the latest poll may land: a slow tick superseded by the refresh
  // event (or the next tick) must not overwrite newer data.
  const seq = useRef(0)

  const refresh = useCallback(async () => {
    if (document.hidden) return // a hidden tab skips the tick; the next one (or the event) polls
    const n = ++seq.current
    const [h, g] = await Promise.allSettled([api<Record<string, string>>('/health'), api<GateInfo>('/console/api/v1/gate')])
    if (n !== seq.current) return
    setHealth(h.status === 'fulfilled' && Object.values(h.value).every((v) => v === 'ok'))
    if (g.status === 'fulfilled') setGate(g.value)
  }, [])

  useEffect(() => {
    void refresh()
    const t = setInterval(() => void refresh(), POLL_MS)
    const onRefresh = () => void refresh()
    window.addEventListener('tollgate:gate-refresh', onRefresh)
    // Ticks are skipped while hidden, so a tab that sat hidden through a
    // daily reset (or a budget raise) refreshes the moment it is shown.
    const onVisible = () => {
      if (!document.hidden) void refresh()
    }
    document.addEventListener('visibilitychange', onVisible)
    return () => {
      clearInterval(t)
      window.removeEventListener('tollgate:gate-refresh', onRefresh)
      document.removeEventListener('visibilitychange', onVisible)
    }
  }, [refresh])

  return { health, gate }
}

// Meter colour: primary → warning-foreground at >= 80% → destructive at 100%.
const meterColor = (pct: number) => (pct >= 100 ? 'var(--destructive)' : pct >= 80 ? 'var(--warning-foreground)' : 'var(--primary)')

export default function GateBar() {
  const { health, gate } = useGate()
  const b = gate?.budget ?? null
  // In-flight requests already count against the cap but are not in
  // spend_nanos yet, so the meter includes them (#23) — otherwise it reads
  // lower than what the gateway will actually enforce.
  const used = b ? b.spend_nanos + (b.reserved_nanos ?? 0) : 0
  // A budget with budget_nanos <= 0 is treated as reached: 100%, destructive.
  const pct = b ? (b.budget_nanos > 0 ? Math.min((used / b.budget_nanos) * 100, 100) : 100) : 0
  // Below 768px only the budget meter shows; with no budget there is nothing
  // left on a narrow screen, so the whole bar hides there.
  return (
    <div className={`${b ? '' : 'hidden md:block'} mb-4 border-b border-dashed border-border pb-2`}>
      <div className="flex items-center gap-4 font-mono text-[11px] uppercase tracking-[0.06em]">
        <span className="hidden items-center gap-1.5 md:flex">
          <span
            aria-hidden
            className="h-1.5 w-1.5 rounded-sm"
            style={{ background: health == null ? 'var(--muted-foreground)' : health ? 'var(--success-foreground)' : 'var(--destructive)' }}
          />
          {health == null ? 'Gateway…' : health ? 'Gateway online' : 'Gateway down'}
        </span>
        {gate && <span className="hidden md:inline">{gate.providers_up} / {gate.providers_total} providers up</span>}
        {gate?.rpm != null && <span className="hidden md:inline">{gate.rpm} req / min</span>}
        {gate?.p50_ms != null && <span className="hidden md:inline">p50 {gate.p50_ms} ms</span>}
        {b && (
          <span className="ml-auto flex items-center gap-2">
            <span className="hidden md:inline">
              {b.type === 'daily' ? 'Your budget today' : 'Your budget'} {usd(used)} / {usd(b.budget_nanos)}
            </span>
            <span aria-hidden className="h-1 overflow-hidden rounded-sm bg-muted" style={{ width: 64 }}>
              <span className="block h-full" style={{ width: `${pct}%`, background: meterColor(pct) }} />
            </span>
          </span>
        )}
      </div>
    </div>
  )
}
