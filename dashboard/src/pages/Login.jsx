import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { OIDC_ISSUER, OIDC_CLIENT_ID } from '../config'
import { useSession } from '../store'

function base64url(bytes) {
  return btoa(String.fromCharCode(...bytes)).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

async function startPKCE() {
  const verifier = base64url(crypto.getRandomValues(new Uint8Array(32)))
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(verifier))
  const challenge = base64url(new Uint8Array(digest))
  sessionStorage.setItem('pkce_verifier', verifier)
  const params = new URLSearchParams({
    client_id: OIDC_CLIENT_ID,
    redirect_uri: `${window.location.origin}/auth/callback`,
    response_type: 'code',
    scope: 'openid email profile',
    code_challenge: challenge,
    code_challenge_method: 'S256',
  })
  window.location.href = `${OIDC_ISSUER}/protocol/openid-connect/auth?${params}`
}

export default function Login() {
  const [token, setToken] = useState('')
  const [error, setError] = useState('')
  const login = useSession((s) => s.login)
  const navigate = useNavigate()

  const devLogin = (e) => {
    e.preventDefault()
    if (!token.trim()) return setError('Paste a session or admin token')
    login({ token: token.trim(), email: 'dev-operator@local', role: 'admin' })
    navigate('/')
  }

  return (
    <div className="auth-page">
      <div className="auth-card">
        <h1><span className="brand-mark">⚓</span> Signal Yard</h1>
        <p className="muted">One dock. Every agent. Every signal finds its place.</p>
        <button className="primary block" onClick={startPKCE}>Sign in with SSO</button>
        <div className="divider"><span>or dev token</span></div>
        <form onSubmit={devLogin}>
          <input
            type="password"
            placeholder="Session / admin token"
            value={token}
            onChange={(e) => setToken(e.target.value)}
          />
          {error && <p className="error">{error}</p>}
          <button className="block" type="submit">Continue with token</button>
        </form>
      </div>
    </div>
  )
}
