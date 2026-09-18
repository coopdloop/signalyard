import { useEffect, useState } from 'react'
import { api } from '../api'

export default function AuditLog() {
  const [entries, setEntries] = useState(null)
  const [error, setError] = useState('')

  useEffect(() => {
    api.listAuditLog().then((d) => setEntries(d.entries)).catch((e) => setError(e.message))
  }, [])

  return (
    <div>
      <h1>Audit Log</h1>
      <p className="muted">Every approval and rejection, recorded for compliance review.</p>
      {error && <p className="error">{error}</p>}
      {entries === null && <p className="muted">Loading…</p>}
      {entries?.length === 0 && <div className="empty-state">No approvals or rejections recorded yet.</div>}
      <table>
        <thead>
          <tr><th>Time</th><th>Category</th><th>Action</th><th>Notes</th></tr>
        </thead>
        <tbody>
          {entries?.map((e) => (
            <tr key={e.id}>
              <td>{new Date(e.created_at).toLocaleString()}</td>
              <td><strong>{e.category}</strong></td>
              <td><span className={`badge ${e.action}d`}>{e.action}</span></td>
              <td>{e.notes || '—'}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
