import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { useAuth } from '../auth'
import type { ApiKey, Credential, Group, Invite, Model, User } from '../types'
import CredentialDialog from './CredentialDialog'
import InviteDialog from './InviteDialog'
import KeyDialog from './KeyDialog'
import ModelDialog from './ModelDialog'
import { Button, SecretDialog, useLoad } from './ui'

const HIDDEN_KEY = 'tollgate.onboarding.hidden'

const hiddenStored = () => {
  try {
    return localStorage.getItem(HIDDEN_KEY) === '1'
  } catch {
    return false
  }
}

const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? '' : 's'}`

// GetStarted: the "Get Tollgate ready" checklist for anyone with models:write. Steps are
// worked out from live data, never stored; Hide is remembered per browser; at 4/4 it's gone.
export default function GetStarted() {
  const { can } = useAuth()
  const [hidden, setHidden] = useState(hiddenStored)
  // A hidden (or finished) checklist costs no requests.
  const load = (path: string, allowed = true) => (!hidden && allowed ? path : null)
  const creds = useLoad<Credential[]>(load('/console/api/v1/credentials'))
  const models = useLoad<Model[]>(load('/console/api/v1/models'))
  const keys = useLoad<ApiKey[]>(load('/console/api/v1/keys'))
  const groups = useLoad<Group[]>(load('/console/api/v1/groups'))
  const users = useLoad<User[]>(load('/console/api/v1/users', can('users:read')))
  const invites = useLoad<Invite[]>(load('/console/api/v1/invites', can('invites:read')))
  const [dialog, setDialog] = useState<number | null>(null)
  const [secret, setSecret] = useState<{ title: string; body: string; value: string } | null>(null)

  // Step 4 needs users and invites: don't render a premature "3 / 4" while they load.
  const loaded =
    !!creds.data && !!models.data && !!keys.data && !!groups.data &&
    (!can('users:read') || !!users.data) && (!can('invites:read') || !!invites.data)
  const complete =
    loaded &&
    creds.data!.length > 0 &&
    models.data!.some((m) => m.enabled) &&
    keys.data!.length > 0 &&
    ((users.data?.length ?? 1) > 1 || (invites.data?.length ?? 0) > 0)
  // At 4/4 the panel is gone for good, even if a step later becomes false.
  useEffect(() => {
    if (!complete) return
    try {
      localStorage.setItem(HIDDEN_KEY, '1')
    } catch {
      // storage blocked: gone for this page view only
    }
  }, [complete])

  const secretDialog = secret && <SecretDialog title={secret.title} body={secret.body} value={secret.value} onClose={() => setSecret(null)} />
  if (hidden || !loaded) return secretDialog || null

  const enabled = models.data!.filter((m) => m.enabled)
  const teammates = (users.data?.length ?? 1) - 1
  const inviteCount = invites.data?.length ?? 0
  const steps = [
    {
      title: 'Add a provider credential',
      desc: 'An OpenAI, Anthropic or Google API key, or a GCP service account. Encrypted at rest.',
      cta: 'Add credential',
      done: creds.data!.length > 0,
      note: creds.data![0]?.name,
    },
    {
      title: 'Add a model',
      desc: 'Give it the name clients will send, pick the credential, set prices per 1M tokens.',
      cta: 'Add model',
      done: enabled.length > 0,
      note: enabled[0]?.name,
    },
    {
      title: 'Create an API key',
      desc: 'Copy it once, then point any OpenAI SDK at your base URL.',
      cta: 'Create key',
      done: keys.data!.length > 0,
      note: keys.data![0]?.alias,
    },
    {
      title: 'Invite your team',
      desc: 'Invite links carry a role and a group. Everyone lands in "default" until you add groups.',
      cta: 'Create invite',
      done: teammates > 0 || inviteCount > 0,
      note: inviteCount > 0 ? `${plural(inviteCount, 'invite')} sent` : plural(teammates, 'teammate'),
    },
  ]
  const doneCount = steps.filter((s) => s.done).length
  // At 4/4 the panel is gone, but a just-created key or invite link must stay on screen.
  if (doneCount === steps.length) return secretDialog || null
  const next = steps.findIndex((s) => !s.done)

  const hide = () => {
    try {
      localStorage.setItem(HIDDEN_KEY, '1')
    } catch {
      // storage blocked: hidden for this page view only
    }
    setHidden(true)
  }

  return (
    <section aria-label="Get started" className="mb-6 border border-border bg-card">
      <div className="flex flex-wrap items-center justify-between gap-3 px-5 pb-4 pt-5">
        <div className="flex flex-col gap-1">
          <h2 className="text-lg font-semibold">Get Tollgate ready</h2>
          <span className="text-[13px] text-muted-foreground">Four steps to the first request. Each one ticks itself off when it's done.</span>
        </div>
        <div className="flex items-center gap-4">
          <div className="flex items-center gap-2.5">
            <div className="h-1 w-[120px] bg-muted" role="progressbar" aria-valuemin={0} aria-valuemax={4} aria-valuenow={doneCount} aria-label="Setup progress">
              <div className="h-1 bg-primary" style={{ width: `${(doneCount / 4) * 100}%` }} />
            </div>
            <span className="font-mono text-xs text-muted-foreground">{doneCount} / 4</span>
          </div>
          <Button size="sm" variant="quiet" onClick={hide}>Hide</Button>
        </div>
      </div>
      <ol>
        {steps.map((s, i) => (
          <li key={s.title} className="grid grid-cols-[32px_minmax(0,1fr)_auto] items-center gap-4 border-t border-border px-5 py-3.5">
            {s.done ? (
              <span role="img" aria-label="Done" className="flex h-7 w-7 items-center justify-center bg-success text-sm font-semibold text-success-foreground">✓</span>
            ) : (
              <span className="flex h-7 w-7 items-center justify-center border border-border font-mono text-xs text-muted-foreground">{i + 1}</span>
            )}
            <div className="flex min-w-0 flex-col gap-0.5">
              <span className={`font-medium ${s.done ? 'text-muted-foreground line-through' : ''}`}>{s.title}</span>
              <span className="text-[13px] text-muted-foreground">{s.desc}</span>
            </div>
            {s.done ? (
              <span className="text-[13px] text-muted-foreground">{s.note}</span>
            ) : (
              <Button size="sm" variant={i === next ? 'primary' : 'ghost'} onClick={() => setDialog(i)}>{s.cta}</Button>
            )}
          </li>
        ))}
      </ol>
      <div className="border-t border-border px-5 py-3 text-[13px] text-muted-foreground">
        Optional: set your server name, icon, timezone and landing headline in{' '}
        <Link to="/settings" className="text-link-foreground underline underline-offset-2 hover:text-foreground">Settings</Link>.
      </div>

      {dialog === 0 && (
        <CredentialDialog
          onClose={() => setDialog(null)}
          onSaved={() => {
            setDialog(null)
            creds.reload()
          }}
        />
      )}
      {dialog === 1 && (
        <ModelDialog
          model="new"
          creds={creds.data!}
          onClose={() => setDialog(null)}
          onSaved={() => {
            setDialog(null)
            models.reload()
          }}
        />
      )}
      {dialog === 2 && (
        <KeyDialog
          apiKey="new"
          groups={groups.data!}
          canBillingGroup={can('keys:write_all')}
          onClose={() => setDialog(null)}
          onSaved={({ raw }) => {
            setDialog(null)
            if (raw) setSecret({ title: 'Your new API key', body: "Copy it now. It won't be shown again.", value: raw })
            keys.reload()
          }}
        />
      )}
      {dialog === 3 && (
        <InviteDialog
          groups={groups.data!}
          onClose={() => setDialog(null)}
          onCreated={(url) => {
            setDialog(null)
            setSecret({ title: 'Invite link', body: 'Send this link to the invitee. It works once.', value: url })
            invites.reload()
          }}
        />
      )}
      {secretDialog}
    </section>
  )
}
