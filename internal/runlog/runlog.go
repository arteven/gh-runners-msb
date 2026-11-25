// Package runlog owns the host side of Runner evidence: one private directory
// per Runner under a log root, safe extraction of the diagnostics tar from the
// VM, and retention.
package runlog

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const (
	dirMode  = 0o700
	fileMode = 0o600

	// HostLog is the file holding a Runner's Pool events and script output.
	HostLog = "host.log"
	// DockerdLog and DiagDir are the copied-out diagnostics.
	DockerdLog = "dockerd.log"
	DiagDir    = "_diag"
)

// DefaultDir is <user cache dir>/gh-runners-msb/logs.
func DefaultDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "gh-runners-msb", "logs"), nil
}

// Root is an opened log root.
type Root struct{ dir string }

// Dir is the root directory.
func (r *Root) Dir() string { return r.dir }

// Open prepares the log root. A missing directory is created 0700 (with any
// missing parents); an existing one is used as-is, never chmod-ed.
func Open(dir string) (*Root, error) {
	fi, err := os.Stat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := os.MkdirAll(dir, dirMode); err != nil {
			return nil, fmt.Errorf("create log directory: %w", err)
		}
		// MkdirAll is subject to the umask and leaves parents alone.
		if err := os.Chmod(dir, dirMode); err != nil {
			return nil, fmt.Errorf("create log directory: %w", err)
		}
	case err != nil:
		return nil, fmt.Errorf("log directory: %w", err)
	case !fi.IsDir():
		return nil, fmt.Errorf("log directory %s is not a directory", dir)
	}
	return &Root{dir: dir}, nil
}

// Runner is one Runner's directory.
type Runner struct {
	Dir string
	// Host appends to host.log.
	Host io.Writer
	f    *os.File
}

// Runner creates (or reopens) <root>/<name> with its _diag directory and
// host.log, forcing 0700/0600 whatever the umask or parent mode.
func (r *Root) Runner(name string) (*Runner, error) {
	dir := filepath.Join(r.dir, name)
	for _, d := range []string{dir, filepath.Join(dir, DiagDir)} {
		if err := mkdirPrivate(d); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(filepath.Join(dir, HostLog), os.O_CREATE|os.O_WRONLY|os.O_APPEND, fileMode)
	if err != nil {
		return nil, err
	}
	if err := f.Chmod(fileMode); err != nil {
		f.Close()
		return nil, err
	}
	return &Runner{Dir: dir, Host: f, f: f}, nil
}

func mkdirPrivate(d string) error {
	if err := os.Mkdir(d, dirMode); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	return os.Chmod(d, dirMode)
}

// Close closes host.log.
func (r *Runner) Close() error { return r.f.Close() }

// ExtractDiagnostics unpacks a tar stream produced inside the VM. The stream is
// untrusted: only dockerd.log and entries under _diag/ are kept, only regular
// files and directories are written, and all writes go through an os.Root so
// "..", absolute paths and symlinks cannot escape the Runner's directory.
// Modes are forced to 0700/0600. Skipped entries are not an error; a malformed
// or truncated stream is, and what was extracted before it is kept.
func (r *Runner) ExtractDiagnostics(src io.Reader) error {
	root, err := os.OpenRoot(r.Dir)
	if err != nil {
		return err
	}
	defer root.Close()

	tr := tar.NewReader(src)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read diagnostics archive: %w", err)
		}
		name, ok := allowed(h.Name)
		if !ok {
			continue
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if name == DockerdLog {
				continue
			}
			if err := mkdirAllPrivate(root, name); err != nil {
				return err
			}
		case tar.TypeReg:
			if dir := path.Dir(name); dir != "." {
				if err := mkdirAllPrivate(root, dir); err != nil {
					return err
				}
			}
			if err := writeFile(root, name, tr); err != nil {
				return err
			}
		}
	}
}

// allowed cleans an archive name and reports whether it may be extracted.
func allowed(raw string) (string, bool) {
	if strings.HasPrefix(raw, "/") {
		return "", false
	}
	name := path.Clean(raw)
	if name == "." || name == ".." || strings.HasPrefix(name, "../") {
		return "", false
	}
	if name == DockerdLog || name == DiagDir || strings.HasPrefix(name, DiagDir+"/") {
		return name, true
	}
	return "", false
}

func mkdirAllPrivate(root *os.Root, name string) error {
	parts := strings.Split(name, "/")
	for i := range parts {
		p := strings.Join(parts[:i+1], "/")
		if err := root.Mkdir(p, dirMode); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		if err := root.Chmod(p, dirMode); err != nil {
			return err
		}
	}
	return nil
}

func writeFile(root *os.Root, name string, src io.Reader) error {
	// Never follow or replace an existing symlink: remove it first.
	if fi, err := root.Lstat(name); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
		if err := root.Remove(name); err != nil {
			return err
		}
	}
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fileMode)
	if err != nil {
		return err
	}
	if err := f.Chmod(fileMode); err != nil {
		f.Close()
		return err
	}
	if _, err := io.Copy(f, src); err != nil {
		f.Close()
		return fmt.Errorf("read diagnostics archive: %w", err)
	}
	return f.Close()
}

// Prune removes direct child directories of root whose name starts with prefix
// and whose mtime is older than maxAge before now. Symlinks and plain files are
// never touched or followed. It returns the names removed; a failure on one
// entry does not stop the others, and the errors are joined.
func Prune(root, prefix string, maxAge time.Duration, now time.Time) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var removed []string
	var errs []error
	cutoff := now.Add(-maxAge)
	for _, e := range entries {
		if !e.Type().IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !fi.ModTime().Before(cutoff) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, e.Name())); err != nil {
			errs = append(errs, err)
			continue
		}
		removed = append(removed, e.Name())
	}
	return removed, errors.Join(errs...)
}
