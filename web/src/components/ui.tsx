import { Fragment, useCallback, useEffect, useId, useLayoutEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type ButtonHTMLAttributes, type FormEvent, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { Link, matchPath, useLocation } from 'react-router-dom'
import { api } from '../api'
import { useAuth } from '../auth'
import { applyState, groupByProvider, initialPickerState, triggerSummary, type PickerMode, type PickerModel } from './modelPicker'

// Every page composes these; no page-local buttons, inputs, tables or modals.
// Colors, radii and fonts come from Alison UI tokens (see index.css).

// controlCls carries only box, border, padding-x, color and focus styles. Size, margin and
// width live in inputCls or at the call site: Tailwind v4 orders conflicting utilities by
// value, not by position in the class string, so "inputCls + ' mt-0'" would not win.
// controlBox is the same without padding-x, color and the disabled look, for controls that
// need their own (an inline 28px select, the locked API base).
export const controlBox =
  'rounded-md border border-input outline-none focus:border-ring focus:ring-[3px] focus:ring-ring/50 aria-[invalid=true]:border-destructive'
export const controlCls = controlBox + ' px-3 text-foreground disabled:opacity-50'

export const inputCls = controlCls + ' mt-1 block min-h-9 w-full py-1 text-sm'

type Variant = 'primary' | 'ghost' | 'danger' | 'quiet' | 'link'

// quiet: outline with muted text (e.g. Hide). link: an underlined muted text button.
export function Button({ variant = 'primary', size = 'md', className = '', ...props }: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: Variant; size?: 'md' | 'sm' | 'row' | 'xs' }) {
  const styles = {
    primary: 'bg-primary text-primary-foreground hover:opacity-90',
    ghost: 'border border-border bg-transparent text-foreground hover:bg-accent',
    danger: 'bg-destructive text-destructive-foreground hover:opacity-90',
    quiet: 'border border-border bg-transparent text-muted-foreground hover:bg-accent hover:text-foreground',
    link: 'text-muted-foreground underline underline-offset-2 hover:text-foreground',
  }[variant]
  const sizing =
    variant === 'link' ? 'text-[13px]' : size === 'xs' ? 'h-[26px] px-2.5 text-xs' : size === 'row' ? 'h-7 px-2.5 text-xs' : size === 'sm' ? 'h-8 px-3 text-[13px]' : 'h-9 px-4 text-sm'
  return (
    <button
      className={`inline-flex shrink-0 items-center justify-center whitespace-nowrap rounded-md font-medium disabled:pointer-events-none disabled:opacity-50 ${sizing} ${styles} ${className}`}
      {...props}
    />
  )
}

// Banner: a dismissible success notice at the top of a page.
export function Banner({ children, onDismiss }: { children: ReactNode; onDismiss: () => void }) {
  return (
    <div role="status" className="flex items-center justify-between gap-4 bg-success px-3.5 py-2.5 text-sm text-success-foreground">
      <span>{children}</span>
      <button type="button" aria-label="Dismiss" onClick={onDismiss} className="px-1 text-base leading-none hover:opacity-70">×</button>
    </div>
  )
}

export function Field({ label, hint, children }: { label: ReactNode; hint?: ReactNode; children: ReactNode }) {
  return (
    <label className="block text-sm">
      <span className="font-medium">{label}</span>
      {children}
      {hint && <span className="mt-1 block text-xs text-muted-foreground">{hint}</span>}
    </label>
  )
}

export function Modal({ title, subtitle, onClose, children }: { title: string; subtitle?: ReactNode; onClose: () => void; children: ReactNode }) {
  const box = useRef<HTMLDivElement>(null)
  // Move focus into the dialog on open (unless a field autofocused), and back to the opener on close.
  useEffect(() => {
    const opener = document.activeElement as HTMLElement | null
    if (!box.current?.contains(document.activeElement)) {
      box.current?.querySelector<HTMLElement>('input:not([disabled]):not([readonly]), select, textarea, button')?.focus()
    }
    return () => opener?.focus()
  }, [])
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])
  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-background/50 p-4 pt-16 backdrop-blur-[4px]" onClick={onClose}>
      <div
        ref={box}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        className="w-full max-w-[512px] rounded-md border border-border bg-background p-6 shadow-dialog"
        onClick={(e) => e.stopPropagation()}
      >
        <h2 className={`${subtitle ? 'mb-1' : 'mb-4'} text-lg font-semibold`}>{title}</h2>
        {subtitle && <p className="mb-4 text-[13px] text-muted-foreground">{subtitle}</p>}
        {children}
      </div>
    </div>
  )
}

