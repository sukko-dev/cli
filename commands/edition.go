package commands

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/sukko-dev/cli/client"
)

func init() {
	editionCmd.AddCommand(editionCompareCmd)
	rootCmd.AddCommand(editionCmd)
}

var editionCmd = &cobra.Command{
	Use:   "edition",
	Short: "Show current edition, limits, and usage",
	Long: `Show the current Sukko edition (Community/Pro/Enterprise), license status,
hard limits, and live resource usage.

Fetches data from provisioning's /edition endpoint (tenant usage) and
gateway's /edition endpoint (connection/shard usage), merging both.
Falls back to locally stored license key if the platform is unreachable.`,
	RunE: runEdition,
}

// resolveProvisioningURL returns the provisioning URL without requiring a valid
// token. Unlike resolveClientConfig(), this never fails on corrupted tokens —
// the /edition endpoint requires no authentication.
func resolveProvisioningURL() string {
	if apiURL != "" {
		return apiURL
	}
	if resolvedCtx != nil && resolvedCtx.ProvisioningURL != "" {
		return resolvedCtx.ProvisioningURL
	}
	return defaultAPIURL
}

// resolveGatewayHTTPURL returns the gateway HTTP URL without requiring a valid token.
func resolveGatewayHTTPURL() string {
	if resolvedCtx != nil && resolvedCtx.GatewayURL != "" {
		return wsToHTTP(resolvedCtx.GatewayURL)
	}
	return defaultGatewayHTTP
}

// mergeGatewayUsage fetches edition data from the gateway and fills in usage
// fields that provisioning doesn't have (connections, shards). Best-effort —
// if the gateway is unreachable, provisioning data is shown as-is.
func mergeGatewayUsage(cmd *cobra.Command, resp *client.EditionResponse) {
	gwClient, err := client.New(client.Config{
		BaseURL: resolveGatewayHTTPURL(),
		Timeout: 2 * time.Second,
	})
	if err != nil {
		return // best-effort: gateway client creation failed
	}

	gwResp, err := gwClient.GetEdition(cmd.Context())
	if err != nil {
		return // best-effort: gateway unreachable, provisioning data still shown
	}

	mergeEditionUsage(&resp.Usage, &gwResp.Usage)
}

// mergeEditionUsage fills nil fields in dst from src. Does not overwrite
// non-nil fields — provisioning data takes precedence.
func mergeEditionUsage(dst, src *client.EditionUsage) {
	if dst.Connections == nil && src.Connections != nil {
		dst.Connections = src.Connections
	}
	if dst.Shards == nil && src.Shards != nil {
		dst.Shards = src.Shards
	}
}

func runEdition(cmd *cobra.Command, _ []string) error {
	// Try provisioning API (no auth required) — has tenant/rule usage
	provClient, err := client.New(client.Config{
		BaseURL: resolveProvisioningURL(),
		Timeout: client.DefaultClientTimeout,
	})
	if err != nil {
		return fmt.Errorf("create edition client: %w", err)
	}

	resp, apiErr := provClient.GetEdition(cmd.Context())
	if apiErr == nil {
		// Merge gateway edition data for connection/shard usage
		mergeGatewayUsage(cmd, resp)

		if output == "json" {
			return printJSON(resp)
		}
		printEditionStatus(cmd, resp)
		return nil
	}

	// API unreachable — try local license key from context
	if resolvedCtx != nil && resolvedStore != nil && resolvedCtx.LicenseKeyEnc != "" {
		lk, decErr := resolvedCtx.LicenseKey(resolvedStore.Key())
		if decErr == nil && lk != "" {
			claims, claimErr := decodeLicenseClaims(lk)
			if claimErr == nil {
				if output == "json" {
					return printJSON(map[string]any{
						"edition":    claims.Edition,
						"org":        claims.Org,
						"expires_at": claims.Exp,
						"source":     "local_license",
					})
				}
				out := cmd.OutOrStdout()
				fmt.Fprintf(out, "Edition:     %s\n", capitalizeEdition(claims.Edition))
				if claims.Org != "" {
					fmt.Fprintf(out, "Org:         %s\n", claims.Org)
				}
				if claims.Exp > 0 {
					fmt.Fprintf(out, "Expires:     %s\n", formatExpiry(claims.Exp))
				}
				fmt.Fprintln(out, "\n(Platform not running — usage data unavailable)")
				return nil
			}
		}
		// License key exists but could not be used (decrypt or claims decode failed)
		return errors.New("no edition info available — platform unreachable and stored license key could not be decoded. Run 'sukko license set' to update or 'sukko up' to start the platform")
	}

	// Contextual error messages
	if resolvedCtx == nil {
		return errors.New("no edition info available — run 'sukko init' to set up your local context")
	}
	return errors.New("no edition info available — run 'sukko up' to start the platform or 'sukko license set' to store a license key")
}

