// Backend endpoints. Browsers hit the host-published compose ports.
// Override at build time with VITE_* env vars.
export const GATEWAY_URL = import.meta.env.VITE_GATEWAY_URL || 'http://localhost:8080'
export const REGISTRY_URL = import.meta.env.VITE_REGISTRY_URL || 'http://localhost:8082'
export const NORMALIZER_URL = import.meta.env.VITE_NORMALIZER_URL || 'http://localhost:8081'

export const OIDC_ISSUER = import.meta.env.VITE_OIDC_ISSUER || 'http://localhost:8180/realms/signalyard'
export const OIDC_CLIENT_ID = import.meta.env.VITE_OIDC_CLIENT_ID || 'signalyard-dashboard'
