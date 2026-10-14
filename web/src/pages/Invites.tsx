import { useState } from 'react'
import { api, short, when } from '../api'
import { useAuth } from '../auth'
import InviteDialog from '../components/InviteDialog'
import { Badge, Button, ConfirmDialog, ErrorText, PageHeader, RowActions, rowsOf, SecretDialog, Table, useLoad, type Tone } from '../components/ui'
import { PEOPLE_TABS } from '../Layout'
import type { Group, Invite, User } from '../types'

const tone: Record<Invite['status'], Tone> = { active: 'success', used: 'inactive', revoked: 'error', expired: 'warning' }

export default function Invites() {
  const { can } = useAuth()
  const writable = can('invites:write')
  const invites = useLoad<Invite[]>('/console/api/v1/invites')
  const groups = useLoad<Group[]>('/console/api/v1/groups')
  const users = useLoad<User[]>(can('users:read') ? '/console/api/v1/users' : null)
  const [creating, setCreating] = useState(false)
  const [url, setUrl] = useState('')
  const [revoking, setRevoking] = useState<Invite | null>(null)

  const groupName = (id: string) => groups.data?.find((g) => g.id === id)?.name ?? short(id)
  const userEmail = (id: string) => users.data?.find((u) => u.id === id)?.email ?? short(id)

  return (
    <>
      <PageHeader title="Users & groups" tabs={PEOPLE_TABS}>{writable && <Button disabled={!groups.data} onClick={() => setCreating(true)}>Create invite</Button>}</PageHeader>
      <div className="mb-4"><ErrorText>{invites.error}</ErrorText></div>
      <Table
        rows={rowsOf(invites)}
        empty="No invites yet."
        cols={[
          { head: 'For', cell: (i) => i.email || 'anyone with the link' },
          { head: 'Role', cell: (i) => i.role },
          { head: 'Group', cell: (i) => groupName(i.group_id) },
          { head: 'Created by', cell: (i) => userEmail(i.created_by) },
          { head: 'Expires', cell: (i) => when(i.expires_at) },
          { head: 'Status', cell: (i) => <Badge tone={tone[i.status]}>{i.status}</Badge> },
          { head: '', cell: (i) => writable && i.status === 'active' && <RowActions><Button size="sm" variant="danger" onClick={() => setRevoking(i)}>Revoke</Button></RowActions> },
        ]}
      />
      {creating && groups.data && (
        <InviteDialog
          groups={groups.data}
          onClose={() => setCreating(false)}
          onCreated={(u) => {
            setCreating(false)
            setUrl(u)
            invites.reload()
          }}
        />
      )}
      {url && <SecretDialog title="Invite link" body="Send this link to the invitee. It works once." value={url} onClose={() => setUrl('')} />}
      {revoking && (
        <ConfirmDialog
          title={`Revoke invite for "${revoking.email || 'anyone with the link'}"?`}
          body="The link stops working immediately."
          confirm="Revoke"
          onConfirm={() => api(`/console/api/v1/invites/${revoking.id}`, { method: 'DELETE' }).finally(invites.reload)}
          onClose={() => setRevoking(null)}
        />
      )}
    </>
  )
}
