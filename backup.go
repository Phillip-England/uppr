package main

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func backupCommand(args []string) (result error) {
	if len(args) != 1 {
		return errors.New("usage: uppr backup <archive.tar.gz>")
	}
	root, m, err := findPad(".")
	if err != nil {
		return err
	}
	archive, err := filepath.Abs(args[0])
	if err != nil {
		return err
	}
	if within(root, archive) {
		return errors.New("backup archive must be outside the launchpad")
	}
	var stopped []string
	defer func() {
		for _, unit := range stopped {
			if err := privileged("systemctl", "start", unit); err != nil {
				result = errors.Join(result, fmt.Errorf("restart %s: %w", unit, err))
			}
		}
	}()
	if err := requireLinux(); err == nil {
		if _, err := exec.LookPath("systemctl"); err != nil {
			return errors.New("systemctl is required for a consistent Linux backup")
		}
		for _, r := range m.Repos {
			unit := unitName(m, r)
			if activeUnit(unit) {
				if err := privileged("systemctl", "stop", unit); err != nil {
					return err
				}
				stopped = append(stopped, unit)
			}
		}
	} else {
		fmt.Fprintln(os.Stderr, "uppr: ensure apps are stopped before backing up SQLite on this host")
	}
	if err := os.MkdirAll(filepath.Dir(archive), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(archive), ".uppr-backup-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := writeArchive(root, tmp); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), archive); err != nil {
		return err
	}
	fmt.Println("wrote", archive)
	return nil
}

func writeArchive(root string, out io.Writer) error {
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			link, err = os.Readlink(path)
			if err != nil {
				return err
			}
			if filepath.IsAbs(link) || !within(root, filepath.Join(filepath.Dir(path), link)) {
				return fmt.Errorf("symlink %s points outside launchpad", rel)
			}
		}
		if !info.Mode().IsRegular() && !info.IsDir() && link == "" {
			return fmt.Errorf("unsupported file type %s", rel)
		}
		header, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(rel)
		if info.IsDir() {
			header.Name += "/"
		}
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if info.IsDir() || link != "" {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = io.Copy(tw, file)
		return err
	})
	closeTar := tw.Close()
	closeGzip := gz.Close()
	return errors.Join(err, closeTar, closeGzip)
}

func restoreCommand(args []string) error {
	if len(args) != 2 {
		return errors.New("usage: uppr restore <archive.tar.gz> <directory>")
	}
	archive, err := filepath.Abs(args[0])
	if err != nil {
		return err
	}
	dest, err := filepath.Abs(args[1])
	if err != nil {
		return err
	}
	if _, err := os.Stat(dest); err == nil {
		return fmt.Errorf("destination %s already exists", dest)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(dest), ".uppr-restore-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := extractArchive(archive, stage); err != nil {
		return err
	}
	if _, err := loadManifest(stage); err != nil {
		return fmt.Errorf("archive has no valid launchpad: %w", err)
	}
	if err := os.Rename(stage, dest); err != nil {
		return err
	}
	fmt.Printf("restored %s; run `uppr doctor`, `uppr generate`, and `uppr install` there\n", dest)
	return nil
}

func extractArchive(archive, dest string) error {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.FromSlash(h.Name)
		if name == "" || filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) || filepath.Clean(name) != strings.TrimSuffix(name, string(filepath.Separator)) {
			return fmt.Errorf("unsafe archive path %q", h.Name)
		}
		path := filepath.Join(dest, name)
		if !within(dest, path) {
			return fmt.Errorf("unsafe archive path %q", h.Name)
		}
		if err := rejectSymlinkParent(dest, path); err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, os.FileMode(h.Mode)&0o777); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return err
			}
			out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(h.Mode)&0o777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if filepath.IsAbs(h.Linkname) || !within(dest, filepath.Join(filepath.Dir(path), h.Linkname)) {
				return fmt.Errorf("unsafe symlink %q", h.Name)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return err
			}
			if err := os.Symlink(h.Linkname, path); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported archive entry %q", h.Name)
		}
	}
}

func rejectSymlinkParent(root, path string) error {
	parent := filepath.Dir(path)
	for parent != root {
		info, err := os.Lstat(parent)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("archive path traverses symlink: %s", path)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		parent = filepath.Dir(parent)
	}
	return nil
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
