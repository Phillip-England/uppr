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

Protect the deployment UI with simple admin credentials, signed session cookies, and bounded login failure tracking stored in a private SQLite database.

This gives a small self-hosted tool durable abuse protection without requiring an external auth provider.

## When To Use

Use this for an internal or operator-facing web UI where a single admin credential pair is acceptable and the service should remain self-contained.

## Implementation

Server configuration is loaded from `config/.env` using `ADMIN_USERNAME`, `ADMIN_PASSWORD`, `SESSION_SECRET`, and `ADDR`. Server mode refuses to start when required admin values are blank.

Login sessions use the `uppr_session` cookie and an in-memory session store with a 12 hour TTL. Login failure attempts are recorded by client IP in `data/main.sqlite` in a `login_failures` table with an `(ip, attempted_at)` index. The database file is chmodded to `0600` after creation. The login path blocks an IP after five failures in a 24 hour window and purges old rows during login checks so the table remains bounded.

Generated public Caddy routes also include client-IP rate limiting. Global Uppr route policy is configured with `UPPR_RATE_LIMIT_ENABLED`, `UPPR_RATE_LIMIT_ZONE`, `UPPR_RATE_LIMIT_EVENTS`, and `UPPR_RATE_LIMIT_WINDOW`.

## Project Evidence

- `auth.go` defines `serverConfig`, `loadServerConfig`, `initAuthDB`, `ensureAuthDBFile`, `handleLogin`, session helpers, and login failure constants.
- `generate.go` defines `upprRateLimitFromEnv` and `writeCaddyRateLimit`.
- `workspace.go` and `web.go` call `ensureAuthDBFile` during server/project preparation.
- `main_test.go` verifies private database permissions and required env behavior.

## Reuse Notes

Use constant-time comparison for credentials, keep the session secret out of source control, and store only the minimum durable abuse signal needed. File permissions matter for local SQLite databases that contain IP addresses or login metadata.
