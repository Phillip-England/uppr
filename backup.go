package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const backupFormatVersion = 1

type backupManifest struct {
	Version    int      `json:"version"`
	CreatedAt  string   `json:"created_at"`
	Kind       string   `json:"kind"`
	Workspaces []string `json:"workspaces,omitempty"`
}

type backupSource struct {
	DiskPath    string
	ArchivePath string
}

func backupState(args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return errors.New("usage: uppr backup <file> [path]")
	}
	root := "."
	if len(args) == 2 {
		root = args[1]
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	artifact, err := filepath.Abs(args[0])
	if err != nil {
		return err
	}
	manifest, sources, err := backupSources(absRoot)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(artifact), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(artifact), ".uppr-backup-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	gz := gzip.NewWriter(tmp)
	tw := tar.NewWriter(gz)
	manifest.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	manifestBytes, _ := json.MarshalIndent(manifest, "", "  ")
	if err = writeTarBytes(tw, "manifest.json", manifestBytes, 0o600); err == nil {
		for _, source := range sources {
			if samePath(source.DiskPath, artifact) || samePath(source.DiskPath, tmpName) {
				continue
			}
			if err = addPathToTar(tw, source, artifact, tmpName); err != nil {
				break
			}
		}
	}
	if closeErr := tw.Close(); err == nil {
		err = closeErr
	}
	if closeErr := gz.Close(); err == nil {
		err = closeErr
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(tmpName, artifact); err != nil {
		return err
	}
	fmt.Printf("wrote backup %s\n", artifact)
	return nil
}

func backupSources(root string) (backupManifest, []backupSource, error) {
	manifest := backupManifest{Version: backupFormatVersion, Kind: "project"}
	if _, err := os.Stat(filepath.Join(root, workspacesFile)); err == nil {
		manifest.Kind = "server"
		workspaces, err := readWorkspaces(filepath.Join(root, workspacesFile))
		if err != nil {
			return manifest, nil, err
		}
		sources := []backupSource{{root, "server"}}
		for _, ws := range workspaces {
			wsRoot := ws.Path
			if !filepath.IsAbs(wsRoot) {
				wsRoot = filepath.Join(root, wsRoot)
			}
			manifest.Workspaces = append(manifest.Workspaces, ws.Name)
			sources = append(sources, backupSource{wsRoot, path.Join("workspaces", ws.Name)})
		}
		return manifest, sources, nil
	}
	if _, err := os.Stat(filepath.Join(root, reposFile)); err != nil {
		return manifest, nil, fmt.Errorf("%s is not an initialized uppr project or server", root)
	}
	return manifest, []backupSource{{root, "project"}}, nil
}

func addPathToTar(tw *tar.Writer, source backupSource, excludes ...string) error {
	return filepath.Walk(source.DiskPath, func(file string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		for _, exclude := range excludes {
			if samePath(file, exclude) {
				return nil
			}
		}
		rel, err := filepath.Rel(source.DiskPath, file)
		if err != nil {
			return err
		}
		name := source.ArchivePath
		if rel != "." {
			name = path.Join(name, filepath.ToSlash(rel))
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = name
		if info.Mode()&os.ModeSymlink != 0 {
			header.Linkname, err = os.Readlink(file)
			if err != nil {
				return err
			}
		}
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(file)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tw, f)
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}

func writeTarBytes(tw *tar.Writer, name string, contents []byte, mode int64) error {
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(contents)), ModTime: time.Now()}); err != nil {
		return err
	}
	_, err := tw.Write(contents)
	return err
}

