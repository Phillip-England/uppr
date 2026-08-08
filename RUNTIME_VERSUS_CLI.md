# Runtime Versus CLI

Uppr can be used with the CLI source code separated from runtime
configuration. The Go project does not need to be the same directory that holds
`config/.env`, `repos.conf`, `workspaces.conf`, `Caddyfile`, or generated
Docker Compose files.

The intended model is:

- The Uppr repository is source code for the `uppr` executable.
- A runtime directory is an initialized Uppr environment.
- Each runtime directory can have its own credentials, workspaces, repos,
  generated Caddy config, generated Compose file, and persistent data.

## Install the CLI from the source repo

From the cloned Uppr source repository:

```sh
go install .
```

That installs the `uppr` binary into your Go binary directory, usually
`$GOBIN` or `$GOPATH/bin`. Make sure that directory is on `PATH`.

After installation, you no longer need to run Uppr from inside the source repo.

## Create a runtime directory outside the source repo

From anywhere else:

```sh
uppr init ./production
cd ./production
```

That directory is now the runtime root. It contains private and environment
specific files such as:

```text
config/.env
repos.conf
data/
```

Commands like these should be run from that runtime directory:

```sh
uppr add https://github.com/example/app
uppr pull
uppr generate
uppr launch .
uppr web .
uppr serve .
```

Most project commands find the runtime root by walking upward from the current
directory until they find both `config/.env` and `repos.conf`. This is why you
can `cd` into the runtime directory, or into a nested app directory under it,
and still have commands operate on that runtime environment.

## Server runtime directories

For the authenticated server/control-plane mode, the runtime root also contains:

```text
workspaces.conf
Caddyfile
docker-compose.yml
Makefile
data/main.sqlite
```

Those files are runtime artifacts. Some are generated from `config/.env`,
`workspaces.conf`, and workspace `repos.conf` files. They can exist in a runtime
directory without being part of the CLI source repository.

For example:

```sh
mkdir -p ~/uppr-runtimes/prod
uppr init ~/uppr-runtimes/prod
cd ~/uppr-runtimes/prod
uppr serve --addr 0.0.0.0:9944 .
```

## What should stay in the source repo

The source repo should contain the Go code, tests, documentation, and files
needed to build or distribute the `uppr` executable.

Examples:

```text
*.go
go.mod
go.sum
README.md
ENV_SCHEMA_JSON.md
Dockerfile
reload.sh
```

The root `Dockerfile` is source when it describes how to package or run the
Uppr executable itself. That is different from generated runtime files such as
`Caddyfile` and `docker-compose.yml`.

## What should not be committed as source

Runtime-specific files should not be committed to the shared Uppr source repo:

```text
config/.env
data/
repos.conf
workspaces.conf
Caddyfile
caddyx.Dockerfile
docker-compose.yml
Makefile
uppr-caddy.service
```

Some of these may be generated from runtime state. Others may contain private
paths, domains, credentials, service details, or machine-specific deployment
choices.

If they are already tracked in Git, adding them to `.gitignore` is not enough.
Git will keep tracking files that were already committed. Remove them from the
index while leaving local copies in place:

```sh
git rm --cached Caddyfile caddyx.Dockerfile docker-compose.yml Makefile repos.conf workspaces.conf uppr-caddy.service
git commit -m "Remove runtime artifacts from source"
```

Keep local runtime copies in a separate initialized runtime directory instead.

## Recommended workflow

Use one clone for development:

```sh
git clone <uppr-repo-url> uppr
cd uppr
go install .
```

Use separate directories for real environments:

```sh
cd ..
uppr init ./uppr-prod
cd ./uppr-prod
uppr add https://github.com/example/app
uppr pull
uppr launch .
```

You can create multiple runtime directories from the same installed CLI:

```text
uppr-prod/
uppr-staging/
uppr-client-a/
uppr-client-b/
```

Each one can have different repos, domains, secrets, workspaces, and generated
deployment files while sharing the same `uppr` executable.

## Migrating a runtime directory

You can move a runtime environment without moving the Uppr source repository.

From the current runtime directory, create a bundle:

```sh
uppr backup ./uppr-state.tar.gz
```

Then initialize a new runtime directory and restore into it:

```sh
uppr init ./new-runtime
uppr restore ./uppr-state.tar.gz ./new-runtime
cd ./new-runtime
uppr launch .
```

The same flow is available from the web UI:

```sh
uppr init ./new-runtime
cd ./new-runtime
uppr web .
```

Open Backup, upload the `uppr-state.tar.gz` archive, then open Launch to
rebuild and start the restored environment.

For server roots, the backup includes `workspaces.conf` and registered
workspace directories. On restore, workspace paths are rewritten under
the destination runtime's `data/workspaces/` directory. The restored
`UPPR_WORKSPACES_DIR` is rewritten as well, so it cannot continue pointing at
the old machine or old runtime directory.

## Bottom line

The runtime configuration can be decoupled from the CLI codebase. You do not
need a new command model for this. The existing `uppr init [path]` behavior
already supports:

```sh
git clone <uppr-repo-url> uppr
cd uppr
go install .
cd ..
uppr init ./some-runtime
cd ./some-runtime
```

At that point, `./some-runtime` is the environment-specific runtime directory,
and the cloned `uppr` repository can remain a clean source code directory.
