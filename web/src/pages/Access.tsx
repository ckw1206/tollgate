import { useState } from 'react'
import { api } from '../api'
import { useAuth } from '../auth'
import ModelAccessDialog from '../components/ModelAccessDialog'
import { Button, CheckButton, ErrorText, PageHeader, RowActions, Table, Toast, useLoad, type Col } from '../components/ui'
import { PEOPLE_TABS } from '../Layout'
import type { Affected, Group, Model, User } from '../types'

// Model access tab: which models each group may use (models [] = All), saved on every click
// with a 5 s Undo, then the users with extra grants on top of their group.
export default function Access() {
  const { me, can } = useAuth()
  const groups = useLoad<Group[]>('/console/api/v1/groups')
  const models = useLoad<Model[]>('/console/api/v1/models')
  const users = useLoad<User[]>(can('users:read') ? '/console/api/v1/users' : null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [toast, setToast] = useState<{ id: number; text: string; undo: () => void } | null>(null)
  const [editing, setEditing] = useState<User | null>(null)
  const sorted = [...(models.data ?? [])].sort((a, b) => a.name.localeCompare(b.name))
  const names = sorted.map((m) => m.name) // every model, disabled included: they can be granted ahead of time
  const isAll = (g: Group) => g.models.length === 0

  // PUT /groups/{id} replaces the whole group: resend it as loaded, with the new list. Cells
  // stay disabled until the reload lands, so the next click starts from the saved list.
  const save = async (g: Group, list: string[], undo: boolean) => {
    setBusy(true)
    setError('')
    try {
      const { affected_keys: n } = await api<Affected<Group>>(`/console/api/v1/groups/${g.id}`, {
        method: 'PUT',
        body: { name: g.name, budget_type: g.budget_type, budget_nanos: g.budget_nanos, rpm_limit: g.rpm_limit, tpm_limit: g.tpm_limit, models: list },
      })
      if (undo) setToast(null)
      else {
        const keys = n > 0 ? ` ${n} ${n === 1 ? 'key lists' : 'keys list'} models it no longer allows.` : ''
        setToast({ id: Date.now(), text: `Saved "${g.name}".${keys}`, undo: () => save(g, g.models, true) })
      }
    } catch (err) {
      setError((err as Error).message)
    }
    await groups.reload()
    setBusy(false)
  }

  // Unticking All keeps just the first model; unticking a model on an All row keeps every
  // other one; ticking the last missing model is All again. A list never goes empty ([] = All).
  const toggle = (g: Group, m?: string) => {
    if (!m) return save(g, isAll(g) ? names.slice(0, 1) : [], false)
    const base = isAll(g) ? names : g.models.filter((n) => names.includes(n))
    const next = base.includes(m) ? base.filter((n) => n !== m) : [...base, m]
    if (next.length) save(g, next.length === names.length ? [] : next, false)
  }

  const sub = (g: Group) => {
    const k = names.filter((n) => g.models.includes(n)).length
    return `${g.members} ${g.members === 1 ? 'member' : 'members'} · ${isAll(g) ? 'all models' : `${k} of ${names.length}`}`
  }
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
    { head: 'All models', center: true, cell: (g) => <CheckButton on={isAll(g)} disabled={busy} aria-label={`${g.name}: all models`} onClick={() => toggle(g)} /> },
    ...sorted.map((m): Col<Group> => ({
      head: <code className={`font-mono text-xs ${m.enabled ? '' : 'opacity-50'}`} title={m.enabled ? undefined : 'Disabled'}>{m.name}</code>,
      center: true,
      cell: (g) => (
        <CheckButton
          on={isAll(g) || g.models.includes(m.name)}
          dim={isAll(g)}
          title={isAll(g) ? 'Included via All models' : undefined}
          disabled={busy}
          aria-label={`${g.name}: ${m.name}`}
          onClick={() => toggle(g, m.name)}
        />
      ),
    })),
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
        <Toast key={toast.id} action="Undo" onAction={toast.undo} onClose={() => setToast(null)}>
          {toast.text}
        </Toast>
      )}
    </>
  )
}
