package pool

import (
	"bytes"
	"context"
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
		GitHub: &githubtest.Fake{}, Backend: b, Out: &out, Err: &out,
	})
	if err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
	want := `backend: remote (profile "work", saved) → https://api.microsandbox.dev`
	if !strings.Contains(out.String(), want) {
		t.Errorf("banner lacks %q:\n%s", want, out.String())
	}
}
