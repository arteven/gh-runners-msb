package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cli/go-gh/v2/pkg/repository"

	"github.com/arteven/gh-runners-msb/internal/github"
)

const selfHostedSecurityDoc = "https://docs.github.com/en/actions/concepts/runners/self-hosted-runners"

// checkHost refuses any host but github.com. It is pure, so it runs with the
// other flag validation, before any I/O.
func checkHost(r repository.Repository) error {
	if !strings.EqualFold(r.Host, "github.com") {
		return fmt.Errorf("only github.com repositories are supported, not %s", r.Host)
	}
	return nil
}

// checkGitHub refuses to continue unless the token can see the repository,
// belongs to an admin of it, and (without allowPublic) the repository is not
// public. repo is OWNER/REPO, for messages. Each refusal has its own message.
// Admin is checked before public: the flag can't fix a missing admin right.
func checkGitHub(ctx context.Context, gh github.Port, repo string, allowPublic bool) error {
	info, err := gh.RepoInfo(ctx)
	if errors.Is(err, github.ErrNoToken) {
		return errors.New(`no GitHub token for github.com; run "gh auth login"`)
	}
	if err != nil {
		return err
	}
	if !info.Admin {
		return fmt.Errorf("you must be an admin of %s to register self-hosted runners", repo)
	}
	if info.Public && !allowPublic {
		return fmt.Errorf("%s is a public repository: a pull request from a fork could run code on your machine "+
			"(see %s); pass --allow-public-repo to accept that", repo, selfHostedSecurityDoc)
	}
	return nil
}
