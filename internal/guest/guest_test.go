package guest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var scripts = map[string]string{"runner": runnerScript, "selftest": selfTestScript}

func TestScriptSyntax(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	for name, s := range scripts {
		t.Run(name, func(t *testing.T) {
			f := filepath.Join(t.TempDir(), name+".sh")
			if err := os.WriteFile(f, []byte(s), 0o600); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(bash, "-n", f).CombinedOutput(); err != nil {
				t.Fatalf("bash -n: %v\n%s", err, out)
			}
		})
	}
}

func TestScriptsShareTheDockerStartUp(t *testing.T) {
	for name, s := range scripts {
		if !strings.HasPrefix(s, prelude) || !strings.Contains(s, "docker_ready") {
			t.Errorf("%s script does not use the shared prelude", name)
		}
	}
}

func TestRunnerScriptNeverLogsJITConfig(t *testing.T) {
	script := runnerScript
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

func TestSelfTestScriptChecks(t *testing.T) {
	for _, want := range []string{
		"https://api.github.com",
		"--max-time 3 http://169.254.169.254/",
		"docker info",
		"self-test: FAIL",
		"self-test: ok",
	} {
		if !strings.Contains(selfTestScript, want) {
			t.Errorf("self-test script lacks %q", want)
		}
	}
	if strings.Contains(selfTestScript, "config") && strings.Contains(selfTestScript, "run.sh") {
		t.Error("self-test must not register or start a runner")
	}
}
