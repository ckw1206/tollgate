import { useCallback, useEffect, useState, type ButtonHTMLAttributes, type FormEvent, type ReactNode } from 'react'
import { api } from '../api'

// Every page composes these; no page-local buttons, inputs, tables or modals.
// Colors, radii and fonts come from Alison UI tokens (see index.css).

// controlCls carries only box, border, padding-x, color and focus styles. Size, margin and
// width live in inputCls or at the call site: Tailwind v4 orders conflicting utilities by
// value, not by position in the class string, so "inputCls + ' mt-0'" would not win.
export const controlCls =
  'rounded-md border border-input px-3 text-foreground outline-none ' +
  'focus:border-ring focus:ring-[3px] focus:ring-ring/50 aria-[invalid=true]:border-destructive disabled:opacity-50'

export const inputCls = controlCls + ' mt-1 block min-h-9 w-full py-1 text-sm'

type Variant = 'primary' | 'ghost' | 'danger'

export function Button({ variant = 'primary', size = 'md', className = '', ...props }: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: Variant; size?: 'md' | 'sm' }) {
  const styles = {
    primary: 'bg-primary text-primary-foreground hover:opacity-90',
    ghost: 'border border-border bg-transparent text-foreground hover:bg-accent',
    danger: 'bg-destructive text-destructive-foreground hover:opacity-90',
  }[variant]
  const sizing = size === 'sm' ? 'h-8 px-3 text-[13px]' : 'h-9 px-4 text-sm'
  return (
    <button
      className={`inline-flex shrink-0 items-center justify-center whitespace-nowrap rounded-md font-medium disabled:pointer-events-none disabled:opacity-50 ${sizing} ${styles} ${className}`}
      {...props}
    />
  )
}

export function Field({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <label className="block text-sm">
      <span className="font-medium">{label}</span>
      {children}
      {hint && <span className="mt-1 block text-xs text-muted-foreground">{hint}</span>}
    </label>
  )
}

export function Modal({ title, onClose, children }: { title: string; onClose: () => void; children: ReactNode }) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])
  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-background/50 p-4 pt-16 backdrop-blur-[4px]" onClick={onClose}>
      <div
        role="dialog"
        aria-modal="true"
        aria-label={title}
        className="w-full max-w-[512px] rounded-md border border-border bg-background p-6 shadow-dialog"
        onClick={(e) => e.stopPropagation()}
      >
        <h2 className="mb-4 text-lg font-semibold">{title}</h2>
        {children}
      </div>
    </div>
  )
}

export function ModalFooter({ children }: { children: ReactNode }) {
  return <div className="mt-6 flex justify-end gap-2">{children}</div>
}

// ConfirmDialog replaces window.confirm. Title = verb + quoted name + "?",
// body = the consequence, and the danger button repeats the verb. Errors keep it open.
export function ConfirmDialog({ title, body, confirm, onConfirm, onClose, variant = 'danger' }: {
  title: string
  body: ReactNode
  confirm: string
  onConfirm: () => Promise<unknown>
  onClose: () => void
  variant?: Variant
}) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      await onConfirm()
      onClose()
    } catch (err) {
      setError((err as Error).message)
      setBusy(false)
    }
  }
  return (
    <Modal title={title} onClose={onClose}>
      <form onSubmit={submit}>
        <div className="text-sm text-muted-foreground">{body}</div>
        <div className="mt-4">
          <ErrorText>{error}</ErrorText>
        </div>
        <ModalFooter>
          <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
          <Button variant={variant} disabled={busy}>{confirm}</Button>
        </ModalFooter>
      </form>
    </Modal>
  )
}

export interface Col<T> {
  head: string
  cell: (row: T) => ReactNode
}

