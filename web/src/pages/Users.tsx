import { useState } from 'react'
import { short, when } from '../api'
import { useAuth } from '../auth'
import CreateUserDialog from '../components/CreateUserDialog'
import EditUserDialog from '../components/EditUserDialog'
import InviteDialog from '../components/InviteDialog'
import { Badge, Button, ErrorText, PageHeader, RowActions, rowsOf, SecretDialog, Table, useLoad } from '../components/ui'
import { PEOPLE_TABS } from '../Layout'
import type { Group, User } from '../types'

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
  const [inviting, setInviting] = useState(false)
  const [url, setUrl] = useState('')
  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<User | null>(null)
  const [created, setCreated] = useState<{ email: string; password: string } | null>(null)

  const groupOf = (u: User) => groups.data?.find((g) => g.id === u.group_id)
  const groupName = (id: string) => groups.data?.find((g) => g.id === id)?.name ?? short(id)
  const editable = (u: User) => writable && u.id !== me?.user.id // never your own row

  return (
    <>
      <PageHeader
        title="Users & groups"
        tabs={PEOPLE_TABS}
        description="No self-signup — create an account directly, or send a one-time invite link."
      >
        {writable && <Button disabled={!groups.data} onClick={() => setCreating(true)}>Create user</Button>}
        {can('invites:write') && <Button variant="ghost" disabled={!groups.data} onClick={() => setInviting(true)}>Invite user</Button>}
      </PageHeader>
      <div className="mb-4"><ErrorText>{users.error}</ErrorText></div>
      <Table
        rows={rowsOf(users)}
        empty="No users yet."
        cols={[
          { head: 'Name', cell: (u) => u.name || u.email },
          { head: 'Email', cell: (u) => u.email },
          { head: 'Role', cell: (u) => u.role },
          { head: 'Group', cell: (u) => groupName(u.group_id) },
          { head: 'Models', cell: (u) => modelsText(u, groupOf(u)) },
          { head: 'Joined', cell: (u) => when(u.created_at) },
          { head: 'Status', cell: (u) => (u.disabled ? <Badge tone="error">disabled</Badge> : <Badge tone="success">active</Badge>) },
          {
            head: '',
            cell: (u) =>
              editable(u) && (
                <RowActions>
                  <Button size="sm" variant="ghost" onClick={() => setEditing(u)}>Edit</Button>
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
      {creating && groups.data && (
        <CreateUserDialog
          groups={groups.data}
          onClose={() => setCreating(false)}
          onCreated={(email, password) => {
            setCreating(false)
            setCreated({ email, password })
            users.reload()
          }}
        />
      )}
      {editing && (
        <EditUserDialog
          user={editing}
          groups={groups.data ?? []}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null)
            users.reload()
          }}
        />
      )}
      {url && <SecretDialog title="Invite link" body="Send this link to the invitee. It works once." value={url} onClose={() => setUrl('')} />}
      {created && (
        <SecretDialog
          title={`Password for "${created.email}"`}
          body={`Send this to ${created.email}. They sign in with it.`}
          value={created.password}
          onClose={() => setCreated(null)}
        />
      )}
    </>
  )
}
