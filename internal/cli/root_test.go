package cli

import (
	"bytes"
	"os"
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

func TestCleanupIsStillAStub(t *testing.T) {
	cmd := NewRootCmd("dev")
	cmd.SetArgs([]string{"cleanup"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "not implemented yet") {
		t.Errorf("cleanup: want not-implemented error, got %v", err)
	}
}

func TestUpRejectsBadFlagsBeforeAnyIO(t *testing.T) {
	// Point every place up could touch at an empty dir, then check nothing appears.
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("HOME", home)
	t.Setenv("MSB_HOME", home)

	for _, args := range [][]string{
		{"up", "-R", "o/r", "--slots", "0"},
		{"up", "-R", "o/r", "--slots", "-3"},
		{"up", "-R", "o/r", "--label", " "},
		{"up", "-R", "o/r", "--cpus", "0"},
		{"up", "-R", "o/r", "--memory", "4X"},
		{"up", "-R", "o/r", "--disk", "0G"},
		{"up", "-R", "o/r", "--disk", "2G", "--docker-disk", "2G"},
		{"up", "-R", "ghe.example.com/o/r"},
	} {
		cmd := NewRootCmd("dev")
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Errorf("%v: want error", args)
		}
	}
	entries, _ := os.ReadDir(home)
	if len(entries) != 0 {
		t.Errorf("up touched the filesystem before validating flags: %v", entries)
	}
}

func TestUpWithoutLocalMsbStopsBeforeCreatingAnything(t *testing.T) {
	// An empty home holds no msb runtime; an empty MSB_HOME also hides any
	// profile the developer has saved.
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("HOME", home)
	t.Setenv("MSB_HOME", home)
	t.Setenv("MSB_CONFIG_PATH", "")
	for _, k := range []string{"MSB_BACKEND", "MSB_PROFILE", "MSB_API_KEY", "MSB_API_URL", "MSB_PATH", "MSB_LIBKRUNFW_PATH"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}

	var out bytes.Buffer
	cmd := NewRootCmd("dev")
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"up", "-R", "o/r"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "curl -fsSL https://install.microsandbox.dev | sh") {
		t.Fatalf("want an error carrying the msb install command, got %v", err)
	}
	if strings.Contains(out.String(), "Maintaining") {
		t.Errorf("the Pool started despite a missing msb:\n%s", out.String())
	}
}

func TestUpRefusesNonGitHubHost(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("HOME", home)
	cmd := NewRootCmd("dev")
	cmd.SetArgs([]string{"up", "-R", "ghe.example.com/o/r"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "github.com") {
		t.Errorf("err = %v, want it to name github.com", err)
	}
}
