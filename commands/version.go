package commands

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/sukko-dev/cli/internal/version"
)

func init() {
	rootCmd.AddCommand(versionCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show CLI version information",
	RunE: func(cmd *cobra.Command, _ []string) error {
		info := version.Get("sukko-cli")

		if output == "json" {
			return printJSON(info)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "sukko-cli %s (commit: %s, built: %s)\n",
			info.Version, info.CommitHash, info.BuildTime)
		return nil
	},
}
