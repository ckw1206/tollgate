import { useEffect, useState } from 'react'
import { Link, NavLink, Outlet } from 'react-router-dom'
import { useAuth } from './auth'
import GateBar from './components/GateBar'
import { Segmented, type Tab } from './components/ui'
import { resolveMode, storedPref, THEME_KEY, type ThemePref } from './theme'
import type { ServerInfo } from './types'

// One layout for every role; nav items are filtered by permission. Order is fixed.
const nav = [
  { to: '/', label: 'Dashboard', perm: 'spend:read_own' },
  { to: '/playground', label: 'Playground', perm: 'playground:use' },
  { to: '/keys', label: 'API keys', perm: 'keys:read_own' },
  { to: '/models', label: 'Models', perm: 'models:read' },
  { to: '/people', label: 'Users & groups', perm: 'groups:read_own' },
  { to: '/audit', label: 'Audit log', perm: 'audit:read' },
  { to: '/settings', label: 'Settings', perm: 'settings:read' },
]

// Page tabs of the Models and Users & groups nav items, one route each.
export const MODEL_TABS: Tab[] = [
  { to: '/models', label: 'Models', perm: 'models:read' },
  { to: '/models/credentials', label: 'Credentials', perm: 'models:write' },
]
export const PEOPLE_TABS: Tab[] = [
  { to: '/people', label: 'Users', perm: 'users:read' },
  { to: '/people/groups', label: 'Groups', perm: 'groups:read_own' },
  { to: '/people/invites', label: 'Invites', perm: 'invites:read' },
  { to: '/people/access', label: 'Model access', perm: 'groups:write' },
]

const REPO = 'https://github.com/ckw1206/tollgate'

// server_icon is "" (default) or an uploaded image stored as a data URL.
export function BrandIcon({ icon, size = 28, alt = '' }: { icon: string; size?: number; alt?: string }) {
  const src = icon.startsWith('data:image/') ? icon : '/tollgate.png'
  return <img src={src} alt={alt} width={size} height={size} className="shrink-0 object-contain" style={{ width: size, height: size }} />
}

// Theme: System follows the OS live (a matchMedia change updates data-mode
// while the console is open); Dark/Light are explicit picks stored in
// localStorage, and System removes the key. The hook runs only under the
// console layout — the landing and auth shells own their always-dark pages,
// so this never touches data-mode there.
function useTheme(): [ThemePref, (m: ThemePref) => void] {
  const [mode, setMode] = useState<ThemePref>(storedPref)
  useEffect(() => {
    if (mode !== 'system') {
      document.documentElement.dataset.mode = mode
      return
    }
    const mq = window.matchMedia('(prefers-color-scheme: dark)')
    const apply = () => {
      document.documentElement.dataset.mode = resolveMode(null, mq.matches)
    }
    apply()
    mq.addEventListener('change', apply)
    return () => mq.removeEventListener('change', apply)
  }, [mode])
  const set = (m: ThemePref) => {
    try {
      if (m === 'system') localStorage.removeItem(THEME_KEY)
      else localStorage.setItem(THEME_KEY, m)
    } catch {
      // storage blocked: the choice still applies for this page view
    }
    setMode(m)
  }
  return [mode, set]
}

export function Version({ server }: { server: ServerInfo }) {
  const tagged = server.version && server.version !== 'dev'
  return (
    <div className="mt-2 flex flex-wrap gap-x-1 text-[11px] text-muted-foreground">
      <span>Version:</span>
      {tagged ? <a className="hover:underline" href={`${REPO}/releases/tag/${server.version}`} target="_blank" rel="noreferrer">{server.version}</a> : 'dev'}
      {server.commit && (
        <a className="font-mono hover:underline" href={`${REPO}/commit/${server.commit}`} target="_blank" rel="noreferrer">({server.commit.slice(0, 7)})</a>
      )}
    </div>
  )
}

