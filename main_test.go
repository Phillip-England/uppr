package main

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestRunDefaultsToServe(t *testing.T) {
	called := false
	err := runWithServe(nil, func(args []string) error {
		called = true
		if len(args) != 0 {
			t.Fatalf("serve args = %q, want none", args)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("serve was not called")
	}
}

func TestRunServeCommandUsesServe(t *testing.T) {
	want := []string{"--addr", "127.0.0.1:8080", "/srv/uppr"}
	err := runWithServe(append([]string{"serve"}, want...), func(args []string) error {
		if !reflect.DeepEqual(args, want) {
			t.Fatalf("serve args = %q, want %q", args, want)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestInitProjectCreatesFilesInTargetDirectory(t *testing.T) {
	dir := t.TempDir()

	if err := initProject([]string{dir}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dir, envFile)); err != nil {
		t.Fatalf("expected env file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, reposFile)); err != nil {
		t.Fatalf("expected repos file: %v", err)
	}
}

func TestFindProjectRootWalksUpFromNestedDirectory(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "apps", "service")
	if err := os.MkdirAll(filepath.Join(root, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, envFile), []byte("GITHUB_USERNAME=\nGITHUB_PASSWORD=\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, reposFile), []byte("[repo]\nurl = https://github.com/acme/service\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := findProjectRoot(nested)
	if err != nil {
		t.Fatal(err)
	}
	if got != root {
		t.Fatalf("root = %q, want %q", got, root)
	}
}

func TestResolveRepoPathsUsesProjectRoot(t *testing.T) {
	root := t.TempDir()
	absolute := filepath.Join(root, "elsewhere", "api")
	repos := []Repo{
		{Name: "service", Path: filepath.Join("apps", "service")},
		{Name: "api", Path: absolute},
	}

	resolveRepoPaths(root, repos)

	if repos[0].Path != filepath.Join(root, "apps", "service") {
		t.Fatalf("relative path = %q", repos[0].Path)
	}
	if repos[1].Path != absolute {
		t.Fatalf("absolute path = %q", repos[1].Path)
	}
}

func TestReadDotEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("GITHUB_USERNAME=octo\nGITHUB_PASSWORD='token value'\n# ignored\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	values, err := readDotEnv(path)
	if err != nil {
		t.Fatal(err)
	}
	if values["GITHUB_USERNAME"] != "octo" {
		t.Fatalf("username = %q", values["GITHUB_USERNAME"])
	}
	if values["GITHUB_PASSWORD"] != "token value" {
		t.Fatalf("password = %q", values["GITHUB_PASSWORD"])
	}
}

func TestEnsureEnvDefaultsMigratesLegacyServeAddr(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, envFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("ADMIN_USERNAME=admin\nADMIN_PASSWORD=secret\nSESSION_SECRET=secret\nADDR=:8787\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := ensureEnvDefaults(path); err != nil {
		t.Fatal(err)
	}

	values, err := readDotEnv(path)
	if err != nil {
		t.Fatal(err)
	}
	if values["ADDR"] != defaultAddr {
		t.Fatalf("ADDR = %q, want %q", values["ADDR"], defaultAddr)
	}
}

func TestEnsureServerFilesRequiresEnvFile(t *testing.T) {
	root := t.TempDir()

	err := ensureServerFiles(root)
	if err == nil || !strings.Contains(err.Error(), filepath.Join(root, envFile)) {
		t.Fatalf("error = %v, want missing %s error", err, envFile)
	}
	if _, statErr := os.Stat(filepath.Join(root, envFile)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("ensureServerFiles created %s; stat error = %v", envFile, statErr)
	}
}

func TestEnsureProjectFilesRequiresEnvFile(t *testing.T) {
	root := t.TempDir()

	err := ensureProjectFiles(root)
	if err == nil || !strings.Contains(err.Error(), filepath.Join(root, envFile)) {
		t.Fatalf("error = %v, want missing %s error", err, envFile)
	}
}

func ensureTestServerFiles(root string) error {
	if err := os.MkdirAll(filepath.Join(root, "config"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, envFile), defaultEnvContents(), 0o600); err != nil {
		return err
	}
	return ensureServerFiles(root)
}

func TestEnsureEnvDefaultsDoesNotCreateKnownAdminCredentials(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := ensureEnvDefaults(path); err != nil {
		t.Fatal(err)
	}
	values, err := readDotEnv(path)
	if err != nil {
		t.Fatal(err)
	}
	if values["ADMIN_USERNAME"] != "" || values["ADMIN_PASSWORD"] != "" {
		t.Fatalf("admin defaults must be blank: username=%q password=%q", values["ADMIN_USERNAME"], values["ADMIN_PASSWORD"])
	}
}

func TestReadReposDefaultsNameAndPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repos.conf")
	if err := os.WriteFile(path, []byte("[repo]\nurl = https://github.com/acme/service\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	repos, err := readRepos(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 {
		t.Fatalf("len(repos) = %d", len(repos))
	}
	if repos[0].Name != "service" {
		t.Fatalf("name = %q", repos[0].Name)
	}
	if repos[0].Path != filepath.Join("apps", "service") {
		t.Fatalf("path = %q", repos[0].Path)
	}
}

func TestReadReposSupportsOptionalSettings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repos.conf")
	contents := []byte(`[repo]
name = api
url = https://github.com/acme/service
path = services/api
branch = main
`)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}

	repos, err := readRepos(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 {
		t.Fatalf("len(repos) = %d", len(repos))
	}
	if repos[0].Name != "api" {
		t.Fatalf("name = %q", repos[0].Name)
	}
	if repos[0].Path != "services/api" {
		t.Fatalf("path = %q", repos[0].Path)
	}
	if repos[0].Branch != "main" {
		t.Fatalf("branch = %q", repos[0].Branch)
	}
}

func TestParseAddRepoArgs(t *testing.T) {
	repo, err := parseAddRepoArgs([]string{
		"https://github.com/acme/service",
		"--name", "api",
		"--path", "services/api",
		"--branch", "main",
	})
	if err != nil {
		t.Fatal(err)
	}
	if repo.URL != "https://github.com/acme/service" {
		t.Fatalf("url = %q", repo.URL)
	}
	if repo.Name != "api" {
		t.Fatalf("name = %q", repo.Name)
	}
	if repo.Path != "services/api" {
		t.Fatalf("path = %q", repo.Path)
	}
	if repo.Branch != "main" {
		t.Fatalf("branch = %q", repo.Branch)
	}
}

func TestAddRepoWritesReposConf(t *testing.T) {
	root := newTestProject(t)
	chdir(t, root)

	err := addRepo([]string{
		"https://github.com/acme/service",
		"--name", "api",
		"--path", "services/api",
		"--branch", "main",
	})
	if err != nil {
		t.Fatal(err)
	}

	repos, err := readRepos(filepath.Join(root, reposFile))
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 {
		t.Fatalf("len(repos) = %d", len(repos))
	}
	if !reflect.DeepEqual(repos[0], Repo{Name: "api", URL: "https://github.com/acme/service", Path: "services/api", Branch: "main", RateLimit: defaultRateLimit()}) {
		t.Fatalf("repo = %#v", repos[0])
	}
}

func TestReadReposSupportsRuntimeSettings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repos.conf")
	contents := []byte(`[repo]
name = api
url = https://github.com/acme/api
path = apps/api
port = 8080
container_port = 3000
domain = api.localhost
domain = www.api.localhost
rate_limit_enabled = true
rate_limit_zone = clients
rate_limit_events = 25
rate_limit_window = 30s
env = NODE_ENV=production
env = DATABASE_URL=postgres://db/app
volume = ./data/api:/app/data
`)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}

	repos, err := readRepos(path)
	if err != nil {
		t.Fatal(err)
	}
	got := repos[0]
	if got.Port != 8080 || got.ContainerPort != 3000 || got.Domain != "api.localhost" {
		t.Fatalf("runtime settings = %#v", got)
	}
	if !reflect.DeepEqual(got.Domains, []string{"api.localhost", "www.api.localhost"}) {
		t.Fatalf("domains = %#v", got.Domains)
	}
	if !reflect.DeepEqual(got.RateLimit, RateLimit{Enabled: true, Zone: "clients", Events: 25, Window: "30s"}) {
		t.Fatalf("rate limit = %#v", got.RateLimit)
	}
	if !reflect.DeepEqual(got.Env, []string{"NODE_ENV=production", "DATABASE_URL=postgres://db/app"}) {
		t.Fatalf("env = %#v", got.Env)
	}
	if !reflect.DeepEqual(got.Volumes, []string{"./data/api:/app/data"}) {
		t.Fatalf("volumes = %#v", got.Volumes)
	}
}

func TestGenerateProjectFilesAt(t *testing.T) {
	root := newTestProject(t)
	writeTestRepos(t, root, []Repo{{
		Name:          "api",
		URL:           "https://github.com/acme/api",
		Path:          "apps/api",
		Port:          8080,
		ContainerPort: 3000,
		Domain:        "api.localhost",
		Domains:       []string{"api.localhost", "www.api.localhost"},
		RateLimit:     RateLimit{Enabled: true, Zone: "clients", Events: 25, Window: "30s"},
		Env:           []string{"NODE_ENV=production"},
		Volumes:       []string{"./data/api:/app/data", "./cache/api:/app/cache"},
	}})

	if err := generateProjectFilesAt(root); err != nil {
		t.Fatal(err)
	}

	compose := readTestFile(t, filepath.Join(root, dockerComposeFile))
	caddy := readTestFile(t, filepath.Join(root, caddyFile))
	caddyDocker := readTestFile(t, filepath.Join(root, caddyDockerFile))
	makefile := readTestFile(t, filepath.Join(root, makeFile))
	for _, want := range []string{
		"    build:",
		"      dockerfile: caddyx.Dockerfile",
		"    image: uppr-caddyx:latest",
		"  api:",
		"      context: ./apps/api",
		"    env_file:",
		"      - ./apps/api/config/.env",
		"      - \"8080:3000\"",
		"      NODE_ENV: \"production\"",
		"      - \"./apps/api/config:/app/config\"",
		"      - \"./apps/api/data:/app/data\"",
		"      - \"./cache/api:/app/cache\"",
	} {
		if !strings.Contains(compose, want) {
			t.Fatalf("compose missing %q:\n%s", want, compose)
		}
	}
	if strings.Contains(compose, "\"./data/api:/app/data\"") {
		t.Fatalf("compose should protect /app/data from custom mounts:\n%s", compose)
	}
	for _, want := range []string{
		"api.localhost www.api.localhost {",
		"rate_limit {",
		"zone clients {",
		"events 25",
		"window 30s",
		"reverse_proxy api:3000",
	} {
		if !strings.Contains(caddy, want) {
			t.Fatalf("Caddyfile missing %q:\n%s", want, caddy)
		}
	}
	if !strings.Contains(caddyDocker, "github.com/mholt/caddy-ratelimit@latest") {
		t.Fatalf("unexpected caddyx Dockerfile:\n%s", caddyDocker)
	}
	if strings.Contains(caddy, "caddy:2-alpine") {
		t.Fatalf("unexpected Caddyfile:\n%s", caddy)
	}
	if !strings.Contains(makefile, "launch: generate") {
		t.Fatalf("unexpected Makefile:\n%s", makefile)
	}
}

func TestGenerateProjectFilesUsesDockerfileExposeForContainerPort(t *testing.T) {
	root := newTestProject(t)
	repoPath := filepath.Join(root, "apps", "api")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoPath, "Dockerfile"), []byte("FROM alpine\nEXPOSE 3000/tcp 9000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTestRepos(t, root, []Repo{{
		Name:   "api",
		URL:    "https://github.com/acme/api",
		Path:   "apps/api",
		Port:   8080,
		Domain: "api.localhost",
	}})

	if err := generateProjectFilesAt(root); err != nil {
		t.Fatal(err)
	}

	compose := readTestFile(t, filepath.Join(root, dockerComposeFile))
	caddy := readTestFile(t, filepath.Join(root, caddyFile))
	if !strings.Contains(compose, `      - "8080:3000"`) {
		t.Fatalf("compose should use exposed container port:\n%s", compose)
	}
	if !strings.Contains(caddy, "reverse_proxy api:3000") {
		t.Fatalf("Caddyfile should use exposed container port:\n%s", caddy)
	}
}

func TestConfigureRepoContainerPortFromDockerfileWritesMissingPort(t *testing.T) {
	root := newTestProject(t)
	repoPath := filepath.Join(root, "apps", "api")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoPath, "Dockerfile"), []byte("FROM alpine\n# ignored\nEXPOSE 4321 # app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTestRepos(t, root, []Repo{{
		Name: "api",
		URL:  "https://github.com/acme/api",
		Path: "apps/api",
	}})

	if err := configureRepoContainerPortFromDockerfile(root, 0); err != nil {
		t.Fatal(err)
	}

	repos, err := readRepos(filepath.Join(root, reposFile))
	if err != nil {
		t.Fatal(err)
	}
	if repos[0].ContainerPort != 4321 {
		t.Fatalf("container port = %d, want 4321", repos[0].ContainerPort)
	}
	if repos[0].Path != "apps/api" {
		t.Fatalf("path should remain relative, got %q", repos[0].Path)
	}
}

