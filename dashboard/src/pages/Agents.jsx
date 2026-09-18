import { useEffect, useState } from 'react'
import { api } from '../api'

export default function Agents() {
  const [agents, setAgents] = useState(null)
  const [detail, setDetail] = useState(null)
  const [error, setError] = useState('')

  useEffect(() => {
    api.listAgents().then((d) => setAgents(d.agents)).catch((e) => setError(e.message))
  }, [])

  const open = (id) => api.getAgent(id).then(setDetail).catch((e) => setError(e.message))

  return (
    <div>
      <h1>Agent Registry</h1>
      {error && <p className="error">{error}</p>}
      {agents === null && <p className="muted">Loading…</p>}
      {agents?.length === 0 && <div className="empty-state">No agents registered yet.</div>}
      <div className="card-list">
        {agents?.map((a) => (
          <div key={a.agent_id} className="pr-card" onClick={() =>open(a.agent_id)}>
            <div className="pr-card-head">
              <strong>{a.agent_name}</strong>
              <span className={`badge ${a.status}`}>{a.status}</span>
            </div>
            <div className="pr-card-meta">
              <span>{a.agent_type}</span>
              <span className="mono">{a.slug}</span>
            </div>
          </div>
        ))}
      </div>

      {detail && (
        <div className="modal-backdrop" onClick={() => setDetail(null)}>
          <div className="modal" onClick={(e) => e.stopPropagation()}>
            <h2>{detail.agent_name}</h2>
            <p className="muted">status {detail.status}</p>
            <h3>API keys</h3>
            {detail.api_key_metadata?.length === 0 && <p className="muted">No keys issued.</p>}
            <table>
              <thead><tr><th>Prefix</th><th>Scopes</th><th>Revoked</th><th>Created</th></tr></thead>
              <tbody>
                {detail.api_key_metadata?.map((k) => (
                  <tr key={k.key_id}>
                    <td className="mono">{k.key_prefix}</td>
                    <td>{k.scopes?.join(', ') || '—'}</td>
                    <td>{k.revoked ? '❌' : '✅'}</td>
                    <td>{new Date(k.created_at).toLocaleDateString()}</td>
                  </tr>
                ))}
              </tbody>
            </table>
            <button className="link" onClick={() => setDetail(null)}>Close</button>
          </div>
        </div>
      )}
    </div>
  )
}
