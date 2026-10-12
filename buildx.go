package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ensureBuildx runs once before serve or restart begins building images.
// A missing plugin is repaired only with an explicit answer from an interactive user.
// Legacy builds can still proceed if the user declines or no terminal is available.
func ensureBuildx() error {
	if os.Getenv("DOCKER_BUILDKIT") == "0" {
		fmt.Fprintln(os.Stderr, "BuildKit is disabled by DOCKER_BUILDKIT=0; unset it to use Buildx")
		return nil
	}
	if output, err := exec.Command("docker", "buildx", "version").CombinedOutput(); err == nil {
		return nil
	} else {
		packageName := installedDockerBuildxPackage()
		if packageName == "" {
			fmt.Fprintf(os.Stderr, "Docker Buildx is unavailable (%s); install the Buildx package for your Docker distribution, then verify with `docker buildx version`. Continuing with the legacy builder.\n", buildxError(output, err))
			return nil
		}
		install := "sudo apt-get install " + packageName
		if !terminalInput() {
			fmt.Fprintf(os.Stderr, "Docker Buildx is unavailable (%s); run `%s` to enable BuildKit. Continuing with the legacy builder.\n", buildxError(output, err), install)
			return nil
		}
		fmt.Fprintf(os.Stderr, "Docker Buildx is unavailable; install %s now with `%s` to use BuildKit? [y/N] ", packageName, install)
		answer, readErr := bufio.NewReader(os.Stdin).ReadString('\n')
		if readErr != nil {
			return fmt.Errorf("could not read Buildx install confirmation: %w", readErr)
		}
		if strings.ToLower(strings.TrimSpace(answer)) != "y" && strings.ToLower(strings.TrimSpace(answer)) != "yes" {
			fmt.Fprintf(os.Stderr, "Buildx installation declined; run `%s` later. Continuing with the legacy builder.\n", install)
			return nil
		}
		if err := privileged("apt-get", "install", "-y", packageName); err != nil {
			return fmt.Errorf("install Buildx: %w; run `%s` and retry", err, install)
		}
		if output, err := exec.Command("docker", "buildx", "version").CombinedOutput(); err != nil {
			return fmt.Errorf("Buildx is still unavailable after installing %s (%s)", packageName, buildxError(output, err))
		}
		return nil
	}
}

func buildxError(output []byte, err error) string {
	message := strings.TrimSpace(string(output))
	if message != "" {
		return message
	}
	return err.Error()
}

func installedDockerBuildxPackage() string {
	if _, err := exec.LookPath("apt-get"); err != nil {
		return ""
	}
	for _, candidate := range []struct{ docker, buildx string }{
		{"docker-ce-cli", "docker-buildx-plugin"},
		{"docker.io", "docker-buildx"},
	} {
		output, err := exec.Command("dpkg-query", "-W", "-f=${Status}", candidate.docker).Output()
		if err == nil && strings.TrimSpace(string(output)) == "install ok installed" {
			return candidate.buildx
		}
	}
	return ""
}