func TestReadDockerfileExposedPortIgnoresDynamicPort(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Dockerfile")
	if err := os.WriteFile(path, []byte("FROM alpine\nEXPOSE ${PORT}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	port, err := readDockerfileExposedPort(path)
	if err != nil {
		t.Fatal(err)
	}
	if port != 0 {
		t.Fatalf("port = %d, want 0", port)
	}
}

func TestPrepareRepoEnvCreatesConfigEnvFromSchema(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "apps", "api")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoPath, envSchemaFile), []byte("# required by api\nAPI_KEY\nDATABASE_URL\nAPI_KEY\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := prepareRepoEnv(Repo{Name: "api", Path: repoPath}); err != nil {
		t.Fatal(err)
	}

	env := readTestFile(t, filepath.Join(repoPath, envFile))
	if env != "API_KEY=\nDATABASE_URL=\n" {
		t.Fatalf("env = %q", env)
	}
	if info, err := os.Stat(filepath.Join(repoPath, "data")); err != nil || !info.IsDir() {
		t.Fatalf("expected data directory: %v", err)
	}
}

func TestPrepareRepoEnvPreservesExistingValues(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "apps", "api")
	if err := os.MkdirAll(filepath.Join(repoPath, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoPath, envSchemaFile), []byte("API_KEY\nDATABASE_URL\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	existing := "# local values\nAPI_KEY=secret\n"
	if err := os.WriteFile(filepath.Join(repoPath, envFile), []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := prepareRepoEnv(Repo{Name: "api", Path: repoPath}); err != nil {
		t.Fatal(err)
	}

	env := readTestFile(t, filepath.Join(repoPath, envFile))
	want := existing + "DATABASE_URL=\n"
	if env != want {
		t.Fatalf("env = %q, want %q", env, want)
	}
}

func TestReadEnvSchemaRejectsValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, envSchemaFile)
	if err := os.WriteFile(path, []byte("API_KEY=secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := readEnvSchema(path)
	if err == nil {
		t.Fatal("expected schema error")
	}
	if !strings.Contains(err.Error(), "without a value") {
		t.Fatalf("error = %v", err)
	}
}

func TestAddRepoRejectsDuplicateNameURLOrPath(t *testing.T) {
	existing := []Repo{{Name: "api", URL: "https://github.com/acme/api", Path: "apps/api"}}

	for _, tc := range []struct {
		name string
		repo Repo
	}{
		{name: "name", repo: Repo{Name: "api", URL: "https://github.com/acme/other", Path: "apps/other"}},
		{name: "url", repo: Repo{Name: "other", URL: "https://github.com/acme/api", Path: "apps/other"}},
		{name: "path", repo: Repo{Name: "other", URL: "https://github.com/acme/other", Path: "apps/api"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateNewRepo(tc.repo, existing); err == nil {
				t.Fatal("expected duplicate error")
			}
		})
	}
}

func TestRemoveRepoRemovesByIdentifier(t *testing.T) {
	root := newTestProject(t)
	chdir(t, root)
	writeTestRepos(t, root, []Repo{
		{Name: "api", URL: "https://github.com/acme/api", Path: "apps/api"},
		{Name: "web", URL: "https://github.com/acme/web", Path: "apps/web", Branch: "main"},
	})

	if err := removeRepo([]string{"apps/api"}); err != nil {
		t.Fatal(err)
	}

	repos, err := readRepos(filepath.Join(root, reposFile))
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 {
		t.Fatalf("len(repos) = %d", len(repos))
	}
	if repos[0].Name != "web" {
		t.Fatalf("remaining repo = %#v", repos[0])
	}
}

func TestParsePushArgs(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "positional", args: []string{"ship", "changes"}, want: "ship changes"},
		{name: "short message flag", args: []string{"-m", "ship changes"}, want: "ship changes"},
		{name: "long message flag", args: []string{"--message", "ship changes"}, want: "ship changes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parsePushArgs(tc.args)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("message = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParsePushArgsRejectsMissingMessage(t *testing.T) {
	if _, err := parsePushArgs(nil); err == nil {
		t.Fatal("expected missing message error")
	}
	if _, err := parsePushArgs([]string{"--message", "  "}); err == nil {
		t.Fatal("expected empty message error")
	}
}

func TestPushRepoCommitsAndPushesChanges(t *testing.T) {
	dir := t.TempDir()
	remote := filepath.Join(dir, "remote.git")
	seed := filepath.Join(dir, "seed")
	repoPath := filepath.Join(dir, "repo")

	runTestGit(t, dir, "init", "--bare", remote)
	runTestGit(t, dir, "init", "-b", "main", seed)
	configureTestGitUser(t, seed)
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("initial\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, seed, "add", "-A")
	runTestGit(t, seed, "commit", "-m", "initial")
	runTestGit(t, seed, "remote", "add", "origin", remote)
	runTestGit(t, seed, "push", "-u", "origin", "main")

	runTestGit(t, dir, "clone", remote, repoPath)
	configureTestGitUser(t, repoPath)
	if err := os.WriteFile(filepath.Join(repoPath, "feature.txt"), []byte("feature\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	repo := Repo{Name: "repo", Path: repoPath}
	if err := pushRepo(repo, "common message", ""); err != nil {
		t.Fatal(err)
	}

	got := runTestGitOutput(t, remote, "log", "--format=%s", "-1")
	if got != "common message" {
		t.Fatalf("remote subject = %q, want %q", got, "common message")
	}
}

func TestWebAddRepoRedirectsToReposPage(t *testing.T) {
	root := newTestProject(t)
	app := &webApp{root: root}

	form := url.Values{
		"github_owner": {"acme"},
		"github_repo":  {"api"},
		"branch":       {"main"},
	}
	response := postWebForm(t, app, "/repos", form)

	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusSeeOther)
	}
	if location := response.Header().Get("Location"); location != "/repos?message=repo+added" {
		t.Fatalf("location = %q", location)
	}
	repos, err := readRepos(filepath.Join(root, reposFile))
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 {
		t.Fatalf("repos = %#v", repos)
	}
	if repos[0].Name != "api" {
		t.Fatalf("name = %q", repos[0].Name)
	}
	if repos[0].URL != "https://github.com/acme/api" {
		t.Fatalf("url = %q", repos[0].URL)
	}
	if repos[0].Path != filepath.Join("apps", "api") {
		t.Fatalf("path = %q", repos[0].Path)
	}
}

func TestWebSaveRepoAllowsUpdatingOriginalRepo(t *testing.T) {
	root := newTestProject(t)
	writeTestRepos(t, root, []Repo{
		{Name: "api", URL: "https://github.com/acme/api", Path: "apps/api"},
		{Name: "web", URL: "https://github.com/acme/web", Path: "apps/web"},
	})
	app := &webApp{root: root}

	form := url.Values{
		"url":                {"https://github.com/acme/api"},
		"name":               {"api"},
		"path":               {"apps/api"},
		"branch":             {"main"},
		"port":               {"8080"},
		"container_port":     {"3000"},
		"domains":            {"api.localhost\nwww.api.localhost"},
		"rate_limit_enabled": {"true"},
		"rate_limit_zone":    {"clients"},
		"rate_limit_events":  {"50"},
		"rate_limit_window":  {"10s"},
		"env":                {"NODE_ENV=production\nDATABASE_URL=postgres://db/app"},
		"volumes":            {"./data/api:/app/data"},
	}
	response := postWebForm(t, app, "/repos/0/save", form)

	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusSeeOther)
	}
	repos, err := readRepos(filepath.Join(root, reposFile))
	if err != nil {
		t.Fatal(err)
	}
	if repos[0].Branch != "main" || repos[0].Port != 8080 || repos[0].ContainerPort != 3000 {
		t.Fatalf("updated repo = %#v", repos[0])
	}
	if !reflect.DeepEqual(repos[0].Domains, []string{"api.localhost", "www.api.localhost"}) {
		t.Fatalf("domains = %#v", repos[0].Domains)
	}
	if !reflect.DeepEqual(repos[0].RateLimit, RateLimit{Enabled: true, Zone: "clients", Events: 50, Window: "10s"}) {
		t.Fatalf("rate limit = %#v", repos[0].RateLimit)
	}
	if !reflect.DeepEqual(repos[0].Env, []string{"NODE_ENV=production", "DATABASE_URL=postgres://db/app"}) {
		t.Fatalf("env = %#v", repos[0].Env)
	}
	if repos[1].Name != "web" {
		t.Fatalf("second repo = %#v", repos[1])
	}
}

func TestWebDeleteRepoRemovesOnlySelectedRepo(t *testing.T) {
	root := newTestProject(t)
	writeTestRepos(t, root, []Repo{
		{Name: "api", URL: "https://github.com/acme/api", Path: "apps/api"},
		{Name: "web", URL: "https://github.com/acme/web", Path: "apps/web"},
	})
	app := &webApp{root: root}

	response := postWebForm(t, app, "/repos/0/delete", nil)

	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusSeeOther)
	}
	repos, err := readRepos(filepath.Join(root, reposFile))
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 || repos[0].Name != "web" {
		t.Fatalf("repos = %#v", repos)
	}
}

