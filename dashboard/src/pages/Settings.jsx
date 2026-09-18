import { GATEWAY_URL, REGISTRY_URL, NORMALIZER_URL, OIDC_ISSUER } from '../config'
import { useSession } from '../store'

export default function Settings() {
  const { email, role } = useSession()
  return (
    <div>
      <h1>Settings</h1>
      <h2>Session</h2>
      <table>
        <tbody>
          <tr><td>Signed in as</td><td>{email || '—'}</td></tr>
          <tr><td>Role</td><td>{role || '—'}</td></tr>
        </tbody>
      </table>
      <h2>Backends</h2>
      <table>
        <tbody>
          <tr><td>Gateway</td><td className="mono">{GATEWAY_URL}</td></tr>
          <tr><td>Schema registry</td><td className="mono">{REGISTRY_URL}</td></tr>
          <tr><td>Normalizer</td><td className="mono">{NORMALIZER_URL}</td></tr>
          <tr><td>OIDC issuer</td><td className="mono">{OIDC_ISSUER}</td></tr>
        </tbody>
      </table>
      <p className="muted">
        Approval and key actions are recorded in the approval_audit_log table for compliance review.
      </p>
    </div>
  )
}
