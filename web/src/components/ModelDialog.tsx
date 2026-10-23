import { useState, type ChangeEvent, type FormEvent } from 'react'
import { api, ApiError, fromMicros, num, toMicros } from '../api'
import type { Credential, Model } from '../types'
import { credEndpoint, credProvider, PROVIDERS } from './CredentialDialog'
import { Button, controlBox, controlCls, ErrorText, Field, inputCls, Modal, ModalFooter } from './ui'

// ModelDialog: "Add model" / "Edit" dialog, shared by the Models page and the Dashboard checklist.
// The credential sets the provider and the endpoint: while one is picked, API base is locked to
// the credential's endpoint and sent as "". "None" means a keyless OpenAI-compatible server,
// which needs its own API base.

export interface Form {
  name: string; provider: Model['provider']; upstream_model: string; api_base: string; credential_id: string; enabled: boolean
  price_in: string; price_out: string; price_cr: string; price_cw: string; context_window: string; extra: string
}

export const toForm = (m?: Model): Form => ({
  name: m?.name ?? '', provider: m?.provider ?? 'openai', upstream_model: m?.upstream_model ?? '', api_base: m?.api_base ?? '',
  credential_id: m?.credential_id ?? '', enabled: m?.enabled ?? true,
  price_in: m ? fromMicros(m.price_input_micros) : '', price_out: m ? fromMicros(m.price_output_micros) : '',
  price_cr: m?.price_cache_read_micros != null ? fromMicros(m.price_cache_read_micros) : '',
  price_cw: m?.price_cache_write_micros != null ? fromMicros(m.price_cache_write_micros) : '',
  context_window: m?.context_window != null ? String(m.context_window) : '',
  extra: JSON.stringify(m?.extra ?? {}, null, 2),
})

const optMicros = (s: string) => (s.trim() === '' ? null : toMicros(s))

export const payload = (f: Form) => ({
  name: f.name, provider: f.provider, upstream_model: f.upstream_model, api_base: f.credential_id ? '' : f.api_base,
  credential_id: f.credential_id || null, enabled: f.enabled,
  price_input_micros: toMicros(f.price_in), price_output_micros: toMicros(f.price_out),
  price_cache_read_micros: optMicros(f.price_cr), price_cache_write_micros: optMicros(f.price_cw),
  context_window: num(f.context_window),
  extra: JSON.parse(f.extra || '{}'),
})

const monoCls = controlCls + ' mt-1 block min-h-9 w-full py-1 font-mono text-xs'
const lockedCls = controlBox + ' mt-1 block min-h-9 w-full cursor-not-allowed bg-muted px-3 py-1 font-mono text-xs text-muted-foreground opacity-70'

// The locked API base shows the credential's endpoint (the provider default when it has none).
const lockedBase = (c: Credential) => (c.kind === 'gcp_service_account' ? `— (Vertex: ${c.vertex_project}/${c.vertex_location})` : credEndpoint(c))

