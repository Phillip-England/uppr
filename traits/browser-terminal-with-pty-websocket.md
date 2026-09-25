---
title: Browser Terminal With PTY WebSocket
hashtags:
  - "#terminal"
  - "#websocket"
  - "#pty"
  - "#operations"
---

# Browser Terminal With PTY WebSocket

## Intent

Provide an embedded browser shell for operational work while preserving terminal behavior such as binary streams and resize events.

## When To Use

Use this pattern when a local or authenticated admin UI needs direct command execution inside a selected runtime or repository directory.

## Implementation

Terminal routes are split between HTML pages and WebSocket endpoints. `handleTerminal` renders the project-level terminal page, `handleRepoShell` renders a repo shell page, and `handleLaunchShell` exposes launch-command output in the browser.

`handleTerminalShell` upgrades the request with Gorilla WebSocket and starts a shell under a pseudo-terminal using `github.com/creack/pty`. Messages carry JSON fields such as `type`, `data`, `cols`, and `rows`; binary messages stream terminal bytes, and resize messages call `pty.Setsize`.

Shell working directories are resolved through helpers such as `repoShellPath`, so repository shells run from the configured repo path instead of the control-plane source directory.

## Project Evidence

- `web.go`: imports `github.com/gorilla/websocket` and `github.com/creack/pty`, terminal routes, `terminalUpgrader`, and PTY/WebSocket handling.
- `go.mod`: declares `github.com/creack/pty` and `github.com/gorilla/websocket`.
- `README.md`: mentions the embedded browser shell for configured repositories.

## Reuse Notes

Keep terminal endpoints behind the same authentication boundary as the rest of the admin UI. Resolve working directories from trusted registry data and treat terminal access as equivalent to shell access on the host account running the service.
