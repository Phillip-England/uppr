---
title: Portable Backup Restore With Manifest
hashtags:
  - "#backup"
  - "#restore"
  - "#migration"
  - "#portability"
---

# Portable Backup Restore With Manifest

## Intent

Export a complete runtime into a portable `.tar.gz` artifact with a manifest, then restore it safely into another initialized root while rewriting machine-specific workspace paths.

## When To Use

Use this pattern when operators need same-machine moves, server migrations, or disaster recovery for a control plane that stores state across config files, app repositories, workspaces, and data directories.

## Implementation

`uppr backup <file> [path]` calls `backupSources` to classify the runtime as either `project` or `server`. Project backups archive the project root under `project/`. Server backups archive the server root under `server/` and each registered workspace under `workspaces/<name>/`. The archive includes a JSON `manifest.json` with format version, kind, timestamp, and workspace names.

Backup writes to a temporary file in the target directory, skips the final artifact and temp file if they are inside the source tree, closes the tar/gzip writers, then atomically renames the temp file.

`restoreState` extracts into a staging directory, validates the manifest version and kind, rejects unsafe archive paths, rejects unsafe symlinks, and replaces existing regular files instead of truncating them. Server restores rewrite `UPPR_WORKSPACES_DIR` to `<destination>/data/workspaces`, copy workspaces there, and rewrite `workspaces.conf` with destination paths.

`migrateState` stages only runtime assets, rejects overlapping source/destination paths, requires an initialized destination, refuses project repos with absolute or escaping paths, then backs up and restores through the same artifact flow.

## Project Evidence

- `backup.go`: `backupState`, `backupSources`, `restoreState`, `extractBackup`, `copyTree`, `stageRuntimeAssets`, and `migrateState`.
- `web.go`: backup download, restore upload, and migration handlers.
- `README.md`: documents backup, restore, and migration workflows.
- `RUNTIME_VERSUS_CLI.md`: explains runtime migration and path rewriting.

## Reuse Notes

Include a manifest and validate it before copying files into place. Treat restore as hostile input: block absolute paths, parent traversal, unsafe symlinks, and stale absolute runtime paths.
