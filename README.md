# Uppr v2

Uppr is a CLI for deploying Git repositories as independent Docker applications on a Linux host. There is no Uppr web UI, HTTP API, or persistent Uppr control-plane daemon. Each installed app has a systemd service, and one shared Caddy service routes domains to the apps' loopback ports. The CLI generates and installs those services, then controls them through systemd.

## Build and host requirements

Build with Go 1.25 or newer:

```sh
go build -o uppr .
```

Put the binary at a stable, absolute path on the Linux host, such as `~/.local/bin/uppr`, and add it to `PATH`. Generated app units record the binary's absolute path and the account that ran `uppr generate` or `uppr install`. Run those commands as the account that should own and operate the app files, and run `uppr install` again after moving the binary or launchpad.

The host needs Git, Docker, Caddy, and systemd. The account that installs and runs the app units needs Docker access. `uppr deps check` reports missing tools; `uppr deps install` attempts to install Git, Docker, and Caddy with `apt-get` or `dnf` on Linux and starts Docker. Installation and service actions use `sudo` for privileged commands when needed. Caddy needs ports 80 and 443, so stop any other service using them before installing Uppr's shared Caddy service.

You can build, initialize launchpads, manage Git repositories, prepare environments, generate files, and restore backups on other operating systems. Installing and controlling services requires Linux and systemd.

## Deploy a launchpad

```sh
uppr init ./my-stack
cd ./my-stack
uppr add https://github.com/example/api.git --domain api.example.com --container-port 3000
uppr add https://github.com/example/site.git --domain example.com --container-port 8080
uppr pull
uppr prepare
uppr doctor
uppr install
uppr serve
```

Replace the example repository URLs and domains with your own.

Run launchpad commands from its directory or a child directory. `uppr init` creates `launchpad.json`, `repos/`, `systemd/`, a Caddyfile, and a `.gitignore`. `uppr add` records a repository in the manifest; `uppr pull` clones it into `repos/<name>/` or fast-forwards an existing checkout. Each app repository must have a root `Dockerfile` and `schema.json` before `uppr prepare` or `uppr install` can succeed. See [Application contract](#application-contract).

`uppr prepare` creates `config/.env` and `data/main.sqlite` in each app checkout and prompts for required schema variables when run interactively. It preserves existing environment values. `uppr doctor` checks Git, Docker, Caddy, Docker daemon access, and each app's preparation state; its preparation check may create local files. It does not install services.

`uppr install` generates local app units and a Caddyfile, validates the combined routes, installs the units, and starts or reloads the shared Caddy service. It **enables but does not start the app units**. `uppr serve` builds the app images, starts or restarts their units as needed, and waits for the containers to be running or healthy. DNS for public domains must point to the host. You can run `uppr generate` separately to inspect the generated `systemd/` files and Caddyfile before installation; `uppr install` also generates them.

`uppr auth login` is optional for public repositories and SSH remotes. For private GitHub HTTPS repositories, run it before `uppr pull` and enter a personal access token. The token is saved at `~/.config/uppr/credentials.json` with mode `0600`, outside the launchpad and its backups. Git receives it through a temporary askpass helper and environment variable. Use `uppr auth status` or `uppr auth logout` to inspect or remove it.

## Manage apps

```sh
uppr list
uppr pull api
uppr prepare api
uppr restart api
uppr status api
uppr logs api
uppr stop api
```

`restart` builds the image and checks the app environment before restarting that app's unit. `logs` follows the last 200 journal entries. `uppr serve [launchpad] [name]` can start a whole launchpad, including from another directory, or a named app. For example, `uppr serve ./my-stack` starts all its apps and `uppr serve ./my-stack api` starts only `api`.

`uppr push api -m "message"` shows pending changes and requires an interactive confirmation before staging, committing, and pushing. Uppr refuses to push an app if Git already tracks files under `config/` or `data/`.

`uppr remove api` removes the app from `launchpad.json` but leaves its repository files. Run `uppr install` afterward to remove its installed unit and Caddy route. `uppr uninstall` removes all units and routes for the current launchpad while retaining its files.

`uppr add` assigns a host loopback port starting at 20000 and defaults the container port to 3000. Use `--port` and `--container-port` to set them explicitly; the container port must match the app's listening port. `--name` and `--branch` are also available. Host ports and domains must be unique across installed launchpads; `uppr install` checks for conflicts before changing host configuration. The default domain is `<name>.localhost`.

## Application contract

An app repository needs a root `Dockerfile` and a root `schema.json`. The Docker process must listen on `0.0.0.0` at the configured container port and remain in the foreground. A minimal schema is:

```json
{
  "variables": [
    {
      "name": "API_KEY",
      "description": "Token for the upstream API",
      "example": "Get this from the provider dashboard",
      "required": true
    }
  ]
}
```

`variables` may be empty. A variable is required unless `"required": false` is specified. `uppr prepare` prompts for missing required values; `uppr install` and app starts reject missing required values without prompting. Uppr adds `config/` and `data/` to the app's `.gitignore` and `.dockerignore`, creates `config/.env` with mode `0600`, and bind mounts those directories at `/app/config` and `/app/data`. It passes the environment file to Docker with `--env-file`. Store durable data under `/app/data`.

Run `uppr dump` in an app repository to write an `UPPR.md` integration guide there, or `uppr dump <directory>` to choose another location.

## Launchpad files and backups

```text
my-stack/
  launchpad.json
  Caddyfile
  systemd/
  repos/
    api/
      Dockerfile
      schema.json
      config/.env
      data/main.sqlite
```

The launchpad `.gitignore` excludes `repos/`. The manifest, Caddyfile, and generated unit copies can be committed, but generated units contain absolute paths and must be regenerated after moving the launchpad. A copied launchpad retains its ID, so installing the copy on the same host replaces the installed path for that ID. Create a separate launchpad with `uppr init` for an independently running deployment.

```sh
uppr backup ../my-stack.tar.gz
uppr restore ../my-stack.tar.gz ../restored-stack
cd ../restored-stack
uppr doctor
uppr install
uppr serve
```

The backup archive must be outside the launchpad. It contains the entire launchpad, including Git checkouts, app configuration, data, and secrets; protect it accordingly. On Linux, backup stops active app units during the archive and starts them again afterward. On other systems, stop apps yourself before backing up databases. Restore requires a destination that does not exist and leaves host installation, DNS, and dependencies to the destination machine.
