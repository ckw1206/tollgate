import { describe, expect, it } from 'vitest'
import { sparkPoints } from './sparkline'

// sparkPoints: the polyline of a Sparkline — min-max normalised into a w×h
// box with a 1px pad. The SVG must never see a NaN (the flat-series case
// used to divide by max - min = 0).
describe('sparkPoints', () => {
  it('fewer than 2 values: no points', () => {
    expect(sparkPoints([])).toBe('')
    expect(sparkPoints([5])).toBe('')
  })

  it('a flat series renders a mid-height line, no NaN', () => {
    const pts = sparkPoints([3, 3, 3, 3])
    expect(pts).not.toContain('NaN')
    const all = pts.split(' ')
    expect(all).toHaveLength(4)
    // y = (24 - 2) / 2 + 1 = 12; x spans 1..99
    expect(all[0]).toBe('1.00,12.00')
    expect(all[3]).toBe('99.00,12.00')
    expect(new Set(all.map((p) => p.split(',')[1]))).toEqual(new Set(['12.00']))
  })

  it('min-max scaling: endpoints hit the box edges, interior scales', () => {
    // y = 23 - ((v - min) / (max - min)) * 22: 0 -> bottom 23, 10 -> top 1
    const all = sparkPoints([0, 5, 10]).split(' ')
    expect(all).toEqual(['1.00,23.00', '50.00,12.00', '99.00,1.00'])
  })

  it('honours custom sizes', () => {
    // x = (i / (n - 1)) * (w - 2) + 1; y = h - 1 - ((v - min) / (max - min)) * (h - 2)
    expect(sparkPoints([0, 10], 50, 10)).toBe('1.00,9.00 49.00,1.00')
  })
})
