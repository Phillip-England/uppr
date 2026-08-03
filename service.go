package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
)

func printUpprService(args []string) error {
	root, err := serviceRoot(args, "usage: uppr service-uppr [path]")
	if err != nil {
		return err
	}
	if err := bootstrapServiceRoot(root); err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, _ = filepath.Abs(executable)
	account, err := currentServiceAccount()
	if err != nil {
		return err
	}
	fmt.Print(renderUpprSystemdService(root, executable, account))
	return nil
}

func printCaddyService(args []string) error {
	root, err := serviceRoot(args, "usage: uppr service-caddy [path]")
	if err != nil {
		return err
	}
	if err := bootstrapServiceRoot(root); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, "data", "caddy"), 0o755); err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, _ = filepath.Abs(executable)
	caddy, err := findCaddyx()
	if err != nil {
		return err
	}
	account, err := currentServiceAccount()
	if err != nil {
		return err
	}
	fmt.Print(renderCaddySystemdService(root, executable, caddy, account))
	return nil
}

func installCaddyx(args []string) error {
	if len(args) > 1 {
		return errors.New("usage: uppr install-caddyx [path]")
	}
	target := ""
	if len(args) == 1 {
		target = args[0]
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		target = filepath.Join(home, ".local", "bin", "caddyx")
	}
	target, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		return errors.New("go is required to build caddyx")
	}
	cmd := exec.Command(goBin, "run", "github.com/caddyserver/xcaddy/cmd/xcaddy@latest", "build", "--with", "github.com/mholt/caddy-ratelimit@latest", "--output", target)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build caddyx: %w", err)
	}
	if err := os.Chmod(target, 0o755); err != nil {
		return err
	}
	fmt.Printf("installed caddyx at %s\n", target)
	return nil
}

func findCaddyx() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("CADDYX_PATH")); configured != "" {
		return filepath.Abs(configured)
	}
	if path, err := exec.LookPath("caddyx"); err == nil {
		return filepath.Abs(path)
	}
	home, err := os.UserHomeDir()
	if err == nil {
		path := filepath.Join(home, ".local", "bin", "caddyx")
		if _, statErr := os.Stat(path); statErr == nil {
			return path, nil
		}
	}
	return "", errors.New("caddyx not found; run `uppr install-caddyx` first or set CADDYX_PATH")
}

func reloadCaddy(root string) error {
	caddy, err := findCaddyx()
	if err != nil {
		fmt.Fprintln(os.Stderr, "uppr: apps launched; Caddy was not reloaded:", err)
		return nil
	}
	cmd := exec.Command(caddy, "reload", "--config", filepath.Join(root, caddyFile), "--adapter", "caddyfile")
	output, reloadErr := cmd.CombinedOutput()
	if reloadErr != nil {
		// A first launch has no admin API to reload. Start Caddy with the newly
		// generated configuration in that case; subsequent launches stay on the
		// zero-downtime reload path above.
		start := exec.Command(caddy, "start", "--config", filepath.Join(root, caddyFile), "--adapter", "caddyfile")
		start.Stdout, start.Stderr = os.Stdout, os.Stderr
		if startErr := start.Run(); startErr != nil {
			detail := strings.TrimSpace(string(output))
			if detail != "" {
				return fmt.Errorf("apps launched but Caddy could neither reload (%v: %s) nor start: %w", reloadErr, detail, startErr)
			}
			return fmt.Errorf("apps launched but Caddy could neither reload (%v) nor start: %w", reloadErr, startErr)
		}
	} else if len(output) != 0 {
		_, _ = os.Stdout.Write(output)
	}
	return nil
}

func runService(args []string, serve func([]string) error) error {
	root, err := serviceRoot(args, "usage: uppr service-run [path]")
	if err != nil {
		return err
	}
	if err := ensureServerFiles(root); err != nil {
		return err
	}
	if err := generateServerFilesAt(root); err != nil {
		return err
	}
	cmd := exec.Command("docker", "compose", "--env-file", filepath.Join(root, envFile), "up", "--build", "-d", "--remove-orphans")
	cmd.Dir = root
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	return serve([]string{"--addr", "0.0.0.0:9944", root})
}

