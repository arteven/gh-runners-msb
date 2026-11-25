package cli

import (
	"fmt"
	"os"

	microsandbox "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/arteven/gh-runners-msb/internal/msbprofile"
	"github.com/arteven/gh-runners-msb/internal/pool"
)

// resolveBackend works out which Backend msb's active profile selects.
func resolveBackend() (msbprofile.Backend, error) {
	return msbprofile.Resolve(msbprofile.Input{
		Env:         os.LookupEnv,
		ManagedPath: msbprofile.DefaultManagedPath(),
	})
}

// pinBackend builds the one explicit SDK context for b. Handing only its
// Sandboxes to the Pool means a later `msb context use` cannot move the Pool:
// the SDK's package-level calls re-read the profile every time, so nothing
// downstream may use them.
func pinBackend(b msbprofile.Backend) (*microsandbox.Sandboxes, error) {
	if b.Kind == msbprofile.Remote {
		// Always pass URL and key: otherwise the SDK backfills from
		// MSB_API_KEY / MSB_API_URL, which a remote profile must ignore.
		rc, err := microsandbox.NewRemoteContextWithURL(b.URL, b.APIKey())
		if err != nil {
			return nil, fmt.Errorf("opening msb remote backend: %w", err)
		}
		return rc.Sandboxes(), nil
	}
	// The extension never installs or upgrades msb.
	if !microsandbox.IsRuntimeInstalled(microsandbox.RuntimeConfig{}) {
		return nil, fmt.Errorf("msb is not installed; install it with: %s", pool.MsbInstallCmd)
	}
	lc, err := microsandbox.NewLocalContext(microsandbox.LocalOptions{})
	if err != nil {
		return nil, fmt.Errorf("opening msb: %w", err)
	}
	return lc.Sandboxes(), nil
}