// rows === null means still loading: three skeleton rows.
export function Table<T>({ rows, cols, empty = 'Nothing here yet.' }: { rows: T[] | null; cols: Col<T>[]; empty?: string }) {
  const last = cols.length - 1
  const cellCls = (i: number) => `whitespace-nowrap px-3 ${i === 0 ? 'font-medium' : ''} ${i === last && cols[i].head === '' ? 'text-right' : ''}`
  return (
    <div className="overflow-x-auto rounded-md border border-border">
      <table className="min-w-full text-sm">
        <thead className="bg-table-header text-left text-[13px] text-muted-foreground">
          <tr className="h-10">
            {cols.map((c, i) => (
              <th key={i} className="whitespace-nowrap px-3 font-medium">{c.head}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows === null &&
            [0, 1, 2].map((r) => (
              <tr key={r} className="h-11 border-t border-border">
                {cols.map((_, i) => (
                  <td key={i} className="px-3"><Skeleton /></td>
                ))}
              </tr>
            ))}
          {rows?.length === 0 && (
            <tr className="h-11 border-t border-border">
              <td colSpan={cols.length} className="px-3 text-center text-muted-foreground">{empty}</td>
            </tr>
          )}
          {rows?.map((r, ri) => (
            <tr key={ri} className="h-11 border-t border-border hover:bg-accent">
              {cols.map((c, i) => (
                <td key={i} className={cellCls(i)}>{c.cell(r)}</td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

export function Skeleton({ className = 'h-3 w-24' }: { className?: string }) {
  return <span aria-hidden className={`block animate-pulse rounded-sm bg-muted ${className}`} />
}

// RowActions: the last, unlabeled, right-aligned column. Small buttons, 8px apart.
export function RowActions({ children }: { children: ReactNode }) {
  return <div className="flex justify-end gap-2">{children}</div>
}

export type Tone = 'success' | 'warning' | 'error' | 'inactive'

export function Badge({ tone, children }: { tone: Tone; children: ReactNode }) {
  const t = {
    success: 'bg-success text-success-foreground',
    warning: 'bg-warning text-warning-foreground',
    error: 'bg-error text-error-foreground',
    inactive: 'bg-inactive text-inactive-foreground',
  }[tone]
  return <span className={`inline-flex min-w-[72px] justify-center rounded-sm px-2 py-0.5 text-xs font-medium ${t}`}>{children}</span>
}

export function ErrorText({ children }: { children: ReactNode }) {
  if (!children) return null
  return <p role="alert" className="text-sm text-destructive">{children}</p>
}

export function PageHeader({ title, description, children }: { title: string; description?: string; children?: ReactNode }) {
  return (
    <>
      <div className="mb-6 flex flex-wrap items-center justify-between gap-4">
        <h1 className="text-2xl font-semibold leading-[1.2]">{title}</h1>
        {children && <div className="flex flex-wrap items-center gap-2">{children}</div>}
      </div>
      {description && <p className="-mt-2 mb-4 text-sm text-muted-foreground">{description}</p>}
    </>
  )
}

export function CopyBox({ value }: { value: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <div className="flex items-start gap-2">
      <input readOnly aria-label="Secret value" value={value} className={controlCls + ' block min-h-9 w-full py-1 font-mono text-xs'} onFocus={(e) => e.target.select()} />
      <Button type="button" variant="ghost" onClick={() => navigator.clipboard.writeText(value).then(() => setCopied(true))}>
        {copied ? 'Copied' : 'Copy'}
      </Button>
    </div>
  )
}

// SecretDialog: the one-time CopyBox dialog for raw keys and invite URLs.
export function SecretDialog({ title, body, value, children, onClose }: { title: string; body: string; value: string; children?: ReactNode; onClose: () => void }) {
  return (
    <Modal title={title} onClose={onClose}>
      <p className="mb-3 text-sm text-muted-foreground">{body}</p>
      <CopyBox value={value} />
      {children}
      <ModalFooter>
        <Button onClick={onClose}>Done</Button>
      </ModalFooter>
    </Modal>
  )
}

export function Segmented<V extends string | number>({ options, value, onChange, small = false, label }: {
  options: { value: V; label: string }[]
  value: V
  onChange: (v: V) => void
  small?: boolean
  label: string
}) {
  return (
    <div role="radiogroup" aria-label={label} className="inline-flex border border-border">
      {options.map((o) => {
        const on = o.value === value
        return (
          <button
            key={String(o.value)}
            type="button"
            role="radio"
            aria-checked={on}
            onClick={() => onChange(o.value)}
            className={`${small ? 'h-6 px-2 text-[11px]' : 'h-7 px-3 text-xs'} font-medium ${on ? 'bg-primary text-primary-foreground' : 'bg-transparent hover:bg-accent'}`}
          >
            {o.label}
          </button>
        )
      })}
    </div>
  )
}

export function useLoad<T>(path: string | null) {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState('')
  const reload = useCallback(() => {
    if (!path) return
    api<T>(path).then(
      (d) => {
        setData(d)
        setError('')
      },
      (e: Error) => setError(e.message),
    )
  }, [path])
  useEffect(reload, [reload])
  return { data, error, reload }
}

// rowsOf: loading → null (skeletons); load error with no data → [] so the table still renders.
export const rowsOf = <T,>(l: { data: T[] | null; error: string }): T[] | null => l.data ?? (l.error ? [] : null)
