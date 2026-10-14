import { useState, type FormEvent } from 'react'
import { api, ApiError } from '../api'
import { useAuth } from '../auth'
import { budgetText, LimitsFields, limitsPayload, limitsState, limitText, type LimitsState } from '../components/LimitsFields'
import { Button, ErrorText, Field, inputCls, Modal, ModalFooter, PageHeader, RowActions, rowsOf, Table, useLoad } from '../components/ui'
import { PEOPLE_TABS } from '../Layout'
import type { Group } from '../types'

export default function Groups() {
  const { can } = useAuth()
  const writable = can('groups:write')
  const groups = useLoad<Group[]>('/console/api/v1/groups')
  const [editing, setEditing] = useState<Group | 'new' | null>(null)
  const [name, setName] = useState('')
  const [limits, setLimits] = useState<LimitsState>(limitsState())
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const open = (g: Group | 'new') => {
    setEditing(g)
    setName(g === 'new' ? '' : g.name)
    setLimits(limitsState(g === 'new' ? undefined : g))
    setError('')
  }

  const save = async (e: FormEvent) => {
    e.preventDefault()
    const body = { name, ...limitsPayload(limits) }
    setBusy(true)
    setError('')
    try {
      if (editing === 'new') await api('/console/api/v1/groups', { method: 'POST', body })
      else if (editing) await api(`/console/api/v1/groups/${editing.id}`, { method: 'PUT', body })
      setEditing(null)
      groups.reload()
    } catch (err) {
      setError((err as ApiError).status === 409 ? 'A group with this name already exists.' : (err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <PageHeader title="Users & groups" tabs={PEOPLE_TABS} description="A group is a cost center. Its budget and rate limits apply on top of each key's own limits.">
        {writable && <Button onClick={() => open('new')}>New group</Button>}
      </PageHeader>
      <div className="mb-4"><ErrorText>{groups.error}</ErrorText></div>
      <Table
        rows={rowsOf(groups)}
        empty="No groups yet."
        cols={[
          { head: 'Name', cell: (g) => g.name },
          { head: 'Members', cell: (g) => g.members },
          { head: 'Budget (USD)', cell: (g) => budgetText(g) },
          { head: 'RPM / TPM', cell: (g) => `${limitText(g.rpm_limit)} / ${limitText(g.tpm_limit)}` },
          { head: '', cell: (g) => writable && <RowActions><Button size="sm" variant="ghost" onClick={() => open(g)}>Edit</Button></RowActions> },
        ]}
      />
      {editing && (
        <Modal title={editing === 'new' ? 'New group' : `Edit "${editing.name}"`} onClose={() => setEditing(null)}>
          <form onSubmit={save} className="space-y-4">
            <Field label="Name"><input className={inputCls} required value={name} onChange={(e) => setName(e.target.value)} /></Field>
            <LimitsFields value={limits} onChange={setLimits} />
            <ErrorText>{error}</ErrorText>
            <ModalFooter>
              <Button type="button" variant="ghost" onClick={() => setEditing(null)}>Cancel</Button>
              <Button disabled={busy}>{busy ? 'Saving…' : 'Save'}</Button>
            </ModalFooter>
          </form>
        </Modal>
      )}
    </>
  )
}
