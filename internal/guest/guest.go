// Package guest runs the start-up scripts inside a sandbox: the Runner script
// and the Self-test script, which share one Docker start-up prelude.
package guest

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"

	microsandbox "github.com/superradcompany/microsandbox/sdk/go"
)

//go:embed prelude.sh
var prelude string

//go:embed runner.sh
var runnerTail string

//go:embed selftest.sh
var selfTestTail string

var (
	runnerScript   = prelude + runnerTail
	selfTestScript = prelude + selfTestTail
)

// Run starts dockerd and the JIT runner in sb and blocks until the script
// exits. The JIT config is delivered through stdin only. Stdout and stderr of
// the script go to out. It returns the script's exit code; err is set when the
// exec itself failed or ctx ended.
func Run(ctx context.Context, sb *microsandbox.Sandbox, jitConfig string, out io.Writer) (int, error) {
	h, err := sb.ExecStream(ctx, "bash", []string{"-c", runnerScript},
		microsandbox.WithExecUser("runner"),
		microsandbox.WithExecCwd("/home/runner"),
		microsandbox.WithExecStdinPipe(),
		microsandbox.WithExecEnv(map[string]string{
			"ACTIONS_RUNNER_PRINT_LOG_TO_STDOUT": "1",
			"RUNNER_MANUALLY_TRAP_SIG":           "0",
		}),
	)
	if err != nil {
		return 0, fmt.Errorf("start guest script: %w", err)
	}
	defer h.Close()

	stdin := h.TakeStdin()
	if _, err := io.WriteString(stdin, jitConfig+"\n"); err != nil {
		return 0, fmt.Errorf("send JIT config: %w", err)
	}
	if err := stdin.Close(); err != nil {
		return 0, fmt.Errorf("close stdin: %w", err)
	}
	return drain(ctx, h, out, out)
}

// SelfTest runs the Self-test checks in sb: Docker ready, docker info as
// runner, GitHub reachable, the metadata endpoint unreachable. Each check
// prints a "self-test: ok|FAIL <check>" line to out. It returns the script's
// exit code (0 only if every check passed); err is set when the exec itself
// failed or ctx ended. Unlike Run it takes no stdin and no runner env.
func SelfTest(ctx context.Context, sb *microsandbox.Sandbox, out io.Writer) (int, error) {
	h, err := sb.ExecStream(ctx, "bash", []string{"-c", selfTestScript},
		microsandbox.WithExecUser("runner"),
		microsandbox.WithExecCwd("/home/runner"),
	)
	if err != nil {
		return 0, fmt.Errorf("start self-test script: %w", err)
	}
	defer h.Close()
	return drain(ctx, h, out, out)
}

// CopyDiagnostics streams dockerd's log and the Runner's _diag directory out of
// sb as a tar archive on archive; tar's own stderr goes to errs. A missing path
// is not a failure (--ignore-failed-read): the fake runner and early failures
// have no _diag. It returns an error when the exec failed or tar exited
// non-zero. The in-VM paths live only here: /tmp/dockerd.log is written by
// prelude.sh, and /home/runner/_diag is the actions-runner's diagnostics dir.
//
// It runs as root through sudo, as the prelude does, because a job may leave
// root-owned files in _diag. The archive is untrusted: a job can replace tar.
func CopyDiagnostics(ctx context.Context, sb *microsandbox.Sandbox, archive, errs io.Writer) error {
	h, err := sb.ExecStream(ctx, "sudo", []string{"-n", "tar", "-cf", "-", "--ignore-failed-read",
		"-C", "/tmp", "dockerd.log", "-C", "/home/runner", "_diag"},
		microsandbox.WithExecUser("runner"),
		microsandbox.WithExecCwd("/home/runner"),
	)
	if err != nil {
		return fmt.Errorf("start diagnostics copy: %w", err)
	}
	defer h.Close()
	code, err := drain(ctx, h, archive, errs)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("tar exited with status %d", code)
	}
	return nil
}

// drain copies the script's stdout to stdout and stderr to stderr until it is
// done.
func drain(ctx context.Context, h *microsandbox.ExecHandle, stdout, stderr io.Writer) (int, error) {
	code := 0
	for {
		ev, err := h.Recv(ctx)
		if err != nil {
			return 0, fmt.Errorf("guest script: %w", err)
		}
		switch ev.Kind {
		case microsandbox.ExecEventStdout:
			stdout.Write(ev.Data)
		case microsandbox.ExecEventStderr:
			stderr.Write(ev.Data)
		case microsandbox.ExecEventExited:
			code = ev.ExitCode
		case microsandbox.ExecEventFailed:
			return 0, errors.New("guest script failed to run")
		case microsandbox.ExecEventDone:
			return code, nil
		}
	}
}
