import { describe, expect, it } from 'vitest'
import { resolveMode } from './theme'

// Pre-paint theme: the stored pick (tollgate.theme = 'light' | 'dark') wins;
// with no pick, the OS preference decides. System is stored by removing the
// key, so a 'system' value can only be garbage — treat anything else as no pick.
describe('resolveMode', () => {
  it('a stored pick wins over the OS preference', () => {
    expect(resolveMode('light', true)).toBe('light')
    expect(resolveMode('dark', false)).toBe('dark')
  })

  it('no stored pick: the OS preference decides', () => {
    expect(resolveMode(null, true)).toBe('dark')
    expect(resolveMode(null, false)).toBe('light')
  })

  it('a stored value that is not a pick falls back to the OS preference', () => {
    expect(resolveMode('system', true)).toBe('dark')
    expect(resolveMode('Light', false)).toBe('light')
    expect(resolveMode('', true)).toBe('dark')
  })
})
