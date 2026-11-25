package cli

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cli/go-gh/v2/pkg/repository"

	"github.com/arteven/gh-runners-msb/internal/github"
	"github.com/arteven/gh-runners-msb/internal/github/githubtest"
)

func TestCheckGitHub(t *testing.T) {
	for name, tc := range map[string]struct {
		fake        *githubtest.Fake
		allowPublic bool
		want        string // substrings the error must contain; empty means no error
	}{
		"private admin":           {fake: &githubtest.Fake{}},
		"not admin":               {fake: &githubtest.Fake{NotAdmin: true}, want: "admin of o/r"},
		"not admin, public, flag": {fake: &githubtest.Fake{NotAdmin: true, Public: true}, allowPublic: true, want: "admin of o/r"},
		"public without the flag": {fake: &githubtest.Fake{Public: true}, want: "--allow-public-repo"},
		"public with the flag":    {fake: &githubtest.Fake{Public: true}, allowPublic: true},
		"no token":                {fake: &githubtest.Fake{RepoInfoErr: fmt.Errorf("x: %w", github.ErrNoToken)}, want: "gh auth login"},
		"other API error":         {fake: &githubtest.Fake{RepoInfoErr: fmt.Errorf("boom")}, want: "boom"},
	} {
		t.Run(name, func(t *testing.T) {
			err := checkGitHub(t.Context(), tc.fake, "o/r", tc.allowPublic)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestCheckGitHubPublicMessageLinksGuidance(t *testing.T) {
	err := checkGitHub(t.Context(), &githubtest.Fake{Public: true}, "o/r", false)
	if err == nil || !strings.Contains(err.Error(), "docs.github.com") {
		t.Errorf("err = %v", err)
	}
}

func TestCheckGitHubAdminIsCheckedBeforePublic(t *testing.T) {
	err := checkGitHub(t.Context(), &githubtest.Fake{NotAdmin: true, Public: true}, "o/r", false)
	if err == nil || strings.Contains(err.Error(), "--allow-public-repo") {
		t.Errorf("err = %v; the flag can't fix a missing admin right", err)
	}
}

func TestCheckHost(t *testing.T) {
	for host, ok := range map[string]bool{"github.com": true, "GitHub.com": true, "ghe.example.com": false} {
		err := checkHost(repository.Repository{Host: host, Owner: "o", Name: "r"})
		if (err == nil) != ok {
			t.Errorf("%s: err = %v", host, err)
		}
		if err != nil && !strings.Contains(err.Error(), "github.com") {
			t.Errorf("%s: error should mention github.com: %v", host, err)
		}
	}
}
