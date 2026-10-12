package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeBuildxTools(t *testing.T, dockerPackage, buildxOutput string) {
	t.Helper()
	bin := t.TempDir()
	tools := map[string]string{
		"docker":     "#!/bin/sh\nif [ \"$1\" = buildx ] && [ \"$2\" = version ]; then\n  " + buildxOutput + "\nfi\n",
		"apt-get":    "#!/bin/sh\nexit 0\n",
		"dpkg-query": "#!/bin/sh\nif [ \"$3\" = \"" + dockerPackage + "\" ]; then\n  printf 'install ok installed'\nelse\n  exit 1\nfi\n",
	}
	for name, body := range tools {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOCKER_BUILDKIT", "")
}

func TestEnsureBuildxAllowsLegacyBuildWithoutTerminal(t *testing.T) {
	for _, tc := range []struct{ dockerPackage, buildxPackage string }{
		{"docker.io", "docker-buildx"},
		{"docker-ce-cli", "docker-buildx-plugin"},
	} {
		t.Run(tc.dockerPackage, func(t *testing.T) {
			fakeBuildxTools(t, tc.dockerPackage, "echo 'buildx is missing' >&2; exit 1")
			stdin, err := os.Open(os.DevNull)
			if err != nil {
				t.Fatal(err)
			}
			defer stdin.Close()
			previous := os.Stdin
			os.Stdin = stdin
			defer func() { os.Stdin = previous }()
			if got := installedDockerBuildxPackage(); got != tc.buildxPackage {
				t.Fatalf("package = %q, want %q", got, tc.buildxPackage)
			}
			read, write, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			previousStderr := os.Stderr
			os.Stderr = write
			defer func() { os.Stderr = previousStderr }()
			if err := ensureBuildx(); err != nil {
				t.Fatalf("legacy build should remain available: %v", err)
			}
			write.Close()
			warning, err := io.ReadAll(read)
			read.Close()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(warning), "sudo apt-get install "+tc.buildxPackage) {
				t.Fatalf("warning did not name matching package: %s", warning)
			}
		})
	}
}

func TestEnsureBuildxAcceptsInstalledPlugin(t *testing.T) {
	fakeBuildxTools(t, "docker.io", "echo 'buildx 0.1'; exit 0")
	if err := ensureBuildx(); err != nil {
		t.Fatal(err)
	}
}
