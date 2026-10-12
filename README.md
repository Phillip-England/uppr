# Uppr

Uppr is a Bun and Ink terminal app for running independent Docker applications on a Linux host. Each app has its own systemd service; one shared Caddy service routes its domain to its loopback port. Existing Uppr v2 `launchpad.json` files continue to work.

## Install the CLI

Bun must already be installed on the target host. From this repository:

```sh
make install
```

This builds a bundled Bun entry point at `~/.local/share/uppr/uppr.js` and installs `~/.local/bin/uppr`. The launcher records Bun's absolute path so systemd does not need Bun on its `PATH`. Add `~/.local/bin` to `PATH` if needed. Set `PREFIX=/some/path` to install elsewhere. Run `make install` again after updating Uppr, then run `uppr install` in each installed launchpad to refresh its systemd units.

The Linux host also needs Git, Docker, Caddy, Go or xcaddy, and systemd. `uppr deps check` reports missing tools and the Caddy rate-limit module. `uppr deps install` installs Git, Docker, Caddy, and Go with apt or dnf, then builds a Caddy binary with the rate-limit module. Run `uppr deps caddy` to rebuild just that binary. The account running Uppr needs Docker access; run `uppr launch` as that account because the app services run as the account that installed them. Uppr invokes `sudo` for host installation and service control when required.

## Get started

```sh
uppr init my-pad
cd my-pad
uppr
```

You can also run `uppr` in a folder without a launchpad to create one through the TUI. Press `a` to add a repository. The dashboard shows each app's next setup step; select an app and press Enter to follow it through pulling, schema generation, environment setup, installation, and serving. Commands return to the dashboard so you can see the new state. On a non-Linux machine, you can prepare the app; installation and serving require the Linux host.

The same flow is available through commands:

```sh
uppr add https://github.com/you/api.git --name api --domain api.example.com
uppr pull api
uppr schema generate api
uppr prepare api
uppr install
uppr launch api
uppr list
```

In the dashboard, `p` pulls the selected repo, `P` pulls all repos, `s` launches the selected repo, and `A` launches all repos. `u` prompts for a request path and curls the selected repo; `v` shows the launchpad Caddyfile. `c` configures the environment, `i` installs services, `r` restarts, `x` stops, `l` shows recent logs, `e` edits the schema, and `g` generates a schema. Pulls, launches, and restarts run in the background. Press `o` to view their output, then `q` or Esc to return to the dashboard while the operation continues. The viewer keeps the last 200 output lines after completion until another operation replaces them. Background service operations require noninteractive sudo access if service control needs sudo. Press `?` for help or `q` to exit. The environment screen lists required and optional variables, descriptions, examples, and which values are already set. Select a variable and press Enter to edit it, then `s` to save. Existing values and unrelated entries are preserved. `uppr list` prints a readable snapshot showing each app's running state, route, ports, and checkout path, including when output is redirected.

Press `d` to edit the selected app's domains and rate limit in its repository `schema.json`. The dashboard marks local Git changes. Press `w` to review, commit, and push that repository; press `t` to save or remove a GitHub token for HTTPS remotes. SSH remotes continue to use your SSH key. Run `uppr install` after changing domains or rate limits.

Interactive commands show their current action while they run. Installation reports each validation and service phase, and curl shows when it is waiting for a response. After a curl completes, press any key to return to the dashboard. The curl path prompt accepts Enter for `/` or Esc to cancel.

Run launchpad commands from the launchpad or any child directory. `uppr add` records a repository; `uppr pull <name>` clones or fast forwards one checkout into `repos/<name>`, and `uppr pull --all` updates every checkout. Pulling never starts an app. `uppr install` generates app units and Caddy routes, enables app units, and starts or reloads Caddy. `uppr launch <name>` builds and starts one app from its current checkout; `uppr launch --all` does this for every repo. Launching never pulls from Git. `uppr serve` remains available as an alias for the existing launch behavior. DNS for public domains must point to the host.

