import { useState } from 'react'
import { api } from '../api'
import { useAuth } from '../auth'
import ModelAccessDialog from '../components/ModelAccessDialog'
import { Button, ErrorText, ModelPicker, PageHeader, RowActions, Table, Toast, useLoad, type Col } from '../components/ui'
import { PEOPLE_TABS } from '../Layout'
import type { Affected, Group, Model, User } from '../types'

// Model access tab: which models each group may use (models [] = All), saved on every click
// with a 5 s Undo, then the users with extra grants on top of their group.
export default function Access() {
  const { me, can } = useAuth()
  const groups = useLoad<Group[]>('/console/api/v1/groups')
  const models = useLoad<Model[]>('/console/api/v1/models')
  const users = useLoad<User[]>(can('users:read') ? '/console/api/v1/users' : null)
  const [error, setError] = useState('')
  const [toast, setToast] = useState<{ id: number; text: string; undo?: () => void } | null>(null)
  const [editing, setEditing] = useState<User | null>(null)
  const sorted = [...(models.data ?? [])].sort((a, b) => a.name.localeCompare(b.name))
  const names = sorted.map((m) => m.name) // every model, disabled included: they can be granted ahead of time
  const isAll = (g: Group) => g.models.length === 0

  // PUT /groups/{id} replaces the whole group: resend it as loaded, with the new list. The
  // picker closes before the PUT, so the next save starts from the reloaded list; the Undo
  // toast waits for the reload too, so Undo can't start a save before it lands.
  const save = async (g: Group, list: string[], undo: boolean) => {
    setError('')
    setToast(null) // any save supersedes the last Undo, so it can't race this PUT
    let next: typeof toast = null
    try {
      const { affected_keys: n } = await api<Affected<Group>>(`/console/api/v1/groups/${g.id}`, {
        method: 'PUT',
        body: { name: g.name, budget_type: g.budget_type, budget_nanos: g.budget_nanos, rpm_limit: g.rpm_limit, tpm_limit: g.tpm_limit, models: list },
      })
      // Undo restores the list as the UI saw it: names of deleted models are gone from the saved
      // list, and the server only keeps unknown names that are still stored. A list of nothing
      // but such names has no undo: [] would mean All.
      const prev = g.models.filter((m) => names.includes(m))
      if (!undo) {
        const keys = n > 0 ? ` ${n} ${n === 1 ? 'key lists' : 'keys list'} models it no longer allows.` : ''
        const count = list.length ? `${list.length} of ${names.length} models` : 'all models'
        next = { id: Date.now(), text: `Saved "${g.name}" — ${count}.${keys}`, undo: prev.length || isAll(g) ? () => save(g, prev, true) : undefined }
      }
    } catch (err) {
      setError((err as Error).message)
    }
    await groups.reload()
    setToast(next)
  }

  const sub = (g: Group) => `${g.members} ${g.members === 1 ? 'member' : 'members'}`
  const cols: Col<Group>[] = [
    {
      head: 'Group',
      cell: (g) => (
        <span className="flex flex-col leading-tight">
          <span>{g.name}</span>
          <span className="text-xs font-normal text-muted-foreground">{sub(g)}</span>
        </span>
      ),
    },
    {
      head: 'Allowed models',
      cell: (g) => (
        <ModelPicker
          group={g.name}
          models={(models.data ?? []).map((m) => ({ name: m.name, provider: m.provider, enabled: m.enabled }))}
          saved={g.models}
          editable={can('groups:write')}
          onApply={(list) => void save(g, list, false)}
        />
      ),
    },
  ]

  const groupOf = (u: User) => groups.data?.find((g) => g.id === u.group_id)
  const effective = (u: User) => {
    const g = groupOf(u)
    if (!g) return '—'
    return isAll(g) ? 'all (group allows all)' : [...new Set([...g.models, ...u.extra_models])].join(', ')
  }
  const granted = users.data ? users.data.filter((u) => u.extra_models.length) : users.error ? [] : null
  const editable = (u: User) => can('users:write') && u.id !== me?.user.id && !!groupOf(u) // never your own row

  return (
    <>
      <PageHeader title="Users & groups" tabs={PEOPLE_TABS} description="Which models each group may use. Individual users can be granted extra models; keys narrow from there." />
      <div className="mb-4 space-y-1">
        <ErrorText>{groups.error || models.error}</ErrorText>
        <ErrorText>{error}</ErrorText>
      </div>
      <div className="flex flex-col gap-5">
        <Table rows={groups.data && models.data ? groups.data : groups.error || models.error ? [] : null} empty="No groups yet." cols={cols} />
        {can('users:read') && (
          <div className="flex flex-col gap-2">
            <span className="text-sm font-medium text-muted-foreground">Users with extra grants</span>
            <ErrorText>{users.error}</ErrorText>
            <Table
              rows={granted}
              empty="No users have extra grants."
              cols={[
                { head: 'User', cell: (u) => u.name || u.email },
                { head: 'Group', cell: (u) => groupOf(u)?.name ?? '—' },
                {
                  head: 'Extra grants',
                  cell: (u) => (
                    <span className="flex gap-1">
                      {u.extra_models.map((n) => <code key={n} className="bg-secondary px-1.5 py-0.5 font-mono text-xs text-secondary-foreground">{n}</code>)}
                    </span>
                  ),
                },
                { head: 'Effective', cell: (u) => <span className="text-muted-foreground">{effective(u)}</span> },
                { head: '', cell: (u) => editable(u) && <RowActions><Button size="sm" variant="ghost" onClick={() => setEditing(u)}>Edit</Button></RowActions> },
              ]}
            />
          </div>
        )}
        <p className="text-[13px] text-muted-foreground">
          A user may use their group's models plus their own extra grants, and each key can narrow that further. All models includes models added later. Disabled models can be granted ahead of time.
        </p>
      </div>
      {editing && groupOf(editing) && (
        <ModelAccessDialog
          user={editing}
          group={groupOf(editing)!}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null)
            users.reload()
          }}
        />
      )}
      {toast && (
        <Toast key={toast.id} action={toast.undo && 'Undo'} onAction={toast.undo} onClose={() => setToast(null)}>
          {toast.text}
        </Toast>
      )}
    </>
  )
}
