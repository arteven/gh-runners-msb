// Package pool keeps a fixed number of Slots filled with ephemeral Runners.
package pool

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	microsandbox "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/arteven/gh-runners-msb/internal/github"
	"github.com/arteven/gh-runners-msb/internal/guest"
	"github.com/arteven/gh-runners-msb/internal/identity"
	"github.com/arteven/gh-runners-msb/internal/spec"
)

// Config is everything one Pool needs.
type Config struct {
	Slots     int
	Label     string
	Repo      string // shown in the banner: OWNER/REPO, or HOST/OWNER/REPO off github.com
	Identity  identity.Pool
	Sizes     spec.Sizes
	Image     string
	GitHub    github.Port
	Sandboxes *microsandbox.Sandboxes
	Out, Err  io.Writer
	// RetryDelay is the fixed wait after a failed iteration (default 5 s).
	RetryDelay time.Duration

	now func() time.Time // tests only
}

const (
	destroyTimeout = 60 * time.Second
	tailLines      = 40
)

// Run fills all Slots together and refills each as its Runner exits. It
// returns nil once ctx is cancelled and every Slot has cleaned up.
func Run(ctx context.Context, cfg Config) error {
	if cfg.Slots < 1 {
		return errors.New("pool needs at least one slot")
	}
	if cfg.RetryDelay == 0 {
		cfg.RetryDelay = 5 * time.Second
	}
	if cfg.Image == "" {
		cfg.Image = spec.DefaultImage
	}
	if cfg.Sizes == (spec.Sizes{}) {
		cfg.Sizes = spec.Defaults
	}
	now := cfg.now
	if now == nil {
		now = time.Now
	}
	con := &console{out: cfg.Out, err: cfg.Err, now: now}

	p := &runner{cfg: cfg, con: con, spec: spec.Local(cfg.Sizes, cfg.Image, cfg.Identity.ID)}
	con.info("pool", "Maintaining %d slots for %s; label %s", cfg.Slots, cfg.Repo, cfg.Label)

	var wg sync.WaitGroup
	for slot := 1; slot <= cfg.Slots; slot++ {
		wg.Go(func() { p.slot(ctx, slot) })
	}
	wg.Wait()
	return nil
}

type runner struct {
	cfg  Config
	con  *console
	spec spec.Spec
}

func (p *runner) slot(ctx context.Context, n int) {
	who := fmt.Sprintf("slot %d", n)
	for ctx.Err() == nil {
		if err := p.iterate(ctx, n, who); err != nil && ctx.Err() == nil {
			p.con.warn(who, "%v; retrying in %s", err, p.cfg.RetryDelay)
			select {
			case <-ctx.Done():
			case <-time.After(p.cfg.RetryDelay):
			}
		}
	}
}

// iterate is one Runner's life: create, register, run, destroy, deregister.
func (p *runner) iterate(ctx context.Context, n int, who string) error {
	name := p.cfg.Identity.RunnerName(n)

	// Never cancel a create: superradcompany/microsandbox#1687. Abandoned
	// creates are handled by a later ticket (#36).
	sb, err := p.cfg.Sandboxes.Create(context.WithoutCancel(ctx), name, p.spec.Options()...)
	if err != nil {
		return fmt.Errorf("create %s: %w", name, err)
	}

	var runnerID int64
	var code int
	var runErr error
	logs := &tail{n: tailLines}
	if ctx.Err() == nil {
		var jit github.JITConfig
		jit, runErr = p.cfg.GitHub.GenerateJITConfig(ctx, name, []string{"self-hosted", p.cfg.Label})
		if runErr == nil {
			runnerID = jit.RunnerID
			p.con.info(who, "runner %s started", name)
			code, runErr = guest.Run(ctx, sb, jit.Encoded, logs)
		}
	}

	// Explicit order, not defers (LIFO would reverse it): destroy the VM,
	// then deregister. Both use non-cancelled contexts so Ctrl-C leaks nothing.
	p.destroy(who, sb)
	p.deregister(who, runnerID)

	switch {
	case runErr != nil:
		return runErr
	case ctx.Err() != nil:
		return nil
	case code == 0:
		p.con.info(who, "runner finished; replacing VM")
	default:
		for _, l := range logs.Lines() {
			p.con.warn(who, "  | %s", l)
		}
		// An error, so the slot waits RetryDelay: a Runner that dies at once
		// must not turn the Slot into a hot create loop.
		return fmt.Errorf("runner exited with status %d", code)
	}
	return nil
}

func (p *runner) destroy(who string, sb *microsandbox.Sandbox) {
	ctx, cancel := context.WithTimeout(context.Background(), destroyTimeout)
	defer cancel()
	if err := sb.Destroy(ctx); err != nil && !microsandbox.IsKind(err, microsandbox.ErrSandboxNotFound) {
		p.con.warn(who, "destroy %s: %v", sb.Name(), err)
	}
	sb.Close()
}

func (p *runner) deregister(who string, id int64) {
	if id == 0 {
		return
	}
	// The port applies its own 30 s timeout.
	if err := p.cfg.GitHub.DeleteRunner(context.Background(), id); err != nil {
		p.con.warn(who, "deregister runner %d: %v", id, err)
	}
}