Use `uppr curl api /health` to call an app through its local port, or pass normal curl options after the path, such as `uppr curl api /items -X POST -H 'Content-Type: application/json' -d '{"name":"example"}'`. Uppr prints the full curl command before streaming headers and response output. Use `uppr caddyfile` to print the Caddyfile generated from the current launchpad manifest and app schemas without changing files. `uppr generate` writes that file to disk.

## Application contract and schema

Each repository needs a root `Dockerfile`. The container process must listen on `0.0.0.0` at the configured container port and remain in the foreground. It should write durable data under `/app/data`. Uppr mounts the checkout's `config/` and `data/` directories at `/app/config` and `/app/data` and passes `config/.env` to Docker.

A root `schema.json` declares environment variables, domains, and a per-client rate limit:

```json
{
  "domains": ["api.example.com"],
  "rate_limit": { "events": 100, "window": "1m", "zone": "dynamic" },
  "variables": [
    { "name": "API_KEY", "description": "Upstream token", "required": true }
  ]
}
```

A variable is required unless `"required": false` is set. Schema domains take precedence over the launchpad manifest domain; older schemas without `domains` keep using that manifest domain. Each domain gets a Caddy route to the app's host port. The rate limit defaults to 100 requests per client IP in a one-minute window, in zone `dynamic`, even when `rate_limit` is absent. Uppr prefixes the generated zone name with the launchpad and app IDs so limits stay separate between apps. The custom Caddy binary is installed at `/usr/local/bin/uppr-caddy`; Uppr checks for its module before installing routes. To create or change a schema:

If the app declares `PORT`, Uppr uses its value from `config/.env` as the container's listening port. When `PORT` is unset, its numeric schema `example` is used. A declared `PORT` needs one of these values. For example, `{"name":"PORT","example":"8839","required":false}` publishes the container's port 8839 unless `config/.env` overrides it. Caddy still proxies to the host port assigned by `uppr add`; Docker maps that host port to the app port. Apps without a `PORT` schema variable use the `--container-port` value from `uppr add` (default 3000). After changing `PORT`, run `uppr install` to refresh a running container's port mapping.

```sh
uppr schema generate api               # reads .env.example, .env.sample, or .env.template
uppr schema generate api --from-env FILE
uppr schema edit api                   # opens $VISUAL, $EDITOR, or vi; validates before saving
uppr schema domains api api.example.com,alt.example.com
uppr schema rate api --events 250 --window 1m --zone dynamic
uppr schema add api NEW_KEY --description "What this is for" --optional
uppr schema remove api OLD_KEY
uppr schema show api
```

Generation creates an empty valid schema if no example file exists. It never copies example values into the schema. Add `--force` to replace an existing schema. `uppr prepare` prompts for missing required values and creates `config/.env` with mode `0600` and `data/main.sqlite`. Existing values are preserved. Installation and app starts validate required values without prompting. Uppr adds `config/` and `data/` to `.gitignore` and `.dockerignore`.

## Other commands

- `uppr status [name]`, `uppr logs <name>`, `uppr stop [name]`, `uppr restart <name>` inspect and control apps.
- `uppr generate` writes local systemd units and a Caddyfile for review; `uppr caddyfile` prints the current generated content.
- `uppr remove <name>` removes an app from the manifest, preserving its checkout. Run `uppr install` to remove its service and route.
- `uppr uninstall` removes the launchpad's installed units and routes, preserving its files.
- `uppr auth login|status|logout` manages a GitHub token for HTTPS Git remotes under `~/.config/uppr/credentials.json`.
- `uppr push <name> -m <message>` asks before committing and pushing; tracked `config/` and `data/` files are rejected.
- `uppr backup <archive.tar.gz>` stops active app services while archiving a launchpad, then restarts them. The archive contains app data and secrets, so store it securely. `uppr restore <archive> <directory>` restores it to a new directory.
- `uppr doctor` checks host tools and each app's preparation. `uppr dump [directory]` writes an integration guide.

## Development

```sh
bun install
bun test
bun run typecheck
bun run build
```
