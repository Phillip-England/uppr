---
title: Native Control Plane With Managed App Compose
hashtags:
  - "#systemd"
  - "#docker"
  - "#caddy"
  - "#operations"
---

# Native Control Plane With Managed App Compose

## Intent

Run the deployment manager and reverse proxy as native system services while Docker Compose manages only the application containers.

This prevents replacing the app stack from stopping the UI or proxy that initiated the replacement.

## When To Use

Use this when a control plane needs to rebuild or restart managed containers without depending on those same containers for its own availability.

## Implementation

`uppr service` bootstraps the runtime root, renders `uppr.service` and `caddy.service`, stages them in a temporary directory, installs them under `/etc/systemd/system`, reloads systemd, enables both services, and restarts them. Privileged operations are run through `sudo` only when the current process is not root.

The Uppr service runs the current executable as the ordinary operator account with `ExecStart=<uppr> service-run <root>`. `service-run` ensures server files exist, regenerates root launch files, starts app containers with Docker Compose, then serves the authenticated UI on `0.0.0.0:9944`.

The Caddy service runs `caddyx` natively with `ExecStartPre=<uppr> generate-server <root>`, `ExecReload=<caddyx> reload --force`, and `CAP_NET_BIND_SERVICE` so it can bind ports 80 and 443 without running as root. Caddy writes only to the runtime root and `data/caddy`.

## Project Evidence

- `service.go` defines `updateSystemServices`, `bootstrapServiceRoot`, `renderUpprSystemdService`, `renderCaddySystemdService`, and `runService`.
- `main.go` wires `service`, `service-uppr`, `service-caddy`, and `service-run`.
- `main_test.go` verifies that the Uppr and Caddy units run natively and include the expected users, groups, paths, and capabilities.
- `README.md` explains the native control-plane model.

## Reuse Notes

Separate the orchestration process from the workload it controls. Render units from the current executable and runtime root, run services as the operator account, and limit privilege escalation to installing and controlling systemd units.
