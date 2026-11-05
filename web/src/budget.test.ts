import { describe, expect, it } from 'vitest'
import { budgetText } from './components/LimitsFields'
import { budgetUsage } from './budget'
import type { Limits } from './types'

const NOW = Date.parse('2026-11-05T12:00:00Z')
const lim = (o: Partial<Limits>): Limits => ({
  budget_type: 'monthly', budget_nanos: 50e9, spend_nanos: 0, budget_reset_at: '2026-12-01T00:00:00Z',
  rpm_limit: null, tpm_limit: null, ...o,
})

describe('budgetUsage', () => {
  it('unlimited: no bar', () => {
    expect(budgetUsage(lim({ budget_type: 'unlimited', budget_reset_at: null }), NOW)).toBeNull()
  })

  it('monthly: floored percent and the next reset', () => {
    expect(budgetUsage(lim({ spend_nanos: 12.4e9 }), NOW)).toEqual({ usedNanos: 12.4e9, pct: 24, over: false, resetAt: '2026-12-01T00:00:00Z', expired: false })
  })

  it('total: percent, never a reset', () => {
    const u = budgetUsage(lim({ budget_type: 'total', budget_reset_at: null, spend_nanos: 25e9 }), NOW)
    expect(u).toMatchObject({ pct: 50, over: false, resetAt: null })
  })

  it('counts in-flight reservations as used', () => {
    expect(budgetUsage(lim({ spend_nanos: 40e9, reserved_nanos: 10e9 }), NOW)).toMatchObject({ usedNanos: 50e9, pct: 100, over: true })
  })

  it('just under the cap is not over and does not round up to 100%', () => {
    expect(budgetUsage(lim({ spend_nanos: 50e9 - 1 }), NOW)).toMatchObject({ pct: 99, over: false })
  })

  it('past the cap reports the real percent', () => {
    expect(budgetUsage(lim({ budget_type: 'daily', budget_nanos: 10e9, spend_nanos: 15e9, budget_reset_at: '2026-11-06T00:00:00Z' }), NOW))
      .toMatchObject({ pct: 150, over: true })
  })

  it('a zero cap is reached', () => {
    expect(budgetUsage(lim({ budget_type: 'total', budget_nanos: 0, budget_reset_at: null }), NOW)).toMatchObject({ pct: 100, over: true })
  })

  it('an expired, not yet reset window counts as fresh (matches the gateway)', () => {
    const u = budgetUsage(lim({ budget_type: 'daily', spend_nanos: 60e9, reserved_nanos: 1e9, budget_reset_at: '2026-11-05T00:00:00Z' }), NOW)
    expect(u).toEqual({ usedNanos: 1e9, pct: 2, over: false, resetAt: null, expired: true })
  })

  it('spend exactly at the cap is over', () => {
    expect(budgetUsage(lim({ spend_nanos: 50e9 }), NOW)).toMatchObject({ pct: 100, over: true })
  })

  it('a reset instant equal to now has expired (the gateway compares !now.Before(reset))', () => {
    expect(budgetUsage(lim({ budget_type: 'daily', spend_nanos: 9e9, budget_reset_at: new Date(NOW).toISOString() }), NOW))
      .toMatchObject({ usedNanos: 0, expired: true, resetAt: null })
  })

  it('total and unlimited never report an expired window', () => {
    expect(budgetUsage(lim({ budget_type: 'total', budget_reset_at: '2020-01-01T00:00:00Z', spend_nanos: 5e9 }), NOW))
      .toMatchObject({ usedNanos: 5e9, expired: false, resetAt: null })
  })
})

describe('budgetText', () => {
  it('unlimited', () => {
    expect(budgetText(lim({ budget_type: 'unlimited' }))).toBe('Unlimited')
  })

  it('an expired window shows only what is in flight, marked', () => {
    // budgetText reads the real clock: a reset in 2020 is expired whenever this runs.
    expect(budgetText(lim({ budget_type: 'daily', spend_nanos: 60e9, reserved_nanos: 0.3e9, budget_reset_at: '2020-01-01T00:00:00Z' })))
      .toBe('0.30 / 50.00 daily (+0.30 in flight)')
  })
})
