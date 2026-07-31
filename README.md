# uppr

`uppr` keeps a list of git repositories up to date.

## Setup

```sh
go build -o uppr .
./uppr init .
```

Fill in `config/.env`:

```env
GITHUB_USERNAME=your-username
GITHUB_PASSWORD=your-token-or-password
```

Then add repos with the CLI:

```sh
./uppr add https://github.com/phillip-england/some-repo
```

Optional repo settings can be passed when adding:

```sh
./uppr add https://github.com/phillip-england/some-repo --name some-repo --path apps/some-repo --branch main
```

You can also edit `repos.conf` directly:

```ini
[repo]
url = https://github.com/phillip-england/some-repo
```

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
```

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
