import { useEffect, useState } from 'react'
import { GATEWAY_URL } from '../config'

export default function Onboarding() {
  const [manifest, setManifest] = useState(null)
  const [error, setError] = useState('')

  useEffect(() => {
    fetch(`${GATEWAY_URL}/start-here-agents`)
      .then((r) => r.json())
      .then(setManifest)
      .catch((e) => setError(e.message))
  }, [])

  if (error) return <p className="error">{error}</p>
  if (!manifest) return <p className="muted">Loading…</p>

  return (
    <div>
      <h1>Agent Onboarding</h1>
      <p className="muted">
        This is what an agent sees at <code>GET /start-here-agents</code> — no hand-written pipelines required.
      </p>

      <h2>How agents get a key</h2>
      <pre className="code">{manifest.api_key_instructions}</pre>

      <h2>Ingestion</h2>
      <pre className="code">{JSON.stringify(manifest.capability_manifest?.ingestion, null, 2)}</pre>

      <h2>MCP tools</h2>
      <table>
        <thead><tr><th>Tool</th><th>Description</th></tr></thead>
        <tbody>
          {manifest.mcp_manifest?.tools?.map((t) => (
            <tr key={t.name}><td className="mono">{t.name}</td><td>{t.description}</td></tr>
          ))}
        </tbody>
      </table>

      <h2>Example payloads</h2>
      <pre className="code">{JSON.stringify(manifest.example_payloads, null, 2)}</pre>
    </div>
  )
}
