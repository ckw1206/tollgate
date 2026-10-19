import { useEffect, useRef, useState, type InputHTMLAttributes, type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { api } from '../api'
import { BrandIcon, useUnboot } from '../Layout'
import { mount, type GridOpts } from '../lib/tollgateGrid'
import type { LandingInfo } from '../types'

// LandingShell is the /welcome page (toll-lane grid, hero, no form); AuthShell is the page
// around the sign-in, setup and invite cards. Both are always dark.

export const DEFAULT_HEADLINE = 'One gateway for every model your team uses'
export const DEFAULT_TAGLINE = 'Keys, budgets and rate limits in front of OpenAI, Anthropic and Google — self-hosted, and it speaks the OpenAI API.'
const REPO = 'https://github.com/ckw1206/tollgate'
const ROUTES = ['OpenAI-compatible', 'Anthropic', 'Google Gemini', 'Vertex AI']
const FALLBACK: LandingInfo = { server_name: 'Tollgate', server_icon: '', landing_headline: '', landing_tagline: '', version: '', commit: '' }

// useLanding loads GET /auth/landing. `fresh` bypasses the 60s cache (Settings → Preview).
export function useLanding(fresh = false) {
  const [info, setInfo] = useState<LandingInfo | null>(null)
  useEffect(() => {
    api<LandingInfo>('/auth/landing' + (fresh ? `?fresh=${Date.now()}` : '')).then(setInfo, () => setInfo(FALLBACK))
  }, [fresh])
  return info
}

// The toll-lane grid: a fixed, non-interactive canvas painted under the relative content
// blocks (position: fixed sits in the positioned paint layer, so content must be positioned
// to cover it). Module-level opts keep the mount effect stable across renders.
function GridCanvas({ opts }: { opts: Partial<GridOpts> }) {
  const ref = useRef<HTMLCanvasElement>(null)
  useEffect(() => {
    const canvas = ref.current
    if (!canvas) return
    return mount(canvas, opts)
  }, [opts])
  return <canvas ref={ref} aria-hidden className="tg-grid" />
}

const LANDING_GRID: Partial<GridOpts> = { horizon: 0.52 }
const AUTH_GRID: Partial<GridOpts> = { intensity: 0.7, traffic: false, flow: 0.5 }

// /health answers 200 while postgres and redis are up, 503 when either is down.
function Status({ up }: { up: boolean | null }) {
  const dot = up === null ? 'bg-muted-foreground' : up ? 'bg-success-foreground' : 'bg-destructive'
  const state = up === null ? 'checking' : up ? 'Operational' : 'Down'
  return (
    <a href="/health" aria-label={`Status: ${state}`} title={`Status: ${state}`} className="flex items-center gap-1.5 hover:text-foreground">
      <span aria-hidden className={`h-1.5 w-1.5 ${dot}`} />
      Status {state}
    </a>
  )
}

function VersionTag({ info }: { info: LandingInfo | null }) {
  const v = info?.version || 'dev'
  const tagged = !!info?.version && info.version !== 'dev'
  return (
    <span>
      Version{' '}
      {tagged ? (
        <a className="hover:text-foreground" href={`${REPO}/releases/tag/${v}`} target="_blank" rel="noreferrer">
          {v}
        </a>
      ) : (
        v
      )}
    </span>
  )
}

// The header CTA is bracketed: "[ Set up ]".
function BracketCta({ label }: { label: string }) {
  return (
    <>
      <span aria-hidden className="text-muted-foreground">[</span>
      {label}
      <span aria-hidden className="text-muted-foreground">]</span>
    </>
  )
}

const heroPrimaryCls = 'inline-flex h-12 items-center rounded-md bg-foreground px-7 font-mono text-[13px] font-medium uppercase tracking-[0.1em] text-background hover:opacity-[.88]'
const heroGhostCls = 'inline-flex h-12 items-center rounded-md border border-border px-7 font-mono text-[13px] font-medium uppercase tracking-[0.1em] text-foreground hover:text-muted-foreground'

export function LandingShell({ info, cta, preview = false }: {
  info: LandingInfo | null
  cta?: { label: string; to: string }
  preview?: boolean // Settings → Preview: amber strip, and nothing but /auth/landing is loaded
}) {
  const name = info?.server_name || 'Tollgate'
  const headline = info?.landing_headline || DEFAULT_HEADLINE
  const tagline = info?.landing_tagline || DEFAULT_TAGLINE
  // One /health probe drives the footer status (none in preview).
  const [up, setUp] = useState<boolean | null>(null)
  useEffect(() => {
    if (!preview) api('/health').then(() => setUp(true), () => setUp(false))
  }, [preview])
  useUnboot()

  // No CTA until it's known, so "Sign in" never flashes before "Set up" (Landing gates it).
  const headerCta = cta && (preview
    ? <a href={cta.to} className="whitespace-nowrap hover:text-muted-foreground"><BracketCta label={cta.label} /></a>
    : <Link to={cta.to} className="whitespace-nowrap hover:text-muted-foreground"><BracketCta label={cta.label} /></Link>)
  const heroCta = cta && (preview
    ? <a href={cta.to} className={heroPrimaryCls}>{cta.label} <span aria-hidden>→</span></a>
    : <Link to={cta.to} className={heroPrimaryCls}>{cta.label} <span aria-hidden>→</span></Link>)
  const docs = preview
    ? <a href="/docs" className={heroGhostCls}>Read the docs</a>
    : <Link to="/docs" className={heroGhostCls}>Read the docs</Link>
  const docsLink = preview
    ? <a href="/docs" className="hover:text-muted-foreground">Docs</a>
    : <Link to="/docs" className="hover:text-muted-foreground">Docs</Link>

  return (
    <div data-mode="dark" data-theme="mira" className="relative flex min-h-screen flex-col overflow-hidden bg-background text-sm leading-normal text-foreground">
      <GridCanvas opts={LANDING_GRID} />
      {preview && (
        // Plain links: the preview tab never fetched the session, so these are full page loads.
        <div role="status" className="relative flex h-8 items-center justify-center gap-3 bg-warning px-4 text-[13px] text-warning-foreground">
          Preview — this is what signed-out visitors see.
          <a href="/settings" className="underline underline-offset-2">Back to Settings</a>
        </div>
      )}
      <header className="relative flex h-[88px] items-center justify-between gap-4 px-[clamp(20px,4vw,56px)]">
        <div className="flex min-w-0 items-center gap-2.5">
          <BrandIcon icon={info?.server_icon ?? ''} size={30} />
          <span className="truncate text-[22px] font-medium tracking-[-0.02em]">{name}</span>
        </div>
        <nav className="flex shrink-0 items-center gap-[clamp(16px,2.5vw,36px)] font-mono text-[13px] uppercase tracking-[0.08em]">
          <span className="hidden items-center gap-[clamp(16px,2.5vw,36px)] sm:flex">
            {docsLink}
            <a href={REPO} className="hover:text-muted-foreground">GitHub</a>
          </span>
          {headerCta}
        </nav>
      </header>

      <main className="relative flex flex-1 flex-col px-[clamp(20px,4vw,56px)] py-8 sm:py-10">
        <div className="max-w-[480px]">
          <p className="font-mono text-[11px] uppercase tracking-[0.08em] text-muted-foreground">Self-hosted · OpenAI-compatible</p>
          <p className="mt-3 text-[15px] leading-normal text-foreground/72">{tagline}</p>
        </div>
        <div className="my-auto flex flex-col items-center gap-6 pt-10 text-center sm:gap-7">
          <h1 className="text-[clamp(44px,8vw,92px)] font-medium uppercase leading-none tracking-[-0.02em]">{name}</h1>
          <p className="max-w-[760px] font-mono text-[clamp(11px,1.15vw,14px)] leading-loose tracking-[0.32em] text-foreground/72">{headline}</p>
          <div className="mt-1 flex flex-col items-center gap-3 sm:flex-row">
            {heroCta}
            {docs}
          </div>
          <div className="flex items-center gap-[28px] font-mono text-[13px] uppercase tracking-[0.08em] sm:hidden">
            {docsLink}
            <a href={REPO} className="hover:text-muted-foreground">GitHub</a>
          </div>
        </div>
        <div className="sm:self-end sm:text-right">
          <p className="font-mono text-[11px] uppercase tracking-[0.08em] text-muted-foreground">Routes to</p>
          <div className="mt-2.5 flex flex-wrap gap-x-[clamp(20px,3vw,48px)] gap-y-2 text-[15px] font-medium tracking-[-0.01em]">
            {ROUTES.map((r) => <span key={r}>{r}</span>)}
          </div>
        </div>
      </main>

      <footer className="relative flex flex-wrap items-center justify-between gap-x-4 gap-y-1 border-t border-border px-[clamp(20px,4vw,56px)] py-4 font-mono text-[11px] uppercase tracking-[0.06em] text-muted-foreground">
        <VersionTag info={info} />
        <Status up={up} />
      </footer>
    </div>
  )
}

// AuthShell: /login, /setup and /invite/:token. Always dark over the toll-lane grid (dimmed,
// no traffic). A 72px header (brand → /welcome, Docs) over the grid, then a solid 400px card
// about 10vh down. No card until there's content.
export function AuthShell({ info, children }: { info: LandingInfo | null; children?: ReactNode }) {
  useUnboot()
  return (
    <div data-mode="dark" data-theme="mira" className="relative flex min-h-screen flex-col bg-background text-sm leading-normal text-foreground">
      <GridCanvas opts={AUTH_GRID} />
      <header className="relative flex h-[72px] items-center justify-between gap-4 px-[clamp(20px,4vw,40px)]">
        <Link to="/welcome" className="flex min-w-0 items-center gap-2.5 hover:text-muted-foreground">
          <BrandIcon icon={info?.server_icon ?? ''} size={28} />
          <span className="truncate text-xl font-medium tracking-[-0.02em]">{info?.server_name || 'Tollgate'}</span>
        </Link>
        <Link to="/docs" className="font-mono text-xs uppercase tracking-[0.08em] hover:text-muted-foreground">Docs</Link>
      </header>
      <main className="relative flex flex-1 items-start justify-center px-5 pb-16 pt-[clamp(24px,10vh,120px)]">
        {children && <div className="flex w-full max-w-[400px] flex-col gap-4 border border-border bg-background p-7 shadow-landing-card">{children}</div>}
      </main>
    </div>
  )
}

// Auth form controls: 40px transparent inputs, 44px foreground-filled submit
// (dark Mira's --primary is the lane yellow, reserved for the grid and links).
export const landingInputCls =
  'h-10 rounded-md border border-input bg-transparent px-3 py-1 text-sm text-foreground outline-none focus:border-ring focus:ring-[3px] focus:ring-ring/50 disabled:opacity-50'

export function LandingField({ label, hint, className = '', ...input }: { label: string; hint?: ReactNode } & InputHTMLAttributes<HTMLInputElement>) {
  return (
    <label className="flex flex-col gap-1 text-sm">
      <span className="font-medium">{label}</span>
      <input {...input} className={`${landingInputCls} ${className}`} />
      {hint && <span className="text-xs text-muted-foreground">{hint}</span>}
    </label>
  )
}

export function LandingButton({ busy, children, type = 'submit', onClick }: { busy?: boolean; children: ReactNode; type?: 'submit' | 'button'; onClick?: () => void }) {
  return (
    <button type={type} onClick={onClick} disabled={busy} className="h-11 rounded-md bg-foreground text-sm font-medium text-background hover:opacity-90 disabled:opacity-50">
      {children}
    </button>
  )
}
