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
import Settings from './pages/Settings'
import Users from './pages/Users'

const pages = [
  { path: 'keys', perm: 'keys:read_own', el: <Keys /> },
  { path: 'models', perm: 'models:read', el: <Models /> },
  { path: 'groups', perm: 'groups:read_own', el: <Groups /> },
  { path: 'users', perm: 'users:read', el: <Users /> },
  { path: 'invites', perm: 'invites:read', el: <Invites /> },
  { path: 'credentials', perm: 'models:write', el: <Credentials /> },
  { path: 'audit', perm: 'audit:read', el: <Audit /> },
  { path: 'settings', perm: 'settings:read', el: <Settings /> },
]

export default function App() {
  return (
    <BrowserRouter>
      <AuthProvider>
        <Routes>
          <Route path="/login" element={<Landing />} />
          <Route path="/setup" element={<Landing setupRoute />} />
          <Route path="/invite/:token" element={<Invite />} />
          <Route element={<RequireAuth><Layout /></RequireAuth>}>
            <Route index element={<Gate perm="spend:read_own"><Dashboard /></Gate>} />
            {pages.map((p) => <Route key={p.path} path={p.path} element={<Gate perm={p.perm}>{p.el}</Gate>} />)}
            <Route path="docs" element={<Docs />} />
          </Route>
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </AuthProvider>
    </BrowserRouter>
  )
}
