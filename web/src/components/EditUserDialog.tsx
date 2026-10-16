import { useId, useState, type FormEvent } from 'react'
import { api } from '../api'
import type { Group, Model, Role, User } from '../types'
import { Button, ConfirmDialog, ErrorText, Field, inputCls, Modal, ModalFooter, MultiSelect, useLoad } from './ui'
import { extraModelOptions } from './modelOptions'

// EditUserDialog: the one Edit action on a Users row (role, group, extra model grants,
// enabled, password reset). Disabling an enabled user asks for confirmation first.
export default function EditUserDialog({ user, groups, onClose, onSaved }: { user: User; groups: Group[]; onClose: () => void; onSaved: () => void }) {
  const [role, setRole] = useState<Role>(user.role)
  const [group_id, setGroupId] = useState(user.group_id)
  const [extra, setExtra] = useState(user.extra_models)
  const [enabled, setEnabled] = useState(!user.disabled)
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [confirming, setConfirming] = useState(false)
  const label = useId()
  const models = useLoad<Model[]>('/console/api/v1/models')
  const group = groups.find((g) => g.id === group_id)
  const extraOpts = group && group.models.length > 0 ? extraModelOptions(models.data, group, extra) : null

  const save = async () => {
    if (password && password.length < 12) {
      setError('password must be at least 12 characters')
      return
    }
    // enabled→disabled at save time asks first; the second call comes from the
    // ConfirmDialog (confirming === true) and goes straight to the PUT.
    if (!confirming && !user.disabled && !enabled) {
      setConfirming(true)
      return
    }
    setBusy(true)
    setError('')
    const body = { role, group_id, extra_models: extra, disabled: !enabled, ...(password ? { password } : {}) }
    try {
      await api(`/console/api/v1/users/${user.id}`, { method: 'PUT', body })
      onSaved()
    } catch (err) {
      setError((err as Error).message)
      throw err // the ConfirmDialog keeps itself open on a rejected onConfirm
    } finally {
      setBusy(false)
    }
  }

  const submit = (e: FormEvent) => {
    e.preventDefault()
    void save().catch(() => undefined) // the error is shown in the dialog
  }

  return (
    <>
      <Modal title={`Edit "${user.name || user.email}"`} onClose={() => { if (!confirming) onClose() }}>
        <form onSubmit={submit} className="space-y-4">
          <Field label="Role">
            <select className={inputCls} value={role} onChange={(e) => setRole(e.target.value as Role)}>
              <option value="admin">admin</option>
              <option value="member">member</option>
              <option value="viewer">viewer</option>
            </select>
          </Field>
          <Field label="Group">
            <select className={inputCls} value={group_id} onChange={(e) => setGroupId(e.target.value)}>
              {groups.map((g) => <option key={g.id} value={g.id}>{g.name}</option>)}
            </select>
          </Field>
          {extraOpts ? (
            // Not a <Field>: a wrapping <label> would forward clicks to the first chip's × button.
            <div className="text-sm">
              <span id={label} className="font-medium">Extra models</span>
              <MultiSelect
                labelledBy={label}
                noAll
                options={extraOpts.options}
                value={extra}
                onChange={setExtra}
                allLabel="None"
                allHint="group only"
                staleIds={extraOpts.stale}
              />
              <span className="mt-1 block text-xs text-muted-foreground">Granted to this user on top of the group. Options = enabled models the group doesn't include. None = group only.</span>
              <ErrorText>{models.error}</ErrorText>
            </div>
          ) : (
            <p className="text-sm text-muted-foreground">Group allows all models.</p>
          )}
          <Field label="Enabled" hint="When off, they're signed out and their API keys stop working immediately.">
            <input type="checkbox" className="mt-1" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
          </Field>
          <Field label="New password" hint="Optional. Leave blank to keep the current one.">
            <input className={inputCls} type="password" minLength={12} value={password} onChange={(e) => setPassword(e.target.value)} />
          </Field>
          <ErrorText>{error}</ErrorText>
          <ModalFooter>
            <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
            <Button disabled={busy}>{busy ? 'Saving…' : 'Save'}</Button>
          </ModalFooter>
        </form>
      </Modal>
      {confirming && (
        <ConfirmDialog
          title={`Disable "${user.email}"?`}
          body="They're signed out and their API keys stop working immediately. Re-enabling restores them."
          confirm="Disable"
          onConfirm={save}
          onClose={() => setConfirming(false)}
        />
      )}
    </>
  )
}
