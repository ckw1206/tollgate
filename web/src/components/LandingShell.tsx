import { useEffect, useRef, useState, type InputHTMLAttributes, type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { api } from '../api'
import { BrandIcon, useUnboot } from '../Layout'
import { mount, type GridOpts } from '../lib/tollgateGrid'
import type { LandingInfo } from '../types'

// LandingShell is the /welcome page (toll-lane grid, hero, no form); AuthShell is the page
// around the sign-in, setup and invite cards. Both are always dark. Layout per the
// "Tollgate Landing" / "Tollgate Sign In" design mocks (.sdd/ref/).

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

// The toll-lane grid: a non-interactive canvas sized to the shell, painted under the
// relative content blocks (positioned, so they cover it in paint order). Module-level opts
// keep the mount effect stable across renders.
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

// Corner-bracket decoration from the mock: two L-shaped corners (top-left, bottom-right)
// built from token-coloured borders on absolutely positioned spans, around a padded box.
function Brackets({ size, color, children }: { size: 12 | 14; color: string; children: ReactNode }) {
  const w = size === 12 ? 'h-3 w-3' : 'h-3.5 w-3.5'
  const pad = size === 12 ? 'p-[5px]' : 'p-1.5'
  return (
    <span className={`relative inline-flex ${pad}`}>
      <span aria-hidden className={`absolute left-0 top-0 ${w} border-l border-t ${color}`} />
      <span aria-hidden className={`absolute bottom-0 right-0 ${w} border-b border-r ${color}`} />
      {children}
    </span>
  )
}

// /health answers 200 while postgres and redis are up, 503 when either is down. Nothing
// renders until it answers, so "checking" never flashes.
function Status({ up, className = '' }: { up: boolean | null; className?: string }) {
  if (up === null) return null
  const state = up ? 'Operational' : 'Down'
  return (
    <a href="/health" aria-label={`Status: ${state}`} title={`Status: ${state}`} className={`items-center gap-2 hover:text-primary ${className}`}>
      Status
      <span className="flex items-center gap-1.5 text-foreground">
        <span aria-hidden className={`h-1.5 w-1.5 ${up ? 'bg-primary' : 'bg-destructive'}`} />
        {state}
      </span>
    </a>
  )
}

// Footer: "Version {v}". The build version already carries the "v" prefix; a tagged build
// links to its release.
function VersionTag({ info }: { info: LandingInfo | null }) {
  const v = info?.version || 'dev'
  const tagged = !!info?.version && info.version !== 'dev'
  return (
    <span className="flex gap-2">
      Version{' '}
      {tagged ? (
        <a className="text-foreground hover:text-primary" href={`${REPO}/releases/tag/${v}`}>
          {v}
        </a>
      ) : (
        <span className="text-foreground">{v}</span>
      )}
    </span>
  )
}

// The centred release-tag link above the wordmark, with the --primary square marker.
function ReleaseTag({ info }: { info: LandingInfo | null }) {
  const v = info?.version || 'dev'
  const tagged = !!info?.version && info.version !== 'dev'
  return (
    <a className="inline-flex items-center gap-2.5 font-mono text-[11px] uppercase tracking-[0.14em] text-muted-foreground hover:text-primary" href={tagged ? `${REPO}/releases/tag/${v}` : REPO}>
      <span aria-hidden className="h-1.5 w-1.5 bg-primary" />
      Self-hosted · OpenAI-compatible
    </a>
  )
}

const headerCtaCls = 'inline-flex h-[38px] items-center bg-foreground px-5 font-medium text-background hover:bg-primary hover:text-primary-foreground'
const heroPrimaryCls = 'inline-flex h-[46px] items-center gap-3 bg-primary px-[26px] font-medium text-primary-foreground hover:bg-foreground hover:text-background'
const heroGhostCls = 'inline-flex h-[46px] items-center border border-border bg-background px-[26px] font-medium text-foreground hover:border-foreground'

export function LandingShell({ info, cta, preview = false }: {
  info: LandingInfo | null
  cta?: { label: string; to: string }
  preview?: boolean // Settings → Preview: amber strip, and nothing but /auth/landing is loaded
}) {
  const name = info?.server_name || 'Tollgate'
  const headline = info?.landing_headline || DEFAULT_HEADLINE
  const tagline = info?.landing_tagline || DEFAULT_TAGLINE
  // One /health probe drives the status (footer, or the centred row below 640px); none in preview.
  const [up, setUp] = useState<boolean | null>(null)
  useEffect(() => {
    if (!preview) api('/health').then(() => setUp(true), () => setUp(false))
  }, [preview])
  useUnboot()

  // No CTA until it's known, so "Sign in" never flashes before "Set up" (Landing gates it).
  const headerCta = cta && (
    <Brackets size={12} color="border-foreground">
      {preview
        ? <a href={cta.to} className={headerCtaCls}>{cta.label}</a>
        : <Link to={cta.to} className={headerCtaCls}>{cta.label}</Link>}
    </Brackets>
  )
  const heroCta = cta && (
    <Brackets size={14} color="border-primary">
      {preview
        ? <a href={cta.to} className={heroPrimaryCls}>{cta.label}<span aria-hidden>→</span></a>
        : <Link to={cta.to} className={heroPrimaryCls}>{cta.label}<span aria-hidden>→</span></Link>}
    </Brackets>
  )
  const docs = (
    <Brackets size={14} color="border-foreground">
      {preview
        ? <a href="/docs" className={heroGhostCls}>Read the docs</a>
        : <Link to="/docs" className={heroGhostCls}>Read the docs</Link>}
    </Brackets>
  )
  const docsLink = preview
    ? <a href="/docs" className="hover:text-primary">Docs</a>
    : <Link to="/docs" className="hover:text-primary">Docs</Link>
  const github = <a href={REPO} className="hover:text-primary">GitHub</a>

  return (
    <div data-mode="dark" data-theme="mira" className="relative flex min-h-screen flex-col overflow-hidden bg-background text-sm leading-normal text-foreground">
      <GridCanvas opts={LANDING_GRID} />
      {preview && (
        // Plain links: the preview tab never fetched the session, so these are full page loads.
        <div role="status" className="relative flex h-8 shrink-0 items-center justify-center gap-3 bg-warning px-4 text-[13px] text-warning-foreground">
          Preview — this is what signed-out visitors see.
          <a href="/settings" className="underline underline-offset-2">Back to Settings</a>
        </div>
      )}
      <header className="relative flex h-[88px] shrink-0 items-center justify-between gap-4 px-[clamp(20px,4vw,48px)]">
        <div className="flex min-w-0 items-center gap-2.5">
          <BrandIcon icon={info?.server_icon ?? ''} size={30} />
          <span className="truncate text-[20px] font-semibold tracking-[-0.02em]">{name}</span>
        </div>
        <nav className="flex shrink-0 items-center gap-[clamp(16px,3vw,36px)] font-mono text-[12px] uppercase tracking-[0.1em]">
          <span className="hidden items-center gap-[clamp(16px,3vw,36px)] sm:flex">
            {docsLink}
            {github}
          </span>
          {headerCta}
        </nav>
      </header>

      <div className="relative shrink-0 pl-[clamp(20px,10vw,160px)] pr-[clamp(20px,4vw,48px)] pt-[clamp(16px,6vh,72px)]">
        <p className="max-w-[300px] font-mono text-[13px] uppercase leading-[1.55] tracking-[0.04em] text-pretty">{tagline}</p>
      </div>

      <main className="relative flex flex-1 flex-col items-center justify-center gap-7 px-5 py-10 text-center">
        <ReleaseTag info={info} />
        <h1 className="text-[clamp(56px,11vw,152px)] font-semibold uppercase leading-[0.9] tracking-[-0.055em]">{name}</h1>
        <p className="max-w-[760px] font-mono text-[clamp(12px,1.2vw,15px)] uppercase tracking-[0.32em] text-balance">{headline}</p>
        <div className="mt-3 flex flex-wrap items-center justify-center gap-5 font-mono text-[13px] uppercase tracking-[0.1em]">
          {heroCta}
          {docs}
        </div>
        {/* Below 640px: Docs, GitHub and Status move here, centred under the hero. */}
        <div className="flex items-center justify-center gap-[clamp(16px,3vw,36px)] font-mono text-[13px] uppercase tracking-[0.1em] sm:hidden">
          {docsLink}
          {github}
          <Status up={up} className="flex" />
        </div>
      </main>

      <div className="relative flex shrink-0 items-end justify-end pl-[clamp(20px,4vw,48px)] pr-[clamp(20px,10vw,160px)] pb-[clamp(16px,5vh,56px)]">
        <div className="flex flex-col gap-1.5 font-mono text-[13px] uppercase tracking-[0.04em]">
          <span className="text-[11px] tracking-[0.14em] text-muted-foreground">Routes to</span>
          {ROUTES.map((r) => <span key={r}>{r}</span>)}
        </div>
      </div>

      <footer className="relative flex shrink-0 flex-wrap items-center justify-between gap-4 px-[clamp(20px,4vw,48px)] pb-6 font-mono text-[11px] uppercase tracking-[0.12em] text-muted-foreground">
        <VersionTag info={info} />
        <Status up={up} className="hidden sm:flex" />
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
    <div data-mode="dark" data-theme="mira" className="relative flex min-h-screen flex-col overflow-hidden bg-background text-sm leading-normal text-foreground">
      <GridCanvas opts={AUTH_GRID} />
      <header className="relative flex h-[72px] shrink-0 items-center justify-between gap-4 px-[clamp(20px,4vw,40px)]">
        <Link to="/welcome" className="flex min-w-0 items-center gap-2.5 hover:text-primary">
          <BrandIcon icon={info?.server_icon ?? ''} size={28} />
          <span className="truncate text-xl font-medium tracking-[-0.02em]">{info?.server_name || 'Tollgate'}</span>
        </Link>
        <Link to="/docs" className="font-mono text-xs uppercase tracking-[0.08em] hover:text-primary">Docs</Link>
      </header>
      <main className="relative flex flex-1 items-start justify-center px-5 pb-16 pt-[clamp(24px,10vh,120px)]">
        {children && <div className="flex w-full max-w-[400px] flex-col gap-4 border border-border bg-card p-7 shadow-landing-card">{children}</div>}
      </main>
    </div>
  )
}

// Auth form controls: 40px transparent inputs, 44px --primary-filled submit (per the mock).
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
    <button type={type} onClick={onClick} disabled={busy} className="h-11 rounded-md bg-primary font-medium text-primary-foreground hover:opacity-[.88] disabled:opacity-50">
      {children}
    </button>
  )
}
