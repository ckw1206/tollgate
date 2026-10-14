import { useState } from 'react'
import { api, short, when } from '../api'
import { useAuth } from '../auth'
import InviteDialog from '../components/InviteDialog'
import ModelAccessDialog from '../components/ModelAccessDialog'
import { Badge, Button, ConfirmDialog, controlBox, ErrorText, PageHeader, RowActions, rowsOf, SecretDialog, Table, useLoad } from '../components/ui'
import { PEOPLE_TABS } from '../Layout'
import type { Group, User } from '../types'

const inlineSelect = controlBox + ' h-7 min-w-[104px] px-2 py-0 text-[13px] text-foreground disabled:opacity-50'

// modelsText: "group (all)", "group + a, b" (extra grants) or "group (N)".
function modelsText(u: User, g: Group | undefined) {
  if (!g) return '—'
  if (!g.models.length) return 'group (all)'
  return u.extra_models.length ? `group + ${u.extra_models.join(', ')}` : `group (${g.models.length})`
}

export default function Users() {
  const { me, can } = useAuth()
  const users = useLoad<User[]>('/console/api/v1/users')
  const groups = useLoad<Group[]>('/console/api/v1/groups')
  const writable = can('users:write')
  const [error, setError] = useState('')
  const [inviting, setInviting] = useState(false)
  const [url, setUrl] = useState('')
  const [disabling, setDisabling] = useState<User | null>(null)
  const [access, setAccess] = useState<User | null>(null)

  const update = (u: User, patch: Partial<Pick<User, 'role' | 'group_id' | 'disabled'>>) =>
    api(`/console/api/v1/users/${u.id}`, { method: 'PUT', body: patch }).finally(users.reload)
  const inline = (u: User, patch: Partial<Pick<User, 'role' | 'group_id' | 'disabled'>>) => {
    setError('')
    update(u, patch).catch((e: Error) => setError(e.message))
  }
  const groupOf = (u: User) => groups.data?.find((g) => g.id === u.group_id)
  const groupName = (id: string) => groups.data?.find((g) => g.id === id)?.name ?? short(id)
  const editable = (u: User) => writable && u.id !== me?.user.id // never your own row

  return (
    <>
      <PageHeader
        title="Users & groups"
        tabs={PEOPLE_TABS}
        description={'Tollgate is invite-only — there is no create-user API. "Invite user" creates a one-time invite link.'}
      >
        {can('invites:write') && <Button disabled={!groups.data} onClick={() => setInviting(true)}>Invite user</Button>}
      </PageHeader>
      <div className="mb-4 space-y-1">
        <ErrorText>{users.error}</ErrorText>
        <ErrorText>{error}</ErrorText>
      </div>
      <Table
        rows={rowsOf(users)}
        empty="No users yet."
        cols={[
          { head: 'Name', cell: (u) => u.name },
          { head: 'Email', cell: (u) => u.email },
          {
            head: 'Role',
            cell: (u) =>
              editable(u) ? (
                <select aria-label={`Role for ${u.email}`} className={inlineSelect} value={u.role} onChange={(e) => inline(u, { role: e.target.value as User['role'] })}>
                  <option value="admin">admin</option>
                  <option value="member">member</option>
                  <option value="viewer">viewer</option>
                </select>
              ) : (
                u.role
              ),
          },
          {
            head: 'Group',
            cell: (u) =>
              editable(u) && groups.data ? (
                <select aria-label={`Group for ${u.email}`} className={inlineSelect} value={u.group_id} onChange={(e) => inline(u, { group_id: e.target.value })}>
                  {groups.data.map((g) => <option key={g.id} value={g.id}>{g.name}</option>)}
                </select>
              ) : (
                groupName(u.group_id)
              ),
          },
          { head: 'Models', cell: (u) => modelsText(u, groupOf(u)) },
          { head: 'Joined', cell: (u) => when(u.created_at) },
          { head: 'Status', cell: (u) => (u.disabled ? <Badge tone="error">disabled</Badge> : <Badge tone="success">active</Badge>) },
          {
            head: '',
            cell: (u) =>
              editable(u) && (
                <RowActions>
                  <Button size="sm" variant="ghost" disabled={!groupOf(u)} onClick={() => setAccess(u)}>Model access</Button>
                  {u.disabled ? (
                    <Button size="sm" variant="ghost" onClick={() => inline(u, { disabled: false })}>Enable</Button>
                  ) : (
                    <Button size="sm" variant="danger" onClick={() => setDisabling(u)}>Disable</Button>
                  )}
                </RowActions>
              ),
          },
        ]}
      />
      {inviting && groups.data && (
        <InviteDialog
          groups={groups.data}
          onClose={() => setInviting(false)}
          onCreated={(u) => {
            setInviting(false)
            setUrl(u)
          }}
        />
      )}
      {access && groupOf(access) && (
        <ModelAccessDialog
          user={access}
          group={groupOf(access)!}
          onClose={() => setAccess(null)}
          onSaved={() => {
            setAccess(null)
            users.reload()
          }}
        />
      )}
      {url && <SecretDialog title="Invite link" body="Send this link to the invitee. It works once." value={url} onClose={() => setUrl('')} />}
      {disabling && (
        <ConfirmDialog
          title={`Disable "${disabling.email}"?`}
          body="They're signed out and their API keys stop working immediately."
          confirm="Disable"
          onConfirm={() => update(disabling, { disabled: true })}
          onClose={() => setDisabling(null)}
        />
      )}
    </>
  )
}
