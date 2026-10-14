import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'
import { AuthProvider, Gate, RequireAuth } from './auth'
import Layout from './Layout'
import Audit from './pages/Audit'
import Credentials from './pages/Credentials'
import Dashboard from './pages/Dashboard'
import Docs from './pages/Docs'
import Groups from './pages/Groups'
import Invite from './pages/Invite'
import Invites from './pages/Invites'
import Keys from './pages/Keys'
import Landing from './pages/Landing'
import Models from './pages/Models'
import People from './pages/People'
import Settings from './pages/Settings'
import SignIn from './pages/SignIn'

// Tabs are routes too (see MODEL_TABS / PEOPLE_TABS in Layout).
const pages = [
  { path: 'keys', perm: 'keys:read_own', el: <Keys /> },
  { path: 'models', perm: 'models:read', el: <Models /> },
  { path: 'models/credentials', perm: 'models:write', el: <Credentials /> },
  { path: 'people/groups', perm: 'groups:read_own', el: <Groups /> },
  { path: 'people/invites', perm: 'invites:read', el: <Invites /> },
  { path: 'audit', perm: 'audit:read', el: <Audit /> },
  { path: 'settings', perm: 'settings:read', el: <Settings /> },
]

// Pre-tabs routes.
const moved = { '/users': '/people', '/groups': '/people/groups', '/invites': '/people/invites', '/credentials': '/models/credentials' }

export default function App() {
  return (
    <BrowserRouter>
      <AuthProvider>
        <Routes>
          <Route path="/welcome" element={<Landing />} />
          <Route path="/login" element={<SignIn />} />
          <Route path="/setup" element={<SignIn setupRoute />} />
          <Route path="/invite/:token" element={<Invite />} />
          <Route element={<RequireAuth><Layout /></RequireAuth>}>
            <Route index element={<Gate perm="spend:read_own"><Dashboard /></Gate>} />
            {pages.map((p) => <Route key={p.path} path={p.path} element={<Gate perm={p.perm}>{p.el}</Gate>} />)}
            <Route path="people" element={<People />} />
            <Route path="docs" element={<Docs />} />
          </Route>
          {Object.entries(moved).map(([from, to]) => <Route key={from} path={from} element={<Navigate to={to} replace />} />)}
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </AuthProvider>
    </BrowserRouter>
  )
}
