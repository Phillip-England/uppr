# Uppr

Uppr is a command line tool for deploying independent Docker apps on a Linux host. Each app runs in its own systemd service. One launchpad can be installed per server. Its Caddy service routes app domains to loopback ports and applies per-client rate limits. Existing Uppr v2 `launchpad.json` files continue to work.

## Install

Install [Bun](https://bun.sh/) first. From this repository:

```sh
make install
uppr --help
```

The launcher goes to `~/.local/bin/uppr` and the bundled program to `~/.local/share/uppr/uppr.js`. Add `~/.local/bin` to `PATH` if needed. Set `PREFIX=/some/path` to install elsewhere. Run `make install` after updating Uppr, then `uppr install` in the installed launchpad to refresh its systemd units.

On the Linux host, check and install shared dependencies:

```sh
uppr deps check
uppr deps install
uppr deps check
```

The host needs Git, Docker, systemd, and Caddy with the `rate_limit` module. The installer supports apt and dnf and may ask for a sudo password. It builds `/usr/local/bin/uppr-caddy` using Go or xcaddy. Use `uppr deps caddy` to rebuild only Caddy. If Docker socket access is denied, run `uppr deps docker-access`, sign out and back in, and retry. Launch apps as the account with Docker access; installed app services run as that account.

On a 512 MB host, the Caddy build may be killed by the Linux out-of-memory manager. Uppr limits Go build parallelism, but the compiler still needs more memory. Check `free -h`, `swapon --show`, and `df -h /` first. If no swap is active and at least 2 GB of disk space is free, create temporary swap and retry:

```sh
sudo fallocate -l 2G /swapfile
sudo chmod 600 /swapfile
sudo mkswap /swapfile
sudo swapon /swapfile
uppr deps caddy
```

The swap stays active until reboot. If `/swapfile` already exists, inspect it before running these commands; do not overwrite an existing swap file. After a successful build, `uppr deps check` verifies the installed Caddy module.

If `uppr install` reports that `caddy.service` is active, review the sites it currently serves: Uppr starts a separate `uppr-caddy.service` with its own generated Caddyfile. Move any routes you need into the launchpad first, then run:

```sh
uppr deps caddy-switch
uppr install
```

`caddy-switch` stops and disables `caddy.service`; run it on the Linux server. Existing sites served only by that service will be unavailable after the switch. To replace an installed launchpad, run `uppr uninstall` from the old launchpad before installing the new one.

Application runtimes and packages belong in each app's Dockerfile. Preparation and schema editing can be done on a non-Linux machine; installation and service control require Linux.

## First deployment

```sh
uppr init my-pad
cd my-pad
uppr add https://github.com/you/api.git --name api --domain api.example.com
uppr pull api
uppr schema generate api
uppr domains api api.example.com
uppr schema show api
uppr prepare api
uppr env list api
uppr doctor
uppr install
uppr launch api
uppr list
uppr curl api /health
```

Run launchpad commands from its root or any child directory. `uppr` or `uppr help` prints the command index; `uppr help add`, `uppr help domains`, and other command topics show options. `uppr list` shows every app's state, routes, ports, checkout path, and next setup step. `uppr ip` prints the server's public IPv4 address from any directory. Point public domains' DNS at that address.

`uppr add` records a repository but does not clone it. Its options are `--name`, `--domain`, `--port` (host port), `--container-port` (legacy fallback, default 3000), and `--branch`. `uppr pull <name>` clones or fast forwards a checkout; `uppr pull --all` updates all of them. Pulling does not start apps. `uppr install` validates and installs systemd units and Caddy routes. `uppr launch <name>` builds and starts from the current checkout, without pulling Git changes; `uppr launch --all` handles all registered apps. `uppr serve [path] [name]` remains as a compatibility alias for launching.

## Application contract

Each app repository needs a root `Dockerfile`. Its process must listen on `0.0.0.0` at the configured container port and remain in the foreground. Store durable data under `/app/data`. Uppr mounts the checkout's `config/` and `data/` at `/app/config` and `/app/data`, and passes `config/.env` to Docker. `prepare` creates the env file with mode `0600` and `data/main.sqlite`, and protects both directories through `.gitignore` and `.dockerignore`.

A root `schema.json` declares variables, domains, and a rate limit:

```json
{
  "port": { "variable": "APP_PORT" },
  "domains": ["api.example.com"],
  "rate_limit": { "events": 100, "window": "1m", "zone": "dynamic" },
  "variables": [
    { "name": "API_KEY", "description": "Upstream token", "required": true },
    { "name": "APP_PORT", "example": "3000", "required": false }
  ]
}
```

Variables are required unless `"required": false`. Schema domains override the launchpad manifest domain. Older schemas without `domains` keep using the manifest domain. Rate limiting defaults to 100 requests per client IP in one minute, in zone `dynamic`. Uppr prefixes zone names with the launchpad and app IDs. The Caddy binary must include `github.com/mholt/caddy-ratelimit`.

New schemas should declare `port` explicitly. Use `{ "variable": "APP_PORT" }` to reference the app's actual listen-port environment variable, including names such as `EGATE_PORT`; give that variable a numeric `example` matching the app's default. Uppr uses the value from `config/.env` or the example, maps Docker to that port, and passes the resolved value into the container. For an app with a fixed port and no port environment variable, use `"port": 8119`. `uppr schema generate` infers the variable form when its env example has exactly one `PORT` or `*_PORT` variable. An undeclared `*_PORT` variable fails before installation instead of silently mapping to 3000. Dockerfile `EXPOSE` does not select the mapped port. Older schemas with implicit `PORT` or no port variable remain supported through the previous fallback. Run `uppr install` after changing a port to refresh the mapping, then `uppr launch --all`.

## Schemas and environment

```sh
uppr schema generate api                    # reads .env.example, .env.sample, or .env.template
uppr schema generate api --from-env FILE    # use a specific example file
uppr schema generate api --force            # replace existing schema
uppr schema show api
uppr schema edit api                        # opens $VISUAL, $EDITOR, or vi; validates on save
uppr schema add api NEW_KEY --description "Purpose" --example "example" --optional
uppr schema remove api OLD_KEY
uppr domains api api.example.com,alt.example.com
uppr schema rate api --events 250 --window 1m --zone dynamic
```

Generation produces a valid empty schema if no example file exists. It does not copy example values into the schema. `uppr domains` creates `schema.json` if needed. Schema changes are local Git changes. Run `uppr install` after changing domains or rate limits.

```sh
uppr prepare api                # prompt for missing required values
uppr env list api               # show descriptions, examples, and set/empty state; no secret values
uppr env set api OPTIONAL_KEY   # hidden terminal prompt; also accepts a value on stdin
uppr env unset api OPTIONAL_KEY # clear an optional value
```

`prepare` preserves existing values. `env set` and `env unset` validate against the schema and preserve unrelated entries. For scripts, pipe one value into `uppr env set`; avoid putting secrets directly on a shell command line. Restart the app to apply env changes. `uppr env list` does not print stored values.

## Operations

| Command | Use |
| --- | --- |
| `uppr list` | Show all apps, routes, states, paths, and next steps. |
| `uppr status [name]` | Show service, container, and health status. |
| `uppr logs <name> [--no-follow]` | Show the last 200 journal lines; follow by default. |
| `uppr curl api /health` | Request an app through its local port. Add normal curl options after the path. |
| `uppr restart <name>` | Rebuild and restart one app, then wait for readiness. |
| `uppr stop [name]` | Stop one app or all apps. |
| `uppr caddyfile` | Print generated routes without changing files. |
| `uppr generate` | Write local Caddyfile and systemd units for review. |
| `uppr install` | Install or refresh services and routes for the server's single launchpad. |
| `uppr deps caddy-switch` | Stop and disable a conflicting `caddy.service`; then run `uppr install`. |
| `uppr remove <name>` | Remove manifest entry, preserving checkout; run `uppr install` to remove its installed service and route. |
| `uppr uninstall` | Remove this launchpad's installed services and routes, preserving files. |
| `uppr doctor` | Check host tools and app preparation. |

For example, `uppr curl api /items -X POST -H 'Content-Type: application/json' -d '{"name":"example"}'` passes the options to curl and streams its response. Uppr prints the curl command before running it.

`uppr auth login|status|logout` manages a GitHub personal access token for HTTPS Git remotes under `~/.config/uppr/credentials.json`. SSH remotes use your SSH key. `uppr push api -m "Update routes"` shows local changes, asks before committing and pushing, and rejects tracked `config/` or `data/` files. Add `--yes` for an already reviewed noninteractive push.

`uppr backup /safe/location/pad.tar.gz` stops active app services while archiving the launchpad, then restarts them. The archive includes app data and secrets; store it accordingly. `uppr restore /safe/location/pad.tar.gz new-pad` restores to a new directory. After restoring, run `uppr doctor`, `uppr generate`, and `uppr install` there. `uppr teach [directory]` writes or replaces an application integration guide named `UPPR.md`.

## Development

```sh
bun install
bun test
bun run typecheck
bun run build
```