// mode/setMode come from the single useTheme() in Layout: Identity renders
// twice (CSS-hidden sidebar copy + mobile drawer), and per-instance state
// would let the two copies disagree — and each would keep its own
// matchMedia listener alive.
function Identity({ onSignOut, onNavigate, mode, setMode }: {
  onSignOut: () => void
  onNavigate?: () => void
  mode: ThemePref
  setMode: (m: ThemePref) => void
}) {
  const { me } = useAuth()
  if (!me) return null
  return (
    <div className="flex flex-col gap-0.5 text-xs text-muted-foreground">
      <span className="font-medium text-foreground">{me.user.name}</span>
      <span>{me.user.email} · {me.user.role}</span>
      <button className="mt-1.5 self-start underline hover:text-foreground" onClick={onSignOut}>Sign out</button>
      <div className="mt-2.5 flex items-center justify-between gap-2">
        <span>Theme</span>
        <Segmented small label="Theme" value={mode} onChange={setMode} options={[{ value: 'system', label: 'System' }, { value: 'dark', label: 'Dark' }, { value: 'light', label: 'Light' }]} />
      </div>
      <NavLink
        to="/docs"
        onClick={onNavigate}
        className={({ isActive }) =>
          `mt-2.5 flex justify-between border-t border-sidebar-border pt-2.5 text-xs font-medium ${isActive ? 'text-primary' : 'text-foreground hover:text-muted-foreground'}`
        }
      >
        <span>API docs</span>
        <span aria-hidden>→</span>
      </NavLink>
      <Version server={me.server} />
    </div>
  )
}

// index.html paints the landing base colour on /login and /setup before React mounts; whichever
// shell mounts owns the page from then on: the landing and auth shells, or the console when
// SignIn (or any other route) redirects a signed-in visitor there.
export const useUnboot = () => useEffect(() => document.documentElement.classList.remove('landing-boot'), [])

const initials = (name: string) => name.split(/\s+/).filter(Boolean).slice(0, 2).map((w) => w[0]!.toUpperCase()).join('') || '?'

export default function Layout() {
  const { me, can, logout: signOut } = useAuth()
  const [menu, setMenu] = useState(false)
  // One theme instance per page, shared by both Identity copies (drawer +
  // sidebar): per-instance state would let them diverge, and each instance's
  // matchMedia listener could overwrite a pick made in the other.
  const [mode, setMode] = useTheme()
  useUnboot()
  if (!me) return null
  const items = nav.filter((n) => can(n.perm))
  const brand = (
    <Link
      to="/welcome"
      title="Go to the landing page"
      className="flex min-w-0 items-center gap-2 text-lg font-semibold hover:opacity-80 focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50"
    >
      <BrandIcon icon={me.server.server_icon} />
      <span className="truncate">{me.server.server_name}</span>
    </Link>
  )
  return (
    <div className="flex min-h-screen flex-col md:flex-row">
      <aside className="border-b border-sidebar-border bg-sidebar md:sticky md:top-0 md:flex md:h-screen md:w-56 md:shrink-0 md:flex-col md:border-b-0 md:border-r">
        <div className="flex items-center justify-between p-4">
          {brand}
          <div className="relative md:hidden">
            <button
              aria-label="Account menu"
              aria-expanded={menu}
              onClick={() => setMenu(!menu)}
              className="flex h-8 w-8 items-center justify-center rounded-md border border-border text-xs font-medium"
            >
              {initials(me.user.name)}
            </button>
            {menu && (
              <div className="absolute right-0 top-10 z-40 w-64 border border-border bg-background p-4 shadow-dialog">
                <Identity onSignOut={signOut} onNavigate={() => setMenu(false)} mode={mode} setMode={setMode} />
              </div>
            )}
          </div>
        </div>
        <nav className="flex gap-1 overflow-x-auto px-2 pb-2 md:flex-1 md:flex-col md:overflow-y-auto md:pb-4">
          {items.map((n) => (
            <NavLink
              key={n.to}
              to={n.to}
              end={n.to === '/'}
              onClick={() => setMenu(false)}
              className={({ isActive }) =>
                `flex h-8 shrink-0 items-center whitespace-nowrap px-2 text-sm ${
                  isActive
                    ? 'bg-sidebar-accent font-medium shadow-[inset_0_-2px_0_var(--sidebar-primary)] md:shadow-[inset_2px_0_0_var(--sidebar-primary)]'
                    : 'hover:bg-sidebar-accent'
                }`
              }
            >
              {n.label}
            </NavLink>
          ))}
        </nav>
        <div className="hidden border-t border-sidebar-border px-4 py-3 md:block">
          <Identity onSignOut={signOut} mode={mode} setMode={setMode} />
        </div>
      </aside>
      <main className="min-w-0 flex-1 p-4 md:p-8">
        <GateBar />
        <Outlet />
      </main>
    </div>
  )
}
