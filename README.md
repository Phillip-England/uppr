# uppr

`uppr` manages workspaces of git repositories and generates Docker/Caddy launch
files for them.

For the plain-English mental model—including runtime directories, systemd,
state ownership, and the complete migration flow—read
[`HOW_UPPR_WORKS.md`](HOW_UPPR_WORKS.md).

## Setup

```sh
go build -o uppr .
./uppr init .
```

Fill in `config/.env`:

```env
GITHUB_USERNAME=your-username
GITHUB_PASSWORD=your-token-or-password
ADMIN_USERNAME=your-admin-username
ADMIN_PASSWORD=use-a-strong-password
SESSION_SECRET=use-a-long-random-secret
ADDR=:9944
UPPR_DOMAINS=uppr.example.com,www.uppr.example.com
UPPR_DOCKER_IMAGE=uppr:local
UPPR_DOCKER_NETWORK=uppr-network
UPPR_RATE_LIMIT_ENABLED=true
UPPR_RATE_LIMIT_ZONE=uppr
UPPR_RATE_LIMIT_EVENTS=100
UPPR_RATE_LIMIT_WINDOW=1m
```

You can also set these from the web UI by opening `./uppr web .` and using the
Credentials page.

Then add repos with the CLI:

```sh
./uppr add https://github.com/phillip-england/some-repo
```

Or open a local web UI for the project:

```sh
./uppr .
./uppr web .
```

The local web UI listens on `http://127.0.0.1:9944`.

From the web UI, open a repository to edit its config, run git actions, and use
the embedded browser shell in that repository's configured path.

For server deployment, run the authenticated web UI from the Uppr installation
root:

```sh
./uppr serve --addr 0.0.0.0:9944 .
```

On first login, create a workspace from the Workspaces page. Uppr stores the
workspace registry in the server root, but workspace directories are created in
a system data location, not under the current project directory:

```text
macOS:   ~/Library/Application Support/uppr/workspaces/<workspace-name>/
Linux:   ${XDG_DATA_HOME:-~/.local/share}/uppr/workspaces/<workspace-name>/
Windows: %LOCALAPPDATA%\uppr\workspaces\<workspace-name>\
```

Set `UPPR_WORKSPACES_DIR` before launching `uppr serve` to choose a deployment
specific workspace storage directory.

Each workspace has its own `repos.conf` and `config/.env`. The server root has
`workspaces.conf` plus the generated root `Caddyfile`, `docker-compose.yml`,
and `Makefile` used to launch all registered workspaces.

`uppr serve` keeps this app's own runtime files in the same layout used by the
generated apps:

- `./config/.env` stores GitHub credentials plus `ADMIN_USERNAME`,
  `ADMIN_PASSWORD`, `SESSION_SECRET`, and `ADDR`.
- `./data/main.sqlite` stores bounded login failure tracking.

`uppr serve` and `uppr web` refuse to start when `config/.env` is missing, and
the authenticated server requires non-empty admin credentials and a session
secret. Login failures are
tracked by client IP for a 24 hour window and old rows are purged during login
checks, so the tracking table does not grow indefinitely.

The included `Dockerfile` exposes port `9944` and starts:

```sh
uppr serve --addr 0.0.0.0:${PORT} .
```

## Install the native control plane

Uppr and Caddy run as independent native services. Docker Compose contains only
the managed applications, so replacing the app stack cannot stop the UI or
reverse proxy that initiated the replacement.

Build the rate-limit-enabled Caddy binary, then generate, install, enable, and
start both units:

```sh
./uppr install-caddyx
./uppr service .
```

Run `uppr service [path]` whenever the service definitions or executable path
change. It updates both `/etc/systemd/system/uppr.service` and
`/etc/systemd/system/caddy.service`, reloads systemd, enables both units, and
restarts them. The more comprehensive `reload.sh` remains available when you
also want to repair ownership and pull every registered workspace first.

Run `reload.sh` as the ordinary account that installed and will operate Uppr;
do not run the whole script with `sudo`. It uses `sudo` only to repair ownership
of this installation, install the root-owned systemd unit files, and control the
services. Both services themselves run as that ordinary user, so generated
files remain editable without `sudo`. That user must have access to Docker
(normally through membership in the `docker` group).

