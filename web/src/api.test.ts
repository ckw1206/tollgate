import { describe, expect, it } from 'vitest'
import { fmtTokens } from './api'

// Contract C3 (pinned). The rule is the spec; these values freeze it, including the
// boundary cases where toFixed(1) carries into the next unit (999999 -> "1000K").
describe('fmtTokens', () => {
  it.each([
    [0, '0'],
    [950, '950'],
    [999, '999'],
    [1000, '1K'],
    [1200, '1.2K'],
    [9900, '9.9K'],
    [100000, '100K'],
    [3400000, '3.4M'],
    [999999, '1000K'],
    [1000000, '1M'],
    [999999999, '1000M'],
    [1200000000, '1.2B'],
    [1000000000, '1B'],
  ])('fmtTokens(%i) === %s', (n, want) => {
    expect(fmtTokens(n)).toBe(want)
  })
})
