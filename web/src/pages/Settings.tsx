import { useEffect, useState, type ChangeEvent, type FormEvent } from 'react'
import { api } from '../api'
import { useAuth } from '../auth'
import { BrandIcon } from '../Layout'
import { Button, controlCls, ErrorText, Field, inputCls, PageHeader, useLoad } from '../components/ui'
import type { Settings as S } from '../types'

// Searchable combobox over the browser's IANA zones: native <input list> + <datalist>.
const zones = Intl.supportedValuesOf('timeZone')

export default function Settings() {
  const { can, refresh } = useAuth()
  const current = useLoad<S>('/console/api/v1/settings')
  const [form, setForm] = useState<S | null>(null)
  const [msg, setMsg] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    if (current.data) setForm(current.data)
  }, [current.data])

  const ro = !can('settings:write') // read-only view: same layout, inputs disabled, no Save
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
  const set = (k: keyof S) => (e: ChangeEvent<HTMLInputElement>) => {
    setMsg('')
    setForm({ ...form, [k]: e.target.value })
  }
  return (
    <>
      <PageHeader title="Settings" />
      <form onSubmit={save} className="max-w-[448px] space-y-4">
        <Field label="Server name"><input className={inputCls} required disabled={ro} value={form.server_name} onChange={set('server_name')} /></Field>
        <Field label="Icon" hint="An emoji or image URL. Blank = the Tollgate icon.">
          <div className="mt-1 flex items-center gap-3">
            <span className="flex h-9 w-9 shrink-0 items-center justify-center border border-border"><BrandIcon icon={form.server_icon} /></span>
            <input className={controlCls + ' block min-h-9 w-full py-1 text-sm'} disabled={ro} value={form.server_icon} onChange={set('server_icon')} />
          </div>
        </Field>
        <Field label="Timezone" hint="Daily/monthly budgets reset at midnight in this timezone.">
          <input className={inputCls} list="tz-list" required disabled={ro} value={form.timezone} onChange={set('timezone')} />
          <datalist id="tz-list">{zones.map((z) => <option key={z} value={z} />)}</datalist>
        </Field>
        <Field label="Currency label" hint="Display only; no conversion."><input className={inputCls} required disabled={ro} value={form.currency} onChange={set('currency')} /></Field>
        <ErrorText>{error}</ErrorText>
        {msg && <p className="text-sm text-success-foreground">{msg}</p>}
        {!ro && <Button disabled={busy}>{busy ? 'Saving…' : 'Save'}</Button>}
      </form>
    </>
  )
}