func TestWebIndexRedirectsToRepos(t *testing.T) {
	root := newTestProject(t)
	app := &webApp{root: root}

	response := getWeb(t, app, "/")

	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusSeeOther)
	}
	if location := response.Header().Get("Location"); location != "/repos" {
		t.Fatalf("location = %q", location)
	}
}

func TestWebFilesPageLinksProjectFiles(t *testing.T) {
	root := newTestProject(t)
	app := &webApp{root: root}

	response := getWeb(t, app, "/files")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	body := response.Body.String()
	for _, want := range []string{
		`href="/files/repos"`,
		`href="/files/env"`,
		`href="/files/makefile"`,
		`href="/files/docker-compose"`,
		`href="/files/caddy-dockerfile"`,
		`href="/files/caddyfile"`,
		`data-shell-url="/files/shell"`,
		`Project Terminal`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("index missing %q:\n%s", want, body)
		}
	}
}

func TestWebReposPageShowsRepoActions(t *testing.T) {
	root := newTestProject(t)
	writeTestRepos(t, root, []Repo{
		{Name: "api", URL: "https://github.com/acme/api", Path: "apps/api", Branch: "main"},
	})
	app := &webApp{root: root}

	response := getWeb(t, app, "/repos")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	body := response.Body.String()
	for _, want := range []string{
		`action="/repos/0/pull"`,
		`href="/repos/0"`,
		`api`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("repos page missing %q:\n%s", want, body)
		}
	}
}

