import { useState, type ChangeEvent, type FormEvent } from 'react'
import { api, ApiError, fromMicros, toMicros } from '../api'
import type { Credential, Model } from '../types'
import { Button, controlCls, ErrorText, Field, inputCls, Modal, ModalFooter } from './ui'

// ModelDialog: "Add model" / "Edit" dialog, shared by the Models page and the Dashboard checklist.

export interface Form {
  name: string; provider: Model['provider']; upstream_model: string; api_base: string; credential_id: string; enabled: boolean
  price_in: string; price_out: string; price_cr: string; price_cw: string; extra: string
}

export const toForm = (m?: Model): Form => ({
  name: m?.name ?? '', provider: m?.provider ?? 'openai', upstream_model: m?.upstream_model ?? '', api_base: m?.api_base ?? '',
  credential_id: m?.credential_id ?? '', enabled: m?.enabled ?? true,
  price_in: m ? fromMicros(m.price_input_micros) : '', price_out: m ? fromMicros(m.price_output_micros) : '',
  price_cr: m?.price_cache_read_micros != null ? fromMicros(m.price_cache_read_micros) : '',
  price_cw: m?.price_cache_write_micros != null ? fromMicros(m.price_cache_write_micros) : '',
  extra: JSON.stringify(m?.extra ?? {}, null, 2),
})

const optMicros = (s: string) => (s.trim() === '' ? null : toMicros(s))

export const payload = (f: Form) => ({
  name: f.name, provider: f.provider, upstream_model: f.upstream_model, api_base: f.api_base,
  credential_id: f.credential_id || null, enabled: f.enabled,
  price_input_micros: toMicros(f.price_in), price_output_micros: toMicros(f.price_out),
  price_cache_read_micros: optMicros(f.price_cr), price_cache_write_micros: optMicros(f.price_cw),
  extra: JSON.parse(f.extra || '{}'),
})

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
          <Field label="Name" hint="What clients send as model"><input className={inputCls} required value={form.name} onChange={set('name')} /></Field>
          <Field label="Provider">
            <select className={inputCls} value={form.provider} onChange={set('provider')}>
              <option value="openai">OpenAI-compatible</option>
              <option value="anthropic">Anthropic</option>
              <option value="google">Google</option>
            </select>
          </Field>
          <Field label="Upstream model ID"><input className={inputCls} required value={form.upstream_model} onChange={set('upstream_model')} /></Field>
          <Field label="Credential">
            <select className={inputCls} value={form.credential_id} onChange={set('credential_id')}>
              <option value="">None</option>
              {creds.map((c) => <option key={c.id} value={c.id}>{c.name} ({c.kind})</option>)}
            </select>
          </Field>
        </div>
        <Field label="API base" hint="Optional. Self-hosted, Azure, or Vertex endpoint override."><input className={inputCls} value={form.api_base} onChange={set('api_base')} /></Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label="Input $ / 1M"><input className={inputCls} type="number" min="0" step="any" required value={form.price_in} onChange={set('price_in')} /></Field>
          <Field label="Output $ / 1M"><input className={inputCls} type="number" min="0" step="any" required value={form.price_out} onChange={set('price_out')} /></Field>
          <Field label="Cache read $ / 1M" hint="Blank = input price"><input className={inputCls} type="number" min="0" step="any" value={form.price_cr} onChange={set('price_cr')} /></Field>
          <Field label="Cache write $ / 1M" hint="Blank = input price"><input className={inputCls} type="number" min="0" step="any" value={form.price_cw} onChange={set('price_cw')} /></Field>
        </div>
        <Field label="Extra (JSON)" hint='e.g. {"vertex_project":"p","vertex_location":"us-east5","headers":{"X-Org":"me"}}'>
          <textarea className={controlCls + ' mt-1 block h-24 w-full py-1 font-mono text-xs'} value={form.extra} onChange={set('extra')} />
        </Field>
        <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={form.enabled} onChange={set('enabled')} /> Enabled</label>
        <ErrorText>{error}</ErrorText>
        <ModalFooter>
          <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
          <Button disabled={busy}>{busy ? 'Saving…' : 'Save'}</Button>
        </ModalFooter>
      </form>
    </Modal>
  )
}
