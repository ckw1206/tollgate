import { describe, expect, it } from 'vitest'
import { RANGES, fmtBucket, isoWeekLabel, mondayOfIsoWeek, spanOf } from './range'

// Contract C4 pins. Fixed instants so the suite never drifts with the clock.
const NOW = Date.parse('2026-10-14T15:30:00Z')
const NY = 'America/New_York'
const UTC = 'UTC'

describe('spanOf — unified range model', () => {
  it('offers the C4 option set (no "today")', () => {
    expect(RANGES.map((r) => r.value)).toEqual(['24h', '7d', '30d', '90d', '1y', '3y', 'custom'])
  })

  it('24h: rolling 24h, hour buckets, 24 labels in the server tz', () => {
    const s = spanOf('24h', null, null, NY, NOW)
    expect(s.granularity).toBe('hour')
    expect(s.groupBy).toBe('hour')
    expect(s.modelGroupBy).toBe('model,hour')
    // now = 15:30Z = 11:30 local (EDT): window [Oct 13 11:00, Oct 14 11:00) local.
    expect(s.buckets.length).toBe(24)
    expect(s.buckets[0]).toBe('2026-10-13T11')
    expect(s.buckets[23]).toBe('2026-10-14T10')
    expect(s.q).toBe('from=2026-10-13T15%3A00%3A00.000Z&to=2026-10-14T15%3A00%3A00.000Z')
  })

  it('24h across a spring-forward skips the nonexistent local hour', () => {
    // 2026-03-08T09:30Z = 04:30 EDT; the window [Mar 7 03:00 EDT, Mar 8 04:00 EDT) contains
    // the 02:00 EST -> 03:00 EDT jump, so the label 2026-03-08T02 does not exist.
    const s = spanOf('24h', null, null, NY, Date.parse('2026-03-08T09:30:00Z'))
    expect(s.buckets).toContain('2026-03-08T00')
    expect(s.buckets).toContain('2026-03-08T01')
    expect(s.buckets).not.toContain('2026-03-08T02')
    expect(s.buckets).toContain('2026-03-08T03')
  })

  it('7d: rolling 7 days, day buckets ending at today', () => {
    const s = spanOf('7d', null, null, UTC, NOW)
    expect(s.granularity).toBe('day')
    expect(s.buckets).toEqual(['2026-10-08', '2026-10-09', '2026-10-10', '2026-10-11', '2026-10-12', '2026-10-13', '2026-10-14'])
  })

  it('30d: rolling 30 days, day buckets', () => {
    const s = spanOf('30d', null, null, UTC, NOW)
    expect(s.granularity).toBe('day')
    expect(s.buckets.length).toBe(30)
    expect(s.buckets[0]).toBe('2026-09-15')
    expect(s.buckets[29]).toBe('2026-10-14')
  })

  it('90d: rolling 90 days, week buckets (ISO, zero-padded)', () => {
    const s = spanOf('90d', null, null, UTC, NOW)
    expect(s.granularity).toBe('week')
    // 2026-07-17 (the first day) is in ISO week 29; now is in W42.
    expect(s.buckets[0]).toBe('2026-W29')
    expect(s.buckets[s.buckets.length - 1]).toBe('2026-W42')
    expect(s.buckets[s.buckets.indexOf('2026-W41') + 1]).toBe('2026-W42') // adjacency across the week boundary
  })

  it('90d across a spring-forward still lands on whole ISO weeks', () => {
    const s = spanOf('90d', null, null, NY, Date.parse('2026-03-08T09:30:00Z'))
    expect(s.buckets[0]).toBe('2025-W50')
    expect(s.buckets[s.buckets.length - 1]).toBe('2026-W10')
    expect(s.buckets).toContain('2026-W01') // ISO year boundary is crossed in labels
  })

  it('1y: current month plus the 12 preceding months (13 labels, both endpoints)', () => {
    const s = spanOf('1y', null, null, UTC, NOW)
    expect(s.granularity).toBe('month')
    expect(s.buckets).toEqual(['2025-10', '2025-11', '2025-12', '2026-01', '2026-02', '2026-03', '2026-04', '2026-05', '2026-06', '2026-07', '2026-08', '2026-09', '2026-10'])
  })

  it('3y: the last three calendar years', () => {
    const s = spanOf('3y', null, null, UTC, NOW)
    expect(s.granularity).toBe('year')
    expect(s.buckets).toEqual(['2024', '2025', '2026'])
  })

  it('custom: auto granularity by span (<=48h hour, <=31d day, <=92d week, else month)', () => {
    const g = (a: string, b: string) => spanOf('custom', a, b, UTC, NOW).granularity
    expect(g('2026-01-01', '2026-01-01')).toBe('hour') // 24h
    expect(g('2026-01-01', '2026-01-02')).toBe('hour') // 48h
    expect(g('2026-01-01', '2026-01-03')).toBe('day') // 72h
    expect(g('2026-01-01', '2026-01-31')).toBe('day') // 31d
    expect(g('2026-01-01', '2026-02-01')).toBe('week') // 32d
    expect(g('2026-01-01', '2026-04-02')).toBe('week') // 92d
    expect(g('2026-01-01', '2026-04-03')).toBe('month') // 93d
  })

  it('custom: a single day is 24 hour buckets; a week-span crossing W41/W42 keeps adjacency', () => {
    const one = spanOf('custom', '2026-10-14', '2026-10-14', UTC, NOW)
    expect(one.granularity).toBe('hour')
    expect(one.buckets.length).toBe(24)
    expect(one.buckets[0]).toBe('2026-10-14T00')
    expect(one.buckets[23]).toBe('2026-10-14T23')
    const weeks = spanOf('custom', '2026-08-24', '2026-10-18', UTC, NOW) // 56d -> week
    expect(weeks.granularity).toBe('week')
    expect(weeks.buckets[weeks.buckets.indexOf('2026-W41') + 1]).toBe('2026-W42')
  })

  it('labels are lexicographically ascending for every granularity', () => {
    for (const range of ['24h', '7d', '30d', '90d', '1y', '3y'] as const) {
      const s = spanOf(range, null, null, NY, NOW)
      for (let i = 1; i < s.buckets.length; i++) expect(s.buckets[i]! > s.buckets[i - 1]!).toBe(true)
    }
  })
})

