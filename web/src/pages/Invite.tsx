import { useEffect, useState, type ChangeEvent, type FormEvent } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { api, ApiError } from '../api'
import { useAuth } from '../auth'
import { LandingButton, LandingField, LandingShell, useLanding } from '../components/LandingShell'
import { Badge, ErrorText, type Tone } from '../components/ui'

interface InviteInfo { role: string; group_name: string; email: string; status: 'active' | 'used' | 'revoked' | 'expired' }

const tone: Record<InviteInfo['status'], Tone> = { active: 'success', used: 'inactive', revoked: 'error', expired: 'warning' }

// The invite page reuses the landing backdrop with the invite card (no hero).
export default function Invite() {
  const { token = '' } = useParams()
  const { refresh } = useAuth()
  const navigate = useNavigate()
  const landing = useLanding()
  const [info, setInfo] = useState<InviteInfo | null>(null)
  const [loadError, setLoadError] = useState('')
  const [form, setForm] = useState({ email: '', name: '', password: '', confirm: '' })
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    api<InviteInfo>(`/auth/invite/${encodeURIComponent(token)}`).then(
      (i) => {
        setInfo(i)
        setForm((f) => ({ ...f, email: i.email }))
      },
      (e: ApiError) => setLoadError(e.status === 404 ? 'This invite link is not valid.' : e.message),
    )
  }, [token])

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (form.password.length < 12) return setError('Password must be at least 12 characters.')
    if (form.password !== form.confirm) return setError('Passwords do not match.')
    setBusy(true)
    setError('')
    try {
      await api(`/auth/invite/${encodeURIComponent(token)}`, { method: 'POST', body: { email: form.email, name: form.name, password: form.password } })
      await refresh()
      navigate('/', { replace: true })
    } catch (err) {
      setError((err as Error).message)
      setBusy(false)
    }
  }

  const set = (k: keyof typeof form) => (e: ChangeEvent<HTMLInputElement>) => setForm({ ...form, [k]: e.target.value })
  if (!info && !loadError) return null // no flash of the form while loading
  return (
    <LandingShell info={landing} hero={false}>
      {loadError ? (
        <p className="text-sm">{loadError}</p>
      ) : info && info.status !== 'active' ? (
        <div className="flex flex-col items-start gap-3">
          <Badge tone={tone[info.status]}>{info.status}</Badge>
          <p className="text-sm">This invite has been {info.status}. Ask an admin for a new link.</p>
        </div>
      ) : (
        info && (
          <form onSubmit={submit} className="flex flex-col gap-4">
            <div className="flex flex-col gap-1">
              <h1 className="text-xl font-semibold">Create your account</h1>
              <span className="text-[13px] text-muted-foreground">
                You're joining <b className="text-foreground">{info.group_name}</b> as <b className="text-foreground">{info.role}</b>.
              </span>
            </div>
            <LandingField
              label="Email"
              type="email"
              autoComplete="username"
              required
              readOnly={!!info.email}
              className="read-only:bg-muted"
              value={form.email}
              onChange={set('email')}
            />
            <LandingField label="Name" autoComplete="name" required value={form.name} onChange={set('name')} />
            <LandingField label="Password" type="password" autoComplete="new-password" required value={form.password} onChange={set('password')} hint="At least 12 characters." />
            <LandingField label="Confirm password" type="password" autoComplete="new-password" required value={form.confirm} onChange={set('confirm')} />
            <ErrorText>{error}</ErrorText>
            <LandingButton busy={busy}>{busy ? 'Creating account…' : 'Create account'}</LandingButton>
          </form>
        )
      )}
    </LandingShell>
  )
}
