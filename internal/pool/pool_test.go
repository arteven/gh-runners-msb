package pool

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arteven/gh-runners-msb/internal/github/githubtest"
	"github.com/arteven/gh-runners-msb/internal/identity"
	"github.com/arteven/gh-runners-msb/internal/msbprofile"
)

// A cancelled parent context is a clean stop (exit 0), and the banner names
// the Backend, never the key.
func TestBannerShowsBackendAndParentCancellationIsACleanStop(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var out bytes.Buffer
	b := msbprofile.Backend{Kind: msbprofile.Remote, Source: msbprofile.SourceSaved, Profile: "work", URL: msbprofile.DefaultURL}
	err := Run(ctx, Config{
		Slots: 1, Label: "l", Repo: "o/r", Identity: identity.NewPool("id", "github.com/o/r", "l"),
		GitHub: &githubtest.Fake{}, Backend: b, LogDir: t.TempDir(), Out: &out, Err: &out,
	})
	if err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
	want := `backend: remote (profile "work", saved) → https://api.microsandbox.dev`
	if !strings.Contains(out.String(), want) {
		t.Errorf("banner lacks %q:\n%s", want, out.String())
	}
}

func TestRunNeedsALogDir(t *testing.T) {
	err := Run(t.Context(), Config{Slots: 1, Identity: identity.NewPool("id", "github.com/o/r", "l"), GitHub: &githubtest.Fake{}})
	if err == nil || !strings.Contains(err.Error(), "log directory") {
		t.Errorf("Run = %v, want a log directory error", err)
	}
}

func TestRunCreatesAMissingLogDirPrivate(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	dir := filepath.Join(t.TempDir(), "x", "logs")
	var out bytes.Buffer
	err := Run(ctx, Config{
		Slots: 1, Identity: identity.NewPool("id", "github.com/o/r", "l"),
		GitHub: &githubtest.Fake{}, LogDir: dir, Out: &out, Err: &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(dir)
	if err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("log dir = %v, %v; want 0700", fi, err)
	}
}
