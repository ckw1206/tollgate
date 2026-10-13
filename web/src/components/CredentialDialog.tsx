import { useState, type FormEvent } from 'react'
import { api, ApiError } from '../api'
import type { Credential } from '../types'
import { Button, controlCls, ErrorText, Field, inputCls, Modal, ModalFooter } from './ui'

// CredentialDialog: "Add credential", shared by the Credentials page and the Dashboard checklist.
// The secret is write-only: it lives in this component's state only while the dialog is open.
export default function CredentialDialog({ onClose, onSaved }: { onClose: () => void; onSaved: (c: Credential) => void }) {
  const [form, setForm] = useState({ name: '', kind: 'api_key', secret: '' })
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const save = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      onSaved(await api<Credential>('/console/api/v1/credentials', { method: 'POST', body: form }))
    } catch (err) {
      setError((err as ApiError).status === 409 ? 'A credential with this name already exists.' : (err as Error).message)
      setBusy(false)
    }
  }

  return (
    <Modal title="Add credential" onClose={onClose}>
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
          <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
          <Button disabled={busy}>{busy ? 'Saving…' : 'Save'}</Button>
        </ModalFooter>
      </form>
    </Modal>
  )
}
