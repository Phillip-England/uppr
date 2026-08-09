---
title: Generated Caddy Compose Runtime
hashtags:
  - "#docker"
  - "#caddy"
  - "#reverse-proxy"
  - "#deployment"
---

# Generated Caddy Compose Runtime

## Intent

Generate runtime infrastructure from application metadata instead of requiring operators to maintain Caddy and Docker Compose files by hand.

The generated files are disposable outputs: operators edit `repos.conf` or workspace configuration, then regenerate launch files.

## When To Use

Use this when many small applications need consistent container build, environment, persistence, reverse proxy, and rate-limit behavior from a central contract.

## Implementation

For a single project, `uppr generate` writes:

- `Caddyfile`
- `caddyx.Dockerfile`
- `docker-compose.yml`
- `Makefile`

For server mode, `uppr generate-server` writes root-level `Caddyfile`, `docker-compose.yml`, and `Makefile` from every registered workspace.

Generation reads each repo's `Dockerfile` `EXPOSE` port when `port` or `container_port` is missing. Caddy routes use configured domains or fall back to `<service>.localhost`. Rate limiting is rendered into Caddy route blocks when enabled. Compose services build from each repo path, load `<repo>/config/.env`, mount `<repo>/config` and `<repo>/data`, and skip user-provided volumes that try to override `/app/config` or `/app/data`.

Server-mode Compose publishes app ports only on `127.0.0.1`, while the generated Caddyfile proxies public hostnames to those loopback ports.

## Project Evidence

- `generate.go` defines `generateProjectFilesAt`, `generateServerFilesAt`, `renderCaddyfile`, `renderServerCaddyfile`, `renderDockerCompose`, and `renderServerDockerCompose`.
- `main.go` defines `readDockerfileExposedPort` and wires `generate` / `generate-server` CLI commands.
- `README.md` documents generated Caddy, Compose, and Makefile behavior.
- `main_test.go` verifies generated system and runtime details.

## Reuse Notes

Include a clear generated-file header that points operators back to the source configuration. Keep public proxy concerns separate from app containers, and protect reserved mounts so app-level customization cannot break the runtime contract.