func printEditionStatus(cmd *cobra.Command, resp *client.EditionResponse) {
	out := cmd.OutOrStdout()

	if resp.Expired {
		printExpiredEdition(out, resp)
		return
	}

	if resp.Org == "" && strings.EqualFold(resp.Edition, "community") {
		printCommunityEdition(out, resp)
		return
	}

	printActiveEdition(out, resp)
}

func printActiveEdition(out io.Writer, resp *client.EditionResponse) {
	fmt.Fprintf(out, "Edition:     %s%s%s\n", colorBold, capitalizeEdition(resp.Edition), colorReset)
	if resp.Org != "" {
		fmt.Fprintf(out, "Org:         %s\n", resp.Org)
	}
	if resp.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, resp.ExpiresAt); err == nil {
			days := int(time.Until(t).Hours() / 24)
			fmt.Fprintf(out, "Expires:     %s (%s%d days remaining%s)\n",
				t.Format("2006-01-02"), colorGreen, days, colorReset)
		}
	}

	fmt.Fprintln(out, "\nResource Usage:")
	printUsageTable(out, &resp.Limits, &resp.Usage)
}

func printExpiredEdition(out io.Writer, resp *client.EditionResponse) {
	fmt.Fprintf(out, "Edition:     %sCommunity%s (%sEXPIRED%s — was %s",
		colorRed, colorReset, colorYellow, colorReset, capitalizeEdition(resp.Edition))
	if resp.Org != "" {
		fmt.Fprintf(out, ", org: %s", resp.Org)
	}
	fmt.Fprintln(out, ")")

	if resp.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, resp.ExpiresAt); err == nil {
			days := int(-time.Until(t).Hours() / 24)
			fmt.Fprintf(out, "Expires:     %s (%sexpired %d days ago%s)\n",
				t.Format("2006-01-02"), colorRed, days, colorReset)
		}
	}

	fmt.Fprintln(out, "\nResource Limits:")
	printLimitsOnly(out, &resp.Limits)
}

func printCommunityEdition(out io.Writer, resp *client.EditionResponse) {
	fmt.Fprintln(out, "Edition:     Community (free)")
	fmt.Fprintln(out, "\nResource Limits:")
	printLimitsOnly(out, &resp.Limits)
}

func printLimitsOnly(out io.Writer, limits *client.EditionLimits) {
	printLimitRow(out, "Tenants", limits.MaxTenants)
	printLimitRow(out, "Connections", limits.MaxTotalConnections)
	printLimitRow(out, "Shards", limits.MaxShards)
}

func printLimitRow(out io.Writer, name string, limit int) {
	if limit == 0 {
		fmt.Fprintf(out, "  %-16s Unlimited\n", name+":")
		return
	}
	fmt.Fprintf(out, "  %-16s %d\n", name+":", limit)
}

func printUsageTable(out io.Writer, limits *client.EditionLimits, usage *client.EditionUsage) {
	type row struct {
		name    string
		current *int
		max     int
	}

	rows := []row{
		{"Tenants", usage.Tenants, limits.MaxTenants},
		{"Connections", usage.Connections, limits.MaxTotalConnections},
		{"Shards", usage.Shards, limits.MaxShards},
	}

	for _, r := range rows {
		maxStr := strconv.Itoa(r.max)
		if r.max == 0 {
			maxStr = "Unlimited"
		}

		if r.current == nil {
			fmt.Fprintf(out, "  %-16s \u2014 / %s\n", r.name+":", maxStr) // em dash for unavailable
			continue
		}

		if r.max == 0 {
			fmt.Fprintf(out, "  %-16s %d / %s\n", r.name+":", *r.current, maxStr)
			continue
		}

		pct := *r.current * 100 / r.max
		color := colorGreen
		if pct > 90 {
			color = colorRed
		} else if pct > 75 {
			color = colorYellow
		}
		fmt.Fprintf(out, "  %-16s %s%d%s / %s  (%s%d%%%s)\n",
			r.name+":", color, *r.current, colorReset, maxStr, color, pct, colorReset)
	}
}