func restoreState(args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return errors.New("usage: uppr restore <file> [path]")
	}
	root := "."
	if len(args) == 2 {
		root = args[1]
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	artifact, err := filepath.Abs(args[0])
	if err != nil {
		return err
	}
	staging, err := os.MkdirTemp("", "uppr-restore-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	manifest, err := extractBackup(artifact, staging)
	if err != nil {
		return err
	}
	if manifest.Version != backupFormatVersion {
		return fmt.Errorf("unsupported backup format version %d", manifest.Version)
	}
	if err := os.MkdirAll(absRoot, 0o755); err != nil {
		return err
	}
	switch manifest.Kind {
	case "project":
		if err := copyTree(filepath.Join(staging, "project"), absRoot); err != nil {
			return err
		}
	case "server":
		if err := copyTree(filepath.Join(staging, "server"), absRoot); err != nil {
			return err
		}
		// A server backup may contain an absolute UPPR_WORKSPACES_DIR from the
		// source machine. Keeping it would make a restored runtime depend on the
		// old directory (and can even write restored state back into it during a
		// same-machine migration). Server restores are therefore self-contained:
		// their workspace directory moves with the destination runtime root.
		workspaceRoot := filepath.Join(absRoot, "data", workspacesDir)
		if err := writeDotEnvValues(filepath.Join(absRoot, envFile), []string{workspacesDirEnv}, map[string]string{
			workspacesDirEnv: workspaceRoot,
		}); err != nil {
			return err
		}
		var restored []Workspace
		seen := make(map[string]bool)
		sort.Strings(manifest.Workspaces)
		for _, name := range manifest.Workspaces {
			if name == "" || normalizeWorkspaceName(name) != name || seen[name] {
				return fmt.Errorf("invalid workspace name %q in backup", name)
			}
			seen[name] = true
			destination := filepath.Join(workspaceRoot, name)
			if err := copyTree(filepath.Join(staging, "workspaces", name), destination); err != nil {
				return err
			}
			restored = append(restored, Workspace{Name: name, Path: destination})
		}
		if err := writeWorkspaces(filepath.Join(absRoot, workspacesFile), restored); err != nil {
			return err
		}
	default:
		return fmt.Errorf("invalid backup kind %q", manifest.Kind)
	}
	fmt.Printf("restored backup into %s\n", absRoot)
	return nil
}

// migrateState makes a portable copy of one initialized runtime in another.
// It intentionally leaves the source untouched so switching the service to the
// destination can be verified before the old runtime is retired.
func migrateState(source, destination string) (string, error) {
	absSource, err := filepath.Abs(source)
	if err != nil {
		return "", err
	}
	absDestination, err := filepath.Abs(strings.TrimSpace(destination))
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(destination) == "" {
		return "", errors.New("enter the initialized destination directory")
	}
	if pathsOverlap(absSource, absDestination) {
		return "", errors.New("destination must be separate from and outside the current runtime root")
	}
	if _, err := os.Stat(filepath.Join(absDestination, envFile)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%s is not initialized; run `uppr init %q` first", absDestination, absDestination)
		}
		return "", err
	}
	if !fileExists(filepath.Join(absDestination, reposFile)) && !fileExists(filepath.Join(absDestination, workspacesFile)) {
		return "", fmt.Errorf("%s is not an initialized uppr runtime", absDestination)
	}
	staging, err := os.MkdirTemp("", "uppr-migrate-runtime-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(staging)
	if err := stageRuntimeAssets(absSource, staging); err != nil {
		return "", err
	}

	tmp, err := os.CreateTemp("", "uppr-migrate-*.tar.gz")
	if err != nil {
		return "", err
	}
	artifact := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(artifact)
		return "", err
	}
	defer os.Remove(artifact)
	if err := backupState([]string{artifact, staging}); err != nil {
		return "", err
	}
	if err := restoreState([]string{artifact, absDestination}); err != nil {
		return "", err
	}
	return absDestination, nil
}

func migrateRuntime(args []string) error {
	if len(args) != 2 {
		return errors.New("usage: uppr migrate <source> <destination>")
	}
	migratedRoot, err := migrateState(args[0], args[1])
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("migrate runtime: %w (run as a user with access to both directories; if necessary, use `sudo uppr migrate %q %q`)", err, args[0], args[1])
		}
		return err
	}
	fmt.Printf("migrated runtime assets into %s; source left unchanged\n", migratedRoot)
	return nil
}

// stageRuntimeAssets deliberately excludes Uppr's own source tree. Configured
// application repositories and server workspaces are copied separately by the
// normal backup machinery.
func stageRuntimeAssets(source, staging string) error {
	entries := []string{
		"config", "data", reposFile, workspacesFile, caddyFile, dockerComposeFile,
		makeFile, caddyDockerFile,
	}
	for _, entry := range entries {
		from := filepath.Join(source, entry)
		if _, err := os.Lstat(from); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		if err := copyTreeEntry(from, filepath.Join(staging, entry)); err != nil {
			return err
		}
	}
	if fileExists(filepath.Join(source, workspacesFile)) {
		return nil
	}
	repos, err := readRepos(filepath.Join(source, reposFile))
	if err != nil {
		return err
	}
	for _, repo := range repos {
		repoPath := repo.Path
		if repoPath == "" {
			repoPath = defaultRepoPath(repo)
		}
		if filepath.IsAbs(repoPath) {
			return fmt.Errorf("repository %q uses absolute path %s; change it to a runtime-relative path before migrating", repoLabel(repo), repoPath)
		}
		clean := filepath.Clean(repoPath)
		if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("repository %q path escapes the runtime root", repoLabel(repo))
		}
		from := filepath.Join(source, clean)
		if _, err := os.Lstat(from); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		if err := copyTreeEntry(from, filepath.Join(staging, clean)); err != nil {
			return err
		}
	}
	return nil
}

