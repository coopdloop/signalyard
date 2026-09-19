"""Token auth for the classifier HTTP API, matching the Go services' model:
HEC-style machine API keys (HMAC-SHA256 with HEC_TOKEN_SALT against the shared
api_keys table) plus an optional static dev admin token.
"""

from __future__ import annotations

import hashlib
import hmac

from fastapi import Request
from fastapi.responses import JSONResponse


def hash_token(salt: str, token: str) -> str:
    return hmac.new(salt.encode(), token.encode(), hashlib.sha256).hexdigest()


def extract_token(request: Request) -> str:
    header = request.headers.get("authorization", "")
    for scheme in ("SignalYard ", "Splunk ", "Bearer "):
        if header.startswith(scheme):
            return header[len(scheme) :].strip()
    return request.headers.get("x-signalyard-token", "")


class TokenAuth:
    def __init__(self, hec_salt: str, dev_admin_token: str, runs_store=None):
        self.hec_salt = hec_salt
        self.dev_admin_token = dev_admin_token
        self.runs_store = runs_store  # reused for api_keys lookup; None => dev token only

    async def resolve(self, token: str) -> bool:
        if not token:
            return False
        if self.dev_admin_token and token == self.dev_admin_token:
            return True
        if self.runs_store is None or self.runs_store.pool is None:
            return False
        async with self.runs_store.pool.acquire() as conn:
            row = await conn.fetchrow(
                """
                SELECT 1 FROM api_keys
                WHERE key_hash = $1 AND revoked_at IS NULL
                  AND (expires_at IS NULL OR expires_at > NOW())
                """,
                hash_token(self.hec_salt, token),
            )
        return row is not None

    async def middleware(self, request: Request, call_next):
        if request.url.path in ("/health", "/metrics"):
            return await call_next(request)
        token = extract_token(request)
        if not await self.resolve(token):
            return JSONResponse({"error": "invalid or missing token"}, status_code=401)
        return await call_next(request)
