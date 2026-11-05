import type { Limits } from './types'

export interface BudgetUsage {
  usedNanos: number // committed in the current window + in flight: what the gateway enforces
  pct: number // whole percent used, floored; may exceed 100
  over: boolean // at or past the cap: the gateway refuses any request with a cost
  resetAt: string | null // next reset for daily/monthly; null for total and for an expired window
  expired: boolean // a daily/monthly window that ended and hasn't been reset yet: shown fresh
}

// budgetUsage: what a Keys/Groups row shows for a budget. null for unlimited.
// It mirrors the gateway's EffectiveSpend (core/db/repo/repo.go): a daily/monthly
// window whose reset instant has passed but hasn't been reset yet counts as 0,
// since its counter is stale and the next request starts a fresh window. In-flight
// reservations count as used (#23). A cap of 0 is treated as reached. Expiry is
// judged by the browser clock; the gateway uses the server's.
// Percentages are display only; the money values stay integer nanos.
export function budgetUsage(l: Limits, now = Date.now()): BudgetUsage | null {
  if (l.budget_type === 'unlimited') return null
  const windowed = l.budget_type === 'daily' || l.budget_type === 'monthly'
  const expired = windowed && l.budget_reset_at != null && new Date(l.budget_reset_at).getTime() <= now
  const used = (expired ? 0 : l.spend_nanos) + (l.reserved_nanos ?? 0)
  const capped = l.budget_nanos > 0
  return {
    usedNanos: used,
    pct: capped ? Math.floor((used / l.budget_nanos) * 100) : 100,
    over: !capped || used >= l.budget_nanos,
    resetAt: windowed && !expired ? l.budget_reset_at : null,
    expired,
  }
}
