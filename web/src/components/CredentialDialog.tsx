import { useState, type ChangeEvent, type FormEvent } from 'react'
import { api, ApiError } from '../api'
import type { Credential, CredentialTest } from '../types'
import { Button, controlCls, ErrorText, Field, inputCls, Modal, ModalFooter } from './ui'

// The dialog's provider choice: Vertex AI is provider "google" with a service account.
export type CredProvider = 'openai' | 'anthropic' | 'google' | 'vertex'

// Defaults match core/llm/providers (DefaultOpenAIBase etc.).
export const PROVIDERS: Record<CredProvider, { label: string; base: string }> = {
  openai: { label: 'OpenAI-compatible', base: 'https://api.openai.com/v1' },
  anthropic: { label: 'Anthropic', base: 'https://api.anthropic.com' },
  google: { label: 'Google AI Studio', base: 'https://generativelanguage.googleapis.com/v1beta' },
  vertex: { label: 'Vertex AI', base: '' },
}

export const credProvider = (c: Credential): CredProvider => (c.kind === 'gcp_service_account' ? 'vertex' : c.provider)

// What the Credentials table shows: the effective endpoint.
export const credEndpoint = (c: Credential) =>
  c.kind === 'gcp_service_account' ? `${c.vertex_location} (Vertex)` : c.api_base || PROVIDERS[c.provider].base

const plural = (n: number, w: string) => `${n} ${w}${n === 1 ? '' : 's'}`

// CredentialDialog: "Add credential", shared by the Credentials page and the Dashboard checklist.
// The secret is write-only: it lives in this component's state only while the dialog is open.
export default function CredentialDialog({ onClose, onSaved }: { onClose: () => void; onSaved: (c: Credential) => void }) {
  const [form, setForm] = useState({
    name: '', provider: 'openai' as CredProvider, api_base: PROVIDERS.openai.base, secret: '', vertex_project: '', vertex_location: 'us-central1',
  })
  const [baseEdited, setBaseEdited] = useState(false)
  const [result, setResult] = useState<CredentialTest | null>(null)
  const [testing, setTesting] = useState(false)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const vertex = form.provider === 'vertex'

  // Any edit invalidates the last test result.
  const update = (patch: Partial<typeof form>) => {
    setForm({ ...form, ...patch })
    setResult(null)
  }
  const set = (k: keyof typeof form) => (e: ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) => update({ [k]: e.target.value })
  const setProvider = (e: ChangeEvent<HTMLSelectElement>) => {
    const p = e.target.value as CredProvider
    update({ provider: p, api_base: baseEdited ? form.api_base : PROVIDERS[p].base })
  }

  const body = () => {
    const base = form.api_base.trim()
    return {
      name: form.name,
      provider: vertex ? 'google' : form.provider,
      kind: vertex ? 'gcp_service_account' : 'api_key',
      api_base: vertex || base === PROVIDERS[form.provider].base ? '' : base, // '' = follow the provider default
      secret: form.secret,
      vertex_project: vertex ? form.vertex_project : '',
      vertex_location: vertex ? form.vertex_location : '',
    }
  }

  const test = async () => {
    setTesting(true)
    setResult(null)
    try {
      setResult(await api<CredentialTest>('/console/api/v1/credentials/test', { method: 'POST', body: body() }))
    } catch (err) {
      setResult({ ok: false, status: 0, model_count: 0, latency_ms: 0, message: (err as Error).message })
    } finally {
      setTesting(false)
    }
  }

  const save = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      onSaved(await api<Credential>('/console/api/v1/credentials', { method: 'POST', body: body() }))
    } catch (err) {
      setError((err as ApiError).status === 409 ? 'A credential with this name already exists.' : (err as Error).message)
      setBusy(false)
    }
  }

  return (
    <Modal title="Add credential" onClose={onClose}>
      <form onSubmit={save} className="space-y-4">
        <div className="grid grid-cols-2 gap-3">
          <Field label="Name" hint="Can't be renamed later."><input className={inputCls} required value={form.name} onChange={set('name')} /></Field>
          <Field label="Provider">
            <select className={inputCls} value={form.provider} onChange={setProvider}>
              {(Object.keys(PROVIDERS) as CredProvider[]).map((p) => <option key={p} value={p}>{PROVIDERS[p].label}</option>)}
            </select>
          </Field>
        </div>
        {vertex ? (
          <>
            <div className="grid grid-cols-2 gap-3">
              <Field label="Project ID"><input className={inputCls + ' font-mono'} required value={form.vertex_project} onChange={set('vertex_project')} /></Field>
              <Field label="Location" hint="e.g. us-central1, europe-west4, global"><input className={inputCls + ' font-mono'} required value={form.vertex_location} onChange={set('vertex_location')} /></Field>
            </div>
            <Field label="Service account JSON" hint="The key file's contents. Encrypted at rest.">
              <textarea className={controlCls + ' mt-1 block h-32 w-full py-1 font-mono text-xs'} required value={form.secret} onChange={set('secret')} />
            </Field>
          </>
        ) : (
          <>
            <Field label="API endpoint" hint="Plain http only for private or loopback hosts. Models can override it.">
              <input
                className={inputCls + ' font-mono'}
                value={form.api_base}
                placeholder={PROVIDERS[form.provider].base}
                onChange={(e) => {
                  setBaseEdited(true)
                  update({ api_base: e.target.value })
                }}
              />
            </Field>
            <Field label="API key" hint={form.provider === 'openai' ? 'Optional: leave blank for servers without auth. Encrypted at rest.' : 'Encrypted at rest.'}>
              <input className={inputCls + ' font-mono'} type="password" autoComplete="off" required={form.provider !== 'openai'} value={form.secret} onChange={set('secret')} />
            </Field>
          </>
        )}
        {result && (
          <p role="status" className={`px-3 py-2 text-sm ${result.ok ? 'bg-success text-success-foreground' : 'bg-error text-error-foreground'}`}>
            {result.ok
              ? `Connected · GET /models → ${result.status} · ${plural(result.model_count, 'model')} · ${result.latency_ms} ms`
              : `${result.status ? `GET /models → ${result.status} · ` : ''}${result.message || 'Connection failed.'}`}
          </p>
        )}
        <ErrorText>{error}</ErrorText>
        <ModalFooter>
          <div className="mr-auto">
            <Button type="button" variant="ghost" disabled={testing} onClick={test}>{testing ? 'Testing…' : 'Test connection'}</Button>
          </div>
          <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
          <Button disabled={busy}>{busy ? 'Saving…' : 'Save'}</Button>
        </ModalFooter>
      </form>
    </Modal>
  )
}
