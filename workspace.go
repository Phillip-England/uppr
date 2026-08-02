package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

const (
	workspacesDirEnv = "UPPR_WORKSPACES_DIR"
	workspacesDir    = "workspaces"
	workspacesFile   = "workspaces.conf"
)

type Workspace struct {
	Name string
	Path string
}

var nonWorkspaceName = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

func ensureServerFiles(root string) error {
	if err := requireEnvFile(root); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, "config"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, "data"), 0o755); err != nil {
		return err
	}
	if err := writeFileIfMissing(filepath.Join(root, workspacesFile), nil); err != nil {
		return err
	}
	if err := ensureAuthDBFile(root); err != nil {
		return err
	}
	return nil
}

func requireEnvFile(root string) error {
	envPath := filepath.Join(root, envFile)
	if info, err := os.Stat(envPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s is missing; create it before starting uppr (or run `uppr init %s`)", envPath, root)
		}
		return err
	} else if info.IsDir() {
		return fmt.Errorf("%s must be a file", envPath)
	}
	return nil
}

func createWorkspace(serverRoot, name string) (Workspace, error) {
	name = normalizeWorkspaceName(name)
	if name == "" {
		return Workspace{}, errors.New("workspace name is required")
	}
	workspaces, err := readWorkspaces(filepath.Join(serverRoot, workspacesFile))
	if err != nil {
		return Workspace{}, err
	}
	for _, workspace := range workspaces {
		if workspace.Name == name {
			return Workspace{}, fmt.Errorf("workspace %q already exists", name)
		}
	}
	baseDir, err := defaultWorkspacesRoot()
	if err != nil {
		return Workspace{}, err
	}
	workspace := Workspace{Name: name, Path: filepath.Join(baseDir, name)}
	root := workspace.Path
	if err := ensureWorkspaceFiles(root, filepath.Join(serverRoot, envFile)); err != nil {
		return Workspace{}, err
	}
	workspaces = append(workspaces, workspace)
	if err := writeWorkspaces(filepath.Join(serverRoot, workspacesFile), workspaces); err != nil {
		return Workspace{}, err
	}
	return workspace, nil
}

func defaultWorkspacesRoot() (string, error) {
	if dir := strings.TrimSpace(os.Getenv(workspacesDirEnv)); dir != "" {
		return filepath.Abs(dir)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determine workspace directory: %w", err)
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "uppr", workspacesDir), nil
	case "windows":
		if dir := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); dir != "" {
			return filepath.Join(dir, "uppr", workspacesDir), nil
		}
		if dir := strings.TrimSpace(os.Getenv("APPDATA")); dir != "" {
			return filepath.Join(dir, "uppr", workspacesDir), nil
		}
		return filepath.Join(home, "AppData", "Local", "uppr", workspacesDir), nil
	default:
		if dir := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); dir != "" {
			return filepath.Join(dir, "uppr", workspacesDir), nil
		}
		return filepath.Join(home, ".local", "share", "uppr", workspacesDir), nil
	}
}

func ensureWorkspaceFiles(root, serverEnvPath string) error {
	if err := os.MkdirAll(filepath.Join(root, "config"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, "data"), 0o755); err != nil {
		return err
	}
	if err := writeFileIfMissing(filepath.Join(root, reposFile), nil); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(root, envFile)); errors.Is(err, os.ErrNotExist) {
		if contents, readErr := os.ReadFile(serverEnvPath); readErr == nil {
			if writeErr := os.WriteFile(filepath.Join(root, envFile), contents, 0o600); writeErr != nil {
				return writeErr
			}
		} else if !errors.Is(readErr, os.ErrNotExist) {
			return readErr
		}
	}
	if err := writeFileIfMissing(filepath.Join(root, envFile), defaultEnvContents()); err != nil {
		return err
	}
	return ensureEnvDefaults(filepath.Join(root, envFile))
}

func readWorkspaces(path string) ([]Workspace, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%s is missing; run `uppr serve` first", path)
		}
		return nil, err
	}
	defer file.Close()

	var workspaces []Workspace
	var current *Workspace
	scanner := bufio.NewScanner(file)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line == "[workspace]" {
			workspaces = append(workspaces, Workspace{})
			current = &workspaces[len(workspaces)-1]
			continue
		}
		if current == nil {
			return nil, fmt.Errorf("%s:%d: expected [workspace] before settings", path, lineNumber)
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("%s:%d: expected key = value", path, lineNumber)
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch key {
		case "name":
			current.Name = normalizeWorkspaceName(value)
		case "path":
			current.Path = filepath.Clean(value)
		default:
			return nil, fmt.Errorf("%s:%d: unknown workspace setting %q", path, lineNumber, key)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	for i := range workspaces {
		if workspaces[i].Name == "" {
			return nil, fmt.Errorf("workspace %d is missing name", i)
		}
		if workspaces[i].Path == "" || workspaces[i].Path == "." {
			workspaces[i].Path = filepath.Join(workspacesDir, workspaces[i].Name)
		}
	}
	return workspaces, nil
}

func writeWorkspaces(path string, workspaces []Workspace) error {
	var builder strings.Builder
	for i, workspace := range workspaces {
		if i > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString("[workspace]\n")
		fmt.Fprintf(&builder, "name = %s\n", workspace.Name)
		fmt.Fprintf(&builder, "path = %s\n", filepath.ToSlash(workspace.Path))
	}
	return os.WriteFile(path, []byte(builder.String()), 0o600)
}

func resolveWorkspace(serverRoot, name string) (Workspace, string, error) {
	name = normalizeWorkspaceName(name)
	workspaces, err := readWorkspaces(filepath.Join(serverRoot, workspacesFile))
	if err != nil {
		return Workspace{}, "", err
	}
	for _, workspace := range workspaces {
		if workspace.Name != name {
			continue
		}
		root := workspace.Path
		if !filepath.IsAbs(root) {
			root = filepath.Join(serverRoot, root)
		}
		return workspace, root, nil
	}
	return Workspace{}, "", fmt.Errorf("workspace %q not found", name)
}

func deleteWorkspace(serverRoot, name string) error {
	name = normalizeWorkspaceName(name)
	workspaces, err := readWorkspaces(filepath.Join(serverRoot, workspacesFile))
	if err != nil {
		return err
	}
	kept := make([]Workspace, 0, len(workspaces))
	var removed bool
	for _, workspace := range workspaces {
		if workspace.Name == name {
			removed = true
			continue
		}
		kept = append(kept, workspace)
	}
	if !removed {
		return fmt.Errorf("workspace %q not found", name)
	}
	return writeWorkspaces(filepath.Join(serverRoot, workspacesFile), kept)
}

func normalizeWorkspaceName(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	name = strings.Trim(nonWorkspaceName.ReplaceAllString(name, "-"), "-_")
	return name
}
