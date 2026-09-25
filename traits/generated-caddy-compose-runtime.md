---
title: Generated Caddy Compose Runtime
hashtags:
  - "#docker"
  - "#caddy"
  - "#reverse-proxy"
  - "#generation"
---

# Generated Caddy Compose Runtime

## Intent

Generate Docker Compose, Caddy routing, and Makefile commands from repository metadata instead of requiring operators to maintain deployment files by hand.

## When To Use

Use this pattern when each application follows a common container contract and deployment files should be reproducible from a registry such as `repos.conf`.

## Implementation

`uppr generate` reads `repos.conf`, applies missing ports from each app's Dockerfile `EXPOSE` instruction, and writes `Caddyfile`, `caddyx.Dockerfile`, `docker-compose.yml`, and `Makefile`.

Generated Compose services build from each repo path, set `working_dir: /app`, load `config/.env` as an `env_file`, mount app `config/` to `/app/config`, and mount app `data/` to `/app/data`. Extra volumes from `repos.conf` are allowed, but `isProtectedAppVolume` prevents overriding `/app/config` and `/app/data`.

Generated Caddy routes use configured domains, reverse proxy to the app service or loopback host port, and include optional `rate_limit` blocks with defaults from `defaultRateLimit`.

## Project Evidence

- `generate.go`: `generateProjectFilesAt`, `renderCaddyfile`, `renderCaddyDockerfile`, `renderDockerCompose`, `renderMakefile`, and `isProtectedAppVolume`.
- `main.go`: `readDockerfileExposedPort` and port auto-configuration during pull.
- `README.md`: documents generated files and the app repository contract.
- `dump.go`: describes the expected app `Dockerfile`, `schema.json`, `config/`, and `data/` layout.

## Reuse Notes

Make generated files clearly disposable by writing headers that tell operators which source config to edit. Preserve protected mounts so app-level custom volumes cannot accidentally replace control-plane-owned config or data paths.
