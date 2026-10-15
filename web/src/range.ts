// The unified time-range model for the Usage page (spec: admin-usage-dashboard, contract C4).
// One dropdown value sets both the window and its bucket granularity. Bucket labels are
// generated in the SERVER timezone and must match the SQL labels in core/db/repo/spend.go
// exactly: hour 'YYYY-MM-DDTHH24', day 'YYYY-MM-DD', week 'IYYY-\"W\"IW' (ISO, zero-padded),
// month 'YYYY-MM', year 'YYYY'. All labels are lexicographically sortable within a granularity.

export type Range = '24h' | '7d' | '30d' | '90d' | '1y' | '3y' | 'custom'
export type Granularity = 'hour' | 'day' | 'week' | 'month' | 'year'

export const RANGES: { value: Range; label: string }[] = [
  { value: '24h', label: 'Last 24 hours' },
  { value: '7d', label: 'Last 7 days' },
  { value: '30d', label: 'Last 30 days' },
  { value: '90d', label: 'Last 90 days' },
  { value: '1y', label: 'Last year' },
  { value: '3y', label: 'Last 3 years' },
  { value: 'custom', label: 'Custom…' },
]

export interface Span {
  q: string // from=&to= as RFC3339 — the server accepts RFC3339
  buckets: string[] // one C1 label per bucket in the window, ascending
  fromDay: string // YYYY-MM-DD, first day covered (custom DayInput wiring)
  toDay: string // YYYY-MM-DD, last day covered
  label: string
  phrase: string
  granularity: Granularity
  groupBy: Granularity
  modelGroupBy: string // `model,${granularity}`
}

const DAY = 86_400_000
const HOUR = 3_600_000
const pad = (n: number) => String(n).padStart(2, '0')

// Calendar days are YYYY-MM-DD; years before 2000 are rejected (DayInput), and so are
// impossible days (a hand-edited ?from=).
export const isDay = (s: string | null): s is string =>
  !!s && /^2\d{3}-\d{2}-\d{2}$/.test(s) && !Number.isNaN(Date.parse(s)) && new Date(s).toISOString().slice(0, 10) === s

const nextDay = (d: string) => new Date(Date.parse(d) + DAY).toISOString().slice(0, 10)
export const fmtDay = (d: string) => new Date(Date.parse(d)).toLocaleDateString(undefined, { month: 'short', day: 'numeric', timeZone: 'UTC' })

const clockFmt = (tz: string) =>
  new Intl.DateTimeFormat('en-US', { timeZone: tz, hourCycle: 'h23', year: 'numeric', month: 'numeric', day: 'numeric', hour: 'numeric', minute: 'numeric', second: 'numeric' })

function offsetAt(t: number, tz: string) {
  const p = Object.fromEntries(clockFmt(tz).formatToParts(t).map((x) => [x.type, x.value]))
  return Date.UTC(+p.year, +p.month - 1, +p.day, +p.hour, +p.minute, +p.second) - Math.floor(t / 1000) * 1000
}

// The UTC instant of the local wall-clock (y, m, d, h, min) in tz. Measured at the first guess
// and again at the result, so a DST change on that day still lands on the wall-clock time.
function localToUtc(y: number, m: number, d: number, h: number, min: number, tz: string) {
  const guess = Date.UTC(y, m - 1, d, h, min, 0)
  const t = guess - offsetAt(guess, tz)
  return guess - offsetAt(t, tz)
}

function utcParts(t: number, tz: string) {
  const p = Object.fromEntries(clockFmt(tz).formatToParts(t).map((x) => [x.type, x.value]))
  return { y: +p.year, m: +p.month, d: +p.day, h: +p.hour }
}

const dayIn = (t: number, tz: string) => {
  const p = utcParts(t, tz)
  return `${p.y}-${pad(p.m)}-${pad(p.d)}`
}

// midnight: the UTC instant 00:00 starts on day d in tz.
function midnight(d: string, tz: string) {
  const p = d.split('-').map(Number)
  return localToUtc(p[0]!, p[1]!, p[2]!, 0, 0, tz)
}

const hourLabel = (t: number, tz: string) => {
  const p = utcParts(t, tz)
  return `${p.y}-${pad(p.m)}-${pad(p.d)}T${pad(p.h)}`
}

