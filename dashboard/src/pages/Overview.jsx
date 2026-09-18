import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api } from '../api'

function BigNumber({ label, value, status, to }) {
  const card = (
    <div className={`stat-card ${status || ''}`}>
      <div className="stat-value">{value ?? '—'}</div>
      <div className="stat-label">{label}</div>
    </div>
  )
  return to ? <Link to={to}>{card}</Link> : card
}

export default function Overview() {
  const [data, setData] = useState({})
  const [error, setError] = useState('')

  useEffect(() => {
    ;(async () => {
      try {
        const [proposals, agents, quarantine, stats] = await Promise.allSettled([
          api.listProposals('pending'),
          api.listAgents(),
          api.listQuarantine(),
          api.routingStats(),
        ])
        setData({
          pending: proposals.status === 'fulfilled' ? proposals.value.proposals.length : '—',
          agents: agents.status === 'fulfilled' ? agents.value.agents.length : '—',
          quarantined: quarantine.status === 'fulfilled' ? quarantine.value.total : '—',
          throughput: stats.status === 'fulfilled' ? stats.value.throughput.toFixed(2) : '—',
          quarantineRate: stats.status === 'fulfilled' ? `${(stats.value.quarantine_rate * 100).toFixed(0)}%` : '—',
        })
      } catch (e) {
        setError(e.message)
      }
    })()
  }, [])

  return (
    <div>
      <h1>Overview</h1>
      {error && <p className="error">{error}</p>}
      <div className="stat-grid">
        <BigNumber label="Proposals awaiting review" value={data.pending} status={data.pending > 0 ? 'warn' : 'ok'} to="/proposals" />
        <BigNumber label="Registered agents" value={data.agents} to="/agents" />
        <BigNumber label="Events in quarantine" value={data.quarantined} status={data.quarantined > 0 ? 'warn' : 'ok'} />
        <BigNumber label="Throughput (events/s)" value={data.throughput} />
        <BigNumber label="Quarantine rate" value={data.quarantineRate} />
      </div>
      {data.pending > 0 && (
        <div className="callout">
          <strong>{data.pending} schema proposal{data.pending > 1 ? 's' : ''}</strong> waiting for human review.{' '}
          <Link to="/proposals">Review now →</Link>
        </div>
      )}
      <p className="muted">
        Deeper views live in <a href="http://localhost:3000" target="_blank" rel="noreferrer">Grafana</a> (Signal Yard — Exec Rollup dashboard).
      </p>
    </div>
  )
}
