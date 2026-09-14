package commands

import (
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"
)

const healthHTTPTimeout = 5 * time.Second

func init() {
	rootCmd.AddCommand(healthCmd)
}

var healthCmd = &cobra.Command{
	Use:   "health",
	Short: "Check health of all Sukko components",
	Long: `Check health of all Sukko components.

Probes gateway, server, provisioning, and their dependencies. Reports
per-component status with troubleshooting suggestions on failure.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		type healthCheck struct {
			name    string
			url     string
			suggest string
		}

		// Resolve URLs from context before building checks (no index-based patching).
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

		checks := []healthCheck{
			{
				name:    "provisioning",
				url:     provURL + "/health",
				suggest: "Check PROVISIONING_URL. Is the provisioning service running?",
			},
			{
				name:    "provisioning (ready)",
				url:     provURL + "/ready",
				suggest: "Provisioning is up but not ready. Check database connectivity.",
			},
			{
				name:    "ws-gateway",
				url:     gwHTTP + "/health",
				suggest: "Check gateway URL. Is ws-gateway running?",
			},
			{
				name:    "ws-server",
				url:     serverURL + "/health",
				suggest: "Check ws-server. Is it running? Check VALKEY_ADDRS connectivity.",
			},
			{
				name:    "sukko-tester",
				url:     testerURL + "/health",
				suggest: "Tester service not running. Start with 'sukko up' or deploy separately.",
			},
		}

		httpClient := &http.Client{Timeout: healthHTTPTimeout}
		ctx := cmd.Context()

		items := make([]StatusItem, 0, len(checks))
		allHealthy := true

		for _, check := range checks {
			status := "healthy"
			details := ""

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, check.url, http.NoBody)
			if err != nil {
				status = "unreachable"
				details = check.suggest
				allHealthy = false
			} else {
				resp, reqErr := httpClient.Do(req)
				if reqErr != nil {
					status = "unreachable"
					details = check.suggest
					allHealthy = false
				} else {
					_ = resp.Body.Close() // response body not read; close error is inconsequential
					if resp.StatusCode != http.StatusOK {
						status = fmt.Sprintf("unhealthy (%d)", resp.StatusCode)
						details = check.suggest
						allHealthy = false
					}
				}
			}

			items = append(items, StatusItem{
				Name:    check.name,
				Status:  status,
				Details: details,
			})
		}

		if output == "json" {
			return printJSON(items)
		}

		printStatus(items)

		if allHealthy {
			fmt.Fprintf(cmd.OutOrStdout(), "\n%sAll components healthy%s\n", colorGreen, colorReset)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "\n%sSome components unhealthy%s — see suggestions above\n", colorRed, colorReset)
		}

		return nil
	},
}
