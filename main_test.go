package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

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
	if repos[0] != (Repo{Name: "api", URL: "https://github.com/acme/service", Path: "services/api", Branch: "main"}) {
		t.Fatalf("repo = %#v", repos[0])
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

func writeTestRepos(t *testing.T, root string, repos []Repo) {
	t.Helper()
	if err := writeRepos(filepath.Join(root, reposFile), repos); err != nil {
		t.Fatal(err)
	}
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
