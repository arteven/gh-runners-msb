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
	"github.com/arteven/gh-runners-msb/internal/msbprofile"
	"github.com/arteven/gh-runners-msb/internal/runlog"
	"github.com/arteven/gh-runners-msb/internal/spec"
)

// MsbInstallCmd is how a user installs msb (microsandbox README, v0.8.2). The
// extension itself never installs or upgrades it.
const MsbInstallCmd = "curl -fsSL https://install.microsandbox.dev | sh"

// Config is everything one Pool needs.
type Config struct {
	Slots    int
	Label    string
	Repo     string // shown in the banner: OWNER/REPO
	Identity identity.Pool
	Sizes    spec.Sizes
	Image    string
	GitHub   github.Port
	// Backend is the resolved msb Backend, shown in the banner. Sandboxes must
	// already be a context pinned to it.
	Backend   msbprofile.Backend
	Sandboxes *microsandbox.Sandboxes
	Out, Err  io.Writer
	// RetryDelay is the fixed wait after a failed iteration (default 5 s).
	RetryDelay time.Duration
	// LogDir is where each Runner's directory goes. Required: Run creates it
	// 0700 when missing and uses it as-is otherwise.
	LogDir string

	now func() time.Time // tests only
}

const (
	destroyTimeout = 60 * time.Second
	// copyTimeout bounds the diagnostics copy-out. It is best effort.
	copyTimeout = 20 * time.Second
	// logMaxAge and pruneEvery set retention: this Pool's directories older
	// than logMaxAge are removed at start and every pruneEvery.
	logMaxAge  = 7 * 24 * time.Hour
	pruneEvery = time.Hour
	tailLines  = 40
	// selfTestTimeout bounds the guest script: 90 s for Docker plus the curls,
	// with slack. A hung guest is a failed Self-test, not a hung Pool.
	selfTestTimeout = 3 * time.Minute
)

// Run fills all Slots together and refills each as its Runner exits. It
// returns nil once ctx is cancelled and every Slot has cleaned up, or the
// fatal error that stopped the Pool (see runner.fatal).
func Run(parent context.Context, cfg Config) error {
	if cfg.Slots < 1 {
		return errors.New("pool needs at least one slot")
	}
	if cfg.RetryDelay == 0 {
		cfg.RetryDelay = 5 * time.Second
	}
	if cfg.LogDir == "" {
		return errors.New("pool needs a log directory")
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
	logs, err := runlog.Open(cfg.LogDir)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	p := &runner{cfg: cfg, con: con, spec: spec.Local(cfg.Sizes, cfg.Image, cfg.Identity.ID), cancel: cancel, logs: logs}
	p.prune()
	// Prove a Runner can start before filling any Slot. A Ctrl-C during it is a
	// clean stop, with no banner.
	if ctx.Err() == nil {
		if err := p.selfTest(ctx); err != nil {
			return err
		}
		if ctx.Err() != nil {
			return nil
		}
	}
	con.info("pool", "Maintaining %d slots for %s; label %s", cfg.Slots, cfg.Repo, cfg.Label)
	con.info("pool", "backend: %s", cfg.Backend)

	var wg sync.WaitGroup
	wg.Go(func() {
		t := time.NewTicker(pruneEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				p.prune()
			}
		}
	})
	for slot := 1; slot <= cfg.Slots; slot++ {
		wg.Go(func() { p.slot(ctx, slot) })
	}
	wg.Wait()
	// A parent cancellation (Ctrl-C) is a clean stop; only our own fatal is an error.
	var f *fatalError
	if errors.As(context.Cause(ctx), &f) {
		return f.err
	}
	return nil
}

type runner struct {
	cfg    Config
	con    *console
	spec   spec.Spec
	cancel context.CancelCauseFunc
	logs   *runlog.Root
}

