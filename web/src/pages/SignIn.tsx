import { useEffect, useState, type ChangeEvent, type FormEvent } from 'react'
import { Navigate, useLocation, useNavigate, useSearchParams } from 'react-router-dom'
import { api, ApiError, safePath } from '../api'
import { useAuth } from '../auth'
import { AuthShell, LandingButton, LandingField, useLanding } from '../components/LandingShell'
import { ErrorText } from '../components/ui'
import type { LandingInfo } from '../types'

// SignIn serves /login and /setup. While GET /auth/setup says {needed:true}, both show the
// setup card; otherwise /login shows sign-in and /setup redirects to /login.
export default function SignIn({ setupRoute = false }: { setupRoute?: boolean }) {
  const { me, refresh } = useAuth()
  const navigate = useNavigate()
  const loc = useLocation()
  const [params] = useSearchParams()
  const from = safePath((loc.state as { from?: string } | null)?.from ?? params.get('from'))
  const info = useLanding()
  const [needed, setNeeded] = useState<boolean | null>(null)
  const [notice, setNotice] = useState('')
  const [done, setDone] = useState(false)
  // Read ?token= once (the setup card shows on /login too), then strip it from the address bar.
  const [token] = useState(() => params.get('token') ?? '')

  useEffect(() => {
    if (params.has('token')) navigate(loc.pathname, { replace: true, state: loc.state }) // history.replaceState under the hood
  }, [params, navigate, loc.pathname, loc.state])
  useEffect(() => {
    api<{ needed: boolean }>('/auth/setup').then((r) => setNeeded(r.needed), () => setNeeded(false))
  }, [])
  useEffect(() => {
    if (setupRoute && needed === false) navigate('/login', { replace: true })
  }, [setupRoute, needed, navigate])

  if (me) return <Navigate to={done ? '/?welcome=1' : from} replace />

  // No card until /auth/setup answers, so sign-in never flashes before the setup card.
  return (
    <AuthShell info={info}>
      {needed === true ? (
        <SetupCard
          info={info}
          initialToken={token}
          onDone={async () => {
            setDone(true)
            await refresh()
          }}
          onAlreadySetUp={(msg) => {
            setNotice(msg)
            setNeeded(false)
          }}
        />
      ) : needed === false ? (
        <SignInCard info={info} notice={notice || (from.startsWith('/docs') ? 'Sign in to read the API docs.' : '')} from={from} />
      ) : null}
    </AuthShell>
  )
}

function SignInCard({ info, notice, from }: { info: LandingInfo | null; notice: string; from: string }) {
  const { refresh } = useAuth()
  const navigate = useNavigate()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState(notice)
  const [busy, setBusy] = useState(false)

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      await api('/auth/login', { method: 'POST', body: { email, password } })
      await refresh()
      navigate(from, { replace: true })
    } catch (err) {
      setError((err as Error).message) // server message verbatim, incl. the 429 lockout
    } finally {
      setBusy(false)
    }
  }
  return (
    <form onSubmit={submit} className="flex flex-col gap-4">
      <div className="flex flex-col gap-1">
        <h1 className="text-xl font-semibold">Sign in</h1>
        <span className="text-[13px] text-muted-foreground">to {info?.server_name || 'Tollgate'}</span>
      </div>
      <LandingField label="Email" type="email" autoComplete="username" required value={email} onChange={(e) => setEmail(e.target.value)} />
      <LandingField label="Password" type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
      <ErrorText>{error}</ErrorText>
      <LandingButton busy={busy}>{busy ? 'Signing in…' : 'Sign in'}</LandingButton>
      <span className="text-[13px] text-muted-foreground">New here? Ask an admin for an invite link.</span>
    </form>
  )
}

function SetupCard({ info, initialToken, onDone, onAlreadySetUp }: {
  info: LandingInfo | null
  initialToken: string
  onDone: () => Promise<void>
  onAlreadySetUp: (msg: string) => void
}) {
  const [f, setF] = useState({ token: initialToken, name: '', email: '', password: '', confirm: '' })
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [done, setDone] = useState(false)
  const set = (k: keyof typeof f) => (e: ChangeEvent<HTMLInputElement>) => {
    setF({ ...f, [k]: e.target.value })
    setError('')
  }

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (!f.token.trim()) return setError('Enter the setup token from the server log.')
    if (f.password.length < 12) return setError('Password must be at least 12 characters.')
    if (f.password !== f.confirm) return setError('Passwords do not match.')
    setBusy(true)
    setError('')
    try {
      await api('/auth/setup', { method: 'POST', body: { token: f.token.trim(), name: f.name, email: f.email, password: f.password } })
      setDone(true)
      await onDone()
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) return onAlreadySetUp(err.message)
      setError((err as Error).message)
      setBusy(false)
    }
  }
  const name = info?.server_name || 'Tollgate'
  return (
    <form onSubmit={submit} className="flex flex-col gap-3.5">
      <div className="flex flex-col gap-1.5">
        <span className="self-start bg-warning px-2 py-0.5 text-xs font-medium text-warning-foreground">First run</span>
        <h1 className="text-xl font-semibold">Set up {name}</h1>
        <span className="text-[13px] text-muted-foreground">No accounts exist yet. Create the first admin — you'll invite everyone else from the console.</span>
      </div>
      <LandingField
        label="Setup token"
        required
        autoComplete="off"
        spellCheck={false}
        placeholder="from the server log"
        className="font-mono text-[13px]"
        value={f.token}
        onChange={set('token')}
        hint={<>Printed once at startup: <code className="font-mono">First-run setup: {'{BASE_URL}'}/setup?token=…</code>. Opening that link fills this in.</>}
      />
      <div className="grid grid-cols-2 gap-3">
        <LandingField label="Name" required autoComplete="name" value={f.name} onChange={set('name')} />
        <LandingField label="Email" type="email" required autoComplete="username" value={f.email} onChange={set('email')} />
      </div>
      <LandingField label="Password" type="password" autoComplete="new-password" required value={f.password} onChange={set('password')} hint="At least 12 characters." />
      <LandingField label="Confirm password" type="password" autoComplete="new-password" required value={f.confirm} onChange={set('confirm')} />
      <ErrorText>{error}</ErrorText>
      {done && <p role="status" className="bg-success px-3 py-2 text-sm text-success-foreground">Admin created. Signing you in…</p>}
      <LandingButton busy={busy}>{busy ? 'Creating…' : 'Create admin account'}</LandingButton>
      <span className="text-xs text-muted-foreground">
        Headless? <code className="font-mono">tollgate admin create --email … --name …</code> does the same.
      </span>
    </form>
  )
}
