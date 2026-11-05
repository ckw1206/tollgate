import { useEffect, useState, type FormEvent } from 'react'
import { api, fromNanos, toNanos } from '../api'
import type { GlobalBudget } from '../types'
import { BudgetCell } from './LimitsFields'
import { Button, ErrorText, Field, inputCls, useLoad } from './ui'

type CapType = GlobalBudget['budget_type']

// GlobalCap: the server-wide spend cap (#31), a circuit breaker above every
// group. Its own endpoint and Save: PUT /settings never touches it. Shown on
// Settings (settings:read); editable with settings:write.
export default function GlobalCap({ editable }: { editable: boolean }) {
  const cap = useLoad<GlobalBudget>('/console/api/v1/settings/budget')
  const [type, setType] = useState<CapType>('unlimited')
  const [amount, setAmount] = useState('')
  const [msg, setMsg] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    if (!cap.data) return
    setType(cap.data.budget_type)
    setAmount(cap.data.budget_type === 'unlimited' ? '' : fromNanos(cap.data.budget_nanos))
  }, [cap.data])

  const save = async (e: FormEvent) => {
    e.preventDefault()
    setMsg('')
    setError('')
    setBusy(true)
    try {
      await api('/console/api/v1/settings/budget', { method: 'PUT', body: { budget_type: type, budget_nanos: type === 'unlimited' ? 0 : toNanos(amount) } })
      cap.reload()
      setMsg('Saved.')
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <section aria-label="Global spend cap" className="mt-8 max-w-[448px] space-y-3 border-t border-border pt-4">
      <div>
        <h2 className="text-sm font-semibold">Global spend cap</h2>
        <p className="mt-1 text-xs text-muted-foreground">
          A circuit breaker above every group: once total spend across all keys and the playground reaches it, every request is refused until the window resets.
        </p>
      </div>
      <ErrorText>{cap.error}</ErrorText>
      {cap.data && (
        <div className="text-sm">
          <span className="font-medium">Current: </span>
          <BudgetCell l={{ ...cap.data, rpm_limit: null, tpm_limit: null }} />
        </div>
      )}
      <form onSubmit={save} className="space-y-3">
        <div className="grid grid-cols-2 gap-3">
          <Field label="Cap">
            <select className={inputCls} disabled={!editable} value={type} onChange={(e) => { setMsg(''); setType(e.target.value as CapType) }}>
              <option value="unlimited">Unlimited</option>
              <option value="daily">Daily</option>
              <option value="monthly">Monthly</option>
            </select>
          </Field>
          <Field label="Cap amount (USD)" hint="Switching it on counts this day's or month's spend so far.">
            <input className={inputCls} type="number" min="0.01" step="0.01" required={type !== 'unlimited'} disabled={!editable || type === 'unlimited'}
              value={amount} onChange={(e) => { setMsg(''); setAmount(e.target.value) }} />
          </Field>
        </div>
        <ErrorText>{error}</ErrorText>
        {msg && <p className="text-sm text-success-foreground">{msg}</p>}
        {editable && <Button disabled={busy}>{busy ? 'Saving…' : 'Save cap'}</Button>}
      </form>
    </section>
  )
}
