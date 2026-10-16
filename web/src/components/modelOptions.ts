import type { Group, Model } from '../types'
import type { MultiOption } from './ui'

// Options for a user's extra grants: enabled models the group doesn't already include,
// sorted by name (shared by ModelAccessDialog and the user Edit dialog). stale = granted
// names that are no longer options; enabledNames backs the "can use" chips.
export function extraModelOptions(models: Model[] | null, group: Group, extra: string[]) {
  const enabled = (models ?? []).filter((m) => m.enabled).sort((a, b) => a.name.localeCompare(b.name))
  const options: MultiOption[] = enabled
    .filter((m) => !group.models.includes(m.name))
    .map((m) => ({ id: m.name, label: m.name, meta: m.provider }))
  const stale = models ? extra.filter((n) => !options.some((o) => o.id === n)) : []
  return { options, stale, enabledNames: enabled.map((m) => m.name) }
}