export default function ModelDialog({ model, creds, onClose, onSaved }: {
  model: Model | 'new'
  creds: Credential[]
  onClose: () => void
  onSaved: (m: Model) => void
}) {
  const [form, setForm] = useState<Form>(() => toForm(model === 'new' ? undefined : model))
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const set = (k: keyof Form) => (e: ChangeEvent<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>) =>
    setForm({ ...form, [k]: e.target.type === 'checkbox' ? (e.target as HTMLInputElement).checked : e.target.value })
  const cred = creds.find((c) => c.id === form.credential_id)
  // Picking a credential takes its provider and clears a typed API base; None is OpenAI-compatible.
  const pickCredential = (e: ChangeEvent<HTMLSelectElement>) => {
    const c = creds.find((c) => c.id === e.target.value)
    setForm({ ...form, credential_id: e.target.value, provider: c?.provider ?? 'openai', api_base: c ? '' : form.api_base })
  }
  // Saved with both a credential and its own api_base (from before the credential owned the endpoint).
  const ownBase = model !== 'new' && model.credential_id ? model.api_base : ''

  const save = async (e: FormEvent) => {
    e.preventDefault()
    let body
    try {
      body = payload(form)
    } catch {
      return setError('Extra must be valid JSON.')
    }
    setBusy(true)
    setError('')
    try {
      const saved =
        model === 'new'
          ? await api<Model>('/console/api/v1/models', { method: 'POST', body })
          : await api<Model>(`/console/api/v1/models/${model.id}`, { method: 'PUT', body })
      onSaved(saved)
    } catch (err) {
      setError((err as ApiError).status === 409 ? 'A model with this name already exists.' : (err as Error).message)
      setBusy(false)
    }
  }

  return (
    <Modal title={model === 'new' ? 'Add model' : `Edit "${model.name}"`} onClose={onClose}>
      <form onSubmit={save} className="space-y-4">
        <div className="grid grid-cols-2 gap-3">
          <Field label="Name" hint={<>What clients send as <code className="font-mono">model</code>.</>}>
            <input className={inputCls} required value={form.name} onChange={set('name')} />
          </Field>
          <Field label="Upstream model"><input className={monoCls} required value={form.upstream_model} onChange={set('upstream_model')} /></Field>
        </div>
        <Field label="Credential" hint='Sets the provider and endpoint. Pick "None" for a keyless endpoint.'>
          <select className={inputCls} value={form.credential_id} onChange={pickCredential}>
            <option value="">None</option>
            {creds.map((c) => <option key={c.id} value={c.id}>{c.name} ({PROVIDERS[credProvider(c)].label})</option>)}
          </select>
        </Field>
        {form.credential_id ? (
          <Field
            label={
              <span className="text-muted-foreground">
                API base
                {ownBase && <span title={ownBase} className="ml-2 bg-warning px-2 py-0.5 text-xs font-normal text-warning-foreground">Overridden — will be cleared on save</span>}
              </span>
            }
            hint={<>From <b className="font-medium text-foreground">{cred?.name ?? 'the credential'}</b>. Edit the credential to change it.</>}
          >
            <input className={lockedCls} disabled value={cred ? lockedBase(cred) : ''} />
          </Field>
        ) : (
          <Field label="API base">
            <input className={monoCls} required placeholder="https://…/v1" value={form.api_base} onChange={set('api_base')} />
          </Field>
        )}
        <div className="grid grid-cols-2 gap-3">
          <Field label="Input $ / 1M"><input className={inputCls} type="number" min="0" step="any" required value={form.price_in} onChange={set('price_in')} /></Field>
          <Field label="Output $ / 1M"><input className={inputCls} type="number" min="0" step="any" required value={form.price_out} onChange={set('price_out')} /></Field>
          <Field label="Cache read $ / 1M" hint="Blank = input price"><input className={inputCls} type="number" min="0" step="any" value={form.price_cr} onChange={set('price_cr')} /></Field>
          <Field label="Cache write $ / 1M" hint="Blank = input price"><input className={inputCls} type="number" min="0" step="any" value={form.price_cw} onChange={set('price_cw')} /></Field>
          <Field label="Context window (tokens)" hint="Blank = not set. Checked against the upstream's reported limit; a larger value is rejected."><input className={inputCls} type="number" min="1" step="1" value={form.context_window} onChange={set('context_window')} /></Field>
        </div>
        <Field label="Extra (JSON)" hint='e.g. {"headers":{"X-Org":"me"}}. vertex_project / vertex_location override the credential.'>
          <textarea className={controlCls + ' mt-1 block h-24 w-full py-1 font-mono text-xs'} value={form.extra} onChange={set('extra')} />
        </Field>
        <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={form.enabled} onChange={set('enabled')} /> Enabled</label>
        <ErrorText>{error}</ErrorText>
        <ModalFooter>
          <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
          <Button disabled={busy}>{model === 'new' ? (busy ? 'Adding…' : 'Add model') : busy ? 'Saving…' : 'Save'}</Button>
        </ModalFooter>
      </form>
    </Modal>
  )
}
