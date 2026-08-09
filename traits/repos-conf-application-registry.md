---
title: Repos Conf Application Registry
hashtags:
  - "#configuration"
  - "#git"
  - "#deployment"
  - "#ini"
---

# Repos Conf Application Registry

## Intent

Represent managed applications in a small, hand-editable registry file that both CLI commands and the web UI can read and write.

The registry becomes the source of truth for Git repository location, deployment domains, port mapping, rate-limit policy, environment overrides, and extra volumes.

## When To Use

Use this when an operator should be able to manage applications without a database, and when a single file should drive cloning, launch generation, and UI editing.

## Implementation

`repos.conf` is parsed as repeated `[repo]` blocks. Supported settings include:

- `name`, `url`, `path`, and `branch` for repository identity and Git operations.
- `port` and `container_port` for host/container routing.
- Repeated `domain` values for Caddy routes.
- `rate_limit_enabled`, `rate_limit_zone`, `rate_limit_events`, and `rate_limit_window`.
- Repeated `env` and `volume` entries for Compose service customization.

The parser validates unknown settings, missing URLs, bad ports, bad booleans, and non-positive rate-limit event counts. Missing names are derived from the repository URL; missing paths default to `apps/<repo-name>`. Repository paths are resolved relative to the discovered project root so commands can be run from nested directories.

## Project Evidence

- `main.go` defines `Repo`, `RateLimit`, `readRepos`, `writeRepos`, `addRepo`, `removeRepo`, `findProjectRoot`, and `resolveRepoPaths`.
- `generate.go` consumes `Repo` values when rendering Caddy and Docker Compose files.
- `web.go` uses the same registry for repo add/edit/delete flows.
- `README.md` documents the `repos.conf` format.

## Reuse Notes

Keep the file format small and explicit. Validate during parsing so every downstream command receives normalized data. If both humans and tools edit the file, preserve repeated fields in a deterministic order when writing.