`install-caddyx` writes `~/.local/bin/caddyx` by default; pass a destination
path to override it. `service-caddy` finds that location, searches `PATH`, or
uses `CADDYX_PATH`. The service commands create `config/.env`, `data/`, and
`workspaces.conf` when missing. Before installing the units, fill in
`ADMIN_USERNAME` and `ADMIN_PASSWORD` in `config/.env`; `SESSION_SECRET` is
generated automatically. Set `UPPR_DOMAINS` to one or more comma-separated
Caddy hostnames.

`reload.sh` pulls every repository in every registered workspace, regenerates
and validates the Caddy configuration, atomically rewrites `/etc/systemd/system/uppr.service` and
`/etc/systemd/system/caddy.service`, reloads systemd, enables both units for
startup, restarts them, and prints their resulting status. It also repairs
ownership under the Uppr installation directory and every path registered in
`workspaces.conf`, including workspaces stored outside the installation. This
fixes files left behind by older root-running units. Repository pulls use
`git pull --ff-only`; if any pull fails, reload stops before changing or
restarting the services.

If Docker itself reports `permission denied` for `/var/run/docker.sock`, add the
operator account to the Docker group, then log out and back in so the new group
is present in the login session:

```sh
sudo usermod -aG docker "$USER"
```

Do not run `uppr`, `uppr launch`, or the whole `reload.sh` with `sudo`; doing so
creates root-owned generated files and workspace contents. The narrow `sudo`
prompts inside `reload.sh` are expected because systemd unit installation and
ownership repair are privileged operations.

The Uppr unit regenerates the root runtime files, launches the managed app
containers, and then runs the current executable directly on port 9944. The
Caddy unit regenerates once more immediately before it runs `caddyx` directly
on ports 80 and 443, so boot never depends on stale generated files. Managed
app ports are published by Docker only on `127.0.0.1`, and the generated
Caddyfile proxies to those loopback ports. After changing or onboarding an app,
`uppr launch` (or Launch in the web UI) regenerates everything, safely replaces
every app container, and reloads Caddy while Uppr remains available. On a first
launch where Caddy is not running yet, Uppr starts it instead.

Uppr's public route is rate limited by client IP. The default permits 100
requests per minute; use `UPPR_RATE_LIMIT_ENABLED`, `UPPR_RATE_LIMIT_EVENTS`,
`UPPR_RATE_LIMIT_WINDOW`, and `UPPR_RATE_LIMIT_ZONE` in `config/.env` to change
or disable it.

Optional repo settings can be passed when adding:

```sh
./uppr add https://github.com/phillip-england/some-repo --name some-repo --path apps/some-repo --branch main
```

You can also edit `repos.conf` directly:

```ini
[repo]
url = https://github.com/phillip-england/some-repo
```

## Back up and restore a complete state

Export the current project (or server root) to one portable compressed artifact:

```sh
./uppr backup ./uppr-state.tar.gz
```

The artifact contains the app repositories themselves, including every app's
`config/` and `data/` directories, plus Uppr configuration and runtime data.
Against a server root it also includes workspaces stored outside that root. Stop
or quiesce applications that are actively writing databases before backing up.

Restore it on the same machine or copy it to another one:

```sh
./uppr restore ./uppr-state.tar.gz
```

An optional final path selects another root. Existing files with the same names
are overwritten; unrelated files are retained. Server workspaces are restored
beneath the destination runtime's `data/workspaces/` directory.
`UPPR_WORKSPACES_DIR` and `workspaces.conf` are rewritten to those portable
paths, so the restored server does not depend on the old runtime directory. Run
`uppr launch` after restoring to rebuild and start the applications, or run
`uppr service` there to make that runtime the systemd-managed server.

## Manage repos

```sh
./uppr list
./uppr remove some-repo
```

`uppr remove` accepts a repo name, URL, or configured path.

## Pull repos

```sh
./uppr pull
```

`uppr pull` looks for the nearest initialized uppr project by walking up from the
current directory. Repo paths in `repos.conf` are resolved relative to that
project directory, so different directories can each have their own `config/.env`,
`repos.conf`, and cloned repos.

For each repo, `uppr pull` clones it if `path` does not contain a git repository, otherwise it runs:

```sh
git -C <path> pull --ff-only
```

If `path` is omitted, `uppr` derives the repo name from the URL and uses `apps/<repo-name>`.
For example, `https://github.com/phillip-england/some-repo` is cloned to `apps/some-repo`.

Optional repo settings:

