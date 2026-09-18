import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api } from '../api'

function age(iso) {
  const mins = Math.max(0, Math.round((Date.now() - new Date(iso)) / 60000))
  if (mins < 60) return `${mins}m`
  const hours = Math.round(mins / 60)
  if (hours < 24) return `${hours}h`
  return `${Math.round(hours / 24)}d`
}

export default function Proposals() {
  const [proposals, setProposals] = useState(null)
  const [filter, setFilter] = useState('pending')
  const [error, setError] = useState('')
  const navigate = useNavigate()

  useEffect(() => {
    api.listProposals(filter)
      .then((d) => setProposals(d.proposals))
      .catch((e) => setError(e.message))
  }, [filter])

  return (
    <div>
      <h1>Approval Queue</h1>
      <div className="filter-bar">
        {['pending', 'approved', 'rejected', ''].map((s) => (
          <button key={s || 'all'} className={filter === s ? 'chip active' : 'chip'} onClick={() => setFilter(s)}>
            {s || 'all'}
          </button>
        ))}
      </div>
      {error && <p className="error">{error}</p>}
      {proposals === null && <p className="muted">Loading…</p>}
      {proposals?.length === 0 && (
        <div className="empty-state">🎉 Nothing to review — every signal has found its place.</div>
      )}
      <div className="card-list">
        {proposals?.map((p) => (
          <div key={p.proposal_id} className="pr-card" onClick={() => navigate(`/proposals/${p.proposal_id}`)}>
            <div className="pr-card-head">
              <strong>{p.category}</strong>
              <span className={`badge ${p.status}`}>{p.status}</span>
            </div>
            <div className="pr-card-meta">
              <span>by {p.generated_by || 'unknown'}</span>
              {p.confidence != null && <span>confidence {(p.confidence * 100).toFixed(0)}%</span>}
              <span>{age(p.created_at)} old</span>
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
