import { useEffect, useRef, useState, type ChangeEvent, type FormEvent } from 'react'
import { api } from '../api'
import { useAuth } from '../auth'
import { ICON_TYPES, iconFromFile } from '../icon'
import { BrandIcon } from '../Layout'
import { DEFAULT_HEADLINE, DEFAULT_TAGLINE } from '../components/LandingShell'
import { Button, controlCls, ErrorText, Field, inputCls, PageHeader, useLoad } from '../components/ui'
import type { Settings as S } from '../types'

// Browsers omit UTC from their zone list, but it's the server default and a valid choice.
const zones = ['UTC', ...Intl.supportedValuesOf('timeZone').filter((z) => z !== 'UTC')]

export default function Settings() {
  const { can, refresh } = useAuth()
  const current = useLoad<S>('/console/api/v1/settings')
  const [form, setForm] = useState<S | null>(null)
  const [msg, setMsg] = useState('')
  const [error, setError] = useState('')
  const [iconError, setIconError] = useState('')
  const [busy, setBusy] = useState(false)
  const fileRef = useRef<HTMLInputElement>(null)
  useEffect(() => {
    if (current.data) setForm(current.data)
  }, [current.data])

  const ro = !can('settings:write') // read-only view: same layout, inputs disabled, no Save or upload
  if (!form)
    return (
      <>
        <PageHeader title="Settings" />
        <ErrorText>{current.error}</ErrorText>
      </>
    )

  const save = async (e: FormEvent) => {
    e.preventDefault()
    setMsg('')
    setError('')
    setBusy(true)
    try {
      await api('/console/api/v1/settings', { method: 'PUT', body: form })
      await refresh()
      setMsg('Saved.')
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }
  // Functional update: an icon finishes processing after an await, and must not undo
  // edits made to other fields in the meantime.
  const update = (patch: Partial<S>) => {
    setMsg('')
    setForm((f) => f && { ...f, ...patch })
  }
  const set = (k: keyof S) => (e: ChangeEvent<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>) => update({ [k]: e.target.value })

  const pickIcon = async (e: ChangeEvent<HTMLInputElement>) => {
    const f = e.target.files?.[0]
    e.target.value = '' // picking the same file again must fire change
    if (!f) return
    setIconError('')
    try {
      update({ server_icon: await iconFromFile(f) })
    } catch (err) {
      setIconError((err as Error).message)
    }
  }

  // A saved zone the browser doesn't list (e.g. Etc/GMT-8, which Go accepts) stays selectable.
  const saved = current.data?.timezone
  const zoneOptions = saved && !zones.includes(saved) ? [saved, ...zones] : zones
  return (
    <>
      <PageHeader title="Settings" />
      <form onSubmit={save} className="max-w-[448px] space-y-4">
        <Field label="Server name"><input className={inputCls} required disabled={ro} value={form.server_name} onChange={set('server_name')} /></Field>
        <div className="text-sm">
          <span className="font-medium">Icon</span>
          <div className="mt-1 flex items-center gap-3">
            <span className="inline-flex shrink-0 border border-border">
              <BrandIcon icon={form.server_icon} size={48} alt={form.server_icon ? 'Current server icon' : 'Default Tollgate icon'} />
            </span>
            {!ro && (
              <>
                <input ref={fileRef} type="file" accept={ICON_TYPES.join(',')} className="hidden" aria-label="Icon file" onChange={pickIcon} />
                <Button type="button" variant="ghost" size="sm" onClick={() => fileRef.current?.click()}>Upload icon…</Button>
                <Button type="button" variant="link" disabled={form.server_icon === ''} onClick={() => { setIconError(''); update({ server_icon: '' }) }}>
                  Reset to default
                </Button>
              </>
            )}
          </div>
          {!ro && <span className="mt-1 block text-xs text-muted-foreground">PNG, JPEG or WebP · square · min 128×128 · max 256 KB</span>}
          <div className="mt-1"><ErrorText>{iconError}</ErrorText></div>
        </div>
        <Field label="Timezone" hint="Daily/monthly budgets reset at midnight in this timezone.">
          <select className={inputCls} required disabled={ro} value={form.timezone} onChange={set('timezone')}>
            {zoneOptions.map((z) => <option key={z} value={z}>{z}</option>)}
          </select>
        </Field>
        <Field label="Currency label" hint="Display only; no conversion."><input className={inputCls} required disabled={ro} value={form.currency} onChange={set('currency')} /></Field>
        <section aria-label="Landing page" className="space-y-3 border-t border-border pt-4">
          <div className="flex items-baseline justify-between">
            <h2 className="text-sm font-semibold">Landing page</h2>
            <a href="/welcome?preview=1" target="_blank" rel="noopener" className="text-[13px] text-link-foreground underline underline-offset-2 hover:text-foreground">
              Preview ↗
            </a>
          </div>
          <Field label="Headline" hint="Shown on the landing page. Max 80 characters · blank = default.">
            <input className={inputCls} maxLength={80} disabled={ro} placeholder={DEFAULT_HEADLINE} value={form.landing_headline} onChange={set('landing_headline')} />
          </Field>
          <Field label="Tagline" hint="Max 160 characters · plain text, no links.">
            <textarea className={controlCls + ' mt-1 block h-16 w-full resize-y py-1 text-sm'} maxLength={160} disabled={ro} placeholder={DEFAULT_TAGLINE} value={form.landing_tagline} onChange={set('landing_tagline')} />
          </Field>
        </section>
        <ErrorText>{error}</ErrorText>
        {msg && <p className="text-sm text-success-foreground">{msg}</p>}
        {!ro && <Button disabled={busy}>{busy ? 'Saving…' : 'Save'}</Button>}
      </form>
    </>
  )
}
