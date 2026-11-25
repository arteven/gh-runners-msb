//go:build integration

// Drives the Pool in-process with the fake GitHub port and real local msb
// microVMs. Needs KVM and a Docker daemon to build the test image. Locally:
//
//	docker build --build-arg RUNNER_VERSION=2.x.y -t grm-base image/
//	docker build --build-arg BASE=grm-base -t grm-it internal/pool/testdata/fake-runner
//	docker save -o /tmp/it.tar grm-it
//	INTEGRATION_IMAGE_TAR=/tmp/it.tar go test -tags integration -timeout 20m ./internal/pool/
//
// TestSelfTestFailureStopsThePool also needs a non-conforming image (no dockerd):
//
//	docker build --build-arg BASE=grm-base -t grm-bad internal/pool/testdata/broken-runner
//	docker save -o /tmp/bad.tar grm-bad
//	INTEGRATION_BAD_IMAGE_TAR=/tmp/bad.tar ... (as above)
package pool_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	microsandbox "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/arteven/gh-runners-msb/internal/github/githubtest"
	"github.com/arteven/gh-runners-msb/internal/identity"
	"github.com/arteven/gh-runners-msb/internal/msbprofile"
	"github.com/arteven/gh-runners-msb/internal/pool"
)

// A localhost/ tag keeps msb from ever looking up registry credentials.
const (
	testImage    = "localhost/gh-runners-msb-test:ci"
	testBadImage = "localhost/gh-runners-msb-test:bad"
)

// badImageLoaded is set when INTEGRATION_BAD_IMAGE_TAR was loaded as testBadImage.
var badImageLoaded bool

// msbConfigPath is the msb config the tests resolve their Backend from. It has
// no active_profile, so the Backend is local until a test flips it.
var msbConfigPath string

func TestMain(m *testing.M) { os.Exit(testMain(m)) }

func testMain(m *testing.M) int {
	tar := os.Getenv("INTEGRATION_IMAGE_TAR")
	if tar == "" {
		os.Stderr.WriteString("INTEGRATION_IMAGE_TAR unset; skipping integration tests\n")
		return 0
	}
	// Not MSB_BACKEND=local: that would beat any saved profile and make the
	// profile-flip test prove nothing. Image.Load uses the default context, which
	// resolves to local through this config.
	dir, err := os.MkdirTemp("", "msb-config-")
	if err != nil {
		os.Stderr.WriteString("MkdirTemp: " + err.Error() + "\n")
		return 1
	}
	defer os.RemoveAll(dir)
	msbConfigPath = filepath.Join(dir, "config.json")
	if err := os.WriteFile(msbConfigPath, []byte(`{"version":1}`), 0o644); err != nil {
		os.Stderr.WriteString("write config: " + err.Error() + "\n")
		return 1
	}
	os.Setenv("MSB_CONFIG_PATH", msbConfigPath)
	for _, k := range []string{"MSB_BACKEND", "MSB_PROFILE", "MSB_API_KEY", "MSB_API_URL"} {
		os.Unsetenv(k)
	}
	ctx := context.Background()
	// Test-only: the Pool itself never installs msb.
	if _, err := microsandbox.EnsureRuntime(ctx, microsandbox.RuntimeConfig{}, microsandbox.InstallOptions{}); err != nil {
		os.Stderr.WriteString("EnsureRuntime: " + err.Error() + "\n")
		return 1
	}
	if _, err := microsandbox.Image.Load(ctx, tar, testImage); err != nil {
		os.Stderr.WriteString("Image.Load: " + err.Error() + "\n")
		return 1
	}
	if bad := os.Getenv("INTEGRATION_BAD_IMAGE_TAR"); bad != "" {
		if _, err := microsandbox.Image.Load(ctx, bad, testBadImage); err != nil {
			os.Stderr.WriteString("Image.Load (bad): " + err.Error() + "\n")
			return 1
		}
		badImageLoaded = true
	}
	return m.Run()
}

