package cli

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/cli/go-gh/v2/pkg/repository"
	"github.com/spf13/cobra"

	"github.com/arteven/gh-runners-msb/internal/github"
	"github.com/arteven/gh-runners-msb/internal/identity"
	"github.com/arteven/gh-runners-msb/internal/pool"
)

const defaultLabel = "msb-docker"

func newUpCmd() *cobra.Command {
	var (
		repoFlag string
		slots    int
		label    string
	)
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Start runners for a repository",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Validate before any I/O.
			if slots < 1 {
				return fmt.Errorf("--slots must be at least 1, got %d", slots)
			}
			if strings.TrimSpace(label) == "" {
				return errors.New("--label must not be empty")
			}
			return runUp(cmd, repoFlag, slots, label)
		},
	}
	f := cmd.Flags()
	f.StringVarP(&repoFlag, "repo", "R", "", "repository as [HOST/]OWNER/REPO (default: current repository)")
	f.IntVar(&slots, "slots", 5, "number of runners to keep ready")
	f.StringVar(&label, "label", defaultLabel, "runner label that jobs target in runs-on")
	return cmd
}

func runUp(cmd *cobra.Command, repoFlag string, slots int, label string) error {
	repo, err := resolveRepo(repoFlag)
	if err != nil {
		return err
	}

	dir, err := identity.DefaultDir()
	if err != nil {
		return fmt.Errorf("locating config dir: %w", err)
	}
	installID, err := identity.LoadOrCreateInstallID(dir)
	if err != nil {
		return fmt.Errorf("reading install ID: %w", err)
	}
	id := identity.NewPool(installID, repo.Host+"/"+repo.Owner+"/"+repo.Name, label)

	backend, err := resolveBackend()
	if err != nil {
		return err
	}
	sandboxes, err := pinBackend(backend)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return pool.Run(ctx, pool.Config{
		Slots:     slots,
		Label:     label,
		Repo:      displayRepo(repo),
		Identity:  id,
		GitHub:    github.New(repo),
		Backend:   backend,
		Sandboxes: sandboxes,
		Out:       cmd.OutOrStdout(),
		Err:       cmd.ErrOrStderr(),
	})
}

// resolveRepo uses -R when given; otherwise go-gh's Current, which already
// honours GH_REPO and `gh repo set-default`.
func resolveRepo(flag string) (repository.Repository, error) {
	if flag != "" {
		r, err := repository.Parse(flag)
		if err != nil {
			return repository.Repository{}, fmt.Errorf("invalid --repo %q: %w", flag, err)
		}
		return r, nil
	}
	r, err := repository.Current()
	if err != nil {
		return repository.Repository{}, fmt.Errorf("could not determine the repository (use -R OWNER/REPO): %w", err)
	}
	return r, nil
}

func displayRepo(r repository.Repository) string {
	if r.Host == "" || strings.EqualFold(r.Host, "github.com") {
		return r.Owner + "/" + r.Name
	}
	return r.Host + "/" + r.Owner + "/" + r.Name
}
