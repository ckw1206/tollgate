import { useId, useState, type FormEvent } from 'react'
import { api } from '../api'
import type { Group, Model, User } from '../types'
import { Button, ErrorText, Modal, ModalFooter, MultiSelect, useLoad } from './ui'
import { extraModelOptions } from './modelOptions'

// ModelAccessDialog: a user's extra model grants on top of their group's (Users tab row
// action and the Model access tab). Saves extra_models; [] = group only.
export default function ModelAccessDialog({ user, group, onClose, onSaved }: { user: User; group: Group; onClose: () => void; onSaved: () => void }) {
  const [extra, setExtra] = useState(user.extra_models)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const label = useId()
  const models = useLoad<Model[]>('/console/api/v1/models')
  const all = group.models.length === 0
  const { options, stale, enabledNames } = extraModelOptions(models.data, group, extra)
  const usable = (n: string) => enabledNames.includes(n)
  const name = (user.name || user.email).split(' ')[0]

  const save = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      await api(`/console/api/v1/users/${user.id}`, { method: 'PUT', body: { extra_models: extra } })
      onSaved()
    } catch (err) {
      setError((err as Error).message)
      setBusy(false)
    }
  }

  const chip = (n: string, cls: string) => <code key={n} className={`px-1.5 py-0.5 font-mono text-xs ${cls}`}>{n}</code>
  return (
    <Modal
      title={`Model access · ${user.name || user.email}`}
      subtitle={<>From group <b className="font-medium text-foreground">{group.name}</b>: {all ? 'all models' : group.models.join(', ')}</>}
      onClose={onClose}
    >
      <form onSubmit={save} className="space-y-4">
        {all ? (
          <p className="text-sm text-muted-foreground">Group allows all models.</p>
        ) : (
          <>
            {/* Not a <Field>: a wrapping <label> would forward clicks to the first chip's × button. */}
            <div className="text-sm">
              <span id={label} className="font-medium">Extra models</span>
              <MultiSelect
                labelledBy={label}
                noAll
                options={options}
                value={extra}
                onChange={setExtra}
                allLabel="None"
                allHint="group only"
                staleIds={stale}
              />
              <span className="mt-1 block text-xs text-muted-foreground">Granted to this user on top of the group. Options = enabled models the group doesn't include. None = group only.</span>
              <ErrorText>{models.error}</ErrorText>
            </div>
            <div className="text-[13px]">
              <span className="font-medium">{name} can use</span>
              <div className="mt-1 flex flex-wrap gap-1">
                {extra.filter((n) => usable(n) && !group.models.includes(n)).map((n) => chip(n, 'bg-primary text-primary-foreground'))}
                {group.models.filter(usable).map((n) => chip(n, 'bg-secondary text-secondary-foreground'))}
              </div>
            </div>
          </>
        )}
        <ErrorText>{error}</ErrorText>
        <ModalFooter>
          {all ? (
            <Button type="button" onClick={onClose}>Close</Button>
          ) : (
            <>
              <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
              <Button disabled={busy}>{busy ? 'Saving…' : 'Save'}</Button>
            </>
          )}
        </ModalFooter>
      </form>
    </Modal>
  )
}
