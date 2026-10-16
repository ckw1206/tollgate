import { describe, expect, it } from 'vitest'
import { applyState, diffPicker, groupByProvider, initialPickerState, triggerSummary, type PickerModel } from './modelPicker'

const pm = (name: string, provider: string, enabled = true): PickerModel => ({ name, provider, enabled })

describe('initialPickerState', () => {
  it('starts in All mode with an empty draft when nothing is saved', () => {
    const s = initialPickerState([])
    expect(s.mode).toBe('all')
    expect(s.draft).toEqual(new Set())
  })

  it('starts in Selected mode with a copy of the saved list', () => {
    const saved = ['b', 'a']
    const s = initialPickerState(saved)
    expect(s.mode).toBe('selected')
    expect(s.draft).toEqual(new Set(['a', 'b']))
    // the draft is a Set copy: mutating it must not touch the saved list
    s.draft.add('c')
    expect(saved).toEqual(['b', 'a'])
  })

  it('carries saved names that are no longer options along in the draft', () => {
    const s = initialPickerState(['ghost'])
    expect(s.mode).toBe('selected')
    expect(s.draft).toEqual(new Set(['ghost']))
  })
})

describe('diffPicker', () => {
  it('computes added, removed and changed', () => {
    expect(diffPicker(['a', 'b'], ['b', 'c'])).toEqual({ added: ['c'], removed: ['a'], changed: true })
  })

  it('sorts the diffs by name', () => {
    expect(diffPicker([], ['c', 'a', 'b']).added).toEqual(['a', 'b', 'c'])
    expect(diffPicker(['c', 'a', 'b'], []).removed).toEqual(['a', 'b', 'c'])
  })

  it('is unchanged when nothing moved', () => {
    expect(diffPicker(['a', 'b'], ['a', 'b'])).toEqual({ added: [], removed: [], changed: false })
  })

  it('carries stale names that stay on both sides; dropping one counts only the real move', () => {
    expect(diffPicker(['stale', 'a'], ['stale', 'a'])).toEqual({ added: [], removed: [], changed: false })
    expect(diffPicker(['stale', 'a'], ['stale'])).toEqual({ added: [], removed: ['a'], changed: true })
  })
})

describe('applyState', () => {
  it('All mode with nothing saved: no changes', () => {
    expect(applyState('all', [], [], 5, 0)).toEqual({ applyable: false, left: 'No changes', destructive: false })
  })

  it('All mode with a saved list: switch to all models', () => {
    expect(applyState('all', ['a'], [], 5, 0)).toEqual({ applyable: true, left: 'Switch to all models', destructive: false })
  })

  it('Selected mode with no picks: destructive prompt', () => {
    expect(applyState('selected', ['a'], [], 5, 0)).toEqual({ applyable: false, left: 'Pick at least one model, or choose All models.', destructive: true })
  })

  it('the empty-picks rule is checked before no-changes', () => {
    expect(applyState('selected', [], [], 0, 0)).toEqual({ applyable: false, left: 'Pick at least one model, or choose All models.', destructive: true })
  })

  it('Selected mode without changes: no changes', () => {
    expect(applyState('selected', ['a'], ['a'], 5, 1)).toEqual({ applyable: false, left: 'No changes', destructive: false })
  })

  it('formats the changed mode', () => {
    expect(applyState('selected', ['a'], ['a', 'b'], 5, 2)).toEqual({ applyable: true, left: '2 of 5 selected · +1', destructive: false })
    expect(applyState('selected', ['a', 'b', 'c'], ['a', 'c'], 5, 3)).toEqual({ applyable: true, left: '3 of 5 selected · −1', destructive: false })
    expect(applyState('selected', ['a', 'b'], ['b', 'c'], 5, 2)).toEqual({ applyable: true, left: '2 of 5 selected · +1 −1', destructive: false })
  })
})

describe('triggerSummary', () => {
  it('reports All when nothing is saved', () => {
    expect(triggerSummary([], 5)).toEqual({ all: true, chips: [], more: 0, label: '' })
  })

  it('shows the first three names sorted, a more count and the k of n label', () => {
    expect(triggerSummary(['e', 'c', 'a', 'b', 'd'], 7)).toEqual({ all: false, chips: ['a', 'b', 'c'], more: 2, label: '5 of 7' })
  })
})

describe('groupByProvider', () => {
  it('sorts providers and models by name and counts picks per provider', () => {
    const groups = groupByProvider([pm('beta', 'zeta'), pm('alpha', 'zeta', false), pm('gamma', 'alpha')], new Set(['beta', 'gamma']))
    expect(groups).toEqual([
      { provider: 'alpha', on: 1, total: 1, models: [pm('gamma', 'alpha')] },
      { provider: 'zeta', on: 1, total: 2, models: [pm('alpha', 'zeta', false), pm('beta', 'zeta')] },
    ])
  })
})
