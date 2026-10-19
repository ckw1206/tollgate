import type { Group, Model, Role } from '../types'
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

// Body for PUT /users/{id} (shared save logic for the user Edit dialog). Extras are cleared only
// when the group is resolved and it allows all models (the field is hidden then); an unresolved
// group keeps the user's existing extras instead of silently wiping them. An empty password omits
// the field — absent (or empty) means keep the current one, per the backend contract.
export function buildUserUpdateBody(args: { role: Role; group_id: string; extra: string[]; enabled: boolean; password: string; group: Group | undefined }) {
  const { role, group_id, extra, enabled, password, group } = args
  return {
    role,
    group_id,
    extra_models: group && group.models.length === 0 ? [] : extra,
    disabled: !enabled,
    ...(password ? { password } : {}),
  }
}
