package cli

import (
	"bytes"
	"strings"
	"testing"

	microsandbox "github.com/superradcompany/microsandbox/sdk/go"
)

func TestVersionFlagReportsExtensionSDKAndImageContract(t *testing.T) {
	// Keep the embedded FFI library out of the real ~/.microsandbox.
	t.Setenv("MSB_HOME", t.TempDir())

	var out bytes.Buffer
	cmd := NewRootCmd("v9.9.9")
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("--version failed: %v", err)
	}

	got := out.String()
	for _, want := range []string{"v9.9.9", microsandbox.SDKVersion(), "Image contract 1"} {
		if !strings.Contains(got, want) {
			t.Errorf("--version output missing %q:\n%s", want, got)
		}
	}
}

func TestStubVerbsAreNotImplemented(t *testing.T) {
	for _, verb := range []string{"up", "cleanup"} {
		cmd := NewRootCmd("dev")
		cmd.SetArgs([]string{verb})
		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "not implemented yet") {
			t.Errorf("%s: want not-implemented error, got %v", verb, err)
		}
	}
}
