package commands

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/sukko-dev/cli/compose"
)

var logsFollow bool

func init() {
	logsCmd.Flags().BoolVarP(&logsFollow, "follow", "f", false, "Follow log output")
	rootCmd.AddCommand(logsCmd)
}

var logsCmd = &cobra.Command{
	Use:   "logs [service...]",
	Short: "View logs from local Docker Compose services",
	Long: `View logs from local Docker Compose services.

Only available for local Docker Compose environments. Specify service names
to filter (e.g., 'sukko logs ws-gateway ws-server').`,
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := compose.NewManager(".", composeFilePath())
		if err != nil {
			return fmt.Errorf("create compose manager: %w", err)
		}

		if !mgr.IsRunning(cmd.Context()) {
			return errors.New("logs only available for local Docker Compose environments (no compose project running)")
		}

		return mgr.Logs(cmd.Context(), args, logsFollow)
	},
}
