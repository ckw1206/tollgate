import { useState, type ChangeEvent, type FormEvent } from 'react'
import { api, ApiError } from '../api'
import type { Credential, CredentialTest } from '../types'
import { Button, controlBox, controlCls, ErrorText, Field, inputCls, Modal, ModalFooter } from './ui'

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

// "Used by 4 models · a, b, c (+1)"
export const usedBy = (names: string[]) =>
  names.length === 0
    ? 'Not used by any model'
    : `Used by ${plural(names.length, 'model')} · ${names.slice(0, 3).join(', ')}${names.length > 3 ? ` (+${names.length - 3})` : ''}`

// CredentialDialog: "Add credential" and Edit, shared by the Credentials page and the Dashboard checklist.
// Secrets are write-only: a new one lives in this component's state only while the dialog is
// open, and editing never shows the saved one, only "Replace".
export default function CredentialDialog({ credential, onClose, onSaved }: {
  credential?: Credential // edit
  onClose: () => void
  onSaved: (c: Credential) => void
}) {
  const editing = !!credential
  const [form, setForm] = useState(() => {
    const p = credential ? credProvider(credential) : 'openai'
    return {
      name: credential?.name ?? '',
      provider: p,
      api_base: credential?.api_base || PROVIDERS[p].base,
      secret: '',
      vertex_project: credential?.vertex_project ?? '',
      vertex_location: credential?.vertex_location || 'us-central1',
    }
  })
  const [replacing, setReplacing] = useState(!editing) // editing keeps the saved secret until Replace
  const [baseEdited, setBaseEdited] = useState(false)
  const [result, setResult] = useState<CredentialTest | null>(null)
  const [testing, setTesting] = useState(false)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const vertex = form.provider === 'vertex'
  const n = credential?.used_by.length ?? 0

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
  const keepSecret = () => {
    setReplacing(false)
    update({ secret: '' })
  }

  const body = () => {
    const base = form.api_base.trim()
    const fields = {
      name: form.name,
      api_base: vertex || base === PROVIDERS[form.provider].base ? '' : base, // '' = follow the provider default
      vertex_project: vertex ? form.vertex_project : '',
      vertex_location: vertex ? form.vertex_location : '',
      secret: replacing ? form.secret : '', // '' on edit = keep the saved secret
    }
    // Provider and kind are fixed once saved; the server rejects a change.
    return editing ? fields : { ...fields, provider: vertex ? 'google' : form.provider, kind: vertex ? 'gcp_service_account' : 'api_key' }
  }

  const test = async () => {
    setTesting(true)
    setResult(null)
    try {
      // Editing: the server uses the saved provider, and the saved secret unless a new one is entered.
      const b = editing ? { ...body(), id: credential.id } : body()
      setResult(await api<CredentialTest>('/console/api/v1/credentials/test', { method: 'POST', body: b }))
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
      onSaved(
        editing
          ? await api<Credential>(`/console/api/v1/credentials/${credential.id}`, { method: 'PUT', body: body() })
          : await api<Credential>('/console/api/v1/credentials', { method: 'POST', body: body() }),
      )
    } catch (err) {
      setError((err as ApiError).status === 409 ? 'A credential with this name already exists.' : (err as Error).message)
      setBusy(false)
    }
  }

  const secretLabel = vertex ? 'Service account JSON' : 'API key'
  const secretField = vertex ? (
    <textarea aria-label={secretLabel} className={controlCls + ' mt-1 block h-32 w-full py-1 font-mono text-xs'} required autoFocus={editing} value={form.secret} onChange={set('secret')} />
  ) : (
    <input
      aria-label={secretLabel}
      className={inputCls + ' font-mono'}
      type="password"
      autoComplete="off"
      autoFocus={editing}
      required={editing || form.provider !== 'openai'}
      value={form.secret}
      onChange={set('secret')}
    />
  )
  const secretHint = vertex ? "The key file's contents. Encrypted at rest." : form.provider === 'openai' && !editing ? 'Encrypted at rest. Leave blank for keyless self-hosted endpoints.' : 'Encrypted at rest.'

  return (
    <Modal title={editing ? `Edit "${credential.name}"` : 'Add credential'} subtitle={editing ? usedBy(credential.used_by) : undefined} onClose={onClose}>
      <form onSubmit={save} className="space-y-4">
        <div className="grid grid-cols-2 gap-3">
          <Field label="Name" hint={editing ? 'Models keep their link.' : 'Shown in the Models dialog.'}>
            <input className={inputCls} required value={form.name} onChange={set('name')} />
          </Field>
          <Field label="Provider" hint={editing ? 'Fixed after creation.' : undefined}>
            {editing ? (
              <input className={controlBox + ' mt-1 block min-h-9 w-full bg-muted px-3 py-1 text-sm text-muted-foreground'} readOnly value={PROVIDERS[form.provider].label} />
            ) : (
              <select className={inputCls} value={form.provider} onChange={setProvider}>
                {(Object.keys(PROVIDERS) as CredProvider[]).map((p) => <option key={p} value={p}>{PROVIDERS[p].label}</option>)}
              </select>
            )}
          </Field>
        </div>
        {vertex ? (
          <div className="grid grid-cols-2 gap-3">
            <Field label="Project ID"><input className={inputCls + ' font-mono'} required value={form.vertex_project} onChange={set('vertex_project')} /></Field>
            <Field label="Location" hint="e.g. us-east5, global, …"><input className={inputCls + ' font-mono'} required value={form.vertex_location} onChange={set('vertex_location')} /></Field>
          </div>
        ) : (
          <Field label="API endpoint" hint={<>Default <code className="font-mono">{PROVIDERS[form.provider].base}</code>. Use your Azure, vLLM, Ollama or proxy URL.</>}>
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
        )}
        {/* Not a <Field> while showing the saved secret: there's no input to label. */}
        <div className="text-sm">
          <span className="font-medium">{secretLabel}</span>
          {replacing ? (
            <>
              {secretField}
              <span className="mt-1 flex justify-between gap-3 text-xs text-muted-foreground">
                <span>{secretHint}</span>
                {editing && <Button type="button" variant="link" className="text-xs" onClick={keepSecret}>Keep current</Button>}
              </span>
            </>
          ) : (
            <>
              <div className="mt-1 flex min-h-9 items-center justify-between gap-3 rounded-md border border-border bg-muted px-3 py-1">
                <span className="font-mono text-xs text-muted-foreground">
                  •••••••• saved · updated {new Date(credential!.secret_updated_at).toLocaleDateString()}
                </span>
                <Button type="button" size="xs" variant="ghost" onClick={() => setReplacing(true)}>Replace</Button>
              </div>
              <span className="mt-1 block text-xs text-muted-foreground">The saved key can't be shown. Replace opens an empty field; leave it closed to keep the current key.</span>
            </>
          )}
        </div>
        {editing && n > 0 && (
          <p className="bg-warning px-3 py-2 text-sm text-warning-foreground">
            Changes apply to {n === 1 ? 'the model' : `all ${n} models`} using this credential as soon as you save.
          </p>
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
          <div className="mr-auto w-full sm:w-auto">
            <Button type="button" variant="ghost" disabled={testing} onClick={test}>{testing ? 'Testing…' : 'Test connection'}</Button>
          </div>
          <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
          <Button disabled={busy}>{busy ? 'Saving…' : editing ? 'Save changes' : 'Save'}</Button>
        </ModalFooter>
      </form>
    </Modal>
  )
}