func serviceRoot(args []string, usage string) (string, error) {
	if len(args) > 1 {
		return "", errors.New(usage)
	}
	root := "."
	if len(args) == 1 {
		root = args[0]
	}
	return filepath.Abs(root)
}

func bootstrapServiceRoot(root string) error {
	if err := os.MkdirAll(filepath.Join(root, "config"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, "data"), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(root, envFile)); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(filepath.Join(root, envFile), defaultEnvContents(), 0o600); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err := ensureEnvDefaults(filepath.Join(root, envFile)); err != nil {
		return err
	}
	values, err := readDotEnv(filepath.Join(root, envFile))
	if err != nil {
		return err
	}
	if strings.TrimSpace(values[workspacesDirEnv]) == "" {
		if err := writeDotEnvValues(filepath.Join(root, envFile), []string{workspacesDirEnv}, map[string]string{
			workspacesDirEnv: filepath.Join(root, "data", workspacesDir),
		}); err != nil {
			return err
		}
	}
	if _, err := os.Stat(filepath.Join(root, workspacesFile)); errors.Is(err, os.ErrNotExist) {
		return os.WriteFile(filepath.Join(root, workspacesFile), nil, 0o600)
	} else {
		return err
	}
}

func envOrDefault(values map[string]string, key, fallback string) string {
	if value := strings.TrimSpace(values[key]); value != "" {
		return value
	}
	return fallback
}

type serviceAccount struct {
	User  string
	Group string
	Home  string
}

func currentServiceAccount() (serviceAccount, error) {
	current, err := user.Current()
	if err != nil {
		return serviceAccount{}, fmt.Errorf("determine service user: %w", err)
	}
	group, err := user.LookupGroupId(current.Gid)
	if err != nil {
		return serviceAccount{}, fmt.Errorf("determine service group: %w", err)
	}
	return serviceAccount{User: current.Username, Group: group.Name, Home: current.HomeDir}, nil
}

func renderUpprSystemdService(root, executable string, account serviceAccount) string {
	q := func(value string) string {
		return strconv.Quote(strings.ReplaceAll(value, "%", "%%"))
	}
	pathValue := func(value string) string {
		var builder strings.Builder
		for _, char := range []byte(value) {
			if char == '%' {
				builder.WriteString("%%")
			} else if char == '/' || char == '.' || char == '_' || char == '-' ||
				(char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') {
				builder.WriteByte(char)
			} else {
				fmt.Fprintf(&builder, `\x%02x`, char)
			}
		}
		return builder.String()
	}
	return fmt.Sprintf(`[Unit]
Description=Uppr deployment manager
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=%s
Group=%s
Environment=HOME=%s
UMask=0022
Restart=always
RestartSec=5
WorkingDirectory=%s
EnvironmentFile=%s
ExecStart=%s service-run %s

[Install]
WantedBy=multi-user.target
`, account.User, account.Group, q(account.Home), pathValue(root), pathValue(filepath.Join(root, envFile)), q(executable), q(root))
}

func renderCaddySystemdService(root, executable, caddy string, account serviceAccount) string {
	q := func(value string) string { return strconv.Quote(strings.ReplaceAll(value, "%", "%%")) }
	return fmt.Sprintf(`[Unit]
Description=Uppr Caddy reverse proxy
After=network-online.target uppr.service
Wants=network-online.target

[Service]
Type=notify
User=%s
Group=%s
Environment=HOME=%s
UMask=0022
ExecStartPre=%s generate-server %s
ExecStart=%s run --environ --config %s --adapter caddyfile
ExecReload=%s reload --config %s --adapter caddyfile --force
TimeoutStopSec=5s
LimitNOFILE=1048576
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
PrivateTmp=true
ProtectSystem=full
ReadWritePaths=%s %s
Environment=XDG_DATA_HOME=%s
Environment=XDG_CONFIG_HOME=%s

[Install]
WantedBy=multi-user.target
`, account.User, account.Group, q(account.Home), q(executable), q(root), q(caddy), q(filepath.Join(root, caddyFile)), q(caddy), q(filepath.Join(root, caddyFile)),
		q(root), q(filepath.Join(root, "data", "caddy")), q(filepath.Join(root, "data", "caddy")), q(filepath.Join(root, "data", "caddy")))
}
