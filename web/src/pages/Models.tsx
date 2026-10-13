import { useState, type ChangeEvent, type FormEvent } from 'react'
import { api, ApiError, fromMicros, toMicros } from '../api'
import { useAuth } from '../auth'
import {
  Badge, Button, ConfirmDialog, controlCls, DetailPanel, ErrorText, Field, inputCls, Modal, ModalFooter, PageHeader, RowActions, rowsOf, Table, useLoad, type DetailItem,
} from '../components/ui'
import type { Credential, Model } from '../types'

interface Form {
  name: string; provider: Model['provider']; upstream_model: string; api_base: string; credential_id: string; enabled: boolean
  price_in: string; price_out: string; price_cr: string; price_cw: string; extra: string
}

const toForm = (m?: Model): Form => ({
  name: m?.name ?? '', provider: m?.provider ?? 'openai', upstream_model: m?.upstream_model ?? '', api_base: m?.api_base ?? '',
  credential_id: m?.credential_id ?? '', enabled: m?.enabled ?? true,
  price_in: m ? fromMicros(m.price_input_micros) : '', price_out: m ? fromMicros(m.price_output_micros) : '',
  price_cr: m?.price_cache_read_micros != null ? fromMicros(m.price_cache_read_micros) : '',
  price_cw: m?.price_cache_write_micros != null ? fromMicros(m.price_cache_write_micros) : '',
  extra: JSON.stringify(m?.extra ?? {}, null, 2),
})

const optMicros = (s: string) => (s.trim() === '' ? null : toMicros(s))

const payload = (f: Form) => ({
  name: f.name, provider: f.provider, upstream_model: f.upstream_model, api_base: f.api_base,
  credential_id: f.credential_id || null, enabled: f.enabled,
  price_input_micros: toMicros(f.price_in), price_output_micros: toMicros(f.price_out),
  price_cache_read_micros: optMicros(f.price_cr), price_cache_write_micros: optMicros(f.price_cw),
  extra: JSON.parse(f.extra || '{}'),
})

const price = (micros: number | null) => (micros == null ? 'input price' : fromMicros(micros))

// API base and Extra are only shown to models:write; the server redacts them for everyone else.
function modelDetail(m: Model, writable: boolean): DetailItem[] {
  const extra = m.extra && Object.keys(m.extra).length ? JSON.stringify(m.extra, null, 2) : null
  return [
    { label: 'Upstream model', value: m.upstream_model },
    ...(writable ? [{ label: 'API base', value: m.api_base || '—' }] : []),
    { label: 'Cache read · write $ / 1M', value: `${price(m.price_cache_read_micros)} · ${price(m.price_cache_write_micros)}` },
    ...(writable ? [{ label: 'Extra', wide: true, value: extra ? <pre className="max-w-2xl overflow-x-auto whitespace-pre">{extra}</pre> : '—' }] : []),
  ]
}

export default function Models() {
  const { can } = useAuth()
  const writable = can('models:write')
  const models = useLoad<Model[]>('/console/api/v1/models')
  const creds = useLoad<Credential[]>(writable ? '/console/api/v1/credentials' : null)
  const [editing, setEditing] = useState<Model | 'new' | null>(null)
  const [form, setForm] = useState<Form>(toForm())
  const [deleting, setDeleting] = useState<Model | null>(null)
  const [error, setError] = useState('')
  const [pageError, setPageError] = useState('')
  const [busy, setBusy] = useState(false)

  const open = (m: Model | 'new') => {
    setEditing(m)
    setForm(toForm(m === 'new' ? undefined : m))
    setError('')
  }
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
      if (editing === 'new') await api('/console/api/v1/models', { method: 'POST', body })
      else if (editing) await api(`/console/api/v1/models/${editing.id}`, { method: 'PUT', body })
      setEditing(null)
      models.reload()
    } catch (err) {
      setError((err as ApiError).status === 409 ? 'A model with this name already exists.' : (err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const toggle = async (m: Model) => {
    setPageError('')
    await api(`/console/api/v1/models/${m.id}`, { method: 'PUT', body: payload({ ...toForm(m), enabled: !m.enabled }) }).catch((e: Error) => setPageError(e.message))
    models.reload()
  }

  const remove = (m: Model) =>
    api(`/console/api/v1/models/${m.id}`, { method: 'DELETE' })
      .catch((e: ApiError) => {
        throw e.status === 409 ? new Error('This model has spend history, so it can’t be deleted. Disable it instead.') : e
      })
      .finally(models.reload)

  const credName = (id: string | null) => (id ? creds.data?.find((c) => c.id === id)?.name ?? id.slice(0, 8) : '—')
  return (
    <>
      <PageHeader title="Models">{writable && <Button onClick={() => open('new')}>Add model</Button>}</PageHeader>
      <div className="mb-4 space-y-1">
        <ErrorText>{models.error}</ErrorText>
        <ErrorText>{pageError}</ErrorText>
      </div>
      <Table
        rows={rowsOf(models)}
        empty="No models yet."
        rowKey={(m) => m.id}
        rowLabel={(m) => m.name}
        expand={(m) => (
          <DetailPanel
            items={modelDetail(m, writable)}
            actions={writable && (
              <RowActions>
                <Button size="sm" variant="ghost" onClick={() => open(m)}>Edit</Button>
                <Button size="sm" variant="ghost" onClick={() => toggle(m)}>{m.enabled ? 'Disable' : 'Enable'}</Button>
                <Button size="sm" variant="danger" onClick={() => setDeleting(m)}>Delete</Button>
              </RowActions>
            )}
          />
        )}
        cols={[
          { head: 'Name', cell: (m) => m.name },
          { head: 'Provider', cell: (m) => m.provider }, // upstream model is in the detail row
          { head: '$ / 1M in · out', cell: (m) => `${fromMicros(m.price_input_micros)} · ${fromMicros(m.price_output_micros)}` },
          ...(writable ? [{ head: 'Credential', cell: (m: Model) => credName(m.credential_id) }] : []),
          { head: 'Status', cell: (m) => (m.enabled ? <Badge tone="success">enabled</Badge> : <Badge tone="inactive">disabled</Badge>) },
        ]}
      />

      {editing && (
        <Modal title={editing === 'new' ? 'Add model' : `Edit "${editing.name}"`} onClose={() => setEditing(null)}>
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
                  {creds.data?.map((c) => <option key={c.id} value={c.id}>{c.name} ({c.kind})</option>)}
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
              <Button type="button" variant="ghost" onClick={() => setEditing(null)}>Cancel</Button>
              <Button disabled={busy}>{busy ? 'Saving…' : 'Save'}</Button>
            </ModalFooter>
          </form>
        </Modal>
      )}

      {deleting && (
        <ConfirmDialog
          title={`Delete "${deleting.name}"?`}
          body="Clients can no longer call this model. Models with spend history can't be deleted; disable them instead."
          confirm="Delete"
          onConfirm={() => remove(deleting)}
          onClose={() => setDeleting(null)}
        />
      )}
    </>
  )
}
