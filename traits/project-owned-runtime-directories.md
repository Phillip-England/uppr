---
title: Project-Owned Runtime Directories
hashtags:
  - "#runtime"
  - "#configuration"
  - "#persistence"
  - "#secrets"
---

# Project-Owned Runtime Directories

## Intent

Keep runtime configuration, credentials, generated deployment files, and mutable data in an initialized runtime root instead of coupling them to the CLI source checkout. This lets the same `uppr` executable operate against separate production, staging, or client-specific roots.

## When To Use

Use this pattern when a deployment manager should be installable as a binary while each managed environment owns its own secrets, repository registry, generated files, and persistent state.

## Implementation

Initialize a runtime with `uppr init [path]`. The command creates `config/.env`, `repos.conf`, and `data/main.sqlite` under the selected root. Runtime files are written with restrictive permissions where appropriate: `writeFileIfMissing` uses `0600`, and `ensureAuthDBFile` chmods `data/main.sqlite` to `0600`.

Most project commands call `findProjectRoot(".")`, which walks upward until it finds an initialized root. Web and server commands call `requireEnvFile` and refuse to start if `config/.env` is missing, making initialization explicit.

The source/runtime split is documented as an operating model: source contains Go code, tests, docs, and packaging files, while runtime roots contain `config/.env`, `repos.conf`, `workspaces.conf`, `Caddyfile`, `docker-compose.yml`, `Makefile`, and `data/`.

## Project Evidence

- `main.go`: `initProject`, `writeFileIfMissing`, `findProjectRoot`, and command dispatch.
- `web.go`: `ensureProjectFiles` and `serveWeb`.
- `workspace.go`: `ensureServerFiles` and `requireEnvFile`.
- `auth.go`: `ensureAuthDBFile`.
- `RUNTIME_VERSUS_CLI.md`: explicit source-versus-runtime guidance.
- `.gitignore`: ignores runtime files such as `config/.env`, `data/`, and the built `uppr` binary.

## Reuse Notes

Copy the root-detection and initialization boundary, not the exact file names unless they fit the target project. Runtime roots should be portable, private by default, and safe to create outside the source repository.