func TestRepoShellPathUsesResolvedRepoPath(t *testing.T) {
	root := newTestProject(t)
	repoPath := filepath.Join(root, "apps", "api")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestRepos(t, root, []Repo{
		{Name: "api", URL: "https://github.com/acme/api", Path: filepath.Join("apps", "api")},
	})

	path, err := repoShellPath(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	if path != repoPath {
		t.Fatalf("path = %q, want %q", path, repoPath)
	}
}

func TestRepoShellPathRejectsMissingRepoPath(t *testing.T) {
	root := newTestProject(t)
	writeTestRepos(t, root, []Repo{
		{Name: "api", URL: "https://github.com/acme/api", Path: filepath.Join("apps", "api")},
	})

	_, err := repoShellPath(root, 0)
	if err == nil {
		t.Fatal("expected missing path error")
	}
	if !strings.Contains(err.Error(), "pull the repository first") {
		t.Fatalf("error = %v", err)
	}
}

func TestWebRepoRendersEmbeddedBrowserShell(t *testing.T) {
	root := newTestProject(t)
	if err := os.MkdirAll(filepath.Join(root, "apps", "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestRepos(t, root, []Repo{
		{Name: "api", URL: "https://github.com/acme/api", Path: filepath.Join("apps", "api")},
	})
	app := &webApp{root: root}

	response := getWeb(t, app, "/repos/0")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	body := response.Body.String()
	for _, want := range []string{
		`data-shell-url="/repos/0/shell"`,
		`Browser shell`,
		`name="domains"`,
		`name="rate_limit_enabled"`,
		`name="rate_limit_events" value="100"`,
		`apps/api`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("repo page missing shell %q:\n%s", want, body)
		}
	}
}

