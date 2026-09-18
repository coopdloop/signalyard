import { useEffect, useState } from 'react'
import { api } from '../api'

export default function SchemaRegistry() {
  const [schemas, setSchemas] = useState(null)
  const [error, setError] = useState('')

  useEffect(() => {
    api.listSchemas().then((d) => setSchemas(d.schemas)).catch((e) => setError(e.message))
  }, [])

  return (
    <div>
      <h1>Schema Registry</h1>
      {error && <p className="error">{error}</p>}
      {schemas === null && <p className="muted">Loading…</p>}
      {schemas?.length === 0 && <div className="empty-state">No schemas yet — they appear when proposals are approved or created directly.</div>}
      <table>
        <thead>
          <tr><th>Category</th><th>Version</th><th>Created by</th><th>Git commit</th><th>Created</th></tr>
        </thead>
        <tbody>
          {schemas?.map((s) => (
            <tr key={s.schema_id}>
              <td><strong>{s.category}</strong></td>
              <td>v{s.version}</td>
              <td>{s.created_by || '—'}</td>
              <td className="mono">{s.git_commit_sha ? s.git_commit_sha.slice(0, 8) : '—'}</td>
              <td>{new Date(s.created_at).toLocaleString()}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
