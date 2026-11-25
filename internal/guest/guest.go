// Package guest runs the start-up script inside a Runner sandbox.
package guest

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"

	microsandbox "github.com/superradcompany/microsandbox/sdk/go"
)

//go:embed start.sh
var script string

// Run starts dockerd and the JIT runner in sb and blocks until the script
// exits. The JIT config is delivered through stdin only. Stdout and stderr of
// the script go to out. It returns the script's exit code; err is set when the
// exec itself failed or ctx ended.
func Run(ctx context.Context, sb *microsandbox.Sandbox, jitConfig string, out io.Writer) (int, error) {
	h, err := sb.ExecStream(ctx, "bash", []string{"-c", script},
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

	code := 0
	for {
		ev, err := h.Recv(ctx)
		if err != nil {
			return 0, fmt.Errorf("guest script: %w", err)
		}
		switch ev.Kind {
		case microsandbox.ExecEventStdout, microsandbox.ExecEventStderr:
			out.Write(ev.Data)
		case microsandbox.ExecEventExited:
			code = ev.ExitCode
		case microsandbox.ExecEventFailed:
			return 0, errors.New("guest script failed to run")
		case microsandbox.ExecEventDone:
			return code, nil
		}
	}
}