func TestWebRepoRendersSchemaEnvFields(t *testing.T) {
	root := newTestProject(t)
	repoPath := filepath.Join(root, "apps", "api")
	if err := os.MkdirAll(filepath.Join(repoPath, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoPath, envSchemaFile), []byte("API_KEY\nDATABASE_URL\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoPath, envFile), []byte("API_KEY=secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTestRepos(t, root, []Repo{
		{Name: "api", URL: "https://github.com/acme/api", Path: filepath.Join("apps", "api")},
	})
	app := &webApp{root: root}

	response := getWeb(t, app, "/repos/0")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	body := response.Body.String()
	for _, want := range []string{
		`name="app_env_key" value="API_KEY"`,
		`id="app-env-API_KEY" name="app_env_value" value="secret"`,
		`name="app_env_key" value="DATABASE_URL"`,
		`terminal terminal--compact`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("repo page missing env field %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, `id="repo-env"`) {
		t.Fatalf("repo page should not render env textarea:\n%s", body)
	}
}

func TestWebSaveRepoWritesSchemaEnvFile(t *testing.T) {
	root := newTestProject(t)
	repoPath := filepath.Join(root, "apps", "api")
	if err := os.MkdirAll(filepath.Join(repoPath, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoPath, envSchemaFile), []byte("API_KEY\nDATABASE_URL\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoPath, envFile), []byte("# keep\nAPI_KEY=old\nOTHER=value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTestRepos(t, root, []Repo{
		{Name: "api", URL: "https://github.com/acme/api", Path: filepath.Join("apps", "api"), Env: []string{"NODE_ENV=production"}},
	})
	app := &webApp{root: root}

	response := postWebForm(t, app, "/repos/0/save", url.Values{
		"url":           {"https://github.com/acme/api"},
		"name":          {"api"},
		"path":          {filepath.Join("apps", "api")},
		"env":           {"NODE_ENV=production"},
		"app_env_key":   {"API_KEY", "DATABASE_URL"},
		"app_env_value": {"new secret", "postgres://db/app"},
	})

	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusSeeOther)
	}
	env := readTestFile(t, filepath.Join(repoPath, envFile))
	for _, want := range []string{
		"# keep",
		`API_KEY="new secret"`,
		"OTHER=value",
		"DATABASE_URL=postgres://db/app",
	} {
		if !strings.Contains(env, want) {
			t.Fatalf("env file missing %q:\n%s", want, env)
		}
	}
	repos, err := readRepos(filepath.Join(root, reposFile))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(repos[0].Env, []string{"NODE_ENV=production"}) {
		t.Fatalf("repo env = %#v", repos[0].Env)
	}
}

func TestWebRepoShellRunsCommandInRepoPath(t *testing.T) {
	root := newTestProject(t)
	repoPath := filepath.Join(root, "apps", "api")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTestRepos(t, root, []Repo{
		{Name: "api", URL: "https://github.com/acme/api", Path: filepath.Join("apps", "api")},
	})
	app := &webApp{root: root}

	response := postWebForm(t, app, "/repos/0/shell", url.Values{
		"command": {"pwd && ls"},
	})

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	body := response.Body.String()
	if !strings.Contains(body, repoPath) || !strings.Contains(body, "README.md") {
		t.Fatalf("unexpected shell response:\n%s", body)
	}
}

func TestWebReposPageLinksCredentials(t *testing.T) {
	root := newTestProject(t)
	app := &webApp{root: root}

	response := getWeb(t, app, "/repos")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if body := response.Body.String(); !strings.Contains(body, `href="/credentials"`) {
		t.Fatalf("repos page missing credentials link:\n%s", body)
	}
}

func TestWebCredentialsPageShowsCurrentUsername(t *testing.T) {
	root := newTestProject(t)
	if err := os.WriteFile(filepath.Join(root, envFile), []byte("GITHUB_USERNAME=octo\nGITHUB_PASSWORD=secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := &webApp{root: root}

	response := getWeb(t, app, "/credentials")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	body := response.Body.String()
	if !strings.Contains(body, `value="octo"`) || !strings.Contains(body, `name="password"`) {
		t.Fatalf("unexpected credentials page:\n%s", body)
	}
}

func TestWebCredentialsSaveWritesEnvFile(t *testing.T) {
	root := newTestProject(t)
	if err := os.WriteFile(filepath.Join(root, envFile), []byte("# keep me\nOTHER=value\nGITHUB_USERNAME=old\nGITHUB_PASSWORD=old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := &webApp{root: root}

	response := postWebForm(t, app, "/credentials", url.Values{
		"username": {"octo"},
		"password": {"token value"},
	})

	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusSeeOther)
	}
	if location := response.Header().Get("Location"); location != "/credentials?message=credentials+saved" {
		t.Fatalf("location = %q", location)
	}
	contents := readTestFile(t, filepath.Join(root, envFile))
	for _, want := range []string{
		"# keep me",
		"OTHER=value",
		"GITHUB_USERNAME=octo",
		`GITHUB_PASSWORD="token value"`,
	} {
		if !strings.Contains(contents, want) {
			t.Fatalf("env file missing %q:\n%s", want, contents)
		}
	}
}

func TestWebSyncPageHasBulkControls(t *testing.T) {
	root := newTestProject(t)
	writeTestRepos(t, root, []Repo{
		{Name: "api", URL: "https://github.com/acme/api", Path: "apps/api"},
	})
	app := &webApp{root: root}

	response := getWeb(t, app, "/sync")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	body := response.Body.String()
	for _, want := range []string{
		`action="/sync/pull"`,
		`action="/sync/push"`,
		`name="message"`,
		`General Commit Message`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("sync page missing %q:\n%s", want, body)
		}
	}
}

func TestWebFileShowsWhitelistedProjectFile(t *testing.T) {
	root := newTestProject(t)
	if err := os.WriteFile(filepath.Join(root, makeFile), []byte("launch:\n\tdocker compose up\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := &webApp{root: root}

	response := getWeb(t, app, "/files/makefile")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	body := response.Body.String()
	if !strings.Contains(body, "Makefile") || !strings.Contains(body, "docker compose up") {
		t.Fatalf("unexpected file page:\n%s", body)
	}
}

func TestWebFileShowsMissingGeneratedFile(t *testing.T) {
	root := newTestProject(t)
	app := &webApp{root: root}

	response := getWeb(t, app, "/files/caddyfile")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if body := response.Body.String(); !strings.Contains(body, "File has not been generated") {
		t.Fatalf("unexpected missing page:\n%s", body)
	}
}

func TestWebFileRejectsUnknownFile(t *testing.T) {
	root := newTestProject(t)
	app := &webApp{root: root}

	response := getWeb(t, app, "/files/readme")

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestCreateWorkspaceInitializesWorkspaceDirectory(t *testing.T) {
	root := t.TempDir()
	workspaceBase := filepath.Join(t.TempDir(), "uppr-workspaces")
	t.Setenv(workspacesDirEnv, workspaceBase)
	if err := ensureTestServerFiles(root); err != nil {
		t.Fatal(err)
	}

	workspace, err := createWorkspace(root, "Production Apps")
	if err != nil {
		t.Fatal(err)
	}

	if workspace.Name != "production-apps" {
		t.Fatalf("workspace name = %q", workspace.Name)
	}
	if workspace.Path != filepath.Join(workspaceBase, "production-apps") {
		t.Fatalf("workspace path = %q, want under %q", workspace.Path, workspaceBase)
	}
	workspaceRoot := workspace.Path
	for _, path := range []string{
		filepath.Join(workspaceRoot, reposFile),
		filepath.Join(workspaceRoot, envFile),
		filepath.Join(root, workspacesFile),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, workspacesDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("server root should not contain %s directory; stat err = %v", workspacesDir, err)
	}
}

func TestWebWorkspaceCreateRedirectsToWorkspaceRepos(t *testing.T) {
	root := t.TempDir()
	t.Setenv(workspacesDirEnv, filepath.Join(t.TempDir(), "uppr-workspaces"))
	if err := ensureTestServerFiles(root); err != nil {
		t.Fatal(err)
	}
	app := &webApp{root: root}

	response := postWebForm(t, app, "/workspaces", url.Values{"name": {"Production Apps"}})

	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusSeeOther)
	}
	if location := response.Header().Get("Location"); location != "/workspaces/production-apps/repos?message=workspace+created" {
		t.Fatalf("location = %q", location)
	}
}

func TestWebWorkspaceRepoRoutesWriteInsideWorkspace(t *testing.T) {
	root := t.TempDir()
	t.Setenv(workspacesDirEnv, filepath.Join(t.TempDir(), "uppr-workspaces"))
	if err := ensureTestServerFiles(root); err != nil {
		t.Fatal(err)
	}
	workspace, err := createWorkspace(root, "ops")
	if err != nil {
		t.Fatal(err)
	}
	app := &webApp{root: root}

	response := postWebForm(t, app, "/workspaces/ops/repos", url.Values{
		"github_owner": {"acme"},
		"github_repo":  {"api"},
	})

	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusSeeOther)
	}
	if location := response.Header().Get("Location"); location != "/workspaces/ops/repos?message=repo+added" {
		t.Fatalf("location = %q", location)
	}
	repos, err := readRepos(filepath.Join(workspace.Path, reposFile))
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 || repos[0].Name != "api" {
		t.Fatalf("repos = %#v", repos)
	}
}

func TestGenerateServerFilesBuildsSingleRootCompose(t *testing.T) {
	root := t.TempDir()
	t.Setenv(workspacesDirEnv, filepath.Join(t.TempDir(), "uppr workspaces"))
	if err := ensureTestServerFiles(root); err != nil {
		t.Fatal(err)
	}
	workspace, err := createWorkspace(root, "ops")
	if err != nil {
		t.Fatal(err)
	}
	workspaceRoot := workspace.Path
	writeTestRepos(t, workspaceRoot, []Repo{{
		Name:          "api",
		URL:           "https://github.com/acme/api",
		Path:          "apps/api",
		Port:          8080,
		ContainerPort: 3000,
	}})

	if err := generateServerFilesAt(root); err != nil {
		t.Fatal(err)
	}

	masterCompose := readTestFile(t, filepath.Join(root, dockerComposeFile))
	for _, want := range []string{
		"services:",
		"  caddy:",
		"  ops-api:",
		strconv.Quote(filepath.ToSlash(filepath.Join(workspace.Path, "apps", "api"))),
	} {
		if !strings.Contains(masterCompose, want) {
			t.Fatalf("master compose missing %q:\n%s", want, masterCompose)
		}
	}
	if strings.Contains(masterCompose, "include:") || strings.Contains(masterCompose, "8080:3000") {
		t.Fatalf("unexpected master compose:\n%s", masterCompose)
	}
	masterCaddy := readTestFile(t, filepath.Join(root, caddyFile))
	if !strings.Contains(masterCaddy, "api.localhost {") || !strings.Contains(masterCaddy, "reverse_proxy ops-api:3000") {
		t.Fatalf("unexpected master Caddyfile:\n%s", masterCaddy)
	}
	if _, err := os.Stat(filepath.Join(workspaceRoot, dockerComposeFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("workspace compose should not be generated; stat err = %v", err)
	}
}

func TestWebWorkspaceGenerateWritesRootCompose(t *testing.T) {
	root := t.TempDir()
	t.Setenv(workspacesDirEnv, filepath.Join(t.TempDir(), "uppr-workspaces"))
	if err := ensureTestServerFiles(root); err != nil {
		t.Fatal(err)
	}
	workspace, err := createWorkspace(root, "ops")
	if err != nil {
		t.Fatal(err)
	}
	writeTestRepos(t, workspace.Path, []Repo{{
		Name:          "api",
		URL:           "https://github.com/acme/api",
		Path:          "apps/api",
		Port:          8080,
		ContainerPort: 3000,
	}})
	app := &webApp{root: root}

	response := postWebForm(t, app, "/workspaces/ops/generate", nil)

	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusSeeOther)
	}
	if _, err := os.Stat(filepath.Join(root, dockerComposeFile)); err != nil {
		t.Fatalf("expected root compose: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace.Path, dockerComposeFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("workspace compose should not be generated; stat err = %v", err)
	}
}

func newTestProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, envFile), []byte("GITHUB_USERNAME=\nGITHUB_PASSWORD=\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, reposFile), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func configureTestGitUser(t *testing.T, dir string) {
	t.Helper()
	runTestGit(t, dir, "config", "user.email", "uppr@example.test")
	runTestGit(t, dir, "config", "user.name", "uppr test")
}

func runTestGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func runTestGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(bytes.TrimSpace(output))
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func writeTestRepos(t *testing.T, root string, repos []Repo) {
	t.Helper()
	if err := writeRepos(filepath.Join(root, reposFile), repos); err != nil {
		t.Fatal(err)
	}
}

func getWeb(t *testing.T, app *webApp, target string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	response := httptest.NewRecorder()
	app.routes().ServeHTTP(response, request)
	return response
}

func postWebForm(t *testing.T, app *webApp, target string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	app.routes().ServeHTTP(response, request)
	return response
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Fatal(err)
		}
	})
}