func eventually(t *testing.T, what string, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestOneSlotFullLifecycle(t *testing.T) {
	const tokenSentinel = "gh-token-sentinel"
	t.Setenv("GH_TOKEN", tokenSentinel)

	// Resolve and pin exactly as `up` does.
	backend, err := msbprofile.Resolve(msbprofile.Input{Env: os.LookupEnv, ManagedPath: filepath.Join(t.TempDir(), "none.json")})
	if err != nil {
		t.Fatal(err)
	}
	if backend.Kind != msbprofile.Local {
		t.Fatalf("backend = %v, want local", backend)
	}
	lc, err := microsandbox.NewLocalContext(microsandbox.LocalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sandboxes := lc.Sandboxes()
	gh := &githubtest.Fake{}
	id := identity.NewPool("integration-install", "github.com/o/r", "msb-docker")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out, errOut syncBuf
	done := make(chan error, 1)
	go func() {
		done <- pool.Run(ctx, pool.Config{
			Slots: 1, Label: "msb-docker", Repo: "o/r", Identity: id, Image: testImage,
			GitHub: gh, Backend: backend, Sandboxes: sandboxes, Out: &out, Err: &errOut,
			RetryDelay: time.Second,
		})
	}()
	stopped := false
	t.Cleanup(func() {
		cancel()
		if !stopped {
			<-done
		}
	})

	// 1. A Runner registers.
	eventually(t, "first registration", 3*time.Minute, func() bool { return len(gh.Registrations()) >= 1 })
	reg1 := gh.Registrations()[0]
	// The Self-test ran first, unregistered, and passed: Docker works, GitHub is
	// reachable and the metadata endpoint is blocked, so the policy is enforced.
	if o := out.String(); !strings.Contains(o, "self-test passed") ||
		strings.Index(o, "self-test passed") > strings.Index(o, "Maintaining") {
		t.Errorf("console lacks self-test passed before the banner:\n%s", o)
	}
	for _, r := range gh.Registrations() {
		if strings.HasPrefix(strings.TrimPrefix(r.Name, id.Prefix()), "0-") {
			t.Errorf("the Self-test registered %q", r.Name)
		}
	}
	if got := strings.Join(reg1.Labels, ","); got != "self-hosted,msb-docker" {
		t.Errorf("labels = %s", got)
	}
	if !id.Owns(reg1.Name) || !strings.HasPrefix(strings.TrimPrefix(reg1.Name, id.Prefix()), "1-") {
		t.Errorf("name %q is not slot 1 of this pool", reg1.Name)
	}

	// 2. The stand-in runner is listening in the VM.
	sb, err := sandboxes.Get(ctx, reg1.Name)
	if err != nil {
		t.Fatalf("Get %s: %v", reg1.Name, err)
	}
	defer sb.Close()
	sh := func(cmd string) string {
		t.Helper()
		o, err := sb.Exec(ctx, "bash", []string{"-c", cmd})
		if err != nil {
			t.Fatalf("exec %q: %v", cmd, err)
		}
		return o.Stdout()
	}
	eventually(t, "stand-in runner listening", 3*time.Minute, func() bool {
		o, err := sb.Exec(ctx, "test", []string{"-f", "/tmp/fake-runner/listening"})
		return err == nil && o.Success()
	})

	// 3. What the runner saw: JIT config via stdin only, right user and cwd,
	// no JIT config or gh token in its environment.
	if got := sh("cat /tmp/fake-runner/jitconfig"); got != reg1.Encoded {
		t.Errorf("jitconfig in guest = %q, want %q", got, reg1.Encoded)
	}
	if got := strings.TrimSpace(sh("cat /tmp/fake-runner/user")); got != "runner" {
		t.Errorf("user = %q", got)
	}
	if got := strings.TrimSpace(sh("cat /tmp/fake-runner/cwd")); got != "/home/runner" {
		t.Errorf("cwd = %q", got)
	}
	env := sh("cat /tmp/fake-runner/env")
	for _, secret := range []string{reg1.Encoded, tokenSentinel} {
		if strings.Contains(env, secret) {
			t.Errorf("runner environment contains %q", secret)
		}
	}
	for _, want := range []string{"ACTIONS_RUNNER_PRINT_LOG_TO_STDOUT=1", "RUNNER_MANUALLY_TRAP_SIG=0"} {
		if !strings.Contains(env, want) {
			t.Errorf("runner environment lacks %s", want)
		}
	}

	// 4. Flip the saved profile to a dead remote Backend mid-run, then take the
	// "job": the Runner exits, is destroyed, deregistered, replaced. The refill
	// registers and is found on the pinned local context only if every SDK call
	// goes through that context; an unpinned call would chase the dead URL.
	flipped := `{"version":1,"active_profile":"dead","profiles":{"dead":{"kind":"remote","url":"http://127.0.0.1:1","api_key_ref":"inline:x"}}}`
	if err := os.WriteFile(msbConfigPath, []byte(flipped), 0o644); err != nil {
		t.Fatal(err)
	}
	if flippedTo, err := msbprofile.Resolve(msbprofile.Input{Env: os.LookupEnv, ManagedPath: filepath.Join(t.TempDir(), "none.json")}); err != nil || flippedTo.Kind != msbprofile.Remote {
		t.Fatalf("flip did not take effect: %v, %v", flippedTo, err)
	}
	sh("touch /tmp/fake-runner/take-job")
	eventually(t, "VM destroyed, runner deregistered and slot refilled", 3*time.Minute, func() bool {
		_, err := sandboxes.Get(ctx, reg1.Name)
		if !microsandbox.IsKind(err, microsandbox.ErrSandboxNotFound) {
			return false
		}
		deleted := false
		for _, d := range gh.Deleted() {
			deleted = deleted || d == reg1.ID
		}
		return deleted && len(gh.Registrations()) >= 2
	})
	reg2 := gh.Registrations()[1]
	if _, err := sandboxes.Get(ctx, reg2.Name); err != nil {
		t.Errorf("refill %s is not on the pinned context after the profile flip: %v", reg2.Name, err)
	}
	if reg2.Name == reg1.Name || !id.Owns(reg2.Name) {
		t.Errorf("replacement name %q (first %q)", reg2.Name, reg1.Name)
	}
	if !strings.Contains(out.String(), "backend: local (default)") {
		t.Errorf("banner lacks the backend line:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "runner finished; replacing VM") {
		t.Errorf("console lacks the finished line:\n%s", out.String())
	}

	// 5. Stopping the Pool returns nil and leaves no sandbox behind.
	cancel()
	select {
	case err := <-done:
		stopped = true
		if err != nil {
			t.Errorf("Run returned %v", err)
		}
	case <-time.After(3 * time.Minute):
		t.Fatal("Run did not return after cancel")
	}
	page, err := sandboxes.List(context.Background(), microsandbox.WithListLabels(map[string]string{identity.LabelKey: id.ID}))
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Sandboxes) != 0 {
		t.Errorf("%d sandboxes left behind", len(page.Sandboxes))
	}
}

// An image that cannot start Docker fails the Self-test: the Pool returns an
// error, prints a readable log tail, registers nothing and leaves no sandbox.
func TestSelfTestFailureStopsThePool(t *testing.T) {
	if !badImageLoaded {
		t.Skip("INTEGRATION_BAD_IMAGE_TAR unset")
	}
	backend, err := msbprofile.Resolve(msbprofile.Input{Env: os.LookupEnv, ManagedPath: filepath.Join(t.TempDir(), "none.json")})
	if err != nil {
		t.Fatal(err)
	}
	lc, err := microsandbox.NewLocalContext(microsandbox.LocalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sandboxes := lc.Sandboxes()
	gh := &githubtest.Fake{}
	id := identity.NewPool("integration-install", "github.com/o/bad", "msb-docker")

	var out, errOut syncBuf
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	err = pool.Run(ctx, pool.Config{
		Slots: 1, Label: "msb-docker", Repo: "o/bad", Identity: id, Image: testBadImage,
		GitHub: gh, Backend: backend, Sandboxes: sandboxes, Out: &out, Err: &errOut,
		RetryDelay: time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "self-test failed") {
		t.Fatalf("Run = %v, want a self-test failure", err)
	}
	if !strings.Contains(errOut.String(), "self-test: FAIL docker ready") {
		t.Errorf("stderr lacks the log tail:\n%s", errOut.String())
	}
	if strings.Contains(out.String(), "Maintaining") {
		t.Errorf("banner printed despite the failed self-test:\n%s", out.String())
	}
	if n := len(gh.Registrations()); n != 0 {
		t.Errorf("%d registrations, want none", n)
	}
	page, err := sandboxes.List(context.Background(), microsandbox.WithListLabels(map[string]string{identity.LabelKey: id.ID}))
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Sandboxes) != 0 {
		t.Errorf("%d sandboxes left behind", len(page.Sandboxes))
	}
}

// syncBuf is a bytes.Buffer safe for the Pool to write while the test reads.
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}
