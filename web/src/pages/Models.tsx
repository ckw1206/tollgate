import { useState } from 'react'
import { api, ApiError, fromMicros } from '../api'
import { useAuth } from '../auth'
import {
  Badge, Button, ConfirmDialog, DetailPanel, ErrorText, PageHeader, RowActions, rowsOf, Table, useLoad, type DetailItem,
} from '../components/ui'
import { credEndpoint } from '../components/CredentialDialog'
import ModelDialog, { payload, toForm } from '../components/ModelDialog'
import { MODEL_TABS } from '../Layout'
import type { Credential, Model } from '../types'

const price = (micros: number | null) => (micros == null ? 'input' : fromMicros(micros))

// The endpoint requests go to, resolved like core/llm/load.go: the credential's, else the
// model's own. A model keeps its own only without a credential, or (legacy) when its
// credential has no endpoint.
function endpoint(m: Model, creds: Credential[] | null) {
  const c = m.credential_id ? creds?.find((c) => c.id === m.credential_id) : undefined
  if (!c || (!c.api_base && m.api_base)) return m.api_base || '—'
  return c.kind === 'gcp_service_account' ? '— (Vertex default)' : `${credEndpoint(c)} (from ${c.name})`
}

// API base and Extra are only shown to models:write; the server redacts them for everyone else.
function modelDetail(m: Model, writable: boolean, creds: Credential[] | null): DetailItem[] {
  const extra = m.extra && Object.keys(m.extra).length ? JSON.stringify(m.extra, null, 2) : null
  return [
    { label: 'Upstream model', value: m.upstream_model },
    ...(writable ? [{ label: 'API base', value: endpoint(m, creds) }] : []),
    { label: 'Cache read · write $ / 1M', value: `${price(m.price_cache_read_micros)} · ${price(m.price_cache_write_micros)}` },
    ...(writable ? [{ label: 'Extra', wide: true, value: extra ? <pre className="max-w-2xl overflow-x-auto whitespace-pre">{extra}</pre> : '—' }] : []),
  ]
}

export default function Models() {
  const { can } = useAuth()
  const writable = can('models:write')
  const models = useLoad<Model[]>('/console/api/v1/models')
  const creds = useLoad<Credential[]>(writable ? '/console/api/v1/credentials' : null)
  const [editing, setEditing] = useState<Model | 'new' | null>(null)
  const [deleting, setDeleting] = useState<Model | null>(null)
  const [pageError, setPageError] = useState('')
  const open = (m: Model | 'new') => setEditing(m)

  const toggle = async (m: Model) => {
    setPageError('')
    await api(`/console/api/v1/models/${m.id}`, { method: 'PUT', body: payload({ ...toForm(m), enabled: !m.enabled }) }).catch((e: Error) => setPageError(e.message))
    models.reload()
  }

  const remove = (m: Model) =>
    api(`/console/api/v1/models/${m.id}`, { method: 'DELETE' })
      .catch((e: ApiError) => {
        throw e.status === 409 ? new Error('This model has spend history, so it can’t be deleted. Disable it instead.') : e
      })
      .finally(models.reload)

  const credName = (id: string | null) => (id ? creds.data?.find((c) => c.id === id)?.name ?? id.slice(0, 8) : '—')
  return (
    <>
      <PageHeader
        title="Models"
        tabs={MODEL_TABS}
        description={writable ? undefined : 'Models you can use — from your group, plus any an admin granted to you. Need another? Ask an admin.'}
      >
        {writable && <Button onClick={() => open('new')}>Add model</Button>}
      </PageHeader>
      <div className="mb-4 space-y-1">
        <ErrorText>{models.error}</ErrorText>
        <ErrorText>{pageError}</ErrorText>
      </div>
      <Table
        rows={rowsOf(models)}
        empty="No models yet."
        rowKey={(m) => m.id}
        rowLabel={(m) => m.name}
        expand={(m) => (
          <DetailPanel
            items={modelDetail(m, writable, creds.data)}
            actions={writable && (
              <RowActions>
                <Button size="sm" variant="ghost" onClick={() => open(m)}>Edit</Button>
                <Button size="sm" variant="ghost" onClick={() => toggle(m)}>{m.enabled ? 'Disable' : 'Enable'}</Button>
                <Button size="sm" variant="danger" onClick={() => setDeleting(m)}>Delete</Button>
              </RowActions>
            )}
          />
        )}
        cols={[
          { head: 'Name', cell: (m) => m.name },
          { head: 'Provider', cell: (m) => `${m.provider} · ${m.upstream_model}` },
          { head: '$ / 1M in · out', cell: (m) => `${fromMicros(m.price_input_micros)} · ${fromMicros(m.price_output_micros)}` },
          ...(writable ? [{ head: 'Credential', cell: (m: Model) => credName(m.credential_id) }] : []),
          // Without models:write the server lists only your enabled, allowed models.
          ...(writable ? [] : [{ head: 'Your access', cell: (m: Model) => (m.access === 'granted' ? 'granted to you' : 'group') }]),
          { head: 'Status', cell: (m) => (m.enabled ? <Badge tone="success">enabled</Badge> : <Badge tone="inactive">disabled</Badge>) },
        ]}
      />

      {editing && (
        <ModelDialog
          model={editing}
          creds={creds.data ?? []}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null)
            models.reload()
          }}
        />
      )}

      {deleting && (
        <ConfirmDialog
          title={`Delete "${deleting.name}"?`}
          body="Clients can no longer call this model. Models with spend history can't be deleted; disable them instead."
          confirm="Delete"
          onConfirm={() => remove(deleting)}
          onClose={() => setDeleting(null)}
        />
      )}
    </>
  )
}
