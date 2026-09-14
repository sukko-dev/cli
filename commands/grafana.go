package commands

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(grafanaCmd)
}

var grafanaCmd = &cobra.Command{
	Use:   "grafana",
	Short: "Open Grafana in the browser",
	Long:  "Open the Grafana dashboard in the system browser. Requires observability to be enabled.",
	RunE:  runGrafana,
}

func runGrafana(cmd *cobra.Command, _ []string) error {
	cfg, err := loadProjectConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if cfg == nil || !cfg.Observability {
		fmt.Fprintln(cmd.OutOrStdout(), "Observability not enabled. Run 'sukko init' to enable it.")
		return nil
	}

	url := "http://localhost:3030"
	if err := openBrowser(cmd.Context(), url); err != nil {
		return fmt.Errorf("open browser: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Opening Grafana at %s\n", url)
	return nil
}

// openBrowser opens the given URL in the system browser.
func openBrowser(ctx context.Context, url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", url) //nolint:gosec // G204: URL is a fixed internal string, not user input
	case "linux":
		cmd = exec.CommandContext(ctx, "xdg-open", url) //nolint:gosec // G204: URL is a fixed internal string, not user input
	case "windows":
		cmd = exec.CommandContext(ctx, "cmd", "/c", "start", url) //nolint:gosec // G204: URL is a fixed internal string, not user input
	default:
		return fmt.Errorf("unsupported platform %s — open %s manually", runtime.GOOS, url)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start browser: %w", err)
	}
	return nil
}
