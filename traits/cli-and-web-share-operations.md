---
title: CLI And Web Share Operations
hashtags:
  - "#cli"
  - "#web-ui"
  - "#operations"
  - "#reuse"
---

# CLI And Web Share Operations

## Intent

Expose operational workflows through both CLI commands and a web UI while keeping the core behavior in shared Go functions.

This avoids drift between automation-friendly commands and browser-driven operator workflows.

## When To Use

Use this when the same deployment tool must support local terminal workflows, remote authenticated administration, and less command-line-oriented operational tasks.

## Implementation

The CLI entry point in `runWithServe` dispatches commands such as `add`, `pull`, `push`, `generate`, `launch`, `backup`, `restore`, `service`, `web`, and `serve`. The web handlers call the same underlying functions where possible:

- Repo add/edit/delete flows read and write `repos.conf`.
- Pull and push buttons call `pullRepoAt` and `pushRepoAt`.
- Generate actions call `generateProjectFilesAt` or `generateServerFilesAt`.
- Sync preparation calls `prepareRepoEnv`.
- Launch shells run the same `uppr launch .` or `docker compose` command shown to the operator.
- Backup and restore routes reuse `backupState` and `restoreState` behavior.

The local `uppr web [path]` mode is unauthenticated and bound to `127.0.0.1:9944`. The deployment `uppr serve [path]` mode requires authentication and can bind to a configured or supplied address.

## Project Evidence

- `main.go` defines `runWithServe` and the shared CLI operations.
- `web.go` defines `serveWeb`, `serveServer`, route handlers, and shared operation calls.
- `auth.go` supplies authentication only when `authRequired` is true.
- `main_test.go` verifies CLI dispatch and serve behavior.

## Reuse Notes

Design the web layer as a thin adapter over command functions, not as a separate implementation. Keep local-only web mode and remote authenticated server mode explicit so development convenience does not weaken deployed access controls.
