# Uppr

Uppr is a Bun and Ink terminal app for running independent Docker applications on a Linux host. Each app has its own systemd service; one shared Caddy service routes its domain to its loopback port. Existing Uppr v2 `launchpad.json` files continue to work.

## Install the CLI

Bun must already be installed on the target host. From this repository:

```sh
make install
```

This builds a bundled Bun entry point at `~/.local/share/uppr/uppr.js` and installs `~/.local/bin/uppr`. Add `~/.local/bin` to `PATH` if needed. Set `PREFIX=/some/path` to install elsewhere. Run `make install` again after updating Uppr, then run `uppr generate` and `uppr install` in each installed launchpad so systemd units use the installed path.

The Linux host also needs Git, Docker, Caddy, and systemd. `uppr deps check` reports missing tools. `uppr deps install` installs Git, Docker, and Caddy with apt or dnf. The account running Uppr needs Docker access; do not run `sudo uppr serve` because the app services run as the account that installed them. Uppr invokes `sudo` for host installation and service control when required.

## Get started

```sh
uppr init my-pad
cd my-pad
uppr add https://github.com/you/api.git --name api --domain api.example.com
uppr pull api
uppr schema generate api
uppr prepare api
uppr install
uppr serve
uppr list
```

Run `uppr` with no arguments for the Ink dashboard. Use the arrow keys to select an app, then `s` to serve, `r` to restart, `x` to stop, `l` for logs, `e` to edit its schema, or `g` to generate a schema. Press `q` to exit. `uppr list` prints a readable snapshot showing each app's running state, route, ports, and checkout path, including when output is redirected.

Run launchpad commands from the launchpad or any child directory. `uppr add` records a repository; `uppr pull` clones or fast forwards it into `repos/<name>`. `uppr install` generates app units and Caddy routes, enables app units, and starts or reloads Caddy. `uppr serve` builds images, starts or restarts app services, and waits for them to become ready. DNS for public domains must point to the host.

## Application contract and schema

Each repository needs a root `Dockerfile`. The container process must listen on `0.0.0.0` at the configured container port and remain in the foreground. It should write durable data under `/app/data`. Uppr mounts the checkout's `config/` and `data/` directories at `/app/config` and `/app/data` and passes `config/.env` to Docker.

A root `schema.json` declares environment variables:

```json
{
  "variables": [
    {"name": "API_KEY", "description": "Upstream token", "required": true}
  ]
}
```

A variable is required unless `"required": false` is set. To create or change a schema:

```sh
uppr schema generate api               # reads .env.example, .env.sample, or .env.template
uppr schema generate api --from-env FILE
uppr schema edit api                   # opens $VISUAL, $EDITOR, or vi; validates before saving
uppr schema add api NEW_KEY --description "What this is for" --optional
uppr schema remove api OLD_KEY
uppr schema show api
```

Generation creates an empty valid schema if no example file exists. It never copies example values into the schema. Add `--force` to replace an existing schema. `uppr prepare` prompts for missing required values and creates `config/.env` with mode `0600` and `data/main.sqlite`. Existing values are preserved. Installation and app starts validate required values without prompting. Uppr adds `config/` and `data/` to `.gitignore` and `.dockerignore`.

## Other commands

- `uppr status [name]`, `uppr logs <name>`, `uppr stop [name]`, `uppr restart <name>` inspect and control apps.
- `uppr generate` writes local systemd units and a Caddyfile for review.
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
