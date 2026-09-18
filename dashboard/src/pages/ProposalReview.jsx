import { useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { api } from '../api'

export default function ProposalReview() {
  const { proposalId } = useParams()
  const [proposal, setProposal] = useState(null)
  const [notes, setNotes] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const navigate = useNavigate()

  useEffect(() => {
    api.getProposal(proposalId).then(setProposal).catch((e) => setError(e.message))
  }, [proposalId])

  const review = async (action) => {
    if (!notes.trim()) return setError('Reviewer notes are required')
    setBusy(true)
    setError('')
    try {
      if (action === 'approve') await api.approveProposal(proposalId, notes)
      else await api.rejectProposal(proposalId, notes)
      navigate('/proposals')
    } catch (e) {
      setError(e.message)
      setBusy(false)
    }
  }

  if (error && !proposal) return <p className="error">{error}</p>
  if (!proposal) return <p className="muted">Loading…</p>

  return (
    <div>
      <h1>Review: {proposal.category}</h1>
      <p className="muted">
        Proposed by {proposal.generated_by} · status <span className={`badge ${proposal.status}`}>{proposal.status}</span>
      </p>

      <h2>Proposed JSON Schema</h2>
      <pre className="code">{JSON.stringify(proposal.json_schema_patch, null, 2)}</pre>

      <h2>Routing YAML</h2>
      <pre className="code">{proposal.routing_yaml_diff || '(none)'}</pre>

      {proposal.status === 'pending' && (
        <div className="review-box">
          <textarea
            placeholder="Reviewer notes (required) — why is this shape acceptable?"
            value={notes}
            onChange={(e) => setNotes(e.target.value)}
            rows={3}
          />
          {error && <p className="error">{error}</p>}
          <div className="review-actions">
            <button className="primary" disabled={busy} onClick={() => review('approve')}>
              Approve & activate schema
            </button>
            <button className="danger" disabled={busy} onClick={() => review('reject')}>
              Reject
            </button>
          </div>
          <p className="muted">
            Approving commits the schema to git, activates it, and replays matching quarantined events.
          </p>
        </div>
      )}
    </div>
  )
}