export function ModalFooter({ children }: { children: ReactNode }) {
  return <div className="mt-6 flex flex-wrap justify-end gap-2">{children}</div>
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
  head: ReactNode
  cell: (row: T) => ReactNode
  center?: boolean
}

// rows === null means still loading: three skeleton rows.
// With `expand`, a leading 40px chevron column is added; clicking the chevron or anywhere
// on the row toggles a full-width detail row. Rows start collapsed and several may be open.
// `rowKey` keeps the open set stable across reloads (defaults to the row index).
export function Table<T>({ rows, cols, empty = 'Nothing here yet.', expand, rowKey, rowLabel }: {
  rows: T[] | null
  cols: Col<T>[]
  empty?: string
  expand?: (row: T) => ReactNode
  rowKey?: (row: T) => string
  rowLabel?: (row: T) => string
}) {
  const [open, setOpen] = useState<Set<string>>(() => new Set())
  // The detail panel is pinned to the visible width of the scroll container, so its
  // actions stay on screen when the table itself is wider (narrow screens, many columns).
  const wrapRef = useRef<HTMLDivElement>(null)
  const [viewWidth, setViewWidth] = useState<number>()
  const expandable = !!expand
  // Measure before first paint (no frame at full table width), then track resizes.
  useLayoutEffect(() => {
    const el = wrapRef.current
    if (!expandable || !el) return
    setViewWidth(el.clientWidth)
    const ro = new ResizeObserver(() => setViewWidth(el.clientWidth))
    ro.observe(el)
    return () => ro.disconnect()
  }, [expandable])
  const toggle = (k: string) =>
    setOpen((prev) => {
      const next = new Set(prev)
      if (next.has(k)) next.delete(k)
      else next.add(k)
      return next
    })
  const last = cols.length - 1
  const span = cols.length + (expand ? 1 : 0)
  const cellCls = (i: number) => `whitespace-nowrap px-3 ${i === 0 ? 'font-medium' : ''} ${i === last && cols[i].head === '' ? 'text-right' : ''} ${cols[i].center ? 'text-center' : ''}`
  return (
    <div ref={wrapRef} className="overflow-x-auto rounded-md border border-border">
      <table className="min-w-full text-sm">
        <thead className="bg-table-header text-left text-[13px] text-muted-foreground">
          <tr className="h-10">
            {expand && <th className="w-10" aria-label="Expand" />}
            {cols.map((c, i) => (
              <th key={i} className={`whitespace-nowrap px-3 font-medium ${c.center ? 'text-center' : ''}`}>{c.head}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows === null &&
            [0, 1, 2].map((r) => (
              <tr key={r} className="h-11 border-t border-border">
                {Array.from({ length: span }, (_, i) => (
                  <td key={i} className="px-3"><Skeleton className={expand && i === 0 ? 'h-3 w-3' : undefined} /></td>
                ))}
              </tr>
            ))}
          {rows?.length === 0 && (
            <tr className="h-11 border-t border-border">
              <td colSpan={span} className="px-3 text-center text-muted-foreground">{empty}</td>
            </tr>
          )}
          {rows?.map((r, ri) => {
            const k = rowKey ? rowKey(r) : String(ri)
            const isOpen = !!expand && open.has(k)
            return (
              <Fragment key={k}>
                <tr
                  className={`h-11 border-t border-border hover:bg-accent ${expand ? 'cursor-pointer' : ''}`}
                  onClick={expand ? () => !window.getSelection()?.toString() && toggle(k) : undefined}
                >
                  {expand && (
                    <td className="w-10 pl-2">
                      <button
                        type="button"
                        aria-expanded={isOpen}
                        aria-label={`Details${rowLabel ? ` for ${rowLabel(r)}` : ''}`}
                        onClick={(e) => {
                          e.stopPropagation() // the row's own click handler would toggle it back
                          toggle(k)
                        }}
                        className="flex h-6 w-6 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-foreground"
                      >
                        {isOpen ? '▾' : '▸'}
                      </button>
                    </td>
                  )}
                  {cols.map((c, i) => (
                    <td key={i} className={cellCls(i)}>{c.cell(r)}</td>
                  ))}
                </tr>
                {isOpen && (
                  <tr className="border-t border-border bg-muted/40">
                    <td colSpan={span} className="p-0">
                      <div className="sticky left-0 p-4 pl-[52px]" style={{ width: viewWidth }}>{expand!(r)}</div>
                    </td>
                  </tr>
                )}
              </Fragment>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}

export interface DetailItem {
  label: string
  value: ReactNode
  wide?: boolean // spans the full grid row (e.g. JSON)
}

// DetailPanel is the body of an expanded Table row: a label/value grid on the left
// (12px muted label, mono 12 value) and small action buttons on the right.
export function DetailPanel({ items, actions }: { items: DetailItem[]; actions?: ReactNode }) {
  return (
    <div className="flex flex-wrap items-start gap-4 whitespace-normal">
      <dl className="grid flex-[1_1_420px] grid-cols-[repeat(auto-fill,minmax(160px,1fr))] gap-x-6 gap-y-3">
        {items.map((it) => (
          <div key={it.label} className={it.wide ? 'col-span-full' : undefined}>
            <dt className="text-xs text-muted-foreground">{it.label}</dt>
            <dd className="mt-1 font-mono text-xs wrap-anywhere">{it.value}</dd>
          </div>
        ))}
      </dl>
      {actions && <div className="ml-auto">{actions}</div>}
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

export interface Tab {
  to: string
  label: string
  perm: string
}

// PageTabs: underline tabs under the page header, one route per tab. Tabs the role can't see
// are dropped, and the bar is hidden when one is left.
function PageTabs({ tabs }: { tabs: Tab[] }) {
  const { can } = useAuth()
  const { pathname } = useLocation()
  const shown = tabs.filter((t) => can(t.perm))
  if (shown.length < 2) return null
  return (
    <div role="tablist" className="-mt-2 mb-5 flex gap-1 overflow-x-auto border-b border-border">
      {shown.map((t) => {
        const on = !!matchPath(t.to, pathname)
        return (
          <Link
            key={t.to}
            to={t.to}
            role="tab"
            aria-selected={on}
            className={`flex h-9 shrink-0 items-center whitespace-nowrap px-3 text-sm ${on ? 'font-medium text-foreground shadow-[inset_0_-2px_0_var(--primary)]' : 'text-muted-foreground hover:text-foreground'}`}
          >
            {t.label}
          </Link>
        )
      })}
    </div>
  )
}

// PageHeader: title and actions (the tab's CTA), then the tabs, then the description.
export function PageHeader({ title, description, tabs, children }: { title: string; description?: string; tabs?: Tab[]; children?: ReactNode }) {
  return (
    <>
      <div className="mb-6 flex flex-wrap items-center justify-between gap-4">
        <h1 className="text-2xl font-semibold leading-[1.2]">{title}</h1>
        {children && <div className="flex flex-wrap items-center gap-2">{children}</div>}
      </div>
      {tabs && <PageTabs tabs={tabs} />}
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

export function Segmented<V extends string | number>({ options, value, onChange, small = false, tall = false, label }: {
  options: { value: V; label: string }[]
  value: V
  onChange: (v: V) => void
  small?: boolean
  tall?: boolean // 34px: lines up with 36px controls (e.g. the /docs language tabs)
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
            className={`${small ? 'h-6 px-2 text-[11px]' : tall ? 'h-[34px] px-3 text-[13px]' : 'h-7 px-3 text-xs'} font-medium ${on ? 'bg-primary text-primary-foreground' : 'bg-transparent hover:bg-accent'}`}
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
  // reload resolves once the new data (or error) is in state. Only the latest request may set
  // it: a slower response for a path (or reload) since superseded is dropped.
  const seq = useRef(0)
  const reload = useCallback(async () => {
    const n = ++seq.current
    if (!path) return
    await api<T>(path).then(
      (d) => {
        if (n !== seq.current) return
        setData(d)
        setError('')
      },
      (e: Error) => {
        if (n === seq.current) setError(e.message)
      },
    )
  }, [path])
  useEffect(() => void reload(), [reload])
  return { data, error, reload }
}

// rowsOf: loading → null (skeletons); load error with no data → [] so the table still renders.
export const rowsOf = <T,>(l: { data: T[] | null; error: string }): T[] | null => l.data ?? (l.error ? [] : null)

// CopyButton copies `value` and reads "Copied" for 1.5s.
export function CopyButton({ value, label = 'Copy', size = 'xs', variant = 'ghost', className = '', ariaLabel }: { value: string; label?: string; size?: 'xs' | 'row' | 'sm'; variant?: Variant; className?: string; ariaLabel?: string }) {
  const [copied, setCopied] = useState(false)
  useEffect(() => {
    if (!copied) return
    const t = setTimeout(() => setCopied(false), 1500)
    return () => clearTimeout(t)
  }, [copied])
  return (
    <Button type="button" size={size} variant={variant} className={className} aria-label={ariaLabel} aria-live="polite" onClick={() => navigator.clipboard.writeText(value).then(() => setCopied(true), () => undefined)}>
      {copied ? 'Copied' : label}
    </Button>
  )
}

// InlineCopy: a 36px labelled value with a Copy action, e.g. "Base URL | https://…/v1 | Copy".
export function InlineCopy({ label, value }: { label: string; value: string }) {
  const [copied, setCopied] = useState(false)
  useEffect(() => {
    if (!copied) return
    const t = setTimeout(() => setCopied(false), 1500)
    return () => clearTimeout(t)
  }, [copied])
  return (
    <div className="flex h-9 max-w-full items-stretch border border-input">
      <span className="flex items-center border-r border-input px-2.5 text-xs text-muted-foreground">{label}</span>
      <code className="flex min-w-0 items-center truncate px-2.5 font-mono text-xs">{value}</code>
      <button type="button" onClick={() => navigator.clipboard.writeText(value).then(() => setCopied(true), () => undefined)} className="border-l border-input px-3 text-[13px] font-medium hover:bg-accent">
        {copied ? 'Copied' : 'Copy'}
      </button>
    </div>
  )
}

export interface MultiOption {
  id: string
  label: string
  meta?: string // right-aligned, muted
}

// MultiSelect: wrapping chips + a popover listbox with search, an "All" row and a
// "N selected · Clear" footer. value [] means All; with noAll there is no All row and []
// means none (allLabel then names the empty state). allHint follows allLabel on the trigger,
// allSub on the list's All row. Keyboard: Enter/Space/↓ opens, ↑↓ move,
// Space or Enter toggles, Esc closes, Backspace (empty search or on the trigger) drops the
// last chip. staleIds are picks that are no longer options: shown as warning chips.
export function MultiSelect({ options, value, onChange, allLabel, allHint, allSub, noAll = false, renderOption, staleIds = [], labelledBy }: {
  options: MultiOption[]
  value: string[]
  onChange: (v: string[]) => void
  allLabel: string
  allHint?: string
  allSub?: string
  noAll?: boolean
  renderOption?: (o: MultiOption) => ReactNode
  staleIds?: string[]
  labelledBy?: string
}) {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [active, setActive] = useState(0) // 0 = All row (when shown), then the filtered options
  const wrap = useRef<HTMLDivElement>(null)
  const trigger = useRef<HTMLButtonElement>(null)
  const id = useId()
  const byId = new Map(options.map((o) => [o.id, o]))
  const q = query.trim().toLowerCase()
  const shown = q ? options.filter((o) => o.label.toLowerCase().includes(q)) : options
  const rows: (MultiOption | null)[] = q || noAll ? shown : [null, ...shown] // null = All

  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => wrap.current && !wrap.current.contains(e.target as Node) && setOpen(false)
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [open])

  const show = () => {
    setQuery('')
    setActive(0)
    setOpen(true)
  }
  const close = () => {
    setOpen(false)
    trigger.current?.focus()
  }
  const toggle = (o: MultiOption | null) => {
    if (!o) return onChange([]) // All clears the picks
    onChange(value.includes(o.id) ? value.filter((v) => v !== o.id) : [...value, o.id])
  }
  const onListKey = (e: ReactKeyboardEvent<HTMLInputElement>) => {
    const k = e.key
    if (k === 'ArrowDown' || k === 'ArrowUp') {
      e.preventDefault()
      setActive((a) => Math.max(0, Math.min(rows.length - 1, a + (k === 'ArrowDown' ? 1 : -1))))
    } else if ((k === ' ' || k === 'Enter') && rows[active] !== undefined) {
      e.preventDefault() // Space toggles rather than typing; model names have no spaces
      toggle(rows[active])
    } else if (k === 'Escape') {
      e.stopPropagation() // close the popover, not the dialog around it
      close()
    } else if (k === 'Backspace' && !query && value.length) {
      onChange(value.slice(0, -1))
    } else if (k === 'Tab') {
      setOpen(false)
    }
  }
  const optionId = (i: number) => `${id}-opt-${i}`

  return (
    <div ref={wrap} className="relative mt-1">
      <div className={controlCls + ' flex min-h-9 w-full flex-wrap items-center gap-1.5 py-1 text-sm'}>
        {value.map((v) => {
          const stale = staleIds.includes(v)
          return (
            <span
              key={v}
              title={stale ? 'No longer available' : undefined}
              className={`inline-flex items-center gap-1 rounded-sm py-0.5 pl-2 pr-1 font-mono text-xs ${stale ? 'bg-warning text-warning-foreground' : 'bg-secondary text-secondary-foreground'}`}
            >
              {byId.get(v)?.label ?? v}
              <button type="button" aria-label={`Remove ${v}`} className="px-0.5 leading-none hover:opacity-70" onClick={() => onChange(value.filter((x) => x !== v))}>×</button>
            </span>
          )
        })}
        <button
          ref={trigger}
          type="button"
          aria-haspopup="listbox"
          aria-expanded={open}
          aria-labelledby={labelledBy}
          className="flex min-w-[6rem] flex-1 items-center justify-between gap-2 self-stretch text-left outline-none"
          onClick={() => (open ? setOpen(false) : show())}
          onKeyDown={(e) => {
            if (e.key === 'Backspace' && value.length) onChange(value.slice(0, -1))
            else if (e.key === 'ArrowDown' && !open) {
              e.preventDefault()
              show()
            }
          }}
        >
          {value.length === 0 ? (
            <span>{allLabel}{allHint && <span className="text-muted-foreground"> · {allHint}</span>}</span>
          ) : <span className="sr-only">Edit selection</span>}
          <span aria-hidden className="text-muted-foreground">▾</span>
        </button>
      </div>
      {open && (
        <div className="absolute inset-x-0 top-full z-10 mt-1 rounded-md border border-border bg-background shadow-dialog">
          <input
            autoFocus
            role="combobox"
            aria-expanded="true"
            aria-controls={`${id}-list`}
            aria-activedescendant={rows.length ? optionId(active) : undefined}
            aria-label="Search"
            placeholder="Search…"
            className="block h-9 w-full border-b border-border bg-transparent px-3 text-sm outline-none"
            value={query}
            onChange={(e) => {
              setQuery(e.target.value)
              setActive(0)
            }}
            onKeyDown={onListKey}
          />
          <ul id={`${id}-list`} role="listbox" aria-multiselectable="true" aria-labelledby={labelledBy} className="max-h-60 overflow-y-auto py-1">
            {rows.map((o, i) => {
              const checked = o ? value.includes(o.id) : value.length === 0
              return (
                <li
                  key={o?.id ?? '*all'}
                  id={optionId(i)}
                  role="option"
                  aria-selected={checked}
                  className={`flex cursor-pointer items-center gap-2 px-3 py-1.5 text-sm ${i === active ? 'bg-accent' : ''}`}
                  onMouseEnter={() => setActive(i)}
                  onMouseDown={(e) => e.preventDefault()} // keep focus in the search box
                  onClick={() => toggle(o)}
                >
                  <span aria-hidden className={`flex h-4 w-4 shrink-0 items-center justify-center rounded-sm border text-[10px] ${checked ? 'border-primary bg-primary text-primary-foreground' : 'border-input'}`}>
                    {checked && '✓'}
                  </span>
                  {o ? (
                    <>
                      <span className="min-w-0 flex-1 truncate">{renderOption ? renderOption(o) : <span className="font-mono text-xs">{o.label}</span>}</span>
                      {o.meta && <span className="text-xs text-muted-foreground">{o.meta}</span>}
                    </>
                  ) : (
                    <span className="flex-1">{allLabel}{allSub && <span className="text-xs text-muted-foreground"> · {allSub}</span>}</span>
                  )}
                </li>
              )
            })}
            {rows.length === 0 && <li className="px-3 py-1.5 text-sm text-muted-foreground">No matches.</li>}
          </ul>
          <div className="flex items-center gap-1.5 border-t border-border px-3 py-2 text-xs text-muted-foreground">
            <span>{value.length ? `${value.length} selected` : allLabel}</span>
            {value.length > 0 && (
              <>
                <span aria-hidden>·</span>
                <Button type="button" variant="link" className="text-xs" onClick={() => onChange([])}>Clear</Button>
              </>
            )}
          </div>
        </div>
      )}
    </div>
  )
}

// ModelPicker: the allowed-models cell for a group — an "All models" badge or up to three
// name chips in the trigger, and a portaled popover with an All/Selected switch, a
// searchable provider-grouped list and an Apply/Cancel footer. saved [] means All. Stale
// names (deleted models) ride along in the draft but are never rendered or counted.
// onApply gets the sorted pick list; the caller owns the PUT, reload, toast and Undo.
export function ModelPicker({ models, saved, group, onApply, editable = true }: {
  models: PickerModel[] // every model, disabled included (from GET /models); any order
  saved: string[] // the group's saved list; [] = All
  group: string // group name, for the header and aria
  onApply: (list: string[]) => void
  editable?: boolean
}) {
  const [open, setOpen] = useState(false)
  const [mode, setMode] = useState<PickerMode>('all')
  const [draft, setDraft] = useState<Set<string>>(() => new Set())
  const [query, setQuery] = useState('')
  const [active, setActive] = useState(0)
  const [pos, setPos] = useState<{ left: number; top?: number; bottom?: number } | null>(null)
  const wrap = useRef<HTMLSpanElement>(null)
  const trigger = useRef<HTMLButtonElement>(null)
  const box = useRef<HTMLDivElement>(null)
  // The trigger rect and viewport height captured at open, for the post-mount flip pass.
  const anchor = useRef<{ rect: DOMRect; vh: number } | null>(null)
  const id = useId()

  const savedSet = new Set(saved)
  const optionNames = new Set(models.map((m) => m.name))
  const enabledCount = models.filter((m) => m.enabled).length
  const sum = triggerSummary(saved, models.length)
  // The draft may carry stale names; only entries that are current options count as picks.
  const pickCount = [...draft].filter((n) => optionNames.has(n)).length
  const footer = applyState(mode, saved, [...draft], models.length, pickCount)
  const q = query.trim().toLowerCase()
  const groups = groupByProvider(q ? models.filter((m) => m.name.toLowerCase().includes(q)) : models, draft)
  const visible = groups.flatMap((g) => g.models)
  const rowIdx = new Map(visible.map((m, i) => [m.name, i]))

  // Every close path discards the draft (the next open re-derives it from saved) and puts
  // focus back on the trigger.
  const close = () => {
    setOpen(false)
    trigger.current?.focus()
  }
  const show = () => {
    const rect = trigger.current!.getBoundingClientRect()
    // Always start below; the layout effect flips up after mount once the real
    // height is known, so the header is never clipped by the 400px estimate.
    anchor.current = { rect, vh: window.innerHeight }
    setPos({ left: rect.left, top: rect.bottom + 4 })
    const init = initialPickerState(saved)
    setMode(init.mode)
    setDraft(init.draft)
    setQuery('')
    setActive(0)
    setOpen(true)
  }
  const switchMode = (next: PickerMode) => {
    if (next === 'selected' && mode !== 'selected') {
      // All → Selected: re-derive the draft — the saved picks that are still options, or
      // every enabled model when the group allows all. Discards earlier edits: deterministic.
      setDraft(saved.length
        ? new Set(saved.filter((n) => optionNames.has(n)))
        : new Set(models.filter((m) => m.enabled).map((m) => m.name)))
    }
    if (next === 'all') setDraft(new Set()) // All means no picks; Apply sends []
    setMode(next)
  }
  const toggle = (name: string) =>
    setDraft((d) => {
      const next = new Set(d)
      if (next.has(name)) next.delete(name)
      else next.add(name)
      return next
    })
  const mutate = (names: string[], add: boolean) =>
    setDraft((d) => {
      const next = new Set(d)
      for (const n of names) {
        if (add) next.add(n)
        else next.delete(n)
      }
      return next
    })
  const apply = () => {
    onApply(Array.from(draft).sort())
    close()
  }
  const onSearchKey = (e: ReactKeyboardEvent<HTMLInputElement>) => {
    const k = e.key
    const row = visible[active]
    if (k === 'ArrowDown' || k === 'ArrowUp') {
      e.preventDefault()
      setActive((a) => Math.max(0, Math.min(visible.length - 1, a + (k === 'ArrowDown' ? 1 : -1))))
    } else if ((k === ' ' || k === 'Enter') && row) {
      e.preventDefault() // Space toggles rather than typing; model names have no spaces
      toggle(row.name)
    } else if (k === 'Escape') {
      e.stopPropagation() // close the popover, not the dialog around it
      close()
    } else if (k === 'Tab') {
      e.preventDefault()
      close()
    }
  }

  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      const t = e.target as Node
      if (wrap.current && !wrap.current.contains(t) && (!box.current || !box.current.contains(t))) close()
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [open])

  // The popover height varies by mode, so re-place it after mount and on mode
  // switch using the real measured height. Runs before paint, so there is no
  // flicker between the initial below-placement and the corrected one.
  useLayoutEffect(() => {
    if (!open || !pos || !anchor.current || !box.current) return
    const a = anchor.current
    const h = box.current.getBoundingClientRect().height
    const flipUp = a.rect.bottom + 4 + h > a.vh && a.rect.top - 4 - h >= 0
    const next: { left: number; top?: number; bottom?: number } = flipUp
      ? { left: a.rect.left, bottom: a.vh - a.rect.top + 4 }
      : { left: a.rect.left, top: a.rect.bottom + 4 }
    if (pos.left !== next.left || pos.top !== next.top || pos.bottom !== next.bottom) setPos(next)
  }, [open, mode])

  const triggerContent = (
    <>
      <span className="flex min-w-0 items-center gap-1.5 overflow-hidden">
        {sum.all ? (
          <>
            <span className="inline-flex h-[22px] shrink-0 items-center rounded-sm bg-primary px-2 text-xs font-medium text-primary-foreground">All models</span>
            <span className="whitespace-nowrap text-[13px] text-muted-foreground">incl. models added later</span>
          </>
        ) : (
          <>
            {sum.chips.map((c) => (
              <span key={c} className="shrink-0 rounded-sm bg-secondary px-1.5 py-0.5 font-mono text-xs text-secondary-foreground">{c}</span>
            ))}
            {sum.more > 0 && <span className="shrink-0 text-xs text-muted-foreground">+{sum.more} more</span>}
          </>
        )}
      </span>
      <span className="ml-auto shrink-0 whitespace-nowrap text-xs text-muted-foreground">{sum.label}</span>
      {editable && <span aria-hidden className="shrink-0 text-muted-foreground">▾</span>}
    </>
  )

  return (
    <span ref={wrap} className="block w-full max-w-[560px]">
      {editable ? (
        <button
          ref={trigger}
          type="button"
          aria-haspopup="dialog"
          aria-expanded={open}
          aria-label={`Allowed models for ${group}`}
          className="flex h-9 w-full items-center gap-2 overflow-hidden rounded-md border border-input px-2.5 text-left outline-none hover:bg-accent focus:border-ring focus:ring-[3px] focus:ring-ring/50"
          onClick={() => (open ? close() : show())}
          onKeyDown={(e) => {
            if (e.key === 'ArrowDown' && !open) {
              e.preventDefault()
              show()
            }
          }}
        >
          {triggerContent}
        </button>
      ) : (
        <span
          aria-label={`Allowed models for ${group}`}
          className="flex h-9 w-full cursor-default items-center gap-2 overflow-hidden rounded-md border border-input px-2.5 text-left"
        >
          {triggerContent}
        </span>
      )}
      {open && pos && createPortal(
        <div
          ref={box}
          role="dialog"
          aria-label={`Allowed models for ${group}`}
          className="fixed z-40 w-[400px] rounded-md border border-border bg-background shadow-dialog"
          style={{ left: pos.left, top: pos.top, bottom: pos.bottom }}
        >
          <div className="px-3 py-3 border-b border-border">
            <span className="block text-xs text-muted-foreground">Allowed models · {group}</span>
            <div role="radiogroup" aria-label="Selection mode" className="mt-2 grid grid-cols-2 gap-1 rounded-md border border-border p-0.5">
              <button
                type="button"
                role="radio"
                aria-checked={mode === 'all'}
                onClick={() => switchMode('all')}
                className={`h-[30px] rounded-sm text-[13px] font-medium ${mode === 'all' ? 'bg-primary text-primary-foreground' : 'hover:bg-accent'}`}
              >
                All models
              </button>
              <button
                type="button"
                role="radio"
                aria-checked={mode === 'selected'}
                onClick={() => switchMode('selected')}
                className={`h-[30px] rounded-sm text-[13px] font-medium ${mode === 'selected' ? 'bg-primary text-primary-foreground' : 'hover:bg-accent'}`}
              >
                Selected models
              </button>
            </div>
          </div>
          {mode === 'all' ? (
            <div className="px-3 py-3 space-y-1 text-sm">
              <p>Members can use every enabled model — {enabledCount} today, plus any added later.</p>
              <p className="text-[13px] text-muted-foreground">Choose Selected models to restrict this group.</p>
            </div>
          ) : (
            <>
              <div className="px-3 pt-3">
                <input
                  autoFocus
                  role="combobox"
                  aria-expanded="true"
                  aria-controls={`${id}-list`}
                  aria-activedescendant={visible.length ? `${id}-row-${active}` : undefined}
                  aria-label="Search"
                  placeholder="Search…"
                  className="h-9 w-full border-b border-border bg-transparent px-0 text-sm outline-none"
                  value={query}
                  onChange={(e) => {
                    setQuery(e.target.value)
                    setActive(0)
                  }}
                  onKeyDown={onSearchKey}
                />
              </div>
              <ul id={`${id}-list`} role="listbox" aria-multiselectable="true" className="max-h-[280px] overflow-y-auto py-1">
                {groups.map((g) => (
                  <Fragment key={g.provider}>
                    <li className="flex items-center justify-between gap-2 px-3 pt-2 pb-1">
                      <span className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">{g.provider} · {g.on}/{g.total}</span>
                      <span className="flex items-center gap-2">
                        {g.on < g.total && (
                          <button type="button" className="text-xs font-medium text-primary hover:underline" onClick={() => mutate(g.models.map((m) => m.name), true)}>
                            Select all
                          </button>
                        )}
                        {g.on > 0 && (
                          <button type="button" className="text-xs font-medium text-primary hover:underline" onClick={() => mutate(g.models.map((m) => m.name), false)}>
                            Clear
                          </button>
                        )}
                      </span>
                    </li>
                    {g.models.map((m) => {
                      const i = rowIdx.get(m.name) ?? -1
                      const inDraft = draft.has(m.name)
                      return (
                        <li
                          key={m.name}
                          id={`${id}-row-${i}`}
                          role="option"
                          aria-selected={inDraft}
                          className={`flex h-9 cursor-pointer items-center gap-2 px-3 ${i === active ? 'bg-accent' : ''}`}
                          onMouseDown={(e) => e.preventDefault()}
                          onClick={() => toggle(m.name)}
                        >
                          <span aria-hidden className={`flex h-4 w-4 shrink-0 items-center justify-center rounded-sm border text-[10px] ${inDraft ? 'border-primary bg-primary text-primary-foreground' : 'border-input'}`}>
                            {inDraft && '✓'}
                          </span>
                          <span className="min-w-0 flex-1 truncate font-mono text-xs">{m.name}</span>
                          {!m.enabled && <span className="shrink-0 rounded-sm border border-input px-1 text-[10px] text-muted-foreground">disabled</span>}
                          {inDraft && !savedSet.has(m.name) && <span className="shrink-0 bg-success px-1.5 py-0.5 text-[10px] font-medium text-success-foreground">+ added</span>}
                          {!inDraft && savedSet.has(m.name) && <span className="shrink-0 bg-destructive px-1.5 py-0.5 text-[10px] font-medium text-destructive-foreground">− removed</span>}
                        </li>
                      )
                    })}
                  </Fragment>
                ))}
                {visible.length === 0 && <li className="px-3 py-2 text-sm text-muted-foreground">No models match "{query}".</li>}
              </ul>
            </>
          )}
          <div className="flex items-center justify-between gap-2 border-t border-border px-3 py-2.5">
            <span className={`text-xs ${footer.destructive ? 'text-destructive' : 'text-muted-foreground'}`}>{footer.left}</span>
            <span className="flex items-center gap-2">
              <Button type="button" size="sm" variant="ghost" onClick={close}>Cancel</Button>
              <Button type="button" size="sm" disabled={!footer.applyable} onClick={apply}>Apply</Button>
            </span>
          </div>
        </div>,
        document.body,
      )}
    </span>
  )
}

// CheckButton: a 20px square toggle for grids (aria-pressed). dim = ticked by something
// else (e.g. "All models"), shown at 45% opacity.
export function CheckButton({ on, dim = false, ...props }: ButtonHTMLAttributes<HTMLButtonElement> & { on: boolean; dim?: boolean }) {
  return (
    <button
      type="button"
      aria-pressed={on}
      className={`inline-flex h-5 w-5 items-center justify-center border align-middle text-xs font-bold text-primary-foreground disabled:cursor-wait ${on ? 'border-primary bg-primary' : 'border-input bg-transparent'} ${dim ? 'opacity-45' : ''}`}
      {...props}
    >
      {on && '✓'}
    </button>
  )
}

// Toast: a bottom-right notice with an optional action (e.g. Undo) that closes itself after
// `ms`. Give it a new `key` per notice so the timer restarts.
export function Toast({ children, action, onAction, onClose, ms = 5000 }: { children: ReactNode; action?: string; onAction?: () => void; onClose: () => void; ms?: number }) {
  const close = useRef(onClose)
  close.current = onClose
  useEffect(() => {
    const t = setTimeout(() => close.current(), ms)
    return () => clearTimeout(t)
  }, [ms])
  return (
    <div role="status" className="fixed bottom-4 right-4 z-50 flex max-w-[calc(100%-2rem)] items-center gap-4 rounded-md border border-border bg-background py-2 pl-4 pr-2 text-sm shadow-dialog">
      <span>{children}</span>
      {action && <Button size="sm" variant="ghost" onClick={onAction}>{action}</Button>}
    </div>
  )
}
