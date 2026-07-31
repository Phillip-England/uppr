package main

import (
	"os"
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
