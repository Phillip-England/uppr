package main

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const (
	envFile           = "config/.env"
	envSchemaFile     = "env.schema"
	envJSONSchemaFile = "schema.json"
	reposFile         = "repos.conf"
)

type Repo struct {
	Name          string
	URL           string
	Path          string
	Branch        string
	Port          int
	ContainerPort int
	Domain        string
	Domains       []string
	RateLimit     RateLimit
	Env           []string
	Volumes       []string
}

type RateLimit struct {
	Enabled bool
	Zone    string
	Events  int
	Window  string
}

type envSchemaVar struct {
	Key         string
	Description string
	Example     string
	Required    bool
}

func defaultRateLimit() RateLimit {
	return RateLimit{
		Enabled: true,
		Zone:    "dynamic",
		Events:  100,
		Window:  "1m",
	}
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "uppr:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	return runWithServe(args, serveServer)
}

func runWithServe(args []string, serve func([]string) error) error {
	if len(args) == 0 {
		return serve(nil)
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
	case "generate", "gen":
		return generateProjectFiles()
	case "dump":
		return dumpIntegrationGuide(args[1:])
	case "generate-server":
		root := "."
		if len(args) > 2 {
			return errors.New("usage: uppr generate-server [path]")
		}
		if len(args) == 2 {
			root = args[1]
		}
		absRoot, err := filepath.Abs(root)
		if err != nil {
			return err
		}
		return generateServerFilesAt(absRoot)
	case "launch":
		return launchServer(args[1:])
	case "install-caddyx":
		return installCaddyx(args[1:])
	case "service", "service-uppr":
		return printUpprService(args[1:])
	case "service-caddy":
		return printCaddyService(args[1:])
	case "service-run":
		return runService(args[1:], serve)
	case "web", "ui":
		return serveWeb(args[1:])
	case "serve":
		return serve(args[1:])
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		if len(args) == 1 && looksLikePath(args[0]) {
			return serveWeb(args)
		}
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func printUsage() {
	fmt.Println(`uppr keeps a list of git repos current.

Usage:
  uppr                            same as uppr serve
  uppr init [path]                create config/.env and repos.conf in path
  uppr add <url> [options]        add a repo to repos.conf
  uppr list                       list configured repos
  uppr remove <name|url|path>     remove a repo from repos.conf
  uppr pull                       clone missing repos and pull existing repos
  uppr push <message>             commit changes in each repo and push
  uppr generate                   write Caddyfile, docker-compose.yml, and Makefile
  uppr dump [path]                write UPPR.md integration instructions for an AI agent
  uppr generate-server [path]     write master Caddy/Compose/Make files
  uppr launch [path]              rebuild and launch the managed app containers
  uppr install-caddyx [path]      build Caddy with rate limiting (default ~/.local/bin/caddyx)
  uppr service-uppr [path]        print a native systemd unit for Uppr
  uppr service-caddy [path]       print a native systemd unit for Caddy
  uppr service [path]             alias for service-uppr
  uppr web [path]                 open a browser UI for an uppr project
  uppr serve [path]               serve authenticated web UI for deployment
  uppr <path>                     shorthand for uppr web <path>

Add options:
  --name <name>
  --path <path>
  --branch <branch>

Push options:
  -m, --message <message>

repos.conf format:
  [repo]
  url = https://github.com/example/example
  port = 3000
  domain = example.localhost
  domain = www.example.localhost
  rate_limit_enabled = true
  rate_limit_zone = dynamic
  rate_limit_events = 100
  rate_limit_window = 1m
  env = NODE_ENV=production
  volume = ./cache:/app/cache

App repos can include schema.json with environment variable metadata, or the
legacy env.schema file with one environment variable name per line. After
pull/sync, uppr prepares apps/<repo>/config/.env with blank entries for missing
schema keys while preserving existing values.`)
}

func launchServer(args []string) error {
	if len(args) > 1 {
		return errors.New("usage: uppr launch [path]")
	}
	root := "."
	if len(args) == 1 {
		root = args[0]
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if err := ensureServerFiles(absRoot); err != nil {
		return err
	}
	if err := generateServerFilesAt(absRoot); err != nil {
		return err
	}
	cleanup := exec.Command("docker", "compose", "down", "--remove-orphans")
	cleanup.Dir = absRoot
	cleanup.Stdin = os.Stdin
	cleanup.Stdout = os.Stdout
	cleanup.Stderr = os.Stderr
	if err := cleanup.Run(); err != nil {
		return fmt.Errorf("clear existing Docker Compose stack: %w", err)
	}
	// Detached Compose starts can return successfully while a container is
	// immediately crashing and being restarted. Wait until every service is
	// running (and healthy when it defines a healthcheck) before refreshing the
	// public proxy configuration.
	cmd := exec.Command("docker", "compose", "up", "--build", "-d", "--remove-orphans", "--wait", "--wait-timeout", "120")
	cmd.Dir = absRoot
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("launch Docker Compose services and wait for readiness: %w", err)
	}
	return reloadCaddy(absRoot)
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
	if err := writeFileIfMissing(filepath.Join(root, envFile), defaultEnvContents()); err != nil {
		return err
	}
	if err := ensureEnvDefaults(filepath.Join(root, envFile)); err != nil {
		return err
	}
	if err := writeFileIfMissing(filepath.Join(root, reposFile), []byte("[repo]\nurl = https://github.com/your-user/your-repo\n")); err != nil {
		return err
	}
	if err := ensureAuthDBFile(root); err != nil {
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
	repo.RateLimit = normalizeRateLimit(repo.RateLimit)
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

	repo := Repo{URL: args[0], RateLimit: defaultRateLimit()}
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
		if repo.Port != 0 {
			fmt.Fprintf(&builder, "port = %d\n", repo.Port)
		}
		if repo.ContainerPort != 0 {
			fmt.Fprintf(&builder, "container_port = %d\n", repo.ContainerPort)
		}
		if repo.Domain != "" {
			fmt.Fprintf(&builder, "domain = %s\n", repo.Domain)
		}
		for _, domain := range repo.Domains {
			if domain != "" && domain != repo.Domain {
				fmt.Fprintf(&builder, "domain = %s\n", domain)
			}
		}
		rateLimit := normalizeRateLimit(repo.RateLimit)
		fmt.Fprintf(&builder, "rate_limit_enabled = %t\n", rateLimit.Enabled)
		fmt.Fprintf(&builder, "rate_limit_zone = %s\n", rateLimit.Zone)
		fmt.Fprintf(&builder, "rate_limit_events = %d\n", rateLimit.Events)
		fmt.Fprintf(&builder, "rate_limit_window = %s\n", rateLimit.Window)
		for _, env := range repo.Env {
			fmt.Fprintf(&builder, "env = %s\n", env)
		}
		for _, volume := range repo.Volumes {
			fmt.Fprintf(&builder, "volume = %s\n", volume)
		}
	}
	return os.WriteFile(path, []byte(builder.String()), 0o600)
}

func pullRepos() error {
	root, err := findProjectRoot(".")
	if err != nil {
		return err
	}
	return pullReposAt(root)
}

func pullReposAt(root string) error {
	env, err := readDotEnv(filepath.Join(root, envFile))
	if err != nil {
		return err
	}

	reposPath := filepath.Join(root, reposFile)
	repos, err := readRepos(filepath.Join(root, reposFile))
	if err != nil {
		return err
	}
	if len(repos) == 0 {
		return fmt.Errorf("%s does not contain any repos", filepath.Join(root, reposFile))
	}
	resolvedRepos := append([]Repo(nil), repos...)
	resolveRepoPaths(root, resolvedRepos)

	askPass, cleanup, err := makeAskPass(env["GITHUB_USERNAME"], env["GITHUB_PASSWORD"])
	if err != nil {
		return err
	}
	defer cleanup()

	var failed bool
	var changed bool
	for i, repo := range resolvedRepos {
		if err := pullRepo(repo, askPass); err != nil {
			failed = true
			fmt.Fprintf(os.Stderr, "%s: %v\n", repoLabel(repo), err)
			continue
		}
		if err := prepareRepoEnv(repo); err != nil {
			failed = true
			fmt.Fprintf(os.Stderr, "%s: %v\n", repoLabel(repo), err)
		}
		if port, err := readDockerfileExposedPort(filepath.Join(repo.Path, "Dockerfile")); err != nil {
			failed = true
			fmt.Fprintf(os.Stderr, "%s: %v\n", repoLabel(repo), err)
		} else if port != 0 {
			changedRepo := false
			if repos[i].Port == 0 {
				repos[i].Port = port
				changedRepo = true
			}
			if repos[i].ContainerPort == 0 {
				repos[i].ContainerPort = port
				changedRepo = true
			}
			if changedRepo {
				changed = true
				fmt.Printf("[%s] configured port and container_port = %d from Dockerfile\n", repoLabel(repo), port)
			}
		}
	}
	if changed {
		if err := writeRepos(reposPath, repos); err != nil {
			return err
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
	return pushReposAt(root, message)
}

func pushReposAt(root, message string) error {
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

func pullRepoAt(root string, index int) error {
	repos, askPass, cleanup, err := reposForGit(root)
	if err != nil {
		return err
	}
	defer cleanup()
	if index < 0 || index >= len(repos) {
		return fmt.Errorf("repo %d not found", index)
	}
	if err := pullRepo(repos[index], askPass); err != nil {
		return err
	}
	if err := prepareRepoEnv(repos[index]); err != nil {
		return err
	}
	return configureRepoContainerPortFromDockerfile(root, index)
}

func pushRepoAt(root string, index int, message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return errors.New("commit message cannot be empty")
	}
	repos, askPass, cleanup, err := reposForGit(root)
	if err != nil {
		return err
	}
	defer cleanup()
	if index < 0 || index >= len(repos) {
		return fmt.Errorf("repo %d not found", index)
	}
	return pushRepo(repos[index], message, askPass)
}

func reposForGit(root string) ([]Repo, string, func(), error) {
	env, err := readDotEnv(filepath.Join(root, envFile))
	if err != nil {
		return nil, "", nil, err
	}
	repos, err := readRepos(filepath.Join(root, reposFile))
	if err != nil {
		return nil, "", nil, err
	}
	if len(repos) == 0 {
		return nil, "", nil, fmt.Errorf("%s does not contain any repos", filepath.Join(root, reposFile))
	}
	resolveRepoPaths(root, repos)
	askPass, cleanup, err := makeAskPass(env["GITHUB_USERNAME"], env["GITHUB_PASSWORD"])
	if err != nil {
		return nil, "", nil, err
	}
	return repos, askPass, cleanup, nil
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
			repos = append(repos, Repo{RateLimit: defaultRateLimit()})
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
		case "port":
			port, err := parsePort(path, lineNumber, key, value)
			if err != nil {
				return nil, err
			}
			current.Port = port
		case "container_port":
			port, err := parsePort(path, lineNumber, key, value)
			if err != nil {
				return nil, err
			}
			current.ContainerPort = port
		case "domain":
			if current.Domain == "" {
				current.Domain = value
			}
			current.Domains = append(current.Domains, value)
		case "rate_limit_enabled":
			enabled, err := parseBool(path, lineNumber, key, value)
			if err != nil {
				return nil, err
			}
			current.RateLimit.Enabled = enabled
		case "rate_limit_zone":
			current.RateLimit.Zone = value
		case "rate_limit_events":
			events, err := parsePositiveInt(path, lineNumber, key, value)
			if err != nil {
				return nil, err
			}
			current.RateLimit.Events = events
		case "rate_limit_window":
			current.RateLimit.Window = value
		case "env":
			current.Env = append(current.Env, value)
		case "volume":
			current.Volumes = append(current.Volumes, value)
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
		if len(repos[i].Domains) == 0 && repos[i].Domain != "" {
			repos[i].Domains = []string{repos[i].Domain}
		}
		repos[i].RateLimit = normalizeRateLimit(repos[i].RateLimit)
	}
	return repos, nil
}

func parsePort(path string, lineNumber int, key, value string) (int, error) {
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("%s:%d: %s must be a port between 1 and 65535", path, lineNumber, key)
	}
	return port, nil
}

func parsePositiveInt(path string, lineNumber int, key, value string) (int, error) {
	number, err := strconv.Atoi(value)
	if err != nil || number < 1 {
		return 0, fmt.Errorf("%s:%d: %s must be a positive integer", path, lineNumber, key)
	}
	return number, nil
}

func parseBool(path string, lineNumber int, key, value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "yes", "1", "on":
		return true, nil
	case "false", "no", "0", "off":
		return false, nil
	default:
		return false, fmt.Errorf("%s:%d: %s must be true or false", path, lineNumber, key)
	}
}

func normalizeRateLimit(rateLimit RateLimit) RateLimit {
	defaults := defaultRateLimit()
	if rateLimit.Zone == "" {
		rateLimit.Zone = defaults.Zone
	}
	if rateLimit.Events == 0 {
		rateLimit.Events = defaults.Events
	}
	if rateLimit.Window == "" {
		rateLimit.Window = defaults.Window
	}
	return rateLimit
}

func repoDomains(repo Repo) []string {
	var domains []string
	seen := map[string]bool{}
	for _, domain := range append([]string{repo.Domain}, repo.Domains...) {
		domain = strings.TrimSpace(domain)
		if domain == "" || seen[domain] {
			continue
		}
		seen[domain] = true
		domains = append(domains, domain)
	}
	if len(domains) == 0 {
		domains = []string{serviceName(repo) + ".localhost"}
	}
	return domains
}

func looksLikePath(value string) bool {
	return value == "." || value == ".." || strings.HasPrefix(value, "./") || strings.HasPrefix(value, "../") || strings.HasPrefix(value, "/")
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

func prepareRepoEnv(repo Repo) error {
	vars, err := readRepoEnvSchema(repo.Path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(repo.Path, "config"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(repo.Path, "data"), 0o755); err != nil {
		return err
	}
	dbPath := filepath.Join(repo.Path, "data", "main.sqlite")
	if !fileExists(dbPath) {
		db, openErr := sql.Open("sqlite", dbPath)
		if openErr != nil {
			return openErr
		}
		if pingErr := db.Ping(); pingErr != nil {
			_ = db.Close()
			return pingErr
		}
		if closeErr := db.Close(); closeErr != nil {
			return closeErr
		}
	}

	envPath := filepath.Join(repo.Path, envFile)
	contents, existingKeys, err := readEnvFileForUpdate(envPath)
	if err != nil {
		return err
	}
	var body strings.Builder
	body.WriteString(contents)

	var added bool
	for _, variable := range vars {
		if existingKeys[variable.Key] {
			continue
		}
		if body.Len() > 0 && !strings.HasSuffix(body.String(), "\n") {
			body.WriteString("\n")
		}
		fmt.Fprintf(&body, "%s=\n", variable.Key)
		existingKeys[variable.Key] = true
		added = true
	}
	if !added && fileExists(envPath) {
		return nil
	}

	if err := os.WriteFile(envPath, []byte(body.String()), 0o600); err != nil {
		return err
	}
	fmt.Printf("[%s] prepared config/.env and data/main.sqlite\n", repoLabel(repo))
	return nil
}

func readRepoEnvSchema(repoPath string) ([]envSchemaVar, error) {
	jsonPath := filepath.Join(repoPath, envJSONSchemaFile)
	vars, err := readEnvJSONSchema(jsonPath)
	if err != nil {
		return nil, err
	}
	if len(vars) > 0 || fileExists(jsonPath) {
		return vars, nil
	}
	keys, err := readEnvSchema(filepath.Join(repoPath, envSchemaFile))
	if err != nil {
		return nil, err
	}
	vars = make([]envSchemaVar, 0, len(keys))
	for _, key := range keys {
		vars = append(vars, envSchemaVar{Key: key, Required: true})
	}
	return vars, nil
}

func readEnvJSONSchema(path string) ([]envSchemaVar, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var raw struct {
		Variables []struct {
			Name        string `json:"name"`
			Key         string `json:"key"`
			Description string `json:"description"`
			Example     string `json:"example"`
			Required    *bool  `json:"required"`
		} `json:"variables"`
	}
	if err := json.Unmarshal(contents, &raw); err != nil {
		return nil, fmt.Errorf("%s: invalid JSON: %w", path, err)
	}
	vars := make([]envSchemaVar, 0, len(raw.Variables))
	seen := map[string]bool{}
	for i, variable := range raw.Variables {
		key := strings.TrimSpace(variable.Name)
		if key == "" {
			key = strings.TrimSpace(variable.Key)
		}
		if !isEnvKey(key) {
			return nil, fmt.Errorf("%s:variables[%d]: invalid environment variable name %q", path, i, key)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		required := true
		if variable.Required != nil {
			required = *variable.Required
		}
		vars = append(vars, envSchemaVar{
			Key:         key,
			Description: strings.TrimSpace(variable.Description),
			Example:     strings.TrimSpace(variable.Example),
			Required:    required,
		})
	}
	return vars, nil
}

func readEnvSchema(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()

	var keys []string
	seen := map[string]bool{}
	scanner := bufio.NewScanner(file)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Contains(line, "=") {
			return nil, fmt.Errorf("%s:%d: expected environment variable name without a value", path, lineNumber)
		}
		if !isEnvKey(line) {
			return nil, fmt.Errorf("%s:%d: invalid environment variable name %q", path, lineNumber, line)
		}
		if seen[line] {
			continue
		}
		seen[line] = true
		keys = append(keys, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return keys, nil
}

func configureRepoContainerPortFromDockerfile(root string, index int) error {
	reposPath := filepath.Join(root, reposFile)
	repos, err := readRepos(reposPath)
	if err != nil {
		return err
	}
	if index < 0 || index >= len(repos) {
		return fmt.Errorf("repo %d not found", index)
	}
	if repos[index].Port != 0 && repos[index].ContainerPort != 0 {
		return nil
	}

	resolvedRepos := append([]Repo(nil), repos...)
	resolveRepoPaths(root, resolvedRepos)
	port, err := readDockerfileExposedPort(filepath.Join(resolvedRepos[index].Path, "Dockerfile"))
	if err != nil {
		return err
	}
	if port == 0 {
		return nil
	}
	if repos[index].Port == 0 {
		repos[index].Port = port
	}
	if repos[index].ContainerPort == 0 {
		repos[index].ContainerPort = port
	}
	fmt.Printf("[%s] configured port and container_port = %d from Dockerfile\n", repoLabel(resolvedRepos[index]), port)
	return writeRepos(reposPath, repos)
}

func readDockerfileExposedPort(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := stripDockerfileComment(strings.TrimSpace(scanner.Text()))
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.EqualFold(fields[0], "EXPOSE") {
			continue
		}
		for _, field := range fields[1:] {
			portValue, _, _ := strings.Cut(field, "/")
			port, err := strconv.Atoi(portValue)
			if err != nil || port < 1 || port > 65535 {
				continue
			}
			return port, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return 0, nil
}

func stripDockerfileComment(line string) string {
	var escaped bool
	for i, r := range line {
		if r == '#' && !escaped {
			return strings.TrimSpace(line[:i])
		}
		escaped = r == '\\' && !escaped
		if r != '\\' {
			escaped = false
		}
	}
	return strings.TrimSpace(line)
}

func readEnvFileForUpdate(path string) (string, map[string]bool, error) {
	existingKeys := map[string]bool{}
	contents, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", existingKeys, nil
		}
		return "", nil, err
	}

	scanner := bufio.NewScanner(bytes.NewReader(contents))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, _, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if isEnvKey(key) {
			existingKeys[key] = true
		}
	}
	if err := scanner.Err(); err != nil {
		return "", nil, err
	}
	return string(contents), existingKeys, nil
}

func isEnvKey(value string) bool {
	if value == "" {
		return false
	}
	for i, r := range value {
		if r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	first := value[0]
	return first == '_' || first >= 'A' && first <= 'Z' || first >= 'a' && first <= 'z'
}

func pushRepo(repo Repo, message, askPass string) error {
	label := repoLabel(repo)
	if _, err := os.Stat(filepath.Join(repo.Path, ".git")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s is not a git repository; run `uppr pull` first", repo.Path)
		}
		return err
	}
	if err := ensureRepoGitignore(repo.Path); err != nil {
		return fmt.Errorf("protect config and data: %w", err)
	}
	tracked, err := trackedProtectedPaths(repo.Path)
	if err != nil {
		return fmt.Errorf("check protected paths: %w", err)
	}
	if len(tracked) > 0 {
		return fmt.Errorf("refusing to push because protected files are tracked by git: %s; run `git -C %q rm -r --cached --ignore-unmatch config data`, review the changes, and push again", strings.Join(tracked, ", "), repo.Path)
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

const protectedGitignoreBlock = "# uppr: never commit local configuration or application data\n/config/\n/data/\n"

func ensureRepoGitignore(repoPath string) error {
	path := filepath.Join(repoPath, ".gitignore")
	contents, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if bytes.HasSuffix(contents, []byte(protectedGitignoreBlock)) {
		return nil
	}

	var updated bytes.Buffer
	updated.Write(contents)
	if len(contents) > 0 && contents[len(contents)-1] != '\n' {
		updated.WriteByte('\n')
	}
	updated.WriteString(protectedGitignoreBlock)
	return os.WriteFile(path, updated.Bytes(), 0o644)
}

func trackedProtectedPaths(repoPath string) ([]string, error) {
	cmd := exec.Command("git", gitArgsWithSafeDirectory("-C", repoPath, "ls-files", "--", "config", "data")...)
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil, nil
	}
	return lines, nil
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
	cmd := exec.Command("git", gitArgsWithSafeDirectory(args...)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	if askPass != "" {
		cmd.Env = append(cmd.Env, "GIT_ASKPASS="+askPass, "GIT_TERMINAL_PROMPT=0")
	}
	return cmd.Run()
}

// gitArgsWithSafeDirectory trusts only the repository selected by a -C argument.
// This is needed when uppr runs in a service container as a different UID from
// the owner of the bind-mounted workspace. Keeping the exception on each Git
// invocation avoids accumulating machine-specific entries in global config.
func gitArgsWithSafeDirectory(args ...string) []string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] != "-C" {
			continue
		}
		repoPath, err := filepath.Abs(args[i+1])
		if err != nil {
			repoPath = args[i+1]
		}
		configured := make([]string, 0, len(args)+2)
		configured = append(configured, "-c", "safe.directory="+repoPath)
		return append(configured, args...)
	}
	return args
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
