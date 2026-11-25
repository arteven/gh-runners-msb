// Command gh-runners-msb is a gh extension that runs GitHub Actions
// self-hosted runners in msb microVMs.
package main

import (
	"fmt"
	"os"

	"github.com/arteven/gh-runners-msb/internal/cli"
)

// version is stamped at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := cli.NewRootCmd(version).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
