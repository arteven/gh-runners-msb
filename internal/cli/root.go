// Package cli builds the cobra command tree for gh runners-msb.
package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"
	microsandbox "github.com/superradcompany/microsandbox/sdk/go"
)

// imageContractMajor is the Image contract major this build speaks.
// See docs/adr/0002 for what the contract covers.
const imageContractMajor = 1

// NewRootCmd returns the root command. version is the extension version,
// stamped by main via -ldflags.
func NewRootCmd(version string) *cobra.Command {
	var showVersion bool

	root := &cobra.Command{
		Use:           "runners-msb",
		Short:         "Run GitHub Actions self-hosted runners in msb microVMs",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if showVersion {
				return writeVersion(cmd.OutOrStdout(), version)
			}
			return cmd.Help()
		},
	}
	// A plain flag rather than cobra's Version field, so the FFI library is
	// loaded only when --version is asked for, not on every invocation.
	root.Flags().BoolVar(&showVersion, "version", false, "print version information")

	root.AddCommand(stubCmd("up", "Start runners for a repository"))
	root.AddCommand(stubCmd("cleanup", "Remove leftover runners and sandboxes"))
	return root
}

func stubCmd(verb, short string) *cobra.Command {
	return &cobra.Command{
		Use:   verb,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return errors.New(verb + ": not implemented yet")
		},
	}
}

// writeVersion prints the extension, SDK/FFI and Image contract versions.
// RuntimeVersion loads the embedded FFI library, so a broken bundle fails here.
func writeVersion(w io.Writer, version string) error {
	ffi, err := microsandbox.RuntimeVersion()
	if err != nil {
		return fmt.Errorf("loading msb FFI: %w", err)
	}
	_, err = fmt.Fprintf(w, "gh runners-msb %s\nmsb SDK %s (FFI %s)\nImage contract %d\n",
		version, microsandbox.SDKVersion(), ffi, imageContractMajor)
	return err
}
