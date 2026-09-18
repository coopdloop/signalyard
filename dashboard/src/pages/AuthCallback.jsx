import { useEffect, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { OIDC_ISSUER, OIDC_CLIENT_ID } from '../config'
import { api } from '../api'
import { useSession } from '../store'

export default function AuthCallback() {
  const [params] = useSearchParams()
  const [error, setError] = useState('')
  const login = useSession((s) => s.login)
  const navigate = useNavigate()

  useEffect(() => {
    const code = params.get('code')
    const verifier = sessionStorage.getItem('pkce_verifier')
    if (!code || !verifier) return setError('Missing code or PKCE verifier')
    ;(async () => {
      try {
        const res = await fetch(`${OIDC_ISSUER}/protocol/openid-connect/token`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
          body: new URLSearchParams({
            grant_type: 'authorization_code',
            client_id: OIDC_CLIENT_ID,
            code,
            code_verifier: verifier,
            redirect_uri: `${window.location.origin}/auth/callback`,
          }),
        })
        const tokens = await res.json()
        if (!res.ok) throw new Error(tokens.error_description || 'token exchange failed')
        const session = await api.loginOIDC(tokens.id_token)
        const claims = JSON.parse(atob(tokens.id_token.split('.')[1]))
        login({ token: session.session_token, email: claims.email, role: claims.role || 'viewer' })
        navigate('/')
      } catch (e) {
        setError(e.message)
      }
    })()
  }, [])

  return (
    <div className="auth-page">
      <div className="auth-card">
        {error ? <p className="error">{error}</p> : <p>Signing you in…</p>}
      </div>
    </div>
  )
}
