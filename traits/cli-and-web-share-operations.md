---
title: CLI And Web Share Operations
hashtags:
  - "#cli"
  - "#web-ui"
  - "#architecture"
  - "#operations"
---

# CLI And Web Share Operations

## Intent

Expose the same operational capabilities through command-line commands and a browser UI by routing both surfaces through shared Go functions.

## When To Use

Use this pattern when operators need both scriptable automation and a manual control panel, and divergence between the two would create deployment risk.

## Implementation

Command dispatch in `runWithServe` maps CLI commands to functions such as `addRepo`, `pullRepos`, `pushRepos`, `generateProjectFiles`, `backupState`, `restoreState`, `generateServerFilesAt`, and `launchServer`.

The web handlers call the same lower-level operations: repository pages use `readRepos`, `writeRepos`, `pullRepoAt`, and `pushRepoAt`; the generate page calls `generateProjectFilesAt` or `generateServerFilesAt`; launch routes call `launchServer`; backup routes call `backupState`, `restoreState`, and `migrateState`.

Both surfaces operate on the same runtime root files (`config/.env`, `repos.conf`, `workspaces.conf`, generated deployment files, and app `config/`/`data/` directories), so state changed in one surface is immediately visible in the other.

## Project Evidence

- `main.go`: CLI command dispatch and shared repo/git/generation helpers.
- `web.go`: handlers for repos, sync, generate, launch, credentials, and backup.
- `backup.go`: backup, restore, and migration functions reused by CLI and web flows.
- `README.md`: documents equivalent CLI and web workflows.

## Reuse Notes

Design handlers as adapters around domain functions rather than separate implementations. This keeps validation, file formats, and failure behavior consistent across CLI automation and web operations.
