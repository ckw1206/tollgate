import { useState } from 'react'
import { dash, short, when } from '../api'
import { Button, controlCls, ErrorText, PageHeader, Table, useLoad } from '../components/ui'
import type { AuditEntry } from '../types'

const PAGE = 50

export default function Audit() {
  const [action, setAction] = useState('')
  const [offset, setOffset] = useState(0)
  const [openRow, setOpenRow] = useState<number | null>(null)
  const q = new URLSearchParams({ limit: String(PAGE), offset: String(offset), ...(action ? { action } : {}) })
  const audit = useLoad<{ entries: AuditEntry[]; total: number }>(`/console/api/v1/audit?${q}`)
  const total = audit.data?.total ?? 0

  return (
    <>
      <PageHeader title="Audit log">
        <input
          aria-label="Filter by action"
          placeholder="Filter by action, e.g. key.create"
          className={controlCls + ' h-9 w-64 text-sm'}
          value={action}
          onChange={(e) => {
            setAction(e.target.value.trim())
            setOffset(0)
          }}
        />
      </PageHeader>
      <div className="mb-4"><ErrorText>{audit.error}</ErrorText></div>
      <Table
        rows={audit.data?.entries ?? (audit.error ? [] : null)}
        empty="No audit entries."
        cols={[
          { head: 'When', cell: (a) => when(a.created_at) },
          { head: 'Who', cell: (a) => dash(a.actor_email) },
          { head: 'Action', cell: (a) => <code className="font-mono text-xs">{a.action}</code> },
          { head: 'Resource', cell: (a) => `${a.resource} ${short(a.resource_id)}` },
          { head: 'IP', cell: (a) => dash(a.ip) },
          {
            head: 'Details',
            cell: (a) => (
              <div>
                <button className="text-xs text-muted-foreground underline" aria-expanded={openRow === a.id} onClick={() => setOpenRow(openRow === a.id ? null : a.id)}>
                  {openRow === a.id ? 'hide' : 'show'}
                </button>
                {openRow === a.id && <pre className="mt-2 max-w-xl overflow-x-auto whitespace-pre-wrap font-mono text-xs">{JSON.stringify(a.details, null, 2)}</pre>}
              </div>
            ),
          },
        ]}
      />
      <div className="mt-4 flex flex-wrap items-center justify-between gap-2 text-sm text-muted-foreground">
        <span>{total === 0 ? '0 entries' : `${offset + 1}–${Math.min(offset + PAGE, total)} of ${total}`}</span>
        <div className="flex gap-2">
          <Button variant="ghost" size="sm" disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - PAGE))}>Previous</Button>
          <Button variant="ghost" size="sm" disabled={offset + PAGE >= total} onClick={() => setOffset(offset + PAGE)}>Next</Button>
        </div>
      </div>
    </>
  )
}