describe('ISO week helpers', () => {
  it('isoWeekLabel pins the verified 2026/2027 facts', () => {
    expect(isoWeekLabel('2026-10-15')).toBe('2026-W42')
    expect(isoWeekLabel('2026-01-01')).toBe('2026-W01') // 2026-01-01 is a Thursday of W01
    expect(isoWeekLabel('2027-01-01')).toBe('2026-W53') // a Friday that belongs to 2026-W53
    expect(isoWeekLabel('2025-12-29')).toBe('2026-W01') // a Monday of 2025 in ISO 2026-W01
  })

  it('mondayOfIsoWeek round-trips through isoWeekLabel', () => {
    for (const [y, w] of [[2026, 1], [2026, 42], [2026, 53], [2025, 50]] as const) {
      const mon = mondayOfIsoWeek(y, w)
      expect(isoWeekLabel(mon)).toBe(`${y}-W${String(w).padStart(2, '0')}`)
      expect(new Date(Date.parse(mon)).getUTCDay()).toBe(1) // it is a Monday
    }
  })
})

describe('fmtBucket', () => {
  it('formats every granularity for display', () => {
    expect(fmtBucket('2026-10-14T09', 'hour')).toBe('Oct 14 09:00')
    expect(fmtBucket('2026-10-14', 'day')).toBe('Oct 14')
    expect(fmtBucket('2026-W42', 'week')).toBe('Oct 12 (W42)') // Monday of W42
    expect(fmtBucket('2026-10', 'month')).toBe('Oct 2026')
    expect(fmtBucket('2026', 'year')).toBe('2026')
  })
})
