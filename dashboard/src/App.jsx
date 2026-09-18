import { Navigate, NavLink, Route, Routes } from 'react-router-dom'
import { useSession } from './store'
import Login from './pages/Login'
import AuthCallback from './pages/AuthCallback'
import Overview from './pages/Overview'
import Proposals from './pages/Proposals'
import ProposalReview from './pages/ProposalReview'
import SchemaRegistry from './pages/SchemaRegistry'
import Agents from './pages/Agents'
import Onboarding from './pages/Onboarding'
import Settings from './pages/Settings'
import AuditLog from './pages/AuditLog'

function Guard({ children }) {
  const token = useSession((s) => s.token)
  if (!token) return <Navigate to="/login" replace />
  return children
}

function Shell({ children }) {
  const { email, logout } = useSession()
  return (
    <div className="shell">
      <aside className="sidebar">
        <div className="brand">
          <span className="brand-mark">⚓</span> Signal Yard
        </div>
        <nav>
          <NavLink to="/" end>Overview</NavLink>
          <NavLink to="/proposals">Approval Queue</NavLink>
          <NavLink to="/schema-registry">Schema Registry</NavLink>
          <NavLink to="/agents">Agents</NavLink>
          <NavLink to="/start-here-agents">Onboarding</NavLink>
          <NavLink to="/audit-log">Audit Log</NavLink>
          <NavLink to="/settings">Settings</NavLink>
        </nav>
        <div className="sidebar-footer">
          <span className="muted">{email || 'operator'}</span>
          <button className="link" onClick={logout}>Sign out</button>
        </div>
      </aside>
      <main className="content">{children}</main>
    </div>
  )
}

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route path="/auth/callback" element={<AuthCallback />} />
      <Route path="/" element={<Guard><Shell><Overview /></Shell></Guard>} />
      <Route path="/proposals" element={<Guard><Shell><Proposals /></Shell></Guard>} />
      <Route path="/proposals/:proposalId" element={<Guard><Shell><ProposalReview /></Shell></Guard>} />
      <Route path="/schema-registry" element={<Guard><Shell><SchemaRegistry /></Shell></Guard>} />
      <Route path="/agents" element={<Guard><Shell><Agents /></Shell></Guard>} />
      <Route path="/start-here-agents" element={<Guard><Shell><Onboarding /></Shell></Guard>} />
      <Route path="/audit-log" element={<Guard><Shell><AuditLog /></Shell></Guard>} />
      <Route path="/settings" element={<Guard><Shell><Settings /></Shell></Guard>} />
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  )
}
