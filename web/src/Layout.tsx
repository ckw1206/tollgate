import { useState } from 'react'
import { NavLink, Outlet, useNavigate } from 'react-router-dom'
import { useAuth } from './auth'
import { Segmented } from './components/ui'
import type { ServerInfo } from './types'

// One layout for every role; nav items are filtered by permission. Order is fixed.
const nav = [
  { to: '/', label: 'Dashboard', perm: 'spend:read_own' },
  { to: '/keys', label: 'API keys', perm: 'keys:read_own' },
  { to: '/models', label: 'Models', perm: 'models:read' },
  { to: '/groups', label: 'Groups', perm: 'groups:read_own' },
  { to: '/users', label: 'Users', perm: 'users:read' },
  { to: '/invites', label: 'Invites', perm: 'invites:read' },
  { to: '/credentials', label: 'Credentials', perm: 'models:write' },
  { to: '/audit', label: 'Audit log', perm: 'audit:read' },
  { to: '/settings', label: 'Settings', perm: 'settings:read' },
]

const REPO = 'https://github.com/ckw1206/tollgate'

// server_icon is "" (default) or an uploaded image stored as a data URL.
export function BrandIcon({ icon, size = 28, alt = '' }: { icon: string; size?: number; alt?: string }) {
  const src = icon.startsWith('data:image/') ? icon : '/tollgate.png'
  return <img src={src} alt={alt} width={size} height={size} className="shrink-0 object-contain" style={{ width: size, height: size }} />
}

type Mode = 'dark' | 'light'

function useTheme(): [Mode, (m: Mode) => void] {
  const [mode, setMode] = useState<Mode>(document.documentElement.dataset.mode === 'light' ? 'light' : 'dark')
  const set = (m: Mode) => {
    document.documentElement.dataset.mode = m
    try {
      localStorage.setItem('tollgate.theme', m)
    } catch {
      // storage blocked: the toggle still works for this page view
    }
    setMode(m)
  }
  return [mode, set]
}

export function Version({ server }: { server: ServerInfo }) {
  const sha = server.commit ? (
    <a className="font-mono hover:underline" href={`${REPO}/commit/${server.commit}`} target="_blank" rel="noreferrer">{server.commit.slice(0, 7)}</a>
  ) : null
  const tagged = server.version && server.version !== 'dev'
  return (
    <div className="text-[11px] text-muted-foreground">
      Version:{' '}
      {tagged ? <a className="hover:underline" href={`${REPO}/releases/tag/${server.version}`} target="_blank" rel="noreferrer">{server.version}</a> : 'dev'}
      {sha && <> ({sha})</>}
    </div>
  )
}

function Identity({ onSignOut, onNavigate }: { onSignOut: () => void; onNavigate?: () => void }) {
  const { me } = useAuth()
  const [mode, setMode] = useTheme()
  if (!me) return null
  return (
    <div className="space-y-2 text-xs text-muted-foreground">
      <div>
        <div className="font-medium text-foreground">{me.user.name}</div>
        <div>{me.user.email} · {me.user.role}</div>
      </div>
      <button className="underline hover:text-foreground" onClick={onSignOut}>Sign out</button>
      <div className="flex items-center gap-2">
        <span>Theme</span>
        <Segmented small label="Theme" value={mode} onChange={setMode} options={[{ value: 'dark', label: 'Dark' }, { value: 'light', label: 'Light' }]} />
      </div>
      <NavLink
        to="/docs"
        onClick={onNavigate}
        className={({ isActive }) =>
          `mt-0.5 flex justify-between border-t border-sidebar-border pt-2.5 text-xs font-medium ${isActive ? 'text-primary' : 'text-foreground hover:text-muted-foreground'}`
        }
      >
        <span>API docs</span>
        <span aria-hidden>→</span>
      </NavLink>
      <Version server={me.server} />
    </div>
  )
}

const initials = (name: string) => name.split(/\s+/).filter(Boolean).slice(0, 2).map((w) => w[0]!.toUpperCase()).join('') || '?'

export default function Layout() {
  const { me, can, logout } = useAuth()
  const navigate = useNavigate()
  const [menu, setMenu] = useState(false)
  if (!me) return null
  const signOut = () => logout().then(() => navigate('/login'))
  const items = nav.filter((n) => can(n.perm))
  const brand = (
    <div className="flex items-center gap-2 text-lg font-semibold">
      <BrandIcon icon={me.server.server_icon} />
      <span className="truncate">{me.server.server_name}</span>
    </div>
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
                <Identity onSignOut={signOut} onNavigate={() => setMenu(false)} />
              </div>
            )}
          </div>
        </div>
        <nav className="flex gap-1 overflow-x-auto px-2 pb-2 md:flex-1 md:flex-col md:overflow-y-auto">
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
          <Identity onSignOut={signOut} />
        </div>
      </aside>
      <main className="min-w-0 flex-1 p-4 md:p-8">
        <Outlet />
      </main>
    </div>
  )
}
