package commands

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/sukko-dev/cli/compose"
)

var removeVolumes bool

func init() {
	downCmd.Flags().BoolVarP(&removeVolumes, "volumes", "v", false, "Remove volumes (full reset)")
	rootCmd.AddCommand(downCmd)
}

var downCmd = &cobra.Command{
	Use:   "down",
	Short: "Stop the local development environment",
	Long:  "Stop and remove local Docker Compose services. Use -v to also remove volumes.",
	RunE: func(cmd *cobra.Command, _ []string) error {
		mgr, err := compose.NewManager(".", composeFilePath())
		if err != nil {
			return fmt.Errorf("create compose manager: %w", err)
		}

		if !mgr.IsRunning(cmd.Context()) {
			fmt.Fprintln(cmd.OutOrStdout(), "No services running.")
			return nil
		}

		fmt.Fprintln(cmd.OutOrStdout(), "Stopping Sukko services...")
		if err := mgr.Down(cmd.Context(), removeVolumes); err != nil {
			return fmt.Errorf("stop services: %w", err)
		}

		msg := "Services stopped."
		if removeVolumes {
			msg = "Services stopped and volumes removed."
		}
		fmt.Fprintln(cmd.OutOrStdout(), msg)
		return nil
	},
}
