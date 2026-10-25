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
			// Nothing in THIS directory's project. If a Sukko stack is running elsewhere, say so
			// instead of the misleading "No services running" — `down` is scoped to the current
			// directory, so the operator needs to know where the running stack actually lives.
			if dir, found := mgr.FindRunningStackDir(cmd.Context()); found {
				fmt.Fprintf(cmd.ErrOrStderr(), "No Sukko project in this directory. A Sukko stack is running in %s — run 'sukko down' from there.\n", dir)
				return nil
			}
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
