package identity

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
)

func TestPoolIDStableAndSensitive(t *testing.T) {
	base := NewPool("install-a", "github.com/o/r", "msb-docker")
	if base.ID != NewPool("install-a", "github.com/o/r", "msb-docker").ID {
		t.Fatal("same inputs must give the same Pool ID")
	}
	if !regexp.MustCompile(`^[0-9a-f]{6}$`).MatchString(base.ID) {
		t.Fatalf("Pool ID %q is not 6 hex", base.ID)
	}
	for name, other := range map[string]Pool{
		"install": NewPool("install-b", "github.com/o/r", "msb-docker"),
		"repo":    NewPool("install-a", "github.com/o/r2", "msb-docker"),
		"label":   NewPool("install-a", "github.com/o/r", "other"),
	} {
		if other.ID == base.ID {
			t.Errorf("changing %s must change the Pool ID", name)
		}
	}
}

func TestPoolIDRepoIsCaseInsensitive(t *testing.T) {
	a := NewPool("i", "GitHub.com/Foo/Bar", "l")
	b := NewPool("i", "github.com/foo/bar", "l")
	if a.ID != b.ID {
		t.Fatal("repo must be case-insensitive")
	}
	if NewPool("i", "github.com/foo/bar", "L").ID == b.ID {
		t.Fatal("label is case-sensitive")
	}
}

func TestRunnerNameFormatAndOwnership(t *testing.T) {
	p := NewPool("i", "github.com/o/r", "l")
	re := regexp.MustCompile(`^msb-gh-[0-9a-f]{6}-[1-9][0-9]*-[0-9a-f]{8}$`)
	n1, n2 := p.RunnerName(1), p.RunnerName(12)
	for _, n := range []string{n1, n2} {
		if !re.MatchString(n) {
			t.Errorf("bad runner name %q", n)
		}
	}
	if n1 == p.RunnerName(1) {
		t.Error("names must be unique per call")
	}
	if !p.Owns(n1) || !p.Owns(n2) {
		t.Error("pool must own its own names")
	}
	other := NewPool("i", "github.com/o/r", "other")
	if p.Owns(other.RunnerName(1)) || other.Owns(n1) {
		t.Error("pool must not own another pool's names")
	}
	if p.Prefix() != "msb-gh-"+p.ID+"-" {
		t.Errorf("prefix = %q", p.Prefix())
	}
}

func TestLoadOrCreateInstallID(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cfg")
	id1, err := LoadOrCreateInstallID(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(id1) {
		t.Fatalf("install ID %q is not 32 hex", id1)
	}
	id2, err := LoadOrCreateInstallID(dir)
	if err != nil || id2 != id1 {
		t.Fatalf("second load = %q, %v; want %q", id2, err, id1)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(filepath.Join(dir, "install-id"))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("file mode = %v, want 0600", fi.Mode().Perm())
		}
		di, _ := os.Stat(dir)
		if di.Mode().Perm() != 0o700 {
			t.Errorf("dir mode = %v, want 0700", di.Mode().Perm())
		}
	}
}

func TestDefaultDirUnderUserConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/x")
	t.Setenv("HOME", "/x")
	t.Setenv("AppData", "/x")
	dir, err := DefaultDir()
	if err != nil {
		t.Skip(err)
	}
	if filepath.Base(dir) != "gh-runners-msb" {
		t.Errorf("dir = %q", dir)
	}
}
