---
title: OS Aware Workspace Isolation
hashtags:
  - "#workspaces"
  - "#isolation"
  - "#filesystem"
  - "#configuration"
---

# OS Aware Workspace Isolation

## Intent

Separate server-level control-plane state from per-workspace application state. Each workspace gets its own repository registry, credentials file, and runtime directories while the server root keeps the workspace registry and generated master deployment files.

## When To Use

Use this pattern when one control plane manages multiple groups of applications and each group needs isolated repository lists, environment values, and app directories.

## Implementation

The server root owns `workspaces.conf`. Each `[workspace]` entry contains a normalized `name` and a `path`. Workspace names are lowercased and sanitized to `a-z`, `A-Z`, `0-9`, `_`, and `-`.

`createWorkspace` chooses the workspace base directory from `UPPR_WORKSPACES_DIR` when set. Otherwise, it uses OS conventions: `~/Library/Application Support/uppr/workspaces` on macOS, `%LOCALAPPDATA%\uppr\workspaces` or `%APPDATA%\uppr\workspaces` on Windows, and `${XDG_DATA_HOME:-~/.local/share}/uppr/workspaces` on Linux and other Unix-like systems.

`ensureWorkspaceFiles` creates each workspace's `config/`, `data/`, `repos.conf`, and `config/.env`. It copies the server `config/.env` as an initial template when available, then applies current environment defaults.

## Project Evidence

- `workspace.go`: `Workspace`, `createWorkspace`, `defaultWorkspacesRoot`, `ensureWorkspaceFiles`, `readWorkspaces`, `writeWorkspaces`, and `normalizeWorkspaceName`.
- `web.go`: `/workspaces` and `/workspaces/{name}/...` routing.
- `README.md`: documents server root versus workspace directory layout.

## Reuse Notes

Use OS-native data locations by default, but provide an environment-variable override for deployment-specific storage. Store absolute paths in the workspace registry when the workspace lives outside the server root.
