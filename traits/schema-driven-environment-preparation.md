---
title: Schema Driven Environment Preparation
hashtags:
  - "#environment"
  - "#schema"
  - "#configuration"
  - "#onboarding"
---

# Schema Driven Environment Preparation

## Intent

Let each managed application declare its expected environment variables in source while storing actual runtime values outside Git. The control plane prepares missing keys without overwriting existing secrets.

## When To Use

Use this pattern when multiple apps need repeatable onboarding, but each app has different environment variables and secret values must remain runtime-local.

## Implementation

Managed apps can commit a root-level `schema.json` with a top-level `variables` array. Each variable has a `name` and may include `description`, `example`, and `required`. Uppr still supports the legacy `env.schema` format with one variable name per line.

After pull/sync, `prepareRepoEnv` reads the app schema and creates or updates the app's `config/.env` with missing blank entries. Existing values are preserved. The web UI renders schema descriptions and examples in repository configuration pages, and `/env/download` exports workspace environment state.

The integration docs recommend `schema.json` as the canonical machine-readable environment reference and warn against putting real secrets in the schema.

## Project Evidence

- `main.go`: constants `envSchemaFile` and `envJSONSchemaFile`, `prepareRepoEnv`, and schema parsing helpers.
- `web.go`: `repoAppEnvFields`, repository config rendering, `saveRepoAppEnvFromForm`, and `handleEnvDownload`.
- `ENV_SCHEMA_JSON.md`: documents the preferred schema format.
- `dump.go`: generated app integration guidance references `schema.json` and `env.schema`.

## Reuse Notes

Use schema files for variable names and safe guidance only. Keep values in environment-specific files, and make updates additive so repeated syncs never erase existing secrets.
