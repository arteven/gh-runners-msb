// Package identity derives a Pool's identity: the per-user install ID, the
// Pool ID, Runner names and the sandbox label that ties them together.
package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// LabelKey is the sandbox label key whose value is the Pool ID.
const LabelKey = "gh-runners-msb.pool"

const installIDFile = "install-id"

// DefaultDir is where the install ID lives: the user config dir.
func DefaultDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "gh-runners-msb"), nil
}

// LoadOrCreateInstallID returns the per-user random install ID stored in dir,
// creating it on first use. Creation is O_EXCL, so two concurrent first runs
// agree on one ID.
func LoadOrCreateInstallID(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, installIDFile)
	for range 3 {
		if id, err := readID(path); err == nil {
			return id, nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		buf := make([]byte, 16)
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		id := hex.EncodeToString(buf)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue // lost the race; read the winner's ID
		}
		if err != nil {
			return "", err
		}
		_, werr := f.WriteString(id + "\n")
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			os.Remove(path)
			return "", werr
		}
		return id, nil
	}
	return "", fmt.Errorf("could not settle install ID in %s", dir)
}

func readID(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(b))
	if id == "" {
		// A concurrent creator may not have written yet.
		return "", fs.ErrNotExist
	}
	return id, nil
}

// Pool is the identity of one Pool.
type Pool struct {
	ID string
}

// NewPool hashes install ID, repo ("host/owner/repo", case-insensitive) and
// label into a 6-hex Pool ID. The Backend is deliberately not an input.
func NewPool(installID, repo, label string) Pool {
	sum := sha256.Sum256([]byte(installID + "\n" + strings.ToLower(repo) + "\n" + label))
	return Pool{ID: hex.EncodeToString(sum[:3])}
}

// Prefix is the start of every Runner name in this Pool. The Pool ID has a
// fixed length, so no other Pool's names can share the prefix.
func (p Pool) Prefix() string { return "msb-gh-" + p.ID + "-" }

// RunnerName returns a fresh msb-gh-<pool>-<slot>-<8 hex> name; slots count
// from 1.
func (p Pool) RunnerName(slot int) string {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	return fmt.Sprintf("%s%d-%s", p.Prefix(), slot, hex.EncodeToString(buf))
}

// Owns reports whether name is a Runner name of this Pool.
func (p Pool) Owns(name string) bool { return strings.HasPrefix(name, p.Prefix()) }
