---
title: Authenticated Server With Private SQLite Rate Limiting
hashtags:
  - "#authentication"
  - "#sqlite"
  - "#security"
  - "#rate-limiting"
---

# Authenticated Server With Private SQLite Rate Limiting

## Intent

Protect a deployment web UI with simple admin credentials, signed short-lived sessions, security headers, and bounded login failure tracking in a private SQLite database.

## When To Use

Use this pattern for a small self-hosted admin surface where a full external identity provider is unnecessary, but credential checks, session integrity, and brute-force resistance still matter.

## Implementation

Server mode reads `ADMIN_USERNAME`, `ADMIN_PASSWORD`, `SESSION_SECRET`, and `ADDR` from `config/.env`. `loadServerConfig` rejects empty admin credentials or session secret. `ensureAuthDBFile` creates `data/main.sqlite`, initializes a `login_failures` table and index, and sets file mode `0600`.

Login uses HMAC constant-time comparisons for submitted username and password. On success, `createSession` stores an in-memory session ID with a 12-hour TTL and returns an `HttpOnly`, `SameSite=Lax` signed cookie. Cookie signatures are HMAC-SHA256 using `SESSION_SECRET`.

Failed logins are tracked by client IP in SQLite. `isBlocked` and `recordFailure` purge rows older than 24 hours, then block requests after five failures in the active window. `securityHeaders` sets `X-Content-Type-Options: nosniff` and `Referrer-Policy: same-origin`.

## Project Evidence

- `auth.go`: `loadServerConfig`, `initAuthDB`, `ensureAuthDBFile`, `handleLogin`, session helpers, failure tracking, and security headers.
- `web.go`: `serveServer` opens the SQLite database and enables `authRequired`.
- `README.md`: documents required admin settings and bounded login failure tracking.

## Reuse Notes

Keep the session store and login-failure database scoped to the control plane. This pattern is intentionally simple; use a dedicated identity provider when you need multi-user roles, password reset, audit trails, or federated authentication.
