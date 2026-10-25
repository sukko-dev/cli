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

		// A Compose error means there is no Sukko project in THIS directory — not that the stack
		// is down. Report it honestly, once, and point at the running stack's directory if one
		// exists elsewhere; the old blunt "not running" contradicted the live health probed below.
		if composeErr != nil {
			if dir, found := mgr.FindRunningStackDir(cmd.Context()); found {
				fmt.Fprintf(cmd.ErrOrStderr(), "No Sukko Compose project in this directory — a stack is running in %s. Showing live health below.\n", dir)
			} else {
				fmt.Fprintf(cmd.ErrOrStderr(), "No Sukko Compose project in this directory (%v).\n", composeErr)
			}
		}

		if output == "json" && composeErr == nil {
			return printJSON(services)
		}
		// Otherwise fall through to the table (covers table mode, and json mode with no local
		// Compose where we still show live health from the endpoint probes below).

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

		// status reports the LOCAL stack, so it probes the local endpoints, never the active
		// context — otherwise `status` with a remote context active would probe prod and
		// mislabel it as local status. Use `sukko health` to probe whichever context is active.
		provURL, gwHTTP, testerURL := localStackProbeURLs()
		serverURL := defaultServerURL

		if resolvedCtx != nil && !resolvedCtx.IsLocal() {
			fmt.Fprintf(cmd.ErrOrStderr(), "Note: 'sukko status' reports the local stack; use 'sukko health' for the active context %q.\n", resolvedCtx.Name)
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
			// No compose in this directory — probe the local endpoints directly, in case a
			// stack is running without a compose project here.
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

// localStackProbeURLs returns the provisioning, gateway-HTTP, and tester URLs for the LOCAL
// stack — the local context's URLs, or the localhost defaults. It never consults the active
// context, so `status` reports the local stack even when a remote context is active.
func localStackProbeURLs() (prov, gatewayHTTP, tester string) {
	prov, gatewayHTTP, tester = defaultAPIURL, defaultGatewayHTTP, defaultTesterURL
	if resolvedStore == nil {
		return
	}
	// best-effort: an unreadable store falls back to the loopback defaults above.
	localCtx, _ := resolvedStore.FindLocalContext()
	if localCtx == nil {
		return
	}
	if localCtx.ProvisioningURL != "" {
		prov = localCtx.ProvisioningURL
	}
	if localCtx.GatewayURL != "" {
		gatewayHTTP = wsToHTTP(localCtx.GatewayURL)
	}
	if localCtx.TesterURL != "" {
		tester = localCtx.TesterURL
	}
	return
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