```ini
[repo]
name = some-repo
url = https://github.com/phillip-england/some-repo
path = apps/some-repo
branch = main
port = 3000
container_port = 3000
domain = some-repo.localhost
domain = www.some-repo.localhost
rate_limit_enabled = true
rate_limit_zone = dynamic
rate_limit_events = 100
rate_limit_window = 1m
env = NODE_ENV=production
env = API_BASE_URL=https://api.example.test
volume = ./cache/some-repo:/app/cache
```

Runtime settings:

- `port`: host port exposed by Docker Compose.
- `container_port`: app port inside the container. If omitted, `uppr` uses the
  first `EXPOSE` port from the app's `Dockerfile` when available, otherwise
  `port` is used.
- `domain`: repeatable Caddy hostname. If omitted, `uppr` uses `<name>.localhost`.
- `rate_limit_enabled`: enables app-level Caddy rate limiting. Defaults to `true`
  for every newly added application; set it to `false` to explicitly opt out.
- `rate_limit_zone`: Caddy rate-limit zone name. Defaults to `dynamic`.
- `rate_limit_events`: allowed events per window. Defaults to `100`.
- `rate_limit_window`: rate-limit window such as `1m` or `30s`. Defaults to `1m`.
- `env`: repeated `KEY=value` entries for container environment.
- `volume`: repeated Docker volume entries such as `./cache:/app/cache`.

## Generate runtime files

Each configured repo is assumed to have a `Dockerfile` at its configured `path`.
Generate the runtime files from `repos.conf` with:

```sh
./uppr generate
```

This writes:

- `Caddyfile`
- `caddyx.Dockerfile`
- `docker-compose.yml`
- `Makefile`

The generated `caddyx.Dockerfile` builds Caddy with
`github.com/mholt/caddy-ratelimit`, and the generated Docker Compose file uses
that image instead of stock Caddy so app-level rate limiting works at launch.

The generated Docker Compose file always mounts each app's local
`./apps/<app>/config` directory to `/app/config` and `./apps/<app>/data` to
`/app/data`, and uses `./apps/<app>/config/.env` as that service's env file.
Apps can rely on `./config/.env` and `./data/main.sqlite` being available when
their image uses `/app` as its working directory.

When an app Dockerfile includes a line such as `EXPOSE 3000`, `uppr` can infer
that app's port. `uppr pull` writes the discovered value to both `port` and
`container_port` when either is missing, and `uppr generate` also uses the
exposed port in memory for existing local checkouts.

App repos can also include a `schema.json` file at the repo root:

```json
{
  "variables": [
    {
      "name": "API_KEY",
      "description": "Secret token used to call the upstream API.",
      "example": "sk_live_...",
      "required": true
    },
    {
      "name": "DATABASE_URL",
      "description": "Postgres connection string used by the application.",
      "example": "postgres://user:password@db:5432/app"
    }
  ]
}
```

During pull/sync, `uppr` reads `schema.json` and prepares
`./apps/<app>/config/.env` with blank entries for any missing keys. Existing
values in that `.env` file are preserved, so the admin only fills in values and
does not need to remember each app's environment variable names. The web UI also
shows schema descriptions and examples while values are being filled in.
Legacy `env.schema` files with one variable name per line are still supported.
See `ENV_SCHEMA_JSON.md` for the schema contract to give other projects.

The generated `Makefile` includes:

```sh
make launch
make stop
make pull
make push m="common commit message"
```

At the server root, launch the whole system with:

```sh
./uppr launch .
```

`uppr launch` creates any missing server files, regenerates the root runtime
files from all registered workspaces, clears that Compose project's existing
containers and orphans (while preserving named volumes), runs
`docker compose up --build` from the server root, waits until every container is
running (and healthy when it has a healthcheck), and then validates and
force-reloads Caddy with the new configuration (or starts Caddy when it is not
running yet). A crashing container or failed Caddy refresh makes launch fail
instead of displaying a false success.

You can generate the root runtime files without launching Docker with:

```sh
./uppr generate-server .
```

The root Docker Compose file directly defines the Caddy service and every app
service from every workspace. Workspaces do not need their own compose files for
server launch.

## Push repos

```sh
./uppr push "common commit message"
./uppr push -m "common commit message"
```

`uppr push` stages all changes in each configured repo, commits repos that have
staged changes with the shared message, and then runs:

```sh
git -C <path> push
```
