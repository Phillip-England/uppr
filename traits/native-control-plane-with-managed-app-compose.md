---
title: Native Control Plane With Managed App Compose
hashtags:
  - "#systemd"
  - "#docker"
  - "#caddy"
  - "#deployment"
---

# Native Control Plane With Managed App Compose

## Intent

Run the deployment manager and reverse proxy as native services while managed applications run under Docker Compose. This keeps the control plane available while application containers are rebuilt or replaced.

## When To Use

Use this pattern when an admin UI must orchestrate container replacement without being inside the same Compose stack it is replacing.

## Implementation

`uppr service [path]` bootstraps a runtime root, finds the current executable and `caddyx`, renders `uppr.service` and `caddy.service`, installs them under `/etc/systemd/system`, reloads systemd, enables both services, and restarts them. Privileged steps use `sudo` only when the process is not already root.

The Uppr unit runs `uppr service-run <root>`. That command ensures server files, regenerates master deployment files, starts managed app containers with Docker Compose, and then serves the authenticated UI on `0.0.0.0:9944`.

The Caddy unit runs independently, regenerates server files in `ExecStartPre`, starts `caddyx` with the generated `Caddyfile`, and uses `ExecReload` with `--force`. Its systemd sandbox grants only the bind capability needed for ports 80 and 443 and writes under the runtime root plus `data/caddy`.

## Project Evidence

- `service.go`: `updateSystemServices`, `runService`, `renderUpprSystemdService`, `renderCaddySystemdService`, `findCaddyx`, and `installCaddyx`.
- `main.go`: commands `service`, `service-run`, `service-uppr`, `service-caddy`, `install-caddyx`, and `launch`.
- `README.md`: explains the native service model and operator account requirements.

## Reuse Notes

Keep the orchestrator outside the container stack it manages. Generate service files from the actual executable path and runtime root, and keep privileged operations narrow so day-to-day generated files remain owned by the operator account.