// ISO 8601 week label for a calendar day. The Thursday of the day's week fixes the ISO year,
// so 2027-01-01 (a Friday) belongs to 2026-W53 and 2025-12-29 (a Monday) to 2026-W01.
export function isoWeekLabel(d: string) {
  const t = Date.parse(d) // UTC midnight of that calendar day
  const dow = new Date(t).getUTCDay() || 7 // Mon=1 … Sun=7
  const thursday = t + (4 - dow) * DAY
  const isoYear = new Date(thursday).getUTCFullYear()
  const week = 1 + Math.floor((thursday - Date.UTC(isoYear, 0, 1)) / (7 * DAY))
  return `${isoYear}-W${pad(week)}`
}

// Monday of ISO week (y, w). Jan 4 is always in week 1, which fixes the anchor.
export function mondayOfIsoWeek(y: number, w: number) {
  const jan4 = Date.UTC(y, 0, 4)
  const dow = new Date(jan4).getUTCDay() || 7
  return new Date(jan4 - (dow - 1) * DAY + (w - 1) * 7 * DAY).toISOString().slice(0, 10)
}

function bucketsFor(from: number, to: number, g: Granularity, tz: string): string[] {
  if (g === 'hour') {
    // One step per UTC hour, deduped and sorted: on a DST fall-back day a local hour occurs
    // twice (both occurrences sum under the same SQL bucket), and the SQL result is ordered by
    // label — the frontend buckets must match it exactly.
    const set = new Set<string>()
    for (let t = from; t < to; t += HOUR) set.add(hourLabel(t, tz))
    return [...set].sort()
  }
  if (g === 'day') {
    const out: string[] = []
    for (let d = dayIn(from, tz); d <= dayIn(to - 1, tz); d = nextDay(d)) out.push(d)
    return out
  }
  if (g === 'week') {
    // Monday of the first day's week, stepped 7 days in date-string math (stays on Mondays
    // across DST; the label follows the ISO week, which can span year boundaries).
    const last = dayIn(to - 1, tz)
    const lastWk = isoWeekLabel(last)
    const dow = new Date(Date.parse(dayIn(from, tz))).getUTCDay() || 7
    let mon = new Date(Date.parse(dayIn(from, tz)) - (dow - 1) * DAY).toISOString().slice(0, 10)
    const out: string[] = []
    for (;;) {
      const w = isoWeekLabel(mon)
      out.push(w)
      if (w === lastWk) break
      mon = new Date(Date.parse(mon) + 7 * DAY).toISOString().slice(0, 10)
    }
    return out
  }
  if (g === 'month') {
    const first = dayIn(from, tz).split('-').map(Number)
    const last = dayIn(to - 1, tz).split('-').map(Number)
    let y = first[0]!, m = first[1]!
    const out: string[] = []
    for (;;) {
      out.push(`${y}-${pad(m)}`)
      if (y === last[0] && m === last[1]) break
      m++
      if (m > 12) { m = 1; y++ }
    }
    return out
  }
  // year
  const y0 = +dayIn(from, tz).slice(0, 4)
  const y1 = +dayIn(to - 1, tz).slice(0, 4)
  const out: string[] = []
  for (let y = y0; ; y++) {
    out.push(String(y))
    if (y === y1) break
  }
  return out
}

