package main

import (
	"bufio"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	envFile   = "config/.env"
	reposFile = "repos.conf"
)

type Repo struct {
	Name   string
	URL    string
	Path   string
	Branch string
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "uppr:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}

	switch args[0] {
	case "init":
		return initProject(args[1:])
	case "add":
		return addRepo(args[1:])
	case "list":
		return listRepos()
	case "remove", "rm":
		return removeRepo(args[1:])
	case "pull":
		return pullRepos()
	case "push":
		return pushRepos(args[1:])
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func printUsage() {
	fmt.Println(`uppr keeps a list of git repos current.

Usage:
  uppr init [path]                create config/.env and repos.conf in path
  uppr add <url> [options]        add a repo to repos.conf
  uppr list                       list configured repos
  uppr remove <name|url|path>     remove a repo from repos.conf
  uppr pull                       clone missing repos and pull existing repos
  uppr push <message>             commit changes in each repo and push

Add options:
  --name <name>
  --path <path>
  --branch <branch>

Push options:
  -m, --message <message>

repos.conf format:
  [repo]
  url = https://github.com/example/example`)
}

func initProject(args []string) error {
	if len(args) > 1 {
		return errors.New("usage: uppr init [path]")
	}

	root := "."
	if len(args) == 1 {
		root = args[0]
	}
	if err := os.MkdirAll(filepath.Join(root, "config"), 0o755); err != nil {
		return err
	}
	if err := writeFileIfMissing(filepath.Join(root, envFile), []byte("GITHUB_USERNAME=\nGITHUB_PASSWORD=\n")); err != nil {
		return err
	}
	if err := writeFileIfMissing(filepath.Join(root, reposFile), []byte("[repo]\nurl = https://github.com/your-user/your-repo\n")); err != nil {
		return err
	}

	fmt.Printf("initialized uppr project in %s\n", root)
	return nil
}

func writeFileIfMissing(path string, contents []byte) error {
	if _, err := os.Stat(path); err == nil {
		fmt.Printf("exists: %s\n", path)
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.WriteFile(path, contents, 0o600)
}

func addRepo(args []string) error {
	repo, err := parseAddRepoArgs(args)
	if err != nil {
		return err
	}

	root, err := findProjectRoot(".")
	if err != nil {
		return err
	}
	path := filepath.Join(root, reposFile)
	repos, err := readRepos(path)
	if err != nil {
		return err
	}
	if repo.Name == "" {
		repo.Name = repoNameFromURL(repo.URL)
	}
	if repo.Path == "" {
		repo.Path = defaultRepoPath(repo)
	}
	if err := validateNewRepo(repo, repos); err != nil {
		return err
	}

	repos = append(repos, repo)
	if err := writeRepos(path, repos); err != nil {
		return err
	}
	fmt.Printf("added repo %s\n", repoLabel(repo))
	return nil
}

func parseAddRepoArgs(args []string) (Repo, error) {
	if len(args) == 0 {
		return Repo{}, errors.New("usage: uppr add <url> [--name <name>] [--path <path>] [--branch <branch>]")
	}

	repo := Repo{URL: args[0]}
	if strings.HasPrefix(repo.URL, "-") {
		return Repo{}, errors.New("usage: uppr add <url> [--name <name>] [--path <path>] [--branch <branch>]")
	}
	for i := 1; i < len(args); i++ {
		if i+1 >= len(args) {
			return Repo{}, fmt.Errorf("%s requires a value", args[i])
		}
		value := args[i+1]
		switch args[i] {
		case "--name":
			repo.Name = value
		case "--path":
			repo.Path = value
		case "--branch":
			repo.Branch = value
		default:
			return Repo{}, fmt.Errorf("unknown add option %q", args[i])
		}
		i++
	}
	return repo, nil
}

func validateNewRepo(repo Repo, repos []Repo) error {
	for _, existing := range repos {
		if existing.Name == repo.Name {
			return fmt.Errorf("repo named %q already exists", repo.Name)
		}
		if existing.URL == repo.URL {
			return fmt.Errorf("repo url %q already exists", repo.URL)
		}
		if existing.Path == repo.Path {
			return fmt.Errorf("repo path %q already exists", repo.Path)
		}
	}
	return nil
}

func listRepos() error {
	root, err := findProjectRoot(".")
	if err != nil {
		return err
	}
	repos, err := readRepos(filepath.Join(root, reposFile))
	if err != nil {
		return err
	}
	if len(repos) == 0 {
		fmt.Println("no repos configured")
		return nil
	}

	fmt.Println("NAME\tPATH\tBRANCH\tURL")
	for _, repo := range repos {
		branch := repo.Branch
		if branch == "" {
			branch = "-"
		}
		fmt.Printf("%s\t%s\t%s\t%s\n", repo.Name, repo.Path, branch, repo.URL)
	}
	return nil
}

func removeRepo(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: uppr remove <name|url|path>")
	}
	identifier := args[0]

	root, err := findProjectRoot(".")
	if err != nil {
		return err
	}
	path := filepath.Join(root, reposFile)
	repos, err := readRepos(path)
	if err != nil {
		return err
	}

	var kept []Repo
	var removed []Repo
	for _, repo := range repos {
		if repoMatchesIdentifier(repo, identifier) {
			removed = append(removed, repo)
			continue
		}
		kept = append(kept, repo)
	}
	if len(removed) == 0 {
		return fmt.Errorf("repo %q not found", identifier)
	}
	if len(removed) > 1 {
		return fmt.Errorf("repo %q matched %d repos; use a more specific name, url, or path", identifier, len(removed))
	}
	if err := writeRepos(path, kept); err != nil {
		return err
	}
	fmt.Printf("removed repo %s\n", repoLabel(removed[0]))
	return nil
}

func repoMatchesIdentifier(repo Repo, identifier string) bool {
	return repo.Name == identifier || repo.URL == identifier || repo.Path == identifier
}

func writeRepos(path string, repos []Repo) error {
	var builder strings.Builder
	for i, repo := range repos {
		if i > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString("[repo]\n")
		if repo.Name != "" {
			fmt.Fprintf(&builder, "name = %s\n", repo.Name)
		}
		fmt.Fprintf(&builder, "url = %s\n", repo.URL)
		if repo.Path != "" {
			fmt.Fprintf(&builder, "path = %s\n", repo.Path)
		}
		if repo.Branch != "" {
			fmt.Fprintf(&builder, "branch = %s\n", repo.Branch)
		}
	}
	return os.WriteFile(path, []byte(builder.String()), 0o600)
}

func pullRepos() error {
	root, err := findProjectRoot(".")
	if err != nil {
		return err
	}

	env, err := readDotEnv(filepath.Join(root, envFile))
	if err != nil {
		return err
	}

	repos, err := readRepos(filepath.Join(root, reposFile))
	if err != nil {
		return err
	}
	if len(repos) == 0 {
		return fmt.Errorf("%s does not contain any repos", filepath.Join(root, reposFile))
	}
	resolveRepoPaths(root, repos)

	askPass, cleanup, err := makeAskPass(env["GITHUB_USERNAME"], env["GITHUB_PASSWORD"])
	if err != nil {
		return err
	}
	defer cleanup()

	var failed bool
	for _, repo := range repos {
		if err := pullRepo(repo, askPass); err != nil {
			failed = true
			fmt.Fprintf(os.Stderr, "%s: %v\n", repoLabel(repo), err)
		}
	}
	if failed {
		return errors.New("one or more repos failed")
	}
	return nil
}

func pushRepos(args []string) error {
	message, err := parsePushArgs(args)
	if err != nil {
		return err
	}

	root, err := findProjectRoot(".")
	if err != nil {
		return err
	}

	env, err := readDotEnv(filepath.Join(root, envFile))
	if err != nil {
		return err
	}

	repos, err := readRepos(filepath.Join(root, reposFile))
	if err != nil {
		return err
	}
	if len(repos) == 0 {
		return fmt.Errorf("%s does not contain any repos", filepath.Join(root, reposFile))
	}
	resolveRepoPaths(root, repos)

	askPass, cleanup, err := makeAskPass(env["GITHUB_USERNAME"], env["GITHUB_PASSWORD"])
	if err != nil {
		return err
	}
	defer cleanup()

	var failed bool
	for _, repo := range repos {
		if err := pushRepo(repo, message, askPass); err != nil {
			failed = true
			fmt.Fprintf(os.Stderr, "%s: %v\n", repoLabel(repo), err)
		}
	}
	if failed {
		return errors.New("one or more repos failed")
	}
	return nil
}

func parsePushArgs(args []string) (string, error) {
	if len(args) == 0 {
		return "", errors.New("usage: uppr push <message>")
	}
	if len(args) == 2 && (args[0] == "-m" || args[0] == "--message") {
		message := strings.TrimSpace(args[1])
		if message == "" {
			return "", errors.New("commit message cannot be empty")
		}
		return message, nil
	}
	if strings.HasPrefix(args[0], "-") {
		return "", fmt.Errorf("unknown push option %q", args[0])
	}
	message := strings.TrimSpace(strings.Join(args, " "))
	if message == "" {
		return "", errors.New("commit message cannot be empty")
	}
	return message, nil
}

func findProjectRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if fileExists(filepath.Join(dir, envFile)) && fileExists(filepath.Join(dir, reposFile)) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("uppr project not found; run `uppr init .` first")
		}
		dir = parent
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func resolveRepoPaths(root string, repos []Repo) {
	for i := range repos {
		if repos[i].Path != "" && !filepath.IsAbs(repos[i].Path) {
			repos[i].Path = filepath.Join(root, repos[i].Path)
		}
	}
}

func readDotEnv(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%s is missing; run `uppr init` first", path)
		}
		return nil, err
	}
	defer file.Close()

	values := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

