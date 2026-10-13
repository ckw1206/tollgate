import { useState, type FormEvent } from 'react'
import { api } from '../api'
import type { ApiKey, Group } from '../types'
import { LimitsFields, limitsPayload, limitsState, type LimitsState } from './LimitsFields'
import { Button, ErrorText, Field, inputCls, Modal, ModalFooter } from './ui'

// KeyDialog: "New API key" / "Edit" dialog, shared by the Keys page and the Dashboard checklist.
// On create, onSaved receives the raw key; the caller shows it once in a SecretDialog.

interface Form { alias: string; models: string; expires: string; group_id: string; limits: LimitsState }

const emptyForm = (k?: ApiKey): Form => ({
  alias: k?.alias ?? '',
  models: k?.models.join(', ') ?? '',
  expires: k?.expires_at ? toDateInput(k.expires_at) : '',
  group_id: k?.group_id ?? '',
  limits: limitsState(k),
})

// <input type="date"> works in the browser's local calendar; keep it that way both directions.
function toDateInput(iso: string) {
  const d = new Date(iso)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`
}

export default function KeyDialog({ apiKey, groups, canBillingGroup, onClose, onSaved }: {
  apiKey: ApiKey | 'new'
  groups: Group[]
  canBillingGroup: boolean // keys:write_all
  onClose: () => void
  onSaved: (res: { key: ApiKey; raw?: string }) => void
}) {
  const [form, setForm] = useState<Form>(() => emptyForm(apiKey === 'new' ? undefined : apiKey))
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const save = async (e: FormEvent) => {
    e.preventDefault()
    const body = {
      alias: form.alias,
      models: form.models.split(',').map((s) => s.trim()).filter(Boolean),
      expires_at: form.expires ? new Date(form.expires + 'T23:59:59').toISOString() : null,
      group_id: form.group_id || undefined,
      ...limitsPayload(form.limits),
    }
    setBusy(true)
    setError('')
    try {
      if (apiKey === 'new') onSaved(await api<{ key: ApiKey; raw: string }>('/console/api/v1/keys', { method: 'POST', body }))
      else onSaved({ key: await api<ApiKey>(`/console/api/v1/keys/${apiKey.id}`, { method: 'PUT', body }) })
    } catch (err) {
      setError((err as Error).message)
      setBusy(false)
    }
  }

  return (
    <Modal title={apiKey === 'new' ? 'New API key' : `Edit "${apiKey.alias}"`} onClose={onClose}>
      <form onSubmit={save} className="space-y-4">
        <Field label="Alias"><input className={inputCls} required value={form.alias} onChange={(e) => setForm({ ...form, alias: e.target.value })} /></Field>
        <Field label="Allowed models" hint="Comma-separated model names. Blank = all models.">
          <input className={inputCls} value={form.models} onChange={(e) => setForm({ ...form, models: e.target.value })} />
        </Field>
        {canBillingGroup && (
          <Field label="Billing group">
            <select className={inputCls} value={form.group_id} onChange={(e) => setForm({ ...form, group_id: e.target.value })}>
              <option value="">Owner's group</option>
              {groups.map((g) => <option key={g.id} value={g.id}>{g.name}</option>)}
            </select>
          </Field>
        )}
        <LimitsFields value={form.limits} onChange={(limits) => setForm({ ...form, limits })} />
        <Field label="Expires on" hint="Blank = never"><input className={inputCls} type="date" value={form.expires} onChange={(e) => setForm({ ...form, expires: e.target.value })} /></Field>
        <ErrorText>{error}</ErrorText>
        <ModalFooter>
          <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
          <Button disabled={busy}>{busy ? 'Saving…' : 'Save'}</Button>
        </ModalFooter>
      </form>
    </Modal>
  )
}
