import { useState, type ChangeEvent, type FormEvent } from 'react'
import { api } from '../api'
import type { Group } from '../types'
import { Button, ErrorText, Field, inputCls, Modal, ModalFooter } from './ui'

// CreateUserDialog creates an account directly (no invite link) and hands the admin-typed
// email + password to onCreated (the caller shows the password once in a SecretDialog).
export default function CreateUserDialog({ groups, onCreated, onClose }: { groups: Group[]; onCreated: (email: string, password: string) => void; onClose: () => void }) {
  const [form, setForm] = useState({ email: '', name: '', role: 'member', group_id: groups[0]?.id ?? '', password: '' })
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const set = (k: keyof typeof form) => (e: ChangeEvent<HTMLInputElement | HTMLSelectElement>) => setForm({ ...form, [k]: e.target.value })

  const create = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      await api('/console/api/v1/users', { method: 'POST', body: { email: form.email, name: form.name, role: form.role, group_id: form.group_id, password: form.password } })
      onCreated(form.email, form.password)
    } catch (err) {
      setError((err as Error).message)
      setBusy(false)
    }
  }

  return (
    <Modal title="Create user" onClose={onClose}>
      <form onSubmit={create} className="space-y-4">
        <Field label="Email"><input className={inputCls} type="email" required value={form.email} onChange={set('email')} /></Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label="Name" hint="Optional"><input className={inputCls} value={form.name} onChange={set('name')} /></Field>
          <Field label="Role">
            <select className={inputCls} value={form.role} onChange={set('role')}>
              <option value="member">member</option>
              <option value="viewer">viewer</option>
              <option value="admin">admin</option>
            </select>
          </Field>
        </div>
        <Field label="Group">
          <select className={inputCls} required value={form.group_id} onChange={set('group_id')}>
            {groups.map((g) => <option key={g.id} value={g.id}>{g.name}</option>)}
          </select>
        </Field>
        <Field label="Password" hint="At least 12 characters. Shown once, after creation.">
          <input className={inputCls} type="password" required minLength={12} value={form.password} onChange={set('password')} />
        </Field>
        <ErrorText>{error}</ErrorText>
        <ModalFooter>
          <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
          <Button disabled={busy || !form.group_id}>{busy ? 'Creating…' : 'Create'}</Button>
        </ModalFooter>
      </form>
    </Modal>
  )
}
