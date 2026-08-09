---
title: Portable Backup Restore With Manifest
hashtags:
  - "#backup"
  - "#restore"
  - "#migration"
  - "#archive"
---

# Portable Backup Restore With Manifest

## Intent

Export and restore a complete runtime as one compressed artifact with a manifest that describes backup type and format version.

The same mechanism supports simple project backup, server backup with external workspaces, and same-machine migration into another initialized runtime.

## When To Use

Use this when a self-contained tool owns filesystem state spread across a root directory and optional workspace directories, and operators need a portable way to move or recover it.

## Implementation

`uppr backup <file> [path]` writes a `.tar.gz` archive through a temporary file in the target directory, then renames it into place. The archive starts with `manifest.json` containing `version`, `created_at`, `kind`, and workspace names when present.

Project backups archive the project root under `project/`. Server backups archive the server root under `server/` and each registered workspace under `workspaces/<name>/`.

`uppr restore <file> [path]` extracts into a temporary staging directory, validates the manifest version and kind, then copies files into the destination. Restore rejects unsafe archive paths, absolute paths, parent-directory traversal, unsafe symlinks, unsupported entry types, and missing manifests. It replaces existing files rather than truncating them so read-only Git pack files and existing symlinks do not break repeated restores.

Server restores rewrite `UPPR_WORKSPACES_DIR` to `<destination>/data/workspaces` and rewrite `workspaces.conf` to restored workspace paths so the restored runtime does not depend on the source machine's paths.

## Project Evidence

- `backup.go` defines `backupManifest`, `backupState`, `backupSources`, `restoreState`, `extractBackup`, `copyTree`, `stageRuntimeAssets`, and `migrateState`.
- `main.go` wires `backup`, `restore`, and `migrate` CLI commands.
- `web.go` exposes backup download and restore routes.
- `README.md` documents backup, restore, and migration workflows.

## Reuse Notes

Always include a versioned manifest and validate paths before extraction. For relocatable server restores, rewrite machine-specific paths during restore rather than preserving absolute paths from the source environment.