// span: what ?range= (and, for custom, ?from=&to=) means right now. Rolling windows anchor at
// the next whole minute (a stable URL); bucket labels follow the granularity (C4 table):
// 24h→hour, 7d/30d→day, 90d→week, 1y→month (13 labels, both endpoints), 3y→year (the last
// three calendar years), custom→auto by span (≤48h hour, ≤31d day, ≤92d week, else month).
export function spanOf(range: Range, pFrom: string | null, pTo: string | null, tz: string, now = Date.now()): Span {
  const t = Math.floor(now / 60_000) * 60_000 + 60_000 // the next whole minute: a stable URL
  let from: number, to: number, label: string, phrase: string, granularity: Granularity
  if (range === '24h') {
    // H = the start of the local hour containing now. The window is [H−24h, H): exactly 24 UTC
    // hours, so the last bucket is the hour containing now−1h; on a spring-forward day one local
    // label is skipped and on a fall-back day two UTC hours share a label (deduped below).
    const p = utcParts(t, tz)
    const h0 = localToUtc(p.y, p.m, p.d, p.h, 0, tz)
    from = h0 - 24 * HOUR
    to = h0
    granularity = 'hour'
    label = 'Last 24 hours'
    phrase = 'in the last 24 hours'
  } else if (range === '7d' || range === '30d' || range === '90d') {
    const n = +range.slice(0, -1) // 7 | 30 | 90
    granularity = n >= 90 ? 'week' : 'day'
    from = midnight(dayIn(t - (n - 1) * DAY, tz), tz)
    to = midnight(nextDay(dayIn(t, tz)), tz)
    label = `Last ${n} days`
    phrase = `in the last ${n} days`
  } else if (range === '1y') {
    granularity = 'month'
    // Rolling 12 months by date (not month-aligned): the window [same date last year, end of
    // today) carries partial first and last months, so the buckets run 13 labels — 2025-10 …
    // 2026-10 for now in October 2026 (contract C4: both endpoints inclusive).
    const p = utcParts(t, tz)
    const back = p.m - 12
    const y = p.y + (back < 1 ? -1 : 0)
    const m = back < 1 ? back + 12 : back
    from = midnight(`${y}-${pad(m)}-${pad(p.d)}`, tz)
    to = midnight(nextDay(dayIn(t, tz)), tz)
    label = 'Last year'
    phrase = 'over the last year'
  } else if (range === '3y') {
    granularity = 'year'
    // The last three calendar years (contract C4: 2024, 2025, 2026 for now in 2026).
    const y = utcParts(t, tz).y
    from = midnight(`${y - 2}-01-01`, tz)
    to = midnight(`${y + 1}-01-01`, tz)
    label = 'Last 3 years'
    phrase = 'over the last 3 years'
  } else {
    // custom: hand-entered days (existing DayInput), granularity derived from the span.
    const today = dayIn(t, tz)
    const upToToday = (d: string) => (d < today ? d : today) // no spend in the future
    let a = upToToday(isDay(pFrom) ? pFrom : dayIn(t - 29 * DAY, tz))
    let b = upToToday(isDay(pTo) ? pTo : today)
    if (a > b) [a, b] = [b, a]
    from = midnight(a, tz)
    to = midnight(nextDay(b), tz)
    const days = Math.round((Date.parse(b) - Date.parse(a)) / DAY) + 1 // inclusive
    granularity = days * 24 <= 48 ? 'hour' : days <= 31 ? 'day' : days <= 92 ? 'week' : 'month'
    label = a === b ? fmtDay(a) : `${fmtDay(a)} – ${fmtDay(b)}`
    phrase = a === b ? `on ${fmtDay(a)}` : `between ${fmtDay(a)} and ${fmtDay(b)}`
  }
  const buckets = bucketsFor(from, to, granularity, tz)
  const q = new URLSearchParams({ from: new Date(from).toISOString(), to: new Date(to).toISOString() }).toString()
  return { q, buckets, fromDay: dayIn(from, tz), toDay: dayIn(to - 1, tz), label, phrase, granularity, groupBy: granularity, modelGroupBy: `model,${granularity}` }
}

// fmtBucket: display form of a C1 label (chart ticks and tooltips).
export function fmtBucket(b: string, g: Granularity) {
  if (g === 'hour') {
    const [d, h] = b.split('T')
    return `${fmtDay(d!)} ${h!}:00`
  }
  if (g === 'day') return fmtDay(b)
  if (g === 'week') {
    const y = +b.slice(0, 4)
    const w = +b.slice(6)
    return `${fmtDay(mondayOfIsoWeek(y, w))} (W${w})`
  }
  if (g === 'month') {
    const [y, m] = b.split('-').map(Number)
    return new Date(y!, m! - 1, 1).toLocaleDateString(undefined, { month: 'short', year: 'numeric' })
  }
  return b
}

export const PER_WORD: Record<Granularity, string> = { hour: 'hourly', day: 'daily', week: 'weekly', month: 'monthly', year: 'yearly' }
export const PER_UNIT: Record<Granularity, string> = { hour: 'hr', day: 'day', week: 'wk', month: 'mo', year: 'yr' }
