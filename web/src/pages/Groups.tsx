import { useId, useState, type FormEvent } from 'react'
import { api, ApiError } from '../api'
import { useAuth } from '../auth'
import { budgetText, LimitsFields, limitsPayload, limitsState, limitText, type LimitsState } from '../components/LimitsFields'
import { Button, ErrorText, Field, inputCls, Modal, ModalFooter, MultiSelect, PageHeader, RowActions, rowsOf, Table, useLoad } from '../components/ui'
import { PEOPLE_TABS } from '../Layout'
import type { Affected, Group, Model } from '../types'

export default function Groups() {
  const { can } = useAuth()
  const writable = can('groups:write')
  const groups = useLoad<Group[]>('/console/api/v1/groups')
  const models = useLoad<Model[]>(writable ? '/console/api/v1/models' : null)
  const enabled = (models.data ?? []).filter((m) => m.enabled).sort((a, b) => a.name.localeCompare(b.name))
  const modelsLabel = useId()
  const [editing, setEditing] = useState<Group | 'new' | null>(null)
  const [name, setName] = useState('')
  const [allowed, setAllowed] = useState<string[]>([])
  const [limits, setLimits] = useState<LimitsState>(limitsState())
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [affected, setAffected] = useState(0)

  const open = (g: Group | 'new') => {
    setEditing(g)
    setName(g === 'new' ? '' : g.name)
    setAllowed(g === 'new' ? [] : g.models)
    setLimits(limitsState(g === 'new' ? undefined : g))
    setError('')
    setAffected(0)
  }

  const save = async (e: FormEvent) => {
    e.preventDefault()
    const body = { name, models: allowed, ...limitsPayload(limits) }
    setBusy(true)
    setError('')
    try {
      let n = 0
      if (editing === 'new') await api('/console/api/v1/groups', { method: 'POST', body })
      else if (editing) n = (await api<Affected<Group>>(`/console/api/v1/groups/${editing.id}`, { method: 'PUT', body })).affected_keys
      groups.reload()
      // Keys whose lists now name models this group refuses keep their lists: say so before closing.
      if (n > 0) setAffected(n)
      else setEditing(null)
    } catch (err) {
      setError((err as ApiError).status === 409 ? 'A group with this name already exists.' : (err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <PageHeader
        title="Users & groups"
        tabs={PEOPLE_TABS}
        description="A group is a cost center and a model boundary. Its budget, rate limits and allowed models apply on top of each user's and key's own."
      >
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
          { head: 'Allowed models', cell: (g) => (g.models.length ? g.models.join(', ') : 'All') },
          { head: '', cell: (g) => writable && <RowActions><Button size="sm" variant="ghost" onClick={() => open(g)}>Edit</Button></RowActions> },
        ]}
      />
      {editing && (
        <Modal title={editing === 'new' ? 'New group' : `Edit "${editing.name}"`} onClose={() => setEditing(null)}>
          <form onSubmit={save} className="space-y-4">
            <Field label="Name"><input className={inputCls} required value={name} onChange={(e) => setName(e.target.value)} /></Field>
            {/* Not a <Field>: a wrapping <label> would forward clicks to the first chip's × button. */}
            <div className="text-sm">
              <span id={modelsLabel} className="font-medium">Allowed models</span>
              <MultiSelect
                labelledBy={modelsLabel}
                options={enabled.map((m) => ({ id: m.name, label: m.name, meta: m.provider }))}
                value={allowed}
                onChange={setAllowed}
                allLabel="All models"
                allHint={models.data ? `${enabled.length} enabled, incl. future ones` : undefined}
                allSub="Includes models added later"
                staleIds={models.data ? allowed.filter((n) => !enabled.some((m) => m.name === n)) : []}
              />
              <span className="mt-1 block text-xs text-muted-foreground">Members can use these, plus any extra models granted to them. All models = everything enabled, incl. future ones.</span>
              <ErrorText>{models.error}</ErrorText>
            </div>
            <LimitsFields value={limits} onChange={setLimits} />
            <ErrorText>{error}</ErrorText>
            {affected > 0 && (
              <p role="status" className="bg-warning px-3 py-2 text-[13px] text-warning-foreground">
                {affected} {affected === 1 ? 'key lists' : 'keys list'} models this group no longer allows. Their lists aren't changed; requests for those models get 403 <code className="font-mono">model_not_allowed</code>.
              </p>
            )}
            <ModalFooter>
              {affected > 0 ? (
                <Button type="button" onClick={() => setEditing(null)}>Close</Button>
              ) : (
                <>
                  <Button type="button" variant="ghost" onClick={() => setEditing(null)}>Cancel</Button>
                  <Button disabled={busy}>{busy ? 'Saving…' : 'Save'}</Button>
                </>
              )}
            </ModalFooter>
          </form>
        </Modal>
      )}
    </>
  )
}
