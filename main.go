package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "uppr:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return nil
	}
	switch args[0] {
	case "init":
		if len(args) != 2 {
			return errors.New("usage: uppr init <directory>")
		}
		return initPad(args[1])
	case "add":
		return addCommand(args[1:])
	case "list":
		_, m, err := findPad(".")
		if err != nil {
			return err
		}
		for _, r := range sortedRepos(m) {
			fmt.Printf("%-20s %-28s 127.0.0.1:%d -> %d  %s\n", r.Name, r.Domain, r.HostPort, r.ContainerPort, r.URL)
		}
		return nil
	case "remove", "rm":
		return removeCommand(args[1:])
	case "pull":
		return gitCommand(args[1:], false)
	case "push":
		return gitCommand(args[1:], true)
	case "auth":
		return authCommand(args[1:])
	case "prepare":
		return prepareCommand(args[1:])
	case "generate":
		return generateCommand()
	case "install":
		return installCommand()
	case "uninstall":
		return uninstallCommand()
	case "serve":
		return serveCommand(args[1:])
	case "restart":
		return restartCommand(args[1:])
	case "stop":
		return stopCommand(args[1:])
	case "status":
		return statusCommand(args[1:])
	case "logs":
		return logsCommand(args[1:])
	case "doctor":
		return doctorCommand()
	case "deps":
		return depsCommand(args[1:])
	case "backup":
		return backupCommand(args[1:])
	case "restore":
		return restoreCommand(args[1:])
	case "dump":
		return dumpCommand(args[1:])
	case "run-app":
		return runAppCommand(args[1:])
	case "askpass":
		return askPassCommand(args[1:])
	case "help", "--help", "-h":
		usage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usage() {
	fmt.Print(`uppr v2 manages independent Docker applications with systemd and Caddy.

  uppr init <dir>                     create a launchpad
  uppr add <url> [--name N] [--branch B] [--domain D] [--port P] [--container-port P]
  uppr list | remove <name>          manage the repository list
  uppr auth login | status | logout  manage a GitHub token
  uppr pull [name]                    clone or fast-forward repositories
  uppr push <name> -m <message>       commit and push one repository
  uppr prepare [name]                 validate and fill app environments
  uppr generate                       render local systemd units and Caddyfile
  uppr install                        install units and shared Caddy config (Linux)
  uppr uninstall                      remove this pad's units and routes (Linux)
  uppr serve [path] [name]            build and start apps (Linux)
  uppr restart <name>                 build and restart one app (Linux)
  uppr stop [name] | status [name] | logs <name>
  uppr backup <archive.tar.gz>        stop active apps, snapshot, restart
  uppr restore <archive.tar.gz> <dir> extract a portable launchpad
  uppr deps check | install           check or install host dependencies
  uppr doctor                         check launchpad readiness
  uppr dump [dir]                     write UPPR.md integration guide
`)
}

func addCommand(args []string) error {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	name := fs.String("name", "", "repository name")
	domain := fs.String("domain", "", "Caddy hostname")
	port := fs.Int("port", 0, "host loopback port")
	containerPort := fs.Int("container-port", 0, "port inside container")
	branch := fs.String("branch", "", "Git branch")
	if len(args) == 0 {
		return errors.New("usage: uppr add <url> [options]")
	}
	url := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected add arguments")
	}
	root, m, err := findPad(".")
	if err != nil {
		return err
	}
	if *name == "" {
		*name = slug(strings.TrimSuffix(lastURLPart(url), ".git"))
	}
	if !safeName.MatchString(*name) {
		return errors.New("repo name must start with a letter and contain only lowercase letters, digits, and hyphens")
	}
	for _, r := range m.Repos {
		if r.Name == *name || r.URL == url {
			return errors.New("repo name or URL already exists")
		}
	}
	if *domain == "" {
		*domain = *name + ".localhost"
	}
	if err := validateDomain(*domain); err != nil {
		return err
	}
	if *port == 0 {
		*port = freePort(m)
	}
	if *port < 1 || *port > 65535 {
		return errors.New("invalid host port")
	}
	if *containerPort == 0 {
		*containerPort = 3000
	}
	if *containerPort < 1 || *containerPort > 65535 {
		return errors.New("invalid container port")
	}
	for _, r := range m.Repos {
		if r.HostPort == *port || r.Domain == *domain {
			return errors.New("host port or domain already assigned in this launchpad")
		}
	}
	m.Repos = append(m.Repos, Repo{Name: *name, URL: url, Branch: *branch, Domain: *domain, HostPort: *port, ContainerPort: *containerPort})
	if err := saveManifest(root, m); err != nil {
		return err
	}
	fmt.Printf("added %s; run `uppr pull %s`\n", *name, *name)
	return nil
}

func lastURLPart(url string) string {
	parts := strings.FieldsFunc(url, func(r rune) bool { return r == '/' || r == ':' })
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}
func freePort(m Manifest) int {
	used := map[int]bool{}
	for _, r := range m.Repos {
		used[r.HostPort] = true
	}
	if installed, err := readInstalledPads(); err == nil {
		for _, pad := range installed {
			other, err := loadManifest(pad.Root)
			if err != nil {
				continue
			}
			for _, r := range other.Repos {
				used[r.HostPort] = true
			}
		}
	}
	for p := 20000; p < 60000; p++ {
		if !used[p] {
			return p
		}
	}
	return 0
}

func removeCommand(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: uppr remove <name>")
	}
	root, m, err := findPad(".")
	if err != nil {
		return err
	}
	var kept []Repo
	found := false
	for _, r := range m.Repos {
		if r.Name == args[0] {
			found = true
		} else {
			kept = append(kept, r)
		}
	}
	if !found {
		return fmt.Errorf("unknown repo %q", args[0])
	}
	m.Repos = kept
	if err := saveManifest(root, m); err != nil {
		return err
	}
	fmt.Printf("removed %s from manifest; its files remain in repos/%s. Run `uppr install` to remove its service.\n", args[0], args[0])
	return nil
}

func oneOptionalName(args []string) (string, error) {
	if len(args) > 1 {
		return "", errors.New("expected at most one repo name")
	}
	if len(args) == 0 {
		return "", nil
	}
	return args[0], nil
}