// prune applies retention to this Pool's log directories. Failures are only
// warnings: stale logs must never stop the Pool.
func (p *runner) prune() {
	if _, err := runlog.Prune(p.logs.Dir(), p.cfg.Identity.Prefix(), logMaxAge, p.con.now()); err != nil {
		p.con.warn("pool", "prune logs: %v", err)
	}
}

// runnerLog is one Runner's event log: every line goes to the console and to
// that Runner's host.log, in the same format. It is used only from the
// Runner's own goroutine, so host needs no extra locking (console has its own).
type runnerLog struct {
	con  *console
	who  string
	host io.Writer
}

func (l *runnerLog) info(format string, args ...any) {
	l.con.info(l.who, format, args...)
	l.con.line(l.host, l.who, format, args...)
}

// warnFileOnly records a line in host.log only. The caller returns the error,
// and the Slot loop prints it on the console.
func (l *runnerLog) warnFileOnly(format string, args ...any) {
	l.con.line(l.host, l.who, format, args...)
}

func (l *runnerLog) warn(format string, args ...any) {
	l.con.warn(l.who, format, args...)
	l.con.line(l.host, l.who, format, args...)
}

// copyDiagnostics best-effort copies dockerd.log and _diag out of sb into the
// Runner's directory. It uses its own 20 s context, not the Pool's, so it also
// runs after Ctrl-C. Anything that goes wrong is one warning; whatever was
// extracted is kept.
func (p *runner) copyDiagnostics(l *runnerLog, rd *runlog.Runner, sb *microsandbox.Sandbox) {
	ctx, cancel := context.WithTimeout(context.Background(), copyTimeout)
	defer cancel()
	pr, pw := io.Pipe()
	var extractErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		extractErr = rd.ExtractDiagnostics(pr)
		// Unblock the guest side if extraction stopped early.
		pr.CloseWithError(errors.New("extraction stopped"))
	}()
	copyErr := guest.CopyDiagnostics(ctx, sb, pw, l.host)
	pw.CloseWithError(copyErr)
	<-done
	if err := errors.Join(copyErr, extractErr); err != nil {
		l.warn("copy diagnostics from %s: %v", sb.Name(), err)
	}
}

// fatalError marks the cause of a Pool-wide stop, so Run can tell it from a
// parent cancellation.
type fatalError struct{ err error }

func (e *fatalError) Error() string { return e.err.Error() }
func (e *fatalError) Unwrap() error { return e.err }

// fatal stops every Slot, and Run returns err once they have cleaned up. The
// first call wins.
func (p *runner) fatal(err error) { p.cancel(&fatalError{err}) }

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

// iterate is one Runner's life: create, register, run, copy out diagnostics,
// destroy, deregister.
func (p *runner) iterate(ctx context.Context, n int, who string) error {
	name := p.cfg.Identity.RunnerName(n)

	// Opened before Create, so a create error is recorded too. If it cannot be
	// made nothing has been created yet, and the Slot retries.
	rd, err := p.logs.Runner(name)
	if err != nil {
		return fmt.Errorf("log directory for %s: %w", name, err)
	}
	defer rd.Close()
	l := &runnerLog{con: p.con, who: who, host: rd.Host}

	// Never cancel a create: superradcompany/microsandbox#1687. Abandoned
	// creates are handled by a later ticket (#36).
	sb, err := p.cfg.Sandboxes.Create(context.WithoutCancel(ctx), name, p.spec.Options()...)
	if err != nil {
		// Backstop for the CLI's pre-check: msb vanished after it passed.
		if microsandbox.IsKind(err, microsandbox.ErrRuntimeNotInstalled) {
			p.fatal(fmt.Errorf("msb is not installed; install it with: %s", MsbInstallCmd))
		}
		err = fmt.Errorf("create %s: %w", name, err)
		l.warnFileOnly("%v", err)
		return err
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
			l.info("runner %s started", name)
			code, runErr = guest.Run(ctx, sb, jit.Encoded, io.MultiWriter(rd.Host, logs))
		}
	}
	if runErr != nil {
		l.warnFileOnly("%v", runErr)
	}

	// Explicit order, not defers (LIFO would reverse it): copy the evidence out
	// while the VM exists, destroy it, then deregister. All use non-cancelled
	// contexts so Ctrl-C leaks nothing.
	p.copyDiagnostics(l, rd, sb)
	p.destroy(l, sb)
	p.deregister(l, runnerID)

	switch {
	case runErr != nil:
		return runErr
	case ctx.Err() != nil:
		return nil
	case code == 0:
		l.info("runner finished; replacing VM")
	default:
		for _, line := range logs.Lines() {
			p.con.warn(who, "  | %s", line)
		}
		// An error, so the slot waits RetryDelay: a Runner that dies at once
		// must not turn the Slot into a hot create loop.
		err := fmt.Errorf("runner exited with status %d (logs: %s)", code, rd.Dir)
		l.warnFileOnly("%v", err)
		return err
	}
	return nil
}

