package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var version = "dev"

// Version returns the current build version string.
func Version() string { return version }

// NewVersionCmd returns the version command.
func NewVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the slackcli version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "slackcli %s · Christoph Gross\n", version)
			return nil
		},
	}
}
