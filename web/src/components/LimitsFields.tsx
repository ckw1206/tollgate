import type { ChangeEvent } from 'react'
import { fromNanos, num, toNanos, usd } from '../api'
import type { BudgetType, Limits } from '../types'
import { Field, inputCls } from './ui'

export interface LimitsState { budget_type: BudgetType; budget: string; rpm: string; tpm: string }

export const limitsState = (l?: Partial<Limits>): LimitsState => ({
  budget_type: l?.budget_type ?? 'unlimited',
  budget: l?.budget_nanos ? fromNanos(l.budget_nanos) : '',
  rpm: l?.rpm_limit?.toString() ?? '',
  tpm: l?.tpm_limit?.toString() ?? '',
})

export const limitsPayload = (s: LimitsState) => ({
  budget_type: s.budget_type,
  budget_nanos: s.budget_type === 'unlimited' ? 0 : toNanos(s.budget),
  rpm_limit: num(s.rpm),
  tpm_limit: num(s.tpm),
})

// Always a 2×2 grid in this order: Budget, Budget amount, Requests/minute, Tokens/minute.
export function LimitsFields({ value, onChange }: { value: LimitsState; onChange: (v: LimitsState) => void }) {
  const set = (k: keyof LimitsState) => (e: ChangeEvent<HTMLInputElement | HTMLSelectElement>) => onChange({ ...value, [k]: e.target.value })
  return (
    <div className="grid grid-cols-2 gap-3">
      <Field label="Budget">
        <select className={inputCls} value={value.budget_type} onChange={set('budget_type')}>
          <option value="unlimited">Unlimited</option>
          <option value="daily">Daily</option>
          <option value="monthly">Monthly</option>
          <option value="total">Total (never resets)</option>
        </select>
      </Field>
      <Field label="Budget amount (USD)">
        <input className={inputCls} type="number" min="0" step="0.01" required={value.budget_type !== 'unlimited'} disabled={value.budget_type === 'unlimited'} value={value.budget} onChange={set('budget')} />
      </Field>
      <Field label="Requests / minute" hint="Blank = no limit">
        <input className={inputCls} type="number" min="1" value={value.rpm} onChange={set('rpm')} />
      </Field>
      <Field label="Tokens / minute" hint="Blank = no limit">
        <input className={inputCls} type="number" min="1" value={value.tpm} onChange={set('tpm')} />
      </Field>
    </div>
  )
}

// budgetText: "12.40 / 50.00 monthly" or "Unlimited".
export function budgetText(l: Limits) {
  if (l.budget_type === 'unlimited') return 'Unlimited'
  return `${usd(l.spend_nanos).slice(1)} / ${usd(l.budget_nanos).slice(1)} ${l.budget_type}`
}

export const limitText = (n: number | null) => (n == null ? '∞' : String(n))
