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
				out, err = os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(header.Mode))
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
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
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
