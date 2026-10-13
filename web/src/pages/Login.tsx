import { useState, type FormEvent } from 'react'
import { Navigate, useLocation, useNavigate, useSearchParams } from 'react-router-dom'
import { api, safePath } from '../api'
import { useAuth } from '../auth'
import AuthCard from '../components/AuthCard'
import { Button, ErrorText, Field, inputCls } from '../components/ui'

export default function Login() {
  const { me, refresh } = useAuth()
  const navigate = useNavigate()
  const [params] = useSearchParams()
  // RequireAuth passes router state; api()'s 401 redirect passes ?from=. Both stay on this origin.
  const from = safePath((useLocation().state as { from?: string } | null)?.from ?? params.get('from'))
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  if (me) return <Navigate to={from} replace />

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
    <AuthCard>
      <form onSubmit={submit} className="space-y-4">
        <h1 className="text-xl font-semibold">Sign in</h1>
        <Field label="Email"><input className={inputCls} type="email" autoComplete="username" required value={email} onChange={(e) => setEmail(e.target.value)} /></Field>
        <Field label="Password"><input className={inputCls} type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} /></Field>
        <ErrorText>{error}</ErrorText>
        <Button className="w-full" disabled={busy}>{busy ? 'Signing in…' : 'Sign in'}</Button>
      </form>
    </AuthCard>
  )
}
