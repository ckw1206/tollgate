import { useEffect, useState, type InputHTMLAttributes, type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { api } from '../api'
import { BrandIcon } from '../Layout'
import type { LandingInfo } from '../types'

// LandingShell is the /welcome page (illustrated backdrop, hero, no form); AuthShell is the
// plain page around the sign-in, setup and invite cards. Both are always light Mira.

export const DEFAULT_HEADLINE = 'One gateway for every model your team uses'
export const DEFAULT_TAGLINE = 'Keys, budgets and rate limits in front of OpenAI, Anthropic and Google — self-hosted, and it speaks the OpenAI API.'
const REPO = 'https://github.com/ckw1206/tollgate'
const FALLBACK: LandingInfo = { server_name: 'Tollgate', server_icon: '', landing_headline: '', landing_tagline: '', version: '', commit: '' }

// useLanding loads GET /auth/landing. `fresh` bypasses the 60s cache (Settings → Preview).
export function useLanding(fresh = false) {
  const [info, setInfo] = useState<LandingInfo | null>(null)
  useEffect(() => {
    api<LandingInfo>('/auth/landing' + (fresh ? `?fresh=${Date.now()}` : '')).then(setInfo, () => setInfo(FALLBACK))
  }, [fresh])
  return info
}

function Status({ up }: { up: boolean | null }) {
  const color = up === null ? 'bg-muted-foreground' : up ? 'bg-success-foreground' : 'bg-destructive'
  const state = up === null ? 'checking' : up ? 'operational' : 'degraded'
  return (
    <a href="/health" className="flex items-center gap-1.5 hover:text-muted-foreground" aria-label={`Status: ${state}`} title={`Status: ${state}`}>
      <span aria-hidden className={`h-1.5 w-1.5 ${color}`} />
      Status
    </a>
  )
}

// In preview, Docs is a full page load too: in-app it would meet the unfetched session and ask a
// signed-in admin to sign in.
function HeaderLinks({ className, up, preview }: { className: string; up: boolean | null; preview: boolean }) {
  return (
    <nav className={`items-center gap-[clamp(16px,3vw,40px)] font-mono text-[13px] uppercase tracking-[0.08em] ${className}`}>
      {preview ? <a href="/docs" className="hover:text-muted-foreground">Docs</a> : <Link to="/docs" className="hover:text-muted-foreground">Docs</Link>}
      <a href={REPO} className="hover:text-muted-foreground">GitHub</a>
      <Status up={up} />
    </nav>
  )
}

// index.html painted the landing base colour before React mounted; the page owns it now.
const useUnboot = () => useEffect(() => document.documentElement.classList.remove('landing-boot'), [])

export function LandingShell({ info, cta, preview = false }: {
  info: LandingInfo | null
  cta?: { label: string; to: string }
  preview?: boolean // Settings → Preview: amber strip, and nothing but /auth/landing is loaded
}) {
  const name = info?.server_name || 'Tollgate'
  // One /health probe drives the Status dot (none in preview).
  const [up, setUp] = useState<boolean | null>(null)
  useEffect(() => {
    if (!preview) api('/health').then(() => setUp(true), () => setUp(false))
  }, [preview])
  const tagged = !!info?.version && info.version !== 'dev'
  useUnboot()
  const ctaCls = 'inline-flex h-12 items-center rounded-md bg-foreground px-7 font-mono text-[13px] font-medium uppercase tracking-[0.1em] text-background hover:opacity-[.88]'
  return (
    <div data-mode="light" data-theme="mira" className="landing-bg relative flex min-h-screen flex-col overflow-hidden text-sm leading-normal text-foreground">
      {preview && (
        // Plain links: the preview tab never fetched the session, so these are full page loads.
        <div role="status" className="flex h-8 items-center justify-center gap-3 bg-warning px-4 text-[13px] text-warning-foreground">
          Preview — this is what signed-out visitors see.
          <a href="/settings" className="underline underline-offset-2">Back to Settings</a>
        </div>
      )}
      <header className="flex h-[88px] items-center justify-between gap-4 px-[clamp(20px,4vw,56px)] sm:grid sm:grid-cols-[1fr_auto_1fr]">
        <HeaderLinks className="hidden sm:flex" up={up} preview={preview} />
        <div className="flex min-w-0 items-center gap-2.5 sm:col-start-2">
          <BrandIcon icon={info?.server_icon ?? ''} size={32} />
          <span className="truncate text-[26px] font-medium tracking-[-0.02em]">{name}</span>
        </div>
        {cta && (
          <div className="flex shrink-0 justify-end sm:col-start-3">
            {preview ? <a href={cta.to} className={ctaCls}>{cta.label}</a> : <Link to={cta.to} className={ctaCls}>{cta.label}</Link>}
          </div>
        )}
      </header>

      <main className="flex flex-1 flex-col items-center justify-center px-6 pb-[18vh] pt-12 text-center">
        {info && (
          <>
            <a
              href={tagged ? `${REPO}/releases/tag/${info.version}` : REPO}
              className="inline-flex items-center gap-2 border border-border bg-background/80 px-3 py-1.5 text-[13px] font-medium backdrop-blur-[6px] hover:text-muted-foreground"
            >
              <span className="font-mono text-xs text-muted-foreground">{info.version || 'dev'}</span>
              <span>Self-hosted, OpenAI-compatible</span>
              <span aria-hidden>→</span>
            </a>
            <h1 className="mt-7 max-w-[900px] text-[clamp(36px,5vw,62px)] font-medium leading-[1.06] tracking-[-0.03em] text-balance">
              {info.landing_headline || DEFAULT_HEADLINE}
            </h1>
            <p className="mt-[18px] max-w-[620px] text-[clamp(16px,1.4vw,19px)] leading-normal text-foreground/72 text-pretty">
              {info.landing_tagline || DEFAULT_TAGLINE}
            </p>
            <div className="mt-10 flex flex-col items-center gap-3.5">
              <span className="font-mono text-xs uppercase tracking-[0.16em] text-muted-foreground">Routes to</span>
              <div className="flex flex-wrap justify-center gap-x-[clamp(24px,4vw,64px)] gap-y-3 text-[clamp(16px,1.6vw,21px)] font-medium tracking-[-0.01em]">
                <span>OpenAI-compatible</span>
                <span>Anthropic</span>
                <span>Google Gemini</span>
                <span>Vertex AI</span>
              </div>
            </div>
          </>
        )}
        <HeaderLinks className="mt-8 flex flex-wrap justify-center sm:hidden" up={up} preview={preview} />
      </main>
    </div>
  )
}

// AuthShell: /login, /setup and /invite/:token. A 72px header (brand → /welcome, Docs) over a
// plain base colour, then a solid 400px card about 10vh down. No card until there's content.
export function AuthShell({ info, children }: { info: LandingInfo | null; children?: ReactNode }) {
  useUnboot()
  return (
    <div data-mode="light" data-theme="mira" className="auth-bg flex min-h-screen flex-col text-sm leading-normal text-foreground">
      <header className="flex h-[72px] items-center justify-between gap-4 px-[clamp(20px,4vw,40px)]">
        <Link to="/welcome" className="flex min-w-0 items-center gap-2.5 hover:text-muted-foreground">
          <BrandIcon icon={info?.server_icon ?? ''} size={28} />
          <span className="truncate text-xl font-medium tracking-[-0.02em]">{info?.server_name || 'Tollgate'}</span>
        </Link>
        <Link to="/docs" className="font-mono text-xs uppercase tracking-[0.08em] hover:text-muted-foreground">Docs</Link>
      </header>
      <main className="flex flex-1 items-start justify-center px-5 pb-16 pt-[clamp(24px,10vh,120px)]">
        {children && <div className="flex w-full max-w-[400px] flex-col gap-4 border border-border bg-background p-7 shadow-landing-card">{children}</div>}
      </main>
    </div>
  )
}

// Auth form controls: 40px transparent inputs, 44px foreground-filled submit
// (Mira light's --primary is near-white, so it isn't used here).
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
