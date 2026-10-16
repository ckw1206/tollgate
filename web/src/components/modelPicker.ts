// Pure logic for the ModelPicker in ui.tsx: picker state, the diff against the saved list,
// the footer text and the provider-grouped list. No React here, so this file stays testable.

export type PickerModel = { name: string; provider: string; enabled: boolean }
export type PickerMode = 'all' | 'selected'

// mode = 'selected' once anything is saved. The draft is a Set copy of saved: names that are
// no longer options (deleted models) are carried along — they stay in the list state but are
// never rendered, checked or removed.
export function initialPickerState(saved: string[]): { mode: PickerMode; draft: Set<string> } {
  return { mode: saved.length ? 'selected' : 'all', draft: new Set(saved) }
}

// added = draft − saved, removed = saved − draft, each sorted by name.
export function diffPicker(saved: string[], draft: string[]): { added: string[]; removed: string[]; changed: boolean } {
  const savedSet = new Set(saved)
  const draftSet = new Set(draft)
  const added = draft.filter((n) => !savedSet.has(n)).sort()
  const removed = saved.filter((n) => !draftSet.has(n)).sort()
  return { added, removed, changed: added.length > 0 || removed.length > 0 }
}

// Footer for the picker. pickCount is computed by the caller: the draft entries that are
// current options (the draft may carry stale names, which must not count). Rules in order:
// All + saved empty → No changes; All + saved → Switch to all models; selected + no picks →
// destructive prompt; selected + unchanged → No changes; selected + changed →
// "k of n selected" with " · +a" / "−r" parts for the moved counts.
export function applyState(mode: PickerMode, saved: string[], draft: string[], total: number, pickCount: number):
  { applyable: boolean; left: string; destructive: boolean } {
  if (mode === 'all') {
    return saved.length
      ? { applyable: true, left: 'Switch to all models', destructive: false }
      : { applyable: false, left: 'No changes', destructive: false }
  }
  if (pickCount === 0) return { applyable: false, left: 'Pick at least one model, or choose All models.', destructive: true }
  const { added, removed, changed } = diffPicker(saved, draft)
  if (!changed) return { applyable: false, left: 'No changes', destructive: false }
  let left = `${pickCount} of ${total} selected`
  const parts: string[] = []
  if (added.length > 0) parts.push(`+${added.length}`)
  if (removed.length > 0) parts.push(`−${removed.length}`)
  if (parts.length) left += ` · ${parts.join(' ')}`
  return { applyable: true, left, destructive: false }
}

// Trigger content: All, or the first three saved names (sorted by name) plus a more count.
export function triggerSummary(saved: string[], total: number): { all: boolean; chips: string[]; more: number; label: string } {
  if (!saved.length) return { all: true, chips: [], more: 0, label: '' }
  return { all: false, chips: [...saved].sort().slice(0, 3), more: Math.max(0, saved.length - 3), label: `${saved.length} of ${total}` }
}

// The list grouped by provider: providers and models sorted by name, on = the models of the
// provider whose name is in the draft. Call it with the search-filtered models so a provider
// with no matches is omitted.
export function groupByProvider(models: PickerModel[], draft: Set<string>):
  { provider: string; on: number; total: number; models: PickerModel[] }[] {
  const byProvider = new Map<string, PickerModel[]>()
  for (const m of models) {
    const list = byProvider.get(m.provider)
    if (list) list.push(m)
    else byProvider.set(m.provider, [m])
  }
  return [...byProvider.entries()]
    .map(([provider, ms]) => {
      ms.sort((a, b) => a.name.localeCompare(b.name))
      return { provider, on: ms.filter((m) => draft.has(m.name)).length, total: ms.length, models: ms }
    })
    .sort((a, b) => a.provider.localeCompare(b.provider))
}
