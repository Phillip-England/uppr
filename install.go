package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const registryDir = "/etc/uppr/launchpads.d"
const unitDir = "/etc/systemd/system"

type InstalledPad struct {
	ID   string `json:"id"`
	Root string `json:"root"`
}

func installCommand() error {
	if err := requireLinux(); err != nil {
		return err
	}
	if activeUnit("caddy.service") && !activeUnit("uppr-caddy.service") {
		return errors.New("caddy.service is already active; stop it before installing the shared uppr-caddy.service")
	}
	root, m, err := findPad(".")
	if err != nil {
		return err
	}
	for _, r := range m.Repos {
		if err := prepareRepo(root, r, false); err != nil {
			return fmt.Errorf("%s: %w", r.Name, err)
		}
	}
	if err := generate(root, m); err != nil {
		return err
	}
	caddy, err := exec.LookPath("caddy")
	if err != nil {
		return errors.New("caddy is missing; run `uppr deps install`")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return errors.New("docker is missing; run `uppr deps install`")
	}
	installed, err := readInstalledPads()
	if err != nil {
		return err
	}
	installed[m.ID] = InstalledPad{ID: m.ID, Root: root}
	global, err := renderGlobalCaddy(installed)
	if err != nil {
		return err
	}
	for _, r := range m.Repos {
		if activeUnit(unitName(m, r)) {
			continue
		}
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", r.HostPort))
		if err != nil {
			return fmt.Errorf("host port %d for %s is already in use: %w", r.HostPort, r.Name, err)
		}
		listener.Close()
	}
	staging, err := os.MkdirTemp("", "uppr-install-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	globalPath := filepath.Join(staging, "Caddyfile")
	if err := os.WriteFile(globalPath, []byte(global), 0o644); err != nil {
		return err
	}
	if output, err := exec.Command(caddy, "validate", "--config", globalPath, "--adapter", "caddyfile").CombinedOutput(); err != nil {
		return fmt.Errorf("Caddy validation failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	registryData, _ := json.MarshalIndent(installed[m.ID], "", "  ")
	registryPath := filepath.Join(staging, m.ID+".json")
	if err := os.WriteFile(registryPath, registryData, 0o644); err != nil {
		return err
	}
	caddyUnit := filepath.Join(staging, "uppr-caddy.service")
	if err := os.WriteFile(caddyUnit, []byte(renderCaddyUnit(caddy)), 0o644); err != nil {
		return err
	}
	if err := privileged("mkdir", "-p", registryDir, "/var/lib/uppr/caddy"); err != nil {
		return err
	}
	// Remove units for apps no longer listed. The repository directories are kept.
	old, err := filepath.Glob(filepath.Join(unitDir, "uppr-"+m.ID+"-*.service"))
	if err != nil {
		return err
	}
	keep := map[string]bool{}
	changedActive := []string{}
	for _, r := range m.Repos {
		name := unitName(m, r)
		keep[name] = true
		if activeUnit(name) {
			oldBody, _ := os.ReadFile(filepath.Join(unitDir, name))
			newBody, err := os.ReadFile(filepath.Join(root, "systemd", name))
			if err != nil {
				return err
			}
			if !bytes.Equal(oldBody, newBody) {
				changedActive = append(changedActive, name)
			}
		}
	}
	for _, path := range old {
		if keep[filepath.Base(path)] {
			continue
		}
		name := filepath.Base(path)
		if err := privileged("systemctl", "disable", "--now", name); err != nil {
			return err
		}
		if err := privileged("rm", "-f", path); err != nil {
			return err
		}
	}
	for _, r := range m.Repos {
		name := unitName(m, r)
		if err := privileged("install", "-m", "0644", filepath.Join(root, "systemd", name), filepath.Join(unitDir, name)); err != nil {
			return err
		}
	}
	if err := privileged("install", "-m", "0644", registryPath, filepath.Join(registryDir, m.ID+".json")); err != nil {
		return err
	}
	if err := privileged("install", "-m", "0644", globalPath, "/etc/uppr/Caddyfile"); err != nil {
		return err
	}
	if err := privileged("install", "-m", "0644", caddyUnit, filepath.Join(unitDir, "uppr-caddy.service")); err != nil {
		return err
	}
	if err := privileged("systemctl", "daemon-reload"); err != nil {
		return err
	}
	for _, r := range m.Repos {
		if err := privileged("systemctl", "enable", unitName(m, r)); err != nil {
			return err
		}
	}
	for _, name := range changedActive {
		if err := privileged("systemctl", "restart", name); err != nil {
			return fmt.Errorf("restart changed unit %s: %w", name, err)
		}
	}
	if err := privileged("systemctl", "enable", "uppr-caddy.service"); err != nil {
		return err
	}
	if err := privileged("systemctl", "reload-or-restart", "uppr-caddy.service"); err != nil {
		return err
	}
	fmt.Printf("installed %d app units and refreshed shared Caddy routes\n", len(m.Repos))
	return nil
}

func uninstallCommand() error {
	if err := requireLinux(); err != nil {
		return err
	}
	_, m, err := findPad(".")
	if err != nil {
		return err
	}
	installed, err := readInstalledPads()
	if err != nil {
		return err
	}
	if _, ok := installed[m.ID]; !ok {
		return fmt.Errorf("launchpad %s is not installed", m.ID)
	}
	delete(installed, m.ID)
	global, err := renderGlobalCaddy(installed)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp("", "uppr-caddy-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(global); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if caddy, err := exec.LookPath("caddy"); err == nil && len(installed) > 0 {
		if output, err := exec.Command(caddy, "validate", "--config", tmp.Name(), "--adapter", "caddyfile").CombinedOutput(); err != nil {
			return fmt.Errorf("remaining Caddy routes are invalid: %w: %s", err, strings.TrimSpace(string(output)))
		}
	}
	units, err := filepath.Glob(filepath.Join(unitDir, "uppr-"+m.ID+"-*.service"))
	if err != nil {
		return err
	}
	for _, path := range units {
		if err := privileged("systemctl", "disable", "--now", filepath.Base(path)); err != nil {
			return err
		}
		if err := privileged("rm", "-f", path); err != nil {
			return err
		}
	}
	if err := privileged("rm", "-f", filepath.Join(registryDir, m.ID+".json")); err != nil {
		return err
	}
	if err := privileged("install", "-m", "0644", tmp.Name(), "/etc/uppr/Caddyfile"); err != nil {
		return err
	}
	if err := privileged("systemctl", "daemon-reload"); err != nil {
		return err
	}
	if len(installed) == 0 {
		if err := privileged("systemctl", "disable", "--now", "uppr-caddy.service"); err != nil {
			return err
		}
	} else {
		if err := privileged("systemctl", "reload-or-restart", "uppr-caddy.service"); err != nil {
			return err
		}
	}
	fmt.Println("uninstalled launchpad", m.ID)
	return nil
}

func readInstalledPads() (map[string]InstalledPad, error) {
	pads := map[string]InstalledPad{}
	entries, err := os.ReadDir(registryDir)
	if errors.Is(err, os.ErrNotExist) {
		return pads, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(registryDir, e.Name()))
		if err != nil {
			return nil, err
		}
		var p InstalledPad
		if err := json.Unmarshal(data, &p); err != nil {
			return nil, err
		}
		if !safeName.MatchString(p.ID) || !filepath.IsAbs(p.Root) {
			return nil, fmt.Errorf("invalid registry entry %s", e.Name())
		}
		pads[p.ID] = p
	}
	return pads, nil
}

func renderGlobalCaddy(pads map[string]InstalledPad) (string, error) {
	var b strings.Builder
	b.WriteString("# Generated by uppr. Do not edit directly.\n")
	ports := map[int]string{}
	domains := map[string]string{}
	for _, id := range sortedKeys(pads) {
		p := pads[id]
		m, err := loadManifest(p.Root)
		if err != nil {
			return "", fmt.Errorf("installed launchpad %s at %s: %w", id, p.Root, err)
		}
		if m.ID != id {
			return "", fmt.Errorf("launchpad ID mismatch at %s", p.Root)
		}
		for _, r := range sortedRepos(m) {
			if err := validateDomain(r.Domain); err != nil {
				return "", err
			}
			if old := ports[r.HostPort]; old != "" {
				return "", fmt.Errorf("host port %d used by %s and %s/%s", r.HostPort, old, id, r.Name)
			}
			if old := domains[r.Domain]; old != "" {
				return "", fmt.Errorf("domain %s used by %s and %s/%s", r.Domain, old, id, r.Name)
			}
			ports[r.HostPort] = id + "/" + r.Name
			domains[r.Domain] = id + "/" + r.Name
			fmt.Fprintf(&b, "\n%s {\n\treverse_proxy 127.0.0.1:%d\n}\n", r.Domain, r.HostPort)
		}
	}
	return b.String(), nil
}

func renderCaddyUnit(caddy string) string {
	return fmt.Sprintf(`[Unit]
Description=Uppr shared Caddy reverse proxy
After=network-online.target
Wants=network-online.target

[Service]
Type=notify
ExecStart=%s run --environ --config /etc/uppr/Caddyfile --adapter caddyfile
ExecReload=%s reload --config /etc/uppr/Caddyfile --adapter caddyfile
Restart=on-failure
AmbientCapabilities=CAP_NET_BIND_SERVICE
Environment=XDG_DATA_HOME=/var/lib/uppr/caddy

[Install]
WantedBy=multi-user.target
`, unitQuote(caddy), unitQuote(caddy))
}
