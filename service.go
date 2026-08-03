package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func printService(args []string) error {
	root, err := serviceRoot(args, "usage: uppr service [path]")
	if err != nil {
		return err
	}
	if err := bootstrapServiceRoot(root); err != nil {
		return err
	}
	values, err := readDotEnv(filepath.Join(root, envFile))
	if err != nil {
		return err
	}
	docker, err := exec.LookPath("docker")
	if err != nil {
		return errors.New("docker is required to install the Uppr service")
	}
	docker, _ = filepath.Abs(docker)
	image := envOrDefault(values, "UPPR_DOCKER_IMAGE", "uppr:local")
	network := envOrDefault(values, "UPPR_DOCKER_NETWORK", "uppr-network")
	if err := writeServerFilesAt(root); err != nil {
		return err
	}

	fmt.Print(renderSystemdService(root, docker, image, network))
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

func renderSystemdService(root, docker, image, network string) string {
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
Requires=docker.service
After=docker.service network-online.target
Wants=network-online.target

[Service]
Type=simple
Restart=always
RestartSec=5
WorkingDirectory=%s
ExecStartPre=-%s network create %s
ExecStart=%s compose --env-file %s --project-directory %s up --build --remove-orphans
ExecStop=%s compose --env-file %s --project-directory %s down

[Install]
WantedBy=multi-user.target
	`, pathValue(root), q(docker), q(network), q(docker), q(filepath.Join(root, envFile)), q(root),
		q(docker), q(filepath.Join(root, envFile)), q(root))
}