// selfTest runs the Self-test in one unregistered sandbox built exactly like a
// Runner (same image, sizes and network policy). It returns nil when ctx ended
// first (Ctrl-C), and otherwise an error unless every check passed.
func (p *runner) selfTest(ctx context.Context) error {
	const who = "pool"
	// Slots count from 1, so slot 0 is never a Runner. The name carries the Pool
	// prefix, so startup Cleanup removes it if the Pool crashed mid-Self-test.
	name := p.cfg.Identity.RunnerName(0)
	rd, err := p.logs.Runner(name)
	if err != nil {
		return fmt.Errorf("self-test failed: log directory for %s: %w", name, err)
	}
	defer rd.Close()
	l := &runnerLog{con: p.con, who: who, host: rd.Host}
	l.info("self-test: starting")

	// Never cancel a create: superradcompany/microsandbox#1687.
	sb, err := p.cfg.Sandboxes.Create(context.WithoutCancel(ctx), name, p.spec.Options()...)
	if err != nil {
		if microsandbox.IsKind(err, microsandbox.ErrRuntimeNotInstalled) {
			return fmt.Errorf("msb is not installed; install it with: %s", MsbInstallCmd)
		}
		err = fmt.Errorf("self-test failed: create %s (logs: %s): %w", name, rd.Dir, err)
		l.warnFileOnly("%v", err)
		return err
	}
	// Copy out before destroy: on a failure dockerd.log is the evidence.
	defer func() {
		p.copyDiagnostics(l, rd, sb)
		p.destroy(l, sb)
	}()

	logs := &tail{n: tailLines}
	runCtx, cancel := context.WithTimeout(ctx, selfTestTimeout)
	defer cancel()
	code, err := guest.SelfTest(runCtx, sb, io.MultiWriter(rd.Host, logs))
	if ctx.Err() != nil {
		return nil
	}
	if err == nil && code == 0 {
		l.info("self-test passed")
		return nil
	}
	for _, line := range logs.Lines() {
		p.con.warn(who, "  | %s", line)
	}
	if err != nil {
		err = fmt.Errorf("self-test failed (logs: %s): %w", rd.Dir, err)
	} else {
		err = fmt.Errorf("self-test failed: script exited with status %d (logs: %s)", code, rd.Dir)
	}
	l.warnFileOnly("%v", err)
	return err
}

func (p *runner) destroy(l *runnerLog, sb *microsandbox.Sandbox) {
	ctx, cancel := context.WithTimeout(context.Background(), destroyTimeout)
	defer cancel()
	if err := sb.Destroy(ctx); err != nil && !microsandbox.IsKind(err, microsandbox.ErrSandboxNotFound) {
		l.warn("destroy %s: %v", sb.Name(), err)
	}
	sb.Close()
}

func (p *runner) deregister(l *runnerLog, id int64) {
	if id == 0 {
		return
	}
	// The port applies its own 30 s timeout.
	if err := p.cfg.GitHub.DeleteRunner(context.Background(), id); err != nil {
		l.warn("deregister runner %d: %v", id, err)
	}
}
