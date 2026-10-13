import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from 'react'
import { Navigate, useLocation } from 'react-router-dom'
import { api } from './api'
import type { Me } from './types'

interface AuthCtx {
  me: Me | null
  can: (perm: string) => boolean
  refresh: () => Promise<void>
  logout: () => Promise<void>
}

const Ctx = createContext<AuthCtx | null>(null)

// isLandingPreview: /login?preview=1 (Settings → Preview) shows what signed-out visitors
// see and loads only the public landing info, so the session isn't fetched there.
export const isLandingPreview = () => window.location.pathname === '/login' && new URLSearchParams(window.location.search).has('preview')

// Permissions come only from GET /auth/me; never branch on user.role.
export function AuthProvider({ children }: { children: ReactNode }) {
  const [me, setMe] = useState<Me | null | undefined>(() => (isLandingPreview() ? null : undefined))
  const refresh = useCallback(() => api<Me>('/auth/me').then(setMe, () => setMe(null)), [])
  useEffect(() => {
    if (!isLandingPreview()) refresh()
  }, [refresh])
  const logout = async () => {
    await api('/auth/logout', { method: 'POST' }).catch(() => undefined)
    setMe(null)
  }
  if (me === undefined) return null
  const can = (perm: string) => !!me?.permissions.includes(perm)
  return <Ctx.Provider value={{ me, can, refresh, logout }}>{children}</Ctx.Provider>
}

export function useAuth() {
  const c = useContext(Ctx)
  if (!c) throw new Error('useAuth outside AuthProvider')
  return c
}

export function RequireAuth({ children }: { children: ReactNode }) {
  const { me } = useAuth()
  const loc = useLocation()
  if (!me) return <Navigate to="/login" replace state={{ from: loc.pathname + loc.search }} />
  return <>{children}</>
}

// Gate hides a page the user lacks permission for. The server enforces the same rule.
export function Gate({ perm, children }: { perm: string; children: ReactNode }) {
  const { can } = useAuth()
  if (!can(perm)) return <p className="text-sm text-muted-foreground">You don't have access to this page.</p>
  return <>{children}</>
}
