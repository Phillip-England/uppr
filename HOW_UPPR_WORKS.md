# How Uppr Works

This document describes Uppr's operating model in plain English. It is the
reference for changes involving runtime directories, systemd, backup, restore,
or migration.

## The source checkout and a runtime directory are different things

The Git checkout contains the Go source used to build the `uppr` command. A
runtime directory contains one particular installation's configuration and
data. They do not need to be the same directory.

A normal setup looks like this:

```sh
git clone https://github.com/phillip-england/uppr
cd uppr
go install .
cd ..
uppr init ./my-server
cd ./my-server
```

After `go install`, the installed `uppr` executable can manage any runtime
directory. The source checkout is not the server's home and can be updated or
removed independently.

## The current directory is the server identity

When run without a path, directory-oriented commands use the current directory.
For example, running this inside `my-server`:

```sh
uppr service
```

installs and restarts the Uppr and Caddy systemd units with `my-server`'s
absolute path. That path is used for systemd's working directory, Uppr's
`config/.env`, generated files, persistent data, and the workspace registry.
`uppr service ./somewhere` does the same thing explicitly for `./somewhere`.

Running `uppr service` again from a different runtime directory rewires both
systemd services to the new directory. Only one pair of system-wide
`uppr.service` and `caddy.service` units is installed, so the most recent
successful `uppr service` selects the active runtime.

The generated application Compose file uses the stable project name `uppr`.
Docker Compose therefore continues managing the same containers when the
runtime directory is moved instead of creating a directory-named second stack
that collides with the existing stack's published ports.

The service command also records the absolute path of the installed `uppr`
executable. If that executable is later moved or reinstalled at a different
path, run `uppr service` again.

## What state belongs to a runtime

The server runtime owns:

- `config/.env`: server credentials, domains, and service settings.
- `data/main.sqlite`: Uppr's own persistent database.
- `workspaces.conf`: the list of managed workspaces.
- `data/workspaces/`: workspace directories when installed through
  `uppr service` or restored from a server backup.
- Generated `Caddyfile`, `docker-compose.yml`, and `Makefile` files.

Each workspace owns its `repos.conf` and checked-out applications. Each
application keeps private environment values in `config/.env` and durable data,
including SQLite databases, under `data/`. Generated Compose files mount those
two directories into the app container, so replacing a container does not
replace its configuration or database.

## What backup and restore move

`uppr backup` creates one archive containing the server runtime and every
registered workspace, including each application's `config/` and `data/`
directories. App `.env` files and SQLite databases therefore travel in the
archive; users do not have to enter them again after a restore.

Because a live SQLite database can change while it is being copied, stop or
quiesce applications that are writing data before downloading the backup.

Restoring a server archive into a new runtime rewrites machine-specific
workspace paths. Workspaces are placed under the new runtime's
`data/workspaces/` directory, `workspaces.conf` is rewritten to those paths,
and `UPPR_WORKSPACES_DIR` in the restored `config/.env` is updated. The restored
runtime therefore has no dependency on the old runtime directory.

Restore overwrites files present in the archive but leaves unrelated files in
the destination alone. Treat backup archives as secrets because they contain
credentials and application data.

## Migration workflow

For a same-machine migration, initialize the destination and run the migration
from the shell. For consistent databases, quiesce writing apps first.

```sh
uppr init ../new-server
uppr migrate . ../new-server
```

Run the command as the account that should own and operate the destination.
If its parent directory requires administrator access, use
`sudo uppr migrate . ../new-server`, then assign the result to the service
account before operating it without sudo:

```sh
sudo chown -R -- uppr-user:uppr-user ../new-server
```

Replace `uppr-user:uppr-user` with the intended user and group. `chown -R`
changes the directory and every child. The source runtime remains unchanged.

For a migration to another machine, open the old runtime's authenticated web
portal Backup page and download `uppr-state.tar.gz`.

On the destination machine:

```sh
uppr init ./new-server
uppr web ./new-server
```

The local-only portal opens without requiring temporary server credentials.
Upload the archive on its Backup page. The upload restores server
configuration, workspaces, per-app `.env` files, and per-app persistent data
into `new-server`.

For a headless remote restore, configure temporary `ADMIN_USERNAME` and
`ADMIN_PASSWORD` values in the new runtime's `config/.env`, run
`uppr serve ./new-server`, and upload through that authenticated portal. The
restored server configuration replaces those temporary values.

Stop the temporary foreground `uppr web` or `uppr serve` process, then activate
the restored directory:

```sh
cd ./new-server
uppr service
```

That final command regenerates what is needed, points systemd and Caddy at the
new absolute directory, enables both services at boot, and restarts them. For a
same-machine move, retain the old runtime as a rollback copy until the new
services and applications have been verified.

Network-level details do not travel automatically: DNS must point at the new
machine, host firewall rules must permit the intended ports, Docker and Caddyx
must be installed, and the service account must be able to use Docker.

## The invariant to preserve

A runtime directory plus its Uppr backup is a portable deployment unit. After
restore and `uppr service` from the destination runtime, neither systemd,
workspace paths, app environment files, nor app databases should depend on the
source runtime directory.
