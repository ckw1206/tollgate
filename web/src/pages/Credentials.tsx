import { useState } from 'react'
import { api, ApiError, when } from '../api'
import { useAuth } from '../auth'
import {
  Button, ConfirmDialog, ErrorText, PageHeader, RowActions, rowsOf, Table, useLoad,
} from '../components/ui'
import CredentialDialog from '../components/CredentialDialog'
import type { Credential } from '../types'


export default function Credentials() {
  const { can } = useAuth()
  const writable = can('models:write')
  const creds = useLoad<Credential[]>('/console/api/v1/credentials')
  const [open, setOpen] = useState(false)
  const [deleting, setDeleting] = useState<Credential | null>(null)

  const remove = (c: Credential) =>
    api(`/console/api/v1/credentials/${c.id}`, { method: 'DELETE' })
      .catch((e: ApiError) => {
        throw e.status === 409 ? new Error('Models still use this credential. Repoint them first.') : e
      })
      .finally(creds.reload)

  return (
    <>
      <PageHeader
        title="Credentials"
        description="Secrets are encrypted at rest and can't be viewed again. To rotate, add a new credential and repoint models."
      >
        {writable && <Button onClick={() => setOpen(true)}>Add credential</Button>}
      </PageHeader>
      <div className="mb-4"><ErrorText>{creds.error}</ErrorText></div>
      <Table
        rows={rowsOf(creds)}
        empty="No credentials yet."
        cols={[
          { head: 'Name', cell: (c) => c.name },
          { head: 'Kind', cell: (c) => c.kind },
          { head: 'Created', cell: (c) => when(c.created_at) },
          { head: '', cell: (c) => writable && <RowActions><Button size="sm" variant="danger" onClick={() => setDeleting(c)}>Delete</Button></RowActions> },
        ]}
      />
      {open && (
        <CredentialDialog
          onClose={() => setOpen(false)}
          onSaved={() => {
            setOpen(false)
            creds.reload()
          }}
        />
      )}
      {deleting && (
        <ConfirmDialog
          title={`Delete "${deleting.name}"?`}
          body="The encrypted secret is removed. Models must be repointed to another credential first."
          confirm="Delete"
          onConfirm={() => remove(deleting)}
          onClose={() => setDeleting(null)}
        />
      )}
    </>
  )
}
