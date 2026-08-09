---
title: Project-Owned Runtime Directories
hashtags:
  - "#runtime"
  - "#configuration"
  - "#persistence"
  - "#sqlite"
---

# Project-Owned Runtime Directories

## Intent

Keep mutable application state in a small, predictable directory contract owned by each runtime root or app repository. Configuration lives under `config/`, durable data lives under `data/`, and source-controlled metadata lives in simple root-level files.

This keeps secrets, databases, generated launch files, and repository metadata easy to mount, back up, ignore from Git, and recreate during startup.

## When To Use

Use this when a CLI or deployment manager needs to operate from many possible project roots and must separate checked-in source from mutable runtime state.

## Implementation

The project root uses:

- `config/.env` for credentials and runtime environment.
- `data/main.sqlite` for local SQLite state.
- `repos.conf` for managed app repository metadata.
- `workspaces.conf` for server-mode workspace registry.

App repositories managed by Uppr use the same runtime shape:

- `<repo>/config/.env` for app-specific environment values.
- `<repo>/data/main.sqlite` as the conventional primary app database.
- `<repo>/config` and `<repo>/data` are mounted into containers at `/app/config` and `/app/data`.

Startup and preparation routines create required directories with `0755`, create private mutable files with `0600`, and refuse to start the web/server path when `config/.env` is missing. The `.gitignore` excludes `config/.env`, `data/`, and other runtime outputs.

## Project Evidence

- `main.go` defines `envFile`, `reposFile`, `defaultDBPath`, `initProject`, `ensureProjectFiles`, `prepareRepoEnv`, and `writeFileIfMissing`.
- `workspace.go` defines `ensureServerFiles` and `ensureWorkspaceFiles`.
- `generate.go` mounts app `config` and `data` directories in generated Compose services.
- `.gitignore` excludes `config/.env` and `data/`.
- `README.md` documents the runtime layout and app contract.

## Reuse Notes

Copy the directory contract and creation rules, not the exact file names blindly. Make the runtime root explicit, keep secrets and databases out of Git, and use startup checks that fail clearly when required private config is absent.
