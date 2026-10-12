package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

type EnvSchema struct {
	Variables []EnvVariable `json:"variables"`
}
type EnvVariable struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Example     string `json:"example,omitempty"`
	Required    *bool  `json:"required,omitempty"`
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func required(v EnvVariable) bool { return v.Required == nil || *v.Required }
func readSchema(path string) (EnvSchema, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return EnvSchema{}, err
	}
	var s EnvSchema
	if err := json.Unmarshal(data, &s); err != nil {
		return s, err
	}
	if s.Variables == nil {
		return s, errors.New("schema.json needs a variables array")
	}
	seen := map[string]bool{}
	for _, v := range s.Variables {
		if !envName.MatchString(v.Name) || seen[v.Name] {
			return s, fmt.Errorf("invalid or duplicate schema variable %q", v.Name)
		}
		seen[v.Name] = true
	}
	return s, nil
}

func envValues(data []byte) map[string]string {
	values := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		values[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), "\"'")
	}
	return values
}

func prepareCommand(args []string) error {
	name, err := oneOptionalName(args)
	if err != nil {
		return err
	}
	root, m, err := findPad(".")
	if err != nil {
		return err
	}
	repos, err := selectRepos(m, name)
	if err != nil {
		return err
	}
	for _, r := range repos {
		if err := prepareRepo(root, r, true); err != nil {
			return fmt.Errorf("%s: %w", r.Name, err)
		}
	}
	return nil
}

func prepareRepo(root string, r Repo, prompt bool) error {
	path := repoDir(root, r)
	if _, err := os.Stat(filepath.Join(path, "Dockerfile")); err != nil {
		return fmt.Errorf("Dockerfile is missing; run `uppr pull %s`: %w", r.Name, err)
	}
	if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
		tracked, err := gitOutput(path, "ls-files", "--", "config", "data")
		if err != nil {
			return err
		}
		if len(strings.TrimSpace(string(tracked))) > 0 {
			return fmt.Errorf("protected files are tracked by Git: %s; remove them from the index before preparing", strings.TrimSpace(string(tracked)))
		}
	}
	schema, err := readSchema(filepath.Join(path, "schema.json"))
	if err != nil {
		return fmt.Errorf("schema.json: %w", err)
	}
	if err := protectRepo(path); err != nil {
		return err
	}
	if err := protectDockerContext(path); err != nil {
		return err
	}
	config := filepath.Join(path, "config")
	dataDir := filepath.Join(path, "data")
	if err := os.MkdirAll(config, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	db := filepath.Join(dataDir, "main.sqlite")
	if file, err := os.OpenFile(db, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600); err == nil {
		file.Close()
	} else if !errors.Is(err, os.ErrExist) {
		return err
	}
	envPath := filepath.Join(config, ".env")
	content, err := os.ReadFile(envPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	values := envValues(content)
	missing := []string{}
	reader := bufio.NewReader(os.Stdin)
	for _, v := range schema.Variables {
		if !required(v) || strings.TrimSpace(values[v.Name]) != "" {
			continue
		}
		if !prompt || !terminalInput() {
			missing = append(missing, v.Name)
			continue
		}
		fmt.Fprintf(os.Stderr, "%s for %s", v.Name, r.Name)
		if v.Description != "" {
			fmt.Fprintf(os.Stderr, " — %s", v.Description)
		}
		if v.Example != "" {
			fmt.Fprintf(os.Stderr, " (example: %s)", v.Example)
		}
		fmt.Fprint(os.Stderr, ": ")
		value, err := readHiddenLine(reader)
		if err != nil {
			return err
		}
		if value == "" {
			missing = append(missing, v.Name)
			continue
		}
		if strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("%s contains an unsupported control character", v.Name)
		}
		values[v.Name] = value
		content = setEnvValue(content, v.Name, value)
	}
	if len(missing) > 0 {
		return fmt.Errorf("required values missing in config/.env: %s; run `uppr prepare %s` interactively", strings.Join(missing, ", "), r.Name)
	}
	if _, err := os.Stat(envPath); errors.Is(err, os.ErrNotExist) || len(content) > 0 {
		if err := os.WriteFile(envPath, content, 0o600); err != nil {
			return err
		}
	}
	if err := os.Chmod(envPath, 0o600); err != nil {
		return err
	}
	fmt.Println("prepared", r.Name)
	return nil
}

func setEnvValue(content []byte, key, value string) []byte {
	lines := strings.Split(string(content), "\n")
	found := false
	for i, line := range lines {
		name, _, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && strings.TrimSpace(name) == key {
			lines[i] = key + "=" + value
			found = true
		}
	}
	if found {
		return []byte(strings.Join(lines, "\n"))
	}
	if len(content) > 0 && content[len(content)-1] != '\n' {
		content = append(content, '\n')
	}
	return append(content, []byte(key+"="+value+"\n")...)
}
func terminalInput() bool {
	info, err := os.Stdin.Stat()
	return err == nil && (info.Mode()&os.ModeCharDevice) != 0
}
func readHiddenLine(reader *bufio.Reader) (string, error) {
	cmd := exec.Command("stty", "-echo")
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return "", err
	}
	defer func() {
		restore := exec.Command("stty", "echo")
		restore.Stdin = os.Stdin
		_ = restore.Run()
		fmt.Fprintln(os.Stderr)
	}()
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func protectDockerContext(path string) error {
	file := filepath.Join(path, ".dockerignore")
	content, err := os.ReadFile(file)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	block := "# uppr: never include runtime state in build context\nconfig/\ndata/\n.git/\n"
	if strings.Contains(string(content), block) {
		return nil
	}
	if len(content) > 0 && content[len(content)-1] != '\n' {
		content = append(content, '\n')
	}
	return os.WriteFile(file, append(content, []byte(block)...), 0o644)
}
