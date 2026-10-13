import { useState, type FormEvent } from 'react'
import { api, short, when } from '../api'
import { useAuth } from '../auth'
import { budgetText, LimitsFields, limitsPayload, limitsState, limitText, type LimitsState } from '../components/LimitsFields'
import {
  Badge, Button, ConfirmDialog, ErrorText, Field, inputCls, Modal, ModalFooter, PageHeader, RowActions, rowsOf, SecretDialog,
  Segmented, Table, useLoad, type Tone,
} from '../components/ui'
import type { ApiKey, Group, User } from '../types'

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

const DAY = 86_400_000

type Status = 'active' | 'expired' | 'rotating' | 'revoked'

export function keyStatus(k: ApiKey, now = Date.now()): Status {
  if (k.revoked_at) return 'revoked'
  if (k.expires_at && new Date(k.expires_at).getTime() <= now) return 'expired'
  if (k.rotated_at) return 'rotating'
  return 'active'
}

const tone: Record<Status, Tone> = { active: 'success', expired: 'warning', rotating: 'warning', revoked: 'error' }

const GRACE = [
  { value: 0, label: 'Revoke now' },
  { value: 3600, label: '1 hour' },
  { value: 86400, label: '24 hours' },
  { value: 604800, label: '7 days' },
]

// stopsAt mirrors the server: min(expires_at, now + grace).
function stopsAt(k: ApiKey, grace: number) {
  const g = Date.now() + grace * 1000
  return k.expires_at ? Math.min(g, new Date(k.expires_at).getTime()) : g
}

