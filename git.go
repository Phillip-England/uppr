package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Credentials struct {
	Token string `json:"token"`
}

func credentialPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "uppr", "credentials.json"), nil
}
func readCredentials() (Credentials, error) {
	path, err := credentialPath()
	if err != nil {
		return Credentials{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Credentials{}, nil
	}
	if err != nil {
		return Credentials{}, err
	}
	var c Credentials
	err = json.Unmarshal(data, &c)
	return c, err
}
func authCommand(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: uppr auth login|status|logout")
	}
	path, err := credentialPath()
	if err != nil {
		return err
	}
	switch args[0] {
	case "status":
		c, err := readCredentials()
		if err != nil {
			return err
		}
		if c.Token == "" {
			fmt.Println("no GitHub token saved")
		} else {
			fmt.Println("GitHub token saved")
		}
		return nil
	case "logout":
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		fmt.Println("GitHub token removed")
		return nil
	case "login":
		fmt.Fprint(os.Stderr, "GitHub personal access token: ")
		if stdinInfo, err := os.Stdin.Stat(); err == nil && (stdinInfo.Mode()&os.ModeCharDevice) != 0 {
			cmd := exec.Command("stty", "-echo")
			cmd.Stdin = os.Stdin
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("disable terminal echo: %w", err)
			}
			defer func() {
				restore := exec.Command("stty", "echo")
				restore.Stdin = os.Stdin
				_ = restore.Run()
				fmt.Fprintln(os.Stderr)
			}()
		}
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && err != io.EOF {
			return err
		}
		token := strings.TrimSpace(line)
		if token == "" {
			return errors.New("token cannot be empty")
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		data, _ := json.Marshal(Credentials{Token: token})
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return err
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return err
		}
		fmt.Println("GitHub token saved at", path)
		return nil
	default:
		return errors.New("usage: uppr auth login|status|logout")
	}
}

func askPassCommand(args []string) error {
	if len(args) == 0 {
		return nil
	}
	if strings.Contains(strings.ToLower(args[0]), "username") {
		fmt.Println("x-access-token")
	} else if strings.Contains(strings.ToLower(args[0]), "password") {
		fmt.Println(os.Getenv("UPPR_GIT_TOKEN"))
	}
	return nil
}

func gitRun(dir string, args ...string) error {
	c, err := readCredentials()
	if err != nil {
		return err
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if c.Token != "" {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		askDir, err := os.MkdirTemp("", "uppr-askpass-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(askDir)
		askPath := filepath.Join(askDir, "askpass.sh")
		body := "#!/bin/sh\nexec " + shellQuote(exe) + " askpass \"$@\"\n"
		if err := os.WriteFile(askPath, []byte(body), 0o700); err != nil {
			return err
		}
		cmd.Env = append(os.Environ(), "GIT_ASKPASS="+askPath, "GIT_TERMINAL_PROMPT=0", "UPPR_GIT_TOKEN="+c.Token)
	}
	return cmd.Run()
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

func gitOutput(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	return cmd.Output()
}

func gitCommand(args []string, push bool) error {
	root, m, err := findPad(".")
	if err != nil {
		return err
	}
	if push {
		if len(args) < 3 || args[1] != "-m" || strings.TrimSpace(strings.Join(args[2:], " ")) == "" {
			return errors.New("usage: uppr push <name> -m <message>")
		}
		r, err := selectRepos(m, args[0])
		if err != nil {
			return err
		}
		return pushRepo(root, r[0], strings.Join(args[2:], " "))
	}
	name, err := oneOptionalName(args)
	if err != nil {
		return err
	}
	repos, err := selectRepos(m, name)
	if err != nil {
		return err
	}
	for _, r := range repos {
		if err := pullRepo(root, r); err != nil {
			return fmt.Errorf("%s: %w", r.Name, err)
		}
	}
	return nil
}

func pullRepo(root string, r Repo) error {
	path := repoDir(root, r)
	if _, err := os.Stat(filepath.Join(path, ".git")); errors.Is(err, os.ErrNotExist) {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s exists without .git", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		args := []string{"clone"}
		if r.Branch != "" {
			args = append(args, "--branch", r.Branch)
		}
		args = append(args, "--", r.URL, path)
		if err := gitRun(root, args...); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		if err := gitRun(path, "pull", "--ff-only"); err != nil {
			return err
		}
	}
	if err := protectRepo(path); err != nil {
		return err
	}
	return os.MkdirAll(filepath.Join(path, "data"), 0o700)
}

const protectedBlock = "# uppr: local configuration and data\n/config/\n/data/\n"

func protectRepo(path string) error {
	ignore := filepath.Join(path, ".gitignore")
	content, err := os.ReadFile(ignore)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if strings.Contains(string(content), protectedBlock) {
		return nil
	}
	if len(content) > 0 && content[len(content)-1] != '\n' {
		content = append(content, '\n')
	}
	content = append(content, []byte(protectedBlock)...)
	return os.WriteFile(ignore, content, 0o644)
}

func pushRepo(root string, r Repo, message string) error {
	path := repoDir(root, r)
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		return fmt.Errorf("%s is not cloned", r.Name)
	}
	if err := protectRepo(path); err != nil {
		return err
	}
	tracked, err := gitOutput(path, "ls-files", "--", "config", "data")
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(tracked))) > 0 {
		return fmt.Errorf("refusing to push: protected files are tracked:\n%s\nUntrack them with git rm -r --cached -- config data", strings.TrimSpace(string(tracked)))
	}
	status, err := gitOutput(path, "status", "--short")
	if err != nil {
		return err
	}
	fmt.Printf("[%s] changes to commit and push:\n%s", r.Name, status)
	if len(status) == 0 {
		fmt.Println("(no local changes; push any existing commits)")
	}
	if !terminalInput() {
		return errors.New("push requires an interactive confirmation")
	}
	fmt.Fprint(os.Stderr, "Continue? [y/N] ")
	answer, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return err
	}
	if strings.TrimSpace(strings.ToLower(answer)) != "y" {
		return errors.New("push canceled")
	}
	if err := gitRun(path, "add", "-A"); err != nil {
		return err
	}
	if output, err := gitOutput(path, "diff", "--cached", "--name-only"); err != nil {
		return err
	} else if len(strings.TrimSpace(string(output))) > 0 {
		if err := gitRun(path, "commit", "-m", message); err != nil {
			return err
		}
	}
	return gitRun(path, "push")
}
