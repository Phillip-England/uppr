---
title: Repos Conf Application Registry
hashtags:
  - "#configuration"
  - "#git"
  - "#registry"
  - "#deployment"
---

# Repos Conf Application Registry

## Intent

Represent managed applications as a small, editable INI-like registry file. Each `[repo]` block describes one Git repository plus deployment metadata that can feed CLI operations, web forms, Docker Compose generation, and Caddy routing.

## When To Use

Use this pattern when operators need a transparent registry they can edit manually or through a UI, and the registry must be simple enough to inspect during deployment incidents.

## Implementation

The registry lives at `repos.conf`. `readRepos` parses repeated `[repo]` blocks and supports fields such as `name`, `url`, `path`, `branch`, `port`, `container_port`, repeated `domain`, rate limit settings, repeated `env`, and repeated `volume`.

`uppr add` derives missing `name` and `path` values from the URL, defaults paths to `apps/<repo-name>`, normalizes rate limits, and rejects duplicate names, URLs, or paths. `uppr remove` accepts a name, URL, or path. `writeRepos` emits the same file format back to disk with explicit rate limit fields.

The web UI uses the same parser and writer through handlers such as `handleAddRepo`, `handleSaveRepo`, and `handleDeleteRepo`, so CLI and web operations share the same source of truth.

## Project Evidence

- `main.go`: `Repo`, `RateLimit`, `addRepo`, `removeRepo`, `readRepos`, `writeRepos`, and `validateNewRepo`.
- `web.go`: repository add/edit/delete handlers.
- `README.md`: documents the `repos.conf` format and optional repo settings.

## Reuse Notes

Keep the registry schema narrow and explicit. Avoid hiding operational state in an opaque database when a flat config file is the main contract operators need to understand and version.