export default function Keys() {
  const { can } = useAuth()
  const keys = useLoad<ApiKey[]>('/console/api/v1/keys')
  const users = useLoad<User[]>(can('users:read') ? '/console/api/v1/users' : null)
  const groups = useLoad<Group[]>('/console/api/v1/groups')
  const [editing, setEditing] = useState<ApiKey | 'new' | null>(null)
  const [form, setForm] = useState<Form>(emptyForm())
  const [secret, setSecret] = useState<{ title: string; raw: string; until?: string } | null>(null)
  const [revoking, setRevoking] = useState<ApiKey | null>(null)
  const [rotating, setRotating] = useState<ApiKey | null>(null)
  const [grace, setGrace] = useState(86400)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const email = (id: string) => users.data?.find((u) => u.id === id)?.email ?? short(id)
  const groupName = (id: string) => groups.data?.find((g) => g.id === id)?.name ?? short(id)
  const open = (k: ApiKey | 'new') => {
    setEditing(k)
    setForm(emptyForm(k === 'new' ? undefined : k))
    setError('')
  }

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
      if (editing === 'new') {
        const res = await api<{ raw: string }>('/console/api/v1/keys', { method: 'POST', body })
        setSecret({ title: 'Your new API key', raw: res.raw })
      } else if (editing) {
        await api(`/console/api/v1/keys/${editing.id}`, { method: 'PUT', body })
      }
      setEditing(null)
      keys.reload()
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const openRotate = (k: ApiKey) => {
    setRotating(k)
    setGrace(86400)
    setError('')
  }

  const rotate = async (e: FormEvent) => {
    e.preventDefault()
    if (!rotating) return
    setBusy(true)
    setError('')
    try {
      const res = await api<{ raw: string; old: ApiKey }>(`/console/api/v1/keys/${rotating.id}/rotate`, { method: 'POST', body: { grace_seconds: grace } })
      setSecret({ title: `New key for "${rotating.alias}"`, raw: res.raw, until: grace > 0 ? when(res.old.expires_at) : undefined })
      setRotating(null)
      keys.reload()
    } catch (err) {
      setError((err as Error).message)
      if ((err as { status?: number }).status === 404) keys.reload()
    } finally {
      setBusy(false)
    }
  }

  const writable = can('keys:write_own')
  const actions = (k: ApiKey) => {
    const s = keyStatus(k)
    if (!writable || s === 'revoked') return null
    const revoke = <Button size="sm" variant="danger" onClick={() => setRevoking(k)}>{s === 'rotating' ? 'Revoke now' : 'Revoke'}</Button>
    // A rotated key never gets Edit or Rotate, even after it expires: Edit could push its expiry out again.
    if (k.rotated_at) return <RowActions>{revoke}</RowActions>
    return (
      <RowActions>
        <Button size="sm" variant="ghost" onClick={() => open(k)}>Edit</Button>
        {s === 'active' && <Button size="sm" variant="ghost" onClick={() => openRotate(k)}>Rotate</Button>}
        {revoke}
      </RowActions>
    )
  }

  const expiringSoon = (k: ApiKey) => {
    if (keyStatus(k) === 'rotating') return <div className="text-xs font-normal text-warning-foreground">Expires {when(k.expires_at)}</div>
    if (!k.expires_at || keyStatus(k) !== 'active') return null
    const days = Math.ceil((new Date(k.expires_at).getTime() - Date.now()) / DAY)
    return days <= 7 ? <div className="text-xs font-normal text-warning-foreground">Expires in {days} {days === 1 ? 'day' : 'days'}</div> : null
  }

  const regen = grace === 0
  return (
    <>
      <PageHeader title="API keys">{writable && <Button onClick={() => open('new')}>New key</Button>}</PageHeader>
      <div className="mb-4"><ErrorText>{keys.error}</ErrorText></div>
      <Table
        rows={rowsOf(keys)}
        empty="No API keys yet."
        cols={[
          { head: 'Alias', cell: (k) => <div>{k.alias}{expiringSoon(k)}</div> },
          { head: 'Key', cell: (k) => <code className="font-mono text-xs">{k.key_prefix}…</code> },
          ...(can('keys:read_all') ? [{ head: 'Owner', cell: (k: ApiKey) => email(k.user_id) }] : []),
          { head: 'Group', cell: (k) => groupName(k.group_id) },
          { head: 'Budget', cell: (k) => budgetText(k) },
          { head: 'RPM / TPM', cell: (k) => `${limitText(k.rpm_limit)} / ${limitText(k.tpm_limit)}` },
          { head: 'Last used', cell: (k) => when(k.last_used_at) },
          { head: 'Status', cell: (k) => <Badge tone={tone[keyStatus(k)]}>{keyStatus(k)}</Badge> },
          { head: '', cell: actions },
        ]}
      />

      {editing && (
        <Modal title={editing === 'new' ? 'New API key' : `Edit "${editing.alias}"`} onClose={() => setEditing(null)}>
          <form onSubmit={save} className="space-y-4">
            <Field label="Alias"><input className={inputCls} required value={form.alias} onChange={(e) => setForm({ ...form, alias: e.target.value })} /></Field>
            <Field label="Allowed models" hint="Comma-separated model names. Blank = all models.">
              <input className={inputCls} value={form.models} onChange={(e) => setForm({ ...form, models: e.target.value })} />
            </Field>
            {can('keys:write_all') && (
              <Field label="Billing group">
                <select className={inputCls} value={form.group_id} onChange={(e) => setForm({ ...form, group_id: e.target.value })}>
                  <option value="">Owner's group</option>
                  {groups.data?.map((g) => <option key={g.id} value={g.id}>{g.name}</option>)}
                </select>
              </Field>
            )}
            <LimitsFields value={form.limits} onChange={(limits) => setForm({ ...form, limits })} />
            <Field label="Expires on" hint="Blank = never"><input className={inputCls} type="date" value={form.expires} onChange={(e) => setForm({ ...form, expires: e.target.value })} /></Field>
            <ErrorText>{error}</ErrorText>
            <ModalFooter>
              <Button type="button" variant="ghost" onClick={() => setEditing(null)}>Cancel</Button>
              <Button disabled={busy}>{busy ? 'Saving…' : 'Save'}</Button>
            </ModalFooter>
          </form>
        </Modal>
      )}

      {rotating && (
        <Modal title={`Rotate "${rotating.alias}"?`} onClose={() => setRotating(null)}>
          <form onSubmit={rotate} className="space-y-4 text-sm">
            <p className="text-muted-foreground">
              A new key is created with the same models, group, budget, limits and expiry. Spend counters start at the old key's current value.
              {regen && ' Clients using the old key will stop working immediately.'}
            </p>
            <div className="space-y-1">
              <div className="font-medium">Old key keeps working for</div>
              <Segmented label="Old key keeps working for" value={grace} onChange={setGrace} options={GRACE} />
            </div>
            {rotating.expires_at && (
              <p className="text-xs text-muted-foreground">The new key keeps this expiry ({when(rotating.expires_at)}). Edit it afterwards to extend.</p>
            )}
            {!regen && (
              <p className="text-xs text-muted-foreground">
                Old key <code className="font-mono">{rotating.key_prefix}…</code> stops at {new Date(stopsAt(rotating, grace)).toLocaleString()}. If it already expires sooner, the sooner time wins.
              </p>
            )}
            <ErrorText>{error}</ErrorText>
            <ModalFooter>
              <Button type="button" variant="ghost" onClick={() => setRotating(null)}>Cancel</Button>
              <Button variant={regen ? 'danger' : 'primary'} disabled={busy}>
                {busy ? (regen ? 'Regenerating…' : 'Rotating…') : regen ? 'Regenerate key' : 'Rotate key'}
              </Button>
            </ModalFooter>
          </form>
        </Modal>
      )}

      {revoking && (
        <ConfirmDialog
          title={`Revoke "${revoking.alias}"?`}
          body="Clients using it will stop working immediately."
          confirm="Revoke"
          onConfirm={() => api(`/console/api/v1/keys/${revoking.id}`, { method: 'DELETE' }).finally(keys.reload)}
          onClose={() => setRevoking(null)}
        />
      )}

      {secret && (
        <SecretDialog title={secret.title} body="Copy it now. It won't be shown again." value={secret.raw} onClose={() => setSecret(null)}>
          {secret.until && <p className="mt-3 bg-warning px-3 py-2 text-xs text-warning-foreground">Old key works until {secret.until}.</p>}
        </SecretDialog>
      )}
    </>
  )
}