func capitalizeEdition(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// --- sukko edition compare ---

var editionCompareCmd = &cobra.Command{
	Use:   "compare",
	Short: "Compare Community, Pro, and Enterprise editions",
	Long: `Display a comparison table of all three Sukko editions.

This command works offline — the comparison data is hardcoded from the
published edition matrix. No running services required.`,
	RunE: runEditionCompare,
}

// editionMatrix is the compiled-in edition comparison data.
// Keep in sync with license.DefaultLimits() and license.featureEditions in the monorepo
// (github.com/sukko-dev/sukko ws/internal/shared/license/). Edition values change ~1-2x/year.
var editionMatrix = []struct {
	dimension  string
	community  string
	pro        string
	enterprise string
}{
	{"Tenants", "3", "50", "Unlimited"},
	{"Total Connections", "500", "10,000", "Unlimited"},
	{"Shards", "1", "8", "Unlimited"},
	{"Topics/Tenant", "10", "50", "Unlimited"},
	{"Routing Rules/Tenant", "10", "100", "Unlimited"},
	{"", "", "", ""},
	// The full data path is Community (platform ADR-0009): kafka ingest, message
	// history, live gap recovery, REST publish and the channel-to-topic mapping
	// run on every edition — the capacity caps above are the tier wall. Routing
	// rules are the sole mapping (no convention fallback), so gating them would
	// leave the Community kafka backend unusable for publish (platform ADR-0014).
	{"Kafka Backend", "Yes", "Yes", "Yes"},
	{"Message History", "Yes", "Yes", "Yes"},
	{"Live Gap Recovery", "Yes", "Yes", "Yes"},
	{"REST Publish", "Yes", "Yes", "Yes"},
	{"Channel-Topic Routing", "Yes", "Yes", "Yes"},
	{"", "", "", ""},
	{"Tenant Limits & Quotas", "No", "Yes", "Yes"},
	{"Alerting", "No", "Yes", "Yes"},
	{"SSE Transport", "No", "Yes", "Yes"},
	{"Webhooks", "No", "Yes", "Yes"},
	{"Admin UI", "No", "Yes", "Yes"},
	{"Web Push", "No", "Yes", "Yes"},
	{"Push Analytics", "No", "Yes", "Yes"},
	{"", "", "", ""},
	{"Mobile Push (FCM/APNs)", "No", "No", "Yes"},
	{"Audit Logging", "No", "No", "Yes"},
}

func runEditionCompare(cmd *cobra.Command, _ []string) error {
	if output == "json" {
		return printJSON(comparisonData())
	}

	out := cmd.OutOrStdout()

	// Best-effort: detect current edition for highlighting (short timeout — UI decoration only)
	currentEdition := ""
	c, err := client.New(client.Config{
		BaseURL: resolveProvisioningURL(),
		Timeout: 2 * time.Second,
	})
	if err == nil {
		if resp, apiErr := c.GetEdition(cmd.Context()); apiErr == nil {
			currentEdition = strings.ToLower(resp.Edition)
		}
	}

	header := fmt.Sprintf("%s%-24s %-14s %-14s %-14s%s",
		colorBold, "Dimension", "Community", "Pro", "Enterprise", colorReset)
	fmt.Fprintln(out, header)
	fmt.Fprintln(out, strings.Repeat("\u2500", 66)) // ─

	for _, row := range editionMatrix {
		if row.dimension == "" {
			fmt.Fprintln(out)
			continue
		}
		fmt.Fprintf(out, "%-24s %-14s %-14s %-14s\n",
			row.dimension, row.community, row.pro, row.enterprise)
	}

	fmt.Fprintln(out)
	if currentEdition != "" {
		fmt.Fprintf(out, "Current: %s%s%s \u25C0\n", colorBold, capitalizeEdition(currentEdition), colorReset) // ◀
	}
	fmt.Fprintf(out, "Upgrade: %shttps://docs.sukko.dev/editions/upgrade%s\n", colorCyan, colorReset)

	return nil
}

func comparisonData() map[string]any {
	return map[string]any{
		"editions": []map[string]any{
			{
				"name": "community",
				"limits": map[string]any{
					"tenants": 3, "total_connections": 500, "shards": 1,
					"topics_per_tenant": 10, "routing_rules_per_tenant": 10,
				},
				"features": map[string]any{
					"kafka_backend": true, "message_history": true,
					"live_gap_recovery": true, "rest_publish": true,
					"channel_topic_routing": true, "tenant_limits_quotas": false,
					"alerting": false, "sse_transport": false,
					"webhooks": false, "admin_ui": false,
					"web_push": false, "push_analytics": false,
					"mobile_push": false, "audit_logging": false,
				},
			},
			{
				"name": "pro",
				"limits": map[string]any{
					"tenants": 50, "total_connections": 10000, "shards": 8,
					"topics_per_tenant": 50, "routing_rules_per_tenant": 100,
				},
				"features": map[string]any{
					"kafka_backend": true, "message_history": true,
					"live_gap_recovery": true, "rest_publish": true,
					"channel_topic_routing": true, "tenant_limits_quotas": true,
					"alerting": true, "sse_transport": true,
					"webhooks": true, "admin_ui": true,
					"web_push": true, "push_analytics": true,
					"mobile_push": false, "audit_logging": false,
				},
			},
			{
				"name": "enterprise",
				"limits": map[string]any{
					"tenants": "unlimited", "total_connections": "unlimited", "shards": "unlimited",
					"topics_per_tenant": "unlimited", "routing_rules_per_tenant": "unlimited",
				},
				"features": map[string]any{
					"kafka_backend": true, "message_history": true,
					"live_gap_recovery": true, "rest_publish": true,
					"channel_topic_routing": true, "tenant_limits_quotas": true,
					"alerting": true, "sse_transport": true,
					"webhooks": true, "admin_ui": true,
					"web_push": true, "push_analytics": true,
					"mobile_push": true, "audit_logging": true,
				},
			},
		},
		"upgrade_url": "https://docs.sukko.dev/editions/upgrade",
	}
}
