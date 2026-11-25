package runlog

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

func TestOpenCreatesMissingRootPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b", "logs")
	if _, err := Open(dir); err != nil {
		t.Fatal(err)
	}
	if m := mode(t, dir); m != 0o700 {
		t.Errorf("root mode = %o, want 700", m)
	}
}

func TestOpenRejectsAFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(f, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(f); err == nil {
		t.Error("Open on a file = nil error")
	}
}

func TestLooserExistingRootIsKeptButContentsArePrivate(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o777); err != nil {
		t.Fatal(err)
	}
	r, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	rn, err := r.Runner("msb-gh-x-1")
	if err != nil {
		t.Fatal(err)
	}
	defer rn.Close()
	if m := mode(t, root); m != 0o777 {
		t.Errorf("existing root mode = %o, want 777 (used as-is)", m)
	}
	for path, want := range map[string]os.FileMode{
		rn.Dir:                            0o700,
		filepath.Join(rn.Dir, "_diag"):    0o700,
		filepath.Join(rn.Dir, "host.log"): 0o600,
	} {
		if m := mode(t, path); m != want {
			t.Errorf("%s mode = %o, want %o", path, m, want)
		}
	}
	if _, err := rn.Host.Write([]byte("hi\n")); err != nil {
		t.Fatal(err)
	}
}

func TestRunnerReopenAppends(t *testing.T) {
	r, _ := Open(t.TempDir())
	for _, s := range []string{"one\n", "two\n"} {
		rn, err := r.Runner("n")
		if err != nil {
			t.Fatal(err)
		}
		rn.Host.Write([]byte(s))
		rn.Close()
	}
	b, _ := os.ReadFile(filepath.Join(r.dir, "n", "host.log"))
	if string(b) != "one\ntwo\n" {
		t.Errorf("host.log = %q", b)
	}
}

type ent struct {
	name, link string
	typ        byte
	mode       int64
	body       string
}

func buildTar(t *testing.T, ents []ent) *bytes.Reader {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range ents {
		h := &tar.Header{Name: e.name, Typeflag: e.typ, Mode: e.mode, Linkname: e.link, Size: int64(len(e.body))}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	return bytes.NewReader(buf.Bytes())
}

func TestExtractForcesModes(t *testing.T) {
	r, _ := Open(t.TempDir())
	rn, err := r.Runner("n")
	if err != nil {
		t.Fatal(err)
	}
	defer rn.Close()
	err = rn.ExtractDiagnostics(buildTar(t, []ent{
		{name: "dockerd.log", typ: tar.TypeReg, mode: 0o666, body: "d"},
		{name: "_diag/", typ: tar.TypeDir, mode: 0o777},
		{name: "_diag/sub/", typ: tar.TypeDir, mode: 0o755},
		{name: "_diag/sub/Runner.log", typ: tar.TypeReg, mode: 0o644, body: "r"},
		{name: "_diag/deep/x.log", typ: tar.TypeReg, mode: 0o644, body: "x"}, // parent not listed
	}))
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{
		"dockerd.log":          0o600,
		"_diag":                0o700,
		"_diag/sub":            0o700,
		"_diag/sub/Runner.log": 0o600,
		"_diag/deep":           0o700,
		"_diag/deep/x.log":     0o600,
	} {
		if m := mode(t, filepath.Join(rn.Dir, path)); m != want {
			t.Errorf("%s mode = %o, want %o", path, m, want)
		}
	}
}

func TestExtractSkipsHostileEntries(t *testing.T) {
	base := t.TempDir()
	r, _ := Open(filepath.Join(base, "root"))
	rn, err := r.Runner("n")
	if err != nil {
		t.Fatal(err)
	}
	defer rn.Close()
	rn.Host.Write([]byte("original\n"))
	err = rn.ExtractDiagnostics(buildTar(t, []ent{
		{name: "../x", typ: tar.TypeReg, mode: 0o644, body: "x"},
		{name: "_diag/../../y", typ: tar.TypeReg, mode: 0o644, body: "y"},
		{name: "/abs", typ: tar.TypeReg, mode: 0o644, body: "a"},
		{name: "host.log", typ: tar.TypeReg, mode: 0o644, body: "clobbered"},
		{name: "other.txt", typ: tar.TypeReg, mode: 0o644, body: "o"},
		{name: "_diag/l", typ: tar.TypeSymlink, link: "/etc/passwd"},
		{name: "_diag/h", typ: tar.TypeLink, link: "host.log"},
		{name: "_diag/dev", typ: tar.TypeChar},
		// The symlink above was skipped, so this is just a nested regular file.
		{name: "_diag/l/nested.log", typ: tar.TypeReg, mode: 0o644, body: "p"},
		{name: "_diag/ok.log", typ: tar.TypeReg, mode: 0o644, body: "ok"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(rn.Dir, "host.log")); string(b) != "original\n" {
		t.Errorf("host.log = %q, was overwritten", b)
	}
	if _, err := os.ReadFile(filepath.Join(rn.Dir, "_diag", "ok.log")); err != nil {
		t.Errorf("legit entry after hostile ones missing: %v", err)
	}
	var got []string
	filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
		rel, _ := filepath.Rel(base, p)
		got = append(got, rel)
		return nil
	})
	want := map[string]bool{
		".": true, "root": true, "root/n": true, "root/n/host.log": true,
		"root/n/_diag": true, "root/n/_diag/ok.log": true,
		"root/n/_diag/l": true, "root/n/_diag/l/nested.log": true,
	}
	for _, g := range got {
		if !want[g] {
			t.Errorf("unexpected path %q", g)
		}
	}
}

func TestPrune(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 1, 20, 12, 0, 0, 0, time.UTC)
	const prefix = "msb-gh-aa-"
	age := func(name string, d time.Duration, mk func(string)) {
		p := filepath.Join(root, name)
		mk(p)
		if err := os.Chtimes(p, now.Add(-d), now.Add(-d)); err != nil {
			// symlinks: Chtimes follows; fine, target is outside anyway
			t.Fatal(err)
		}
	}
	mkdir := func(p string) {
		if err := os.MkdirAll(filepath.Join(p, "_diag"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	outside := t.TempDir()
	os.Chtimes(outside, now.Add(-30*24*time.Hour), now.Add(-30*24*time.Hour))
	day := 24 * time.Hour
	age(prefix+"1-old", 8*day, mkdir)
	age(prefix+"2-fresh", 1*day, mkdir)
	age("msb-gh-bb-1-old", 8*day, mkdir)
	age(prefix+"file", 8*day, func(p string) { os.WriteFile(p, nil, 0o600) })
	if err := os.Symlink(outside, filepath.Join(root, prefix+"link")); err != nil {
		t.Fatal(err)
	}

	removed, err := Prune(root, prefix, 7*day, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != prefix+"1-old" {
		t.Errorf("removed = %v", removed)
	}
	for name, want := range map[string]bool{
		prefix + "1-old": false, prefix + "2-fresh": true, "msb-gh-bb-1-old": true,
		prefix + "file": true, prefix + "link": true,
	} {
		_, err := os.Lstat(filepath.Join(root, name))
		if (err == nil) != want {
			t.Errorf("%s exists = %v, want %v", name, err == nil, want)
		}
	}
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("symlink target removed: %v", err)
	}
}
