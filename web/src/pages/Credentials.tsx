import { useState, type FormEvent } from 'react'
import { api, ApiError, when } from '../api'
import { useAuth } from '../auth'
import {
  Button, ConfirmDialog, controlCls, ErrorText, Field, inputCls, Modal, ModalFooter, PageHeader, RowActions, rowsOf, Table, useLoad,
} from '../components/ui'
import type { Credential } from '../types'

const blank = { name: '', kind: 'api_key', secret: '' }

export default function Credentials() {
  const { can } = useAuth()
  const writable = can('models:write')
  const creds = useLoad<Credential[]>('/console/api/v1/credentials')
  const [open, setOpen] = useState(false)
  const [form, setForm] = useState(blank)
  const [deleting, setDeleting] = useState<Credential | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  // The secret is write-only: it lives in form state only while the dialog is open.
  const close = () => {
    setOpen(false)
    setForm(blank)
  }

  const save = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      await api('/console/api/v1/credentials', { method: 'POST', body: form })
      close()
      creds.reload()
    } catch (err) {
      setError((err as ApiError).status === 409 ? 'A credential with this name already exists.' : (err as Error).message)
    } finally {
      setBusy(false)
    }
  }

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
        {writable && <Button onClick={() => { setOpen(true); setError('') }}>Add credential</Button>}
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
        <Modal title="Add credential" onClose={close}>
          <form onSubmit={save} className="space-y-4">
            <Field label="Name" hint="Can't be renamed later."><input className={inputCls} required value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} /></Field>
            <Field label="Kind">
              <select className={inputCls} value={form.kind} onChange={(e) => setForm({ ...form, kind: e.target.value })}>
                <option value="api_key">API key</option>
                <option value="gcp_service_account">GCP service account (Vertex)</option>
              </select>
            </Field>
            <Field label={form.kind === 'api_key' ? 'API key' : 'Service account JSON'}>
              {form.kind === 'api_key' ? (
                <input className={inputCls + ' font-mono'} type="password" autoComplete="off" required value={form.secret} onChange={(e) => setForm({ ...form, secret: e.target.value })} />
              ) : (
                <textarea className={controlCls + ' mt-1 block h-32 w-full py-1 font-mono text-xs'} required value={form.secret} onChange={(e) => setForm({ ...form, secret: e.target.value })} />
              )}
            </Field>
            <ErrorText>{error}</ErrorText>
            <ModalFooter>
              <Button type="button" variant="ghost" onClick={close}>Cancel</Button>
              <Button disabled={busy}>{busy ? 'Saving…' : 'Save'}</Button>
            </ModalFooter>
          </form>
        </Modal>
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
