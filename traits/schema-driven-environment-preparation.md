---
title: Schema Driven Environment Preparation
hashtags:
  - "#environment"
  - "#schema"
  - "#configuration"
  - "#developer-workflow"
---

# Schema Driven Environment Preparation

## Intent

Let each managed application declare the environment variables it needs, then have the control plane create missing `config/.env` entries without overwriting existing values.

This gives app repositories a machine-readable deployment contract while keeping real secret values private and local.

## When To Use

Use this when one tool prepares many application repositories and needs to know which environment keys to collect, validate, display, or export.

## Implementation

Apps can include a root-level `schema.json` with a `variables` array. Each variable can use `name` or `key`, plus optional `description`, `example`, and `required` fields. The legacy `env.schema` file is also supported as one variable name per line.

`prepareRepoEnv` reads the schema, ensures `<repo>/config` and `<repo>/data` exist, creates `<repo>/data/main.sqlite` if missing, then updates `<repo>/config/.env`. Existing key values are preserved; only missing keys are appended as blank `KEY=` lines. Invalid JSON, duplicate variable names, and invalid environment variable names fail early with path-specific errors.

The web UI reuses the same schema information to render editable environment fields and supports workspace-wide preparation from the Sync page.

## Project Evidence

- `main.go` defines `envSchemaFile`, `envJSONSchemaFile`, `prepareRepoEnv`, `readRepoEnvSchema`, and `readEnvJSONSchema`.
- `web.go` uses `repoAppEnvFields`, `saveRepoAppEnvFromForm`, and `handleSyncPrepare` to expose the same environment contract in the UI.
- `dump.go` and `public.go` document `schema.json` as the preferred app integration contract.
- `main_test.go` includes tests for JSON schema parsing and environment preparation behavior.

## Reuse Notes

Prefer a structured schema over a loose list when a UI or automation needs descriptions, examples, and required flags. Preserve existing `.env` values by parsing and appending keys instead of rewriting the full file from schema defaults.
