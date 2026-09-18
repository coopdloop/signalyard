import { GATEWAY_URL, REGISTRY_URL, NORMALIZER_URL } from './config'
import { useSession } from './store'

async function call(base, path, { method = 'GET', body } = {}) {
  const token = useSession.getState().token
  const res = await fetch(base + path, {
    method,
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
    body: body ? JSON.stringify(body) : undefined,
  })
  if (res.status === 401) {
    useSession.getState().logout()
    throw new Error('unauthorized')
  }
  const data = await res.json().catch(() => ({}))
  if (!res.ok) throw new Error(data.error || `request failed: ${res.status}`)
  return data
}

export const api = {
  // gateway
  startHere: () => call(GATEWAY_URL, '/start-here-agents'),
  loginOIDC: (oidcToken) => call(GATEWAY_URL, '/login', { method: 'POST', body: { oidc_token: oidcToken } }),
  queryEvents: (q) => call(GATEWAY_URL, '/mcp/tools/query_events', { method: 'POST', body: q }),

  // schema registry
  listSchemas: () => call(REGISTRY_URL, '/v1/schemas'),
  listCategories: () => call(REGISTRY_URL, '/v1/categories'),
  listProposals: (status) => call(REGISTRY_URL, `/v1/proposals${status ? `?status=${status}` : ''}`),
  getProposal: (id) => call(REGISTRY_URL, `/v1/proposals/${id}`),
  approveProposal: (id, notes) => call(REGISTRY_URL, `/v1/proposals/${id}/approve`, { method: 'POST', body: { reviewer_notes: notes } }),
  rejectProposal: (id, notes) => call(REGISTRY_URL, `/v1/proposals/${id}/reject`, { method: 'POST', body: { reviewer_notes: notes } }),
  listAgents: () => call(REGISTRY_URL, '/v1/agents'),
  getAgent: (id) => call(REGISTRY_URL, `/v1/agents/${id}`),

  listAuditLog: (proposalId) => call(REGISTRY_URL, `/v1/audit-log${proposalId ? `?proposal_id=${proposalId}` : ''}`),

  // normalizer
  routingStats: () => call(NORMALIZER_URL, '/v1/routing-stats'),
  listQuarantine: () => call(NORMALIZER_URL, '/v1/quarantine'),
}
