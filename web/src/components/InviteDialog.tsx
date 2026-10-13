import { useState, type ChangeEvent, type FormEvent } from 'react'
import { api } from '../api'
import type { Group } from '../types'
import { Button, ErrorText, Field, inputCls, Modal, ModalFooter } from './ui'

// InviteDialog creates an invite and hands the one-time URL to onCreated
// (the caller shows it in a SecretDialog). Used by Invites and Users.
export default function InviteDialog({ groups, onCreated, onClose }: { groups: Group[]; onCreated: (url: string) => void; onClose: () => void }) {
  const [form, setForm] = useState({ role: 'member', group_id: groups[0]?.id ?? '', email: '', days: '7' })
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const set = (k: keyof typeof form) => (e: ChangeEvent<HTMLInputElement | HTMLSelectElement>) => setForm({ ...form, [k]: e.target.value })

  const create = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      const res = await api<{ url: string }>('/console/api/v1/invites', {
        method: 'POST',
        body: { role: form.role, group_id: form.group_id, email: form.email, ttl_hours: Number(form.days) * 24 },
      })
      onCreated(res.url)
    } catch (err) {
      setError((err as Error).message)
      setBusy(false)
    }
  }

  return (
    <Modal title="Create invite" onClose={onClose}>
      <form onSubmit={create} className="space-y-4">
        <div className="grid grid-cols-2 gap-3">
          <Field label="Role">
            <select className={inputCls} value={form.role} onChange={set('role')}>
              <option value="member">member</option>
              <option value="viewer">viewer</option>
              <option value="admin">admin</option>
            </select>
          </Field>
          <Field label="Group">
            <select className={inputCls} required value={form.group_id} onChange={set('group_id')}>
              {groups.map((g) => <option key={g.id} value={g.id}>{g.name}</option>)}
            </select>
          </Field>
        </div>
        <Field label="Restrict to email" hint="Optional"><input className={inputCls} type="email" value={form.email} onChange={set('email')} /></Field>
        <Field label="Valid for (days)"><input className={inputCls} type="number" min="1" max="30" required value={form.days} onChange={set('days')} /></Field>
        <ErrorText>{error}</ErrorText>
        <ModalFooter>
          <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
          <Button disabled={busy || !form.group_id}>{busy ? 'Creating…' : 'Create'}</Button>
        </ModalFooter>
      </form>
    </Modal>
  )
}