func copyTreeEntry(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return copyTree(source, destination)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func pathsOverlap(a, b string) bool {
	for _, pair := range [][2]string{{a, b}, {b, a}} {
		rel, err := filepath.Rel(pair[0], pair[1])
		if err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))) {
			return true
		}
	}
	return false
}

func extractBackup(artifact, destination string) (backupManifest, error) {
	f, err := os.Open(artifact)
	if err != nil {
		return backupManifest{}, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return backupManifest{}, fmt.Errorf("open backup: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var manifest backupManifest
	foundManifest := false
	type pendingSymlink struct{ target, link string }
	var symlinks []pendingSymlink
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return manifest, err
		}
		clean := path.Clean(header.Name)
		if clean == "." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
			return manifest, fmt.Errorf("unsafe path %q in backup", header.Name)
		}
		target := filepath.Join(destination, filepath.FromSlash(clean))
		if clean == "manifest.json" {
			if err := json.NewDecoder(io.LimitReader(tr, 1<<20)).Decode(&manifest); err != nil {
				return manifest, fmt.Errorf("read manifest: %w", err)
			}
			foundManifest = true
			continue
		}
		switch header.Typeflag {
		case tar.TypeDir:
			err = os.MkdirAll(target, os.FileMode(header.Mode))
		case tar.TypeReg, tar.TypeRegA:
			if err = os.MkdirAll(filepath.Dir(target), 0o755); err == nil {
				var out *os.File
				// Replace rather than truncate. Git pack files are commonly 0444,
				// and a second restore must still be able to update them when the
				// destination directory is owned by the current user. Removing the
				// entry first also avoids following an existing destination symlink.
				if removeErr := os.Remove(target); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
					err = removeErr
				} else {
					out, err = os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(header.Mode))
				}
				if err == nil {
					_, err = io.Copy(out, tr)
					closeErr := out.Close()
					if err == nil {
						err = closeErr
					}
				}
			}
		case tar.TypeSymlink:
			linkTarget := filepath.Clean(filepath.Join(filepath.Dir(target), filepath.FromSlash(header.Linkname)))
			relTarget, relErr := filepath.Rel(destination, linkTarget)
			if path.IsAbs(header.Linkname) || relErr != nil || relTarget == ".." || strings.HasPrefix(relTarget, ".."+string(filepath.Separator)) {
				return manifest, fmt.Errorf("unsafe symlink %q in backup", header.Name)
			}
			symlinks = append(symlinks, pendingSymlink{target: target, link: header.Linkname})
		default:
			return manifest, fmt.Errorf("unsupported entry %q in backup", header.Name)
		}
		if err != nil {
			return manifest, err
		}
	}
	if !foundManifest {
		return manifest, errors.New("backup manifest is missing")
	}
	for _, symlink := range symlinks {
		if err := os.MkdirAll(filepath.Dir(symlink.target), 0o755); err != nil {
			return manifest, err
		}
		if err := os.Remove(symlink.target); err != nil && !errors.Is(err, os.ErrNotExist) {
			return manifest, err
		}
		if err := os.Symlink(symlink.link, symlink.target); err != nil {
			return manifest, err
		}
	}
	return manifest, nil
}

func copyTree(source, destination string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("backup payload %s is not a directory", source)
	}
	return filepath.Walk(source, func(file string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, file)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(file)
			if err != nil {
				return err
			}
			if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			return os.Symlink(link, target)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		in, err := os.Open(file)
		if err != nil {
			return err
		}
		// Restore may be repeated over an existing Git checkout. Git pack files
		// are normally read-only, but their containing directory is writable, so
		// replace the directory entry instead of trying to truncate the file.
		// This also prevents an existing target symlink from being followed.
		if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
			in.Close()
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			in.Close()
			return err
		}
		_, copyErr := io.Copy(out, in)
		inErr := in.Close()
		outErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if inErr != nil {
			return inErr
		}
		return outErr
	})
}

func samePath(a, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	return errA == nil && errB == nil && filepath.Clean(aa) == filepath.Clean(bb)
}
