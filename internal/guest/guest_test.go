package guest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestScriptSyntax(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	f := filepath.Join(t.TempDir(), "start.sh")
	if err := os.WriteFile(f, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(bash, "-n", f).CombinedOutput(); err != nil {
		t.Fatalf("bash -n: %v\n%s", err, out)
	}
}

func TestScriptNeverLogsJITConfig(t *testing.T) {
	if strings.Contains(script, "set -x") || strings.Contains(script, "set -o xtrace") {
		t.Error("script must not enable xtrace")
	}
	if !strings.Contains(script, "read -r config") {
		t.Error("script must read the JIT config from stdin")
	}
	for _, line := range strings.Split(script, "\n") {
		if strings.Contains(line, "echo") && strings.Contains(line, "$config") {
			t.Errorf("script echoes the JIT config: %s", line)
		}
	}
	if !strings.Contains(script, "unset config") {
		t.Error("script must unset the JIT config")
	}
}
