---
title: OS Aware Workspace Isolation
hashtags:
  - "#workspaces"
  - "#filesystem"
  - "#configuration"
  - "#cross-platform"
---

# OS Aware Workspace Isolation

## Intent

Separate the server root from per-workspace repository roots, and store workspaces in an operating-system-appropriate application data directory by default.

This lets one Uppr server manage multiple isolated groups of repositories without placing all workspace data under the installation directory.

## When To Use

Use this when a server has global configuration but each user, tenant, customer, or environment needs independent repo lists, app configs, and data directories.

## Implementation

The server root contains `workspaces.conf`, generated root launch files, shared admin credentials, and public Uppr routing settings. Each workspace has its own:

- `repos.conf`
- `config/.env`
- `data/`
- managed app repository directories

`createWorkspace` normalizes names to lowercase alphanumeric, underscore, and hyphen characters. New workspace paths are created below `UPPR_WORKSPACES_DIR` when set, otherwise under platform-specific locations:

- macOS: `~/Library/Application Support/uppr/workspaces`
- Linux: `${XDG_DATA_HOME:-~/.local/share}/uppr/workspaces`
- Windows: `%LOCALAPPDATA%\uppr\workspaces`

The web router maps `/workspaces/<name>/...` to a child `webApp` rooted at the workspace path while preserving the server root for root-level generation and launch actions.

## Project Evidence

- `workspace.go` defines `Workspace`, `workspacesDirEnv`, `createWorkspace`, `defaultWorkspacesRoot`, `ensureWorkspaceFiles`, `readWorkspaces`, and `resolveWorkspace`.
- `web.go` defines `handleWorkspaces`, `handleWorkspaceRoute`, and `workspaceRoutes`.
- `generate.go` reads all workspaces when rendering server-level Caddy, Compose, and Makefile outputs.
- `README.md` documents the workspace storage locations and `UPPR_WORKSPACES_DIR`.

## Reuse Notes

Keep the workspace registry small and portable, but allow absolute paths when an operator needs custom storage. Normalize workspace names before using them in URLs, service names, or filesystem paths.
