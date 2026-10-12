package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func testPad(t *testing.T) (string, Manifest) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "stack")
	if err := initPad(root); err != nil {
		t.Fatal(err)
	}
	m, err := loadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	return root, m
}

func testRepo(t *testing.T, root string, m Manifest, name string, port int) (Manifest, Repo) {
	t.Helper()
	r := Repo{Name: name, URL: "https://github.com/example/" + name + ".git", Domain: name + ".example.com", HostPort: port, ContainerPort: 3000}
	m.Repos = append(m.Repos, r)
	if err := saveManifest(root, m); err != nil {
		t.Fatal(err)
	}
	path := repoDir(root, r)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "Dockerfile"), []byte("FROM scratch\nEXPOSE 3000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "schema.json"), []byte(`{"variables":[{"name":"API_KEY","required":true}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return m, r
}

func TestPrepareRejectsMissingRequiredValueAndProtectsFiles(t *testing.T) {
	root, m := testPad(t)
	_, r := testRepo(t, root, m, "api", 20000)
	if err := prepareRepo(root, r, false); err == nil || !strings.Contains(err.Error(), "API_KEY") {
		t.Fatalf("missing required value: %v", err)
	}
	path := repoDir(root, r)
	if err := os.MkdirAll(filepath.Join(path, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "config", ".env"), []byte("API_KEY=abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prepareRepo(root, r, false); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".gitignore", ".dockerignore", "data/main.sqlite"} {
		if _, err := os.Stat(filepath.Join(path, name)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestPrepareRejectsTrackedRuntimeState(t *testing.T) {
	root, m := testPad(t)
	_, r := testRepo(t, root, m, "api", 20000)
	path := repoDir(root, r)
	if err := os.MkdirAll(filepath.Join(path, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "config", ".env"), []byte("API_KEY=secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "config/.env"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = path
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	if err := prepareRepo(root, r, false); err == nil || !strings.Contains(err.Error(), "tracked") {
		t.Fatalf("expected tracked file rejection, got %v", err)
	}
}

func TestDockerSocketPermissionErrorGivesRepairSteps(t *testing.T) {
	bin := t.TempDir()
	docker := filepath.Join(bin, "docker")
	if err := os.WriteFile(docker, []byte("#!/bin/sh\necho 'permission denied while trying to connect to the docker API at unix:///var/run/docker.sock' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	err := checkDockerAccess()
	if err == nil || !strings.Contains(err.Error(), "usermod -aG docker") || !strings.Contains(err.Error(), "sign out and back in") {
		t.Fatalf("expected actionable Docker socket error, got %v", err)
	}
}

func TestGeneratedUnitsArePerAppAndCaddyRoutesUseHostPorts(t *testing.T) {
	root, m := testPad(t)
	m, _ = testRepo(t, root, m, "api", 20000)
	m, _ = testRepo(t, root, m, "site", 20001)
	if err := generate(root, m); err != nil {
		t.Fatal(err)
	}
	for _, r := range m.Repos {
		body, err := os.ReadFile(filepath.Join(root, "systemd", unitName(m, r)))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(body, []byte("run-app")) || !bytes.Contains(body, []byte(r.Name)) {
			t.Fatalf("invalid unit %s", r.Name)
		}
	}
	caddy, err := os.ReadFile(filepath.Join(root, "Caddyfile"))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"api.example.com", "127.0.0.1:20000", "site.example.com", "127.0.0.1:20001"} {
		if !bytes.Contains(caddy, []byte(value)) {
			t.Fatalf("Caddyfile missing %s", value)
		}
	}
}

func TestGlobalCaddyRejectsCrossPadConflicts(t *testing.T) {
	root1, m1 := testPad(t)
	m1, _ = testRepo(t, root1, m1, "api", 20000)
	root2, m2 := testPad(t)
	m2, _ = testRepo(t, root2, m2, "site", 20000)
	_, err := renderGlobalCaddy(map[string]InstalledPad{m1.ID: {ID: m1.ID, Root: root1}, m2.ID: {ID: m2.ID, Root: root2}})
	if err == nil || !strings.Contains(err.Error(), "host port") {
		t.Fatalf("expected port conflict, got %v", err)
	}
}

func TestArchiveRoundTripAndRejectsTraversal(t *testing.T) {
	root, m := testPad(t)
	_, r := testRepo(t, root, m, "api", 20000)
	secret := filepath.Join(repoDir(root, r), "config", ".env")
	if err := os.MkdirAll(filepath.Dir(secret), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secret, []byte("API_KEY=private\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := writeArchive(root, &buf); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "backup.tar.gz")
	if err := os.WriteFile(archive, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "restored")
	if err := os.MkdirAll(dest, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := extractArchive(archive, dest); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dest, "repos", "api", "config", ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "API_KEY=private\n" {
		t.Fatalf("restored env = %q", data)
	}
	var malicious bytes.Buffer
	gz := gzip.NewWriter(&malicious)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "../outside", Mode: 0o600, Size: 1, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archive, malicious.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := extractArchive(archive, filepath.Join(t.TempDir(), "target")); err == nil {
		t.Fatal("expected traversal rejection")
	}
}
