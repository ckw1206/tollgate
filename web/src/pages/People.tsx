import { Navigate } from 'react-router-dom'
import { Gate, useAuth } from '../auth'
import { PEOPLE_TABS } from '../Layout'
import Users from './Users'

// /people is the Users tab. Roles without users:read (members, viewers) land on their first
// visible tab instead of the forbidden text, since "Users & groups" is in their nav.
export default function People() {
  const { can } = useAuth()
  const first = PEOPLE_TABS.find((t) => can(t.perm))
  if (!can('users:read') && first) return <Navigate to={first.to} replace />
  return <Gate perm="users:read"><Users /></Gate>
}
