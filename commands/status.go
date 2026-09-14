package commands

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"
	"github.com/sukko-dev/cli/compose"
)

const statusHTTPTimeout = 3 * time.Second

func init() {
	rootCmd.AddCommand(statusCmd)
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show status of all services",
	Long:  "Show a unified view of all Sukko services with health status.",
	RunE: func(cmd *cobra.Command, _ []string) error {
		mgr, err := compose.NewManager(".", composeFilePath())
		if err != nil {
			return fmt.Errorf("create compose manager: %w", err)
		}

		services, composeErr := mgr.Status(cmd.Context())

		if output == "json" {
			if composeErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Docker Compose: not running (%v)\n", composeErr)
			} else {
				return printJSON(services)
			}
		}

		// Table path — reuse the same result
		if composeErr != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "Docker Compose: not running (%v)\n", composeErr)
		}

		var items []StatusItem
		if len(services) > 0 {
			for _, svc := range services {
				health := svc.Health
				if health == "" {
					health = svc.State
				}
				items = append(items, StatusItem{
					Name:    svc.Service,
					Status:  health,
					Details: svc.Status,
				})
			}
		}

		// Resolve URLs from context before building endpoints.
		provURL := defaultAPIURL
		gwHTTP := defaultGatewayHTTP
		serverURL := defaultServerURL
		testerURL := defaultTesterURL

		if resolvedCtx != nil {
			if resolvedCtx.ProvisioningURL != "" {
				provURL = resolvedCtx.ProvisioningURL
			}
			if resolvedCtx.GatewayURL != "" {
				gwHTTP = wsToHTTP(resolvedCtx.GatewayURL)
			}
			if resolvedCtx.TesterURL != "" {
				testerURL = resolvedCtx.TesterURL
			}
		}

		// Health endpoint checks
		endpoints := []struct {
			name string
			url  string
		}{
			{"provisioning", provURL + "/health"},
			{"ws-gateway", gwHTTP + "/health"},
			{"ws-server", serverURL + "/health"},
			{"sukko-tester", testerURL + "/health"},
		}

		// Add observability endpoints if enabled
		if cfg, err := loadProjectConfig(); err == nil && cfg != nil && cfg.Observability {
			endpoints = append(endpoints,
				struct{ name, url string }{"grafana", "http://localhost:3030/api/health"},
				struct{ name, url string }{"prometheus", "http://localhost:9091/-/healthy"},
			)
		}

		if len(services) == 0 {
			// No compose — try health endpoints directly (remote mode)
			ctx := cmd.Context()
			for _, ep := range endpoints {
				status := checkHealth(ctx, ep.url)
				items = append(items, StatusItem{
					Name:   ep.name,
					Status: status,
				})
			}
		}

		if len(items) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No services found. Run 'sukko up' to start local services.")
			return nil
		}

		printStatus(items)
		return nil
	},
}

func checkHealth(ctx context.Context, url string) string {
	httpClient := &http.Client{Timeout: statusHTTPTimeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return "unreachable"
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "unreachable"
	}
	defer resp.Body.Close() // close error inconsequential for health probe
	if resp.StatusCode == http.StatusOK {
		return "healthy"
	}
	return "unhealthy"
}