func readRepos(path string) ([]Repo, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%s is missing; run `uppr init` first", path)
		}
		return nil, err
	}
	defer file.Close()

	var repos []Repo
	var current *Repo
	scanner := bufio.NewScanner(file)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line == "[repo]" {
			repos = append(repos, Repo{})
			current = &repos[len(repos)-1]
			continue
		}
		if current == nil {
			return nil, fmt.Errorf("%s:%d: expected [repo] before repo settings", path, lineNumber)
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("%s:%d: expected key = value", path, lineNumber)
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch key {
		case "name":
			current.Name = value
		case "url":
			current.URL = value
		case "path":
			current.Path = value
		case "branch":
			current.Branch = value
		default:
			return nil, fmt.Errorf("%s:%d: unknown repo setting %q", path, lineNumber, key)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	for i := range repos {
		if repos[i].URL == "" {
			return nil, fmt.Errorf("repo %d is missing url", i)
		}
		if repos[i].Name == "" {
			repos[i].Name = repoNameFromURL(repos[i].URL)
		}
		if repos[i].Path == "" {
			repos[i].Path = defaultRepoPath(repos[i])
		}
	}
	return repos, nil
}

func defaultRepoPath(repo Repo) string {
	name := repo.Name
	if name == "" {
		name = repoNameFromURL(repo.URL)
	}
	if name == "" {
		name = "repo"
	}
	return filepath.Join("apps", name)
}

func repoNameFromURL(raw string) string {
	raw = strings.TrimSuffix(raw, "/")
	raw = strings.TrimSuffix(raw, ".git")
	if u, err := url.Parse(raw); err == nil && u.Path != "" {
		raw = u.Path
	}
	base := filepath.Base(raw)
	if base == "." || base == string(filepath.Separator) {
		return ""
	}
	return base
}

func pullRepo(repo Repo, askPass string) error {
	label := repoLabel(repo)
	if _, err := os.Stat(filepath.Join(repo.Path, ".git")); err == nil {
		fmt.Printf("[%s] git pull\n", label)
		return runGit(askPass, "-C", repo.Path, "pull", "--ff-only")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	fmt.Printf("[%s] git clone\n", label)
	if err := os.MkdirAll(filepath.Dir(repo.Path), 0o755); err != nil {
		return err
	}

	args := []string{"clone"}
	if repo.Branch != "" {
		args = append(args, "--branch", repo.Branch)
	}
	args = append(args, repo.URL, repo.Path)
	return runGit(askPass, args...)
}

func pushRepo(repo Repo, message, askPass string) error {
	label := repoLabel(repo)
	if _, err := os.Stat(filepath.Join(repo.Path, ".git")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s is not a git repository; run `uppr pull` first", repo.Path)
		}
		return err
	}

	fmt.Printf("[%s] git add -A\n", label)
	if err := runGit(askPass, "-C", repo.Path, "add", "-A"); err != nil {
		return err
	}

	hasChanges, err := hasStagedChanges(repo.Path, askPass)
	if err != nil {
		return err
	}
	if hasChanges {
		fmt.Printf("[%s] git commit\n", label)
		if err := runGit(askPass, "-C", repo.Path, "commit", "-m", message); err != nil {
			return err
		}
	} else {
		fmt.Printf("[%s] no changes to commit\n", label)
	}

	fmt.Printf("[%s] git push\n", label)
	return runGit(askPass, "-C", repo.Path, "push")
}

func hasStagedChanges(path, askPass string) (bool, error) {
	err := runGit(askPass, "-C", path, "diff", "--cached", "--quiet")
	if err == nil {
		return false, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return true, nil
	}
	return false, err
}

func repoLabel(repo Repo) string {
	if repo.Name != "" {
		return repo.Name
	}
	return repo.Path
}

func runGit(askPass string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	if askPass != "" {
		cmd.Env = append(cmd.Env, "GIT_ASKPASS="+askPass, "GIT_TERMINAL_PROMPT=0")
	}
	return cmd.Run()
}

func makeAskPass(username, password string) (string, func(), error) {
	if username == "" && password == "" {
		return "", func() {}, nil
	}

	dir, err := os.MkdirTemp("", "uppr-askpass-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() {
		_ = os.RemoveAll(dir)
	}

	path := filepath.Join(dir, "askpass.sh")
	script := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in\n*Username*) printf '%%s\\n' %s ;;\n*Password*) printf '%%s\\n' %s ;;\n*) printf '\\n' ;;\nesac\n", shellQuote(username), shellQuote(password))
	if runtime.GOOS == "windows" {
		path = filepath.Join(dir, "askpass.bat")
		script = fmt.Sprintf("@echo off\r\necho %s\r\n", password)
	}
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
