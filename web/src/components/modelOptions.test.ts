import { describe, expect, it } from 'vitest'
import type { Group, Model } from '../types'
import { buildUserUpdateBody, extraModelOptions } from './modelOptions'

const model = (name: string, enabled: boolean): Model => ({
  id: name,
  name,
  provider: 'openai',
  upstream_model: name,
  api_base: '',
  credential_id: null,
  enabled,
  price_input_micros: 0,
  price_output_micros: 0,
  price_cache_read_micros: null,
  price_cache_write_micros: null,
  context_window: null,
  extra: null,
  created_at: '2026-01-01T00:00:00Z',
})

const group = (models: string[]): Group => ({
  id: 'g',
  name: 'g',
  models,
  members: 0,
  created_at: '2026-01-01T00:00:00Z',
  budget_type: 'unlimited',
  budget_nanos: 0,
  spend_nanos: 0,
  budget_reset_at: null,
  rpm_limit: null,
  tpm_limit: null,
})

const a = model('a', true)
const b = model('b', true)
const c = model('c', false)

describe('extraModelOptions', () => {
  it('offers enabled models the group doesn\'t already include, sorted by name', () => {
    const r = extraModelOptions([a, b, c], group(['a']), [])
    // c is excluded as disabled, a as already in the group.
    expect(r.options).toEqual([{ id: 'b', label: 'b', meta: 'openai' }])
    expect(r.stale).toEqual([])
    expect(r.enabledNames).toEqual(['a', 'b'])
  })

  it('flags granted names that are no longer options as stale', () => {
    const r = extraModelOptions([a, b, c], group(['a']), ['b', 'c', 'ghost'])
    expect(r.stale).toEqual(['c', 'ghost'])
  })

  it('offers every enabled model when the group allows all', () => {
    const r = extraModelOptions([a, b, c], group([]), [])
    expect(r.options.map((o) => o.id)).toEqual(['a', 'b'])
  })

  it('returns nothing while models are still loading', () => {
    const r = extraModelOptions(null, group(['a']), ['b'])
    expect(r.options).toEqual([])
    expect(r.stale).toEqual([])
  })
})

describe('buildUserUpdateBody', () => {
  const base = { role: 'member' as const, group_id: 'g', extra: ['b'], enabled: true, password: '', group: undefined as Group | undefined }

  it('preserves extras when the group is unresolved (unknown group_id)', () => {
    expect(buildUserUpdateBody({ ...base, group: undefined }).extra_models).toEqual(['b'])
  })

  it('clears extras when a resolved group allows all models', () => {
    expect(buildUserUpdateBody({ ...base, group: group([]) }).extra_models).toEqual([])
  })

  it('passes extras through for a normal resolved group', () => {
    expect(buildUserUpdateBody({ ...base, group: group(['a']) }).extra_models).toEqual(['b'])
  })

  it('omits the password field when blank, includes it when set', () => {
    expect(buildUserUpdateBody(base)).not.toHaveProperty('password')
    expect(buildUserUpdateBody({ ...base, password: 'x'.repeat(12) }).password).toBe('x'.repeat(12))
  })
})
