import { useEffect, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { api } from '../api'
import { useAuth } from '../auth'
import { LandingShell, useLanding } from '../components/LandingShell'

// Landing serves /welcome: header + hero, no form. The CTA follows the session: "Open console"
// when signed in, "Set up" on first run, otherwise "Sign in". `?preview` (Settings → Preview)
// shows what signed-out visitors see and loads nothing but /auth/landing.
export default function Landing() {
  const { me } = useAuth()
  const [params] = useSearchParams()
  const preview = params.has('preview')
  const info = useLanding(preview)
  const [needed, setNeeded] = useState<boolean | null>(null)
  useEffect(() => {
    if (!preview && !me) api<{ needed: boolean }>('/auth/setup').then((r) => setNeeded(r.needed), () => setNeeded(false))
  }, [preview, me])

  // No CTA until it's known, so "Sign in" never flashes before "Set up".
  const signIn = { label: 'Sign in', to: '/login' }
  const cta = preview ? signIn : me ? { label: 'Open console', to: '/' } : needed === null ? undefined : needed ? { label: 'Set up', to: '/setup' } : signIn
  return <LandingShell info={info} cta={cta} preview={preview} />
}
