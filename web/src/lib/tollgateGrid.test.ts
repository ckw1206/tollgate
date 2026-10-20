import { describe, expect, it } from 'vitest'
import { alpha } from './tollgateGrid'

describe('alpha', () => {
  it('mixes the colour with transparent at the given percentage', () => {
    expect(alpha('var(--foreground)', 15)).toBe('color-mix(in srgb, var(--foreground) 15%, transparent)')
  })

  it('keeps fractional percentages', () => {
    expect(alpha('oklch(0.985 0 0)', 7.2)).toBe('color-mix(in srgb, oklch(0.985 0 0) 7.2%, transparent)')
  })

  it('is fully transparent at 0 and keeps the colour at 100', () => {
    expect(alpha('black', 0)).toBe('color-mix(in srgb, black 0%, transparent)')
    expect(alpha('black', 100)).toBe('color-mix(in srgb, black 100%, transparent)')
  })
})
