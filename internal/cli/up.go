package cli

import (
	"errors"
	"fmt"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/cli/go-gh/v2/pkg/repository"
	"github.com/spf13/cobra"

	"github.com/arteven/gh-runners-msb/internal/github"
	"github.com/arteven/gh-runners-msb/internal/identity"
	"github.com/arteven/gh-runners-msb/internal/pool"
	"github.com/arteven/gh-runners-msb/internal/runlog"
	"github.com/arteven/gh-runners-msb/internal/size"
	"github.com/arteven/gh-runners-msb/internal/spec"
)

const defaultLabel = "msb-docker"

func newUpCmd() *cobra.Command {
	var (
		repoFlag string
		slots    int
		label    string
		logs     string

		allowPublic bool

		cpus                     int
		memory, disk, dockerDisk string
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
			sizes, err := parseSizes(cpus, memory, disk, dockerDisk)
			if err != nil {
				return err
			}
			return runUp(cmd, upOptions{repo: repoFlag, slots: slots, label: label, sizes: sizes, allowPublic: allowPublic, logs: logs})
		},
	}
	f := cmd.Flags()
	f.StringVarP(&repoFlag, "repo", "R", "", "repository as [HOST/]OWNER/REPO (default: current repository)")
	f.IntVar(&slots, "slots", 5, "number of runners to keep ready")
	f.StringVar(&label, "label", defaultLabel, "runner label that jobs target in runs-on")
	f.StringVar(&logs, "logs", "", "directory for runner logs (default: <user cache dir>/gh-runners-msb/logs)")
	f.BoolVar(&allowPublic, "allow-public-repo", false, "allow a public repository (fork PRs could run code on your machine)")
	f.IntVar(&cpus, "cpus", int(spec.Defaults.CPUs), "virtual CPUs per runner")
	f.StringVar(&memory, "memory", "4G", "memory per runner (e.g. 4G, 512MiB)")
	f.StringVar(&disk, "disk", "5G", "writable disk budget per runner, including Docker's share")
	f.StringVar(&dockerDisk, "docker-disk", "2G", "Docker's share of --disk; must be smaller than --disk")
	return cmd
}

// parseSizes turns the raw sizing flags into spec.Sizes. It is pure so every
// rule is testable without cobra and runs before any I/O.
func parseSizes(cpus int, memory, disk, dockerDisk string) (spec.Sizes, error) {
	if cpus < 1 {
		return spec.Sizes{}, fmt.Errorf("--cpus must be at least 1, got %d", cpus)
	}
	if cpus > math.MaxUint8 {
		return spec.Sizes{}, fmt.Errorf("--cpus must be at most %d, got %d", math.MaxUint8, cpus)
	}
	mem, err := size.ParseMiB(memory)
	if err != nil {
		return spec.Sizes{}, fmt.Errorf("--memory: %w", err)
	}
	dsk, err := size.ParseMiB(disk)
	if err != nil {
		return spec.Sizes{}, fmt.Errorf("--disk: %w", err)
	}
	dkr, err := size.ParseMiB(dockerDisk)
	if err != nil {
		return spec.Sizes{}, fmt.Errorf("--docker-disk: %w", err)
	}
	// Compared in MiB: 2048M and 2G are equal. spec.Local relies on this.
	if dkr >= dsk {
		return spec.Sizes{}, fmt.Errorf("--docker-disk (%s) must be smaller than --disk (%s)", dockerDisk, disk)
	}
	return spec.Sizes{CPUs: uint8(cpus), MemoryMiB: mem, DiskMiB: dsk, DockerMiB: dkr}, nil
}

type upOptions struct {
	repo  string
	slots int
	label string
	sizes spec.Sizes
	logs  string // empty: runlog.DefaultDir

	allowPublic bool
}

func runUp(cmd *cobra.Command, o upOptions) error {
	repo, err := resolveRepo(o.repo)
	if err != nil {
		return err
	}
	if err := checkHost(repo); err != nil {
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
	id := identity.NewPool(installID, repo.Host+"/"+repo.Owner+"/"+repo.Name, o.label)

	logDir, err := resolveLogDir(o.logs)
	if err != nil {
		return err
	}

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

	gh := github.New(repo)
	if err := checkGitHub(ctx, gh, displayRepo(repo), o.allowPublic); err != nil {
		return err
	}

	return pool.Run(ctx, pool.Config{
		Slots:     o.slots,
		Label:     o.label,
		Sizes:     o.sizes,
		Repo:      displayRepo(repo),
		Identity:  id,
		GitHub:    gh,
		Backend:   backend,
		Sandboxes: sandboxes,
		LogDir:    logDir,
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

// displayRepo is OWNER/REPO; checkHost has already limited repo to github.com.
func displayRepo(r repository.Repository) string {
	return r.Owner + "/" + r.Name
}

// resolveLogDir is --logs when given, else the default under the user cache
// dir, made absolute so the paths in messages are usable from anywhere. The
// directory itself is created by pool.Run.
func resolveLogDir(flag string) (string, error) {
	dir := flag
	if dir == "" {
		var err error
		if dir, err = runlog.DefaultDir(); err != nil {
			return "", fmt.Errorf("locating log dir (use --logs): %w", err)
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("--logs: %w", err)
	}
	return abs, nil
}
