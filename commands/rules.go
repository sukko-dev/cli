package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/sukko-dev/cli/client"
)

func init() {
	rootCmd.AddCommand(rulesCmd)
	rulesCmd.AddCommand(routingCmd, channelsCmd, rulesTestAccessCmd)

	// routing subcommands
	routingCmd.AddCommand(routingGetCmd, routingSetCmd, routingDeleteCmd, routingAddCmd)
	routingGetCmd.Flags().String("tenant", "", "Tenant ID (uses active tenant from context if not set)")
	routingSetCmd.Flags().String("tenant", "", "Tenant ID (uses active tenant from context if not set)")
	routingSetCmd.Flags().String("file", "", "Path to JSON routing rules file (required)")
	_ = routingSetCmd.MarkFlagRequired("file")
	routingDeleteCmd.Flags().String("tenant", "", "Tenant ID (uses active tenant from context if not set)")
	routingAddCmd.Flags().String("tenant", "", "Tenant ID (uses active tenant from context if not set)")
	routingAddCmd.Flags().String("pattern", "", "Channel pattern (e.g. trades.**, **)")
	routingAddCmd.Flags().String("topics", "", "Comma-separated topic suffixes (e.g. trades,audit-log)")
	routingAddCmd.Flags().Int("priority", 0, "Rule priority (lower = higher precedence; must be unique per tenant)")
	_ = routingAddCmd.MarkFlagRequired("pattern")
	_ = routingAddCmd.MarkFlagRequired("topics")
	_ = routingAddCmd.MarkFlagRequired("priority")

	// channels subcommands
	channelsCmd.AddCommand(channelsGetCmd, channelsSetCmd, channelsDeleteCmd)
	channelsGetCmd.Flags().String("tenant", "", "Tenant ID (uses active tenant from context if not set)")
	channelsSetCmd.Flags().String("tenant", "", "Tenant ID (uses active tenant from context if not set)")
	channelsSetCmd.Flags().StringSlice("public", nil, "Public channel patterns (repeatable)")
	channelsSetCmd.Flags().String("file", "", "Path to JSON channel rules file (alternative to --public)")
	channelsDeleteCmd.Flags().String("tenant", "", "Tenant ID (uses active tenant from context if not set)")

	// test-access
	rulesTestAccessCmd.Flags().String("tenant", "", "Tenant ID (uses active tenant from context if not set)")
	rulesTestAccessCmd.Flags().String("sub", "", "Subject (user ID) to test access for")
	rulesTestAccessCmd.Flags().String("channel", "", "Channel to test access for")
	rulesTestAccessCmd.Flags().StringSlice("group", nil, "Groups to test access for")
}

var rulesCmd = &cobra.Command{
	Use:   "rules",
	Short: "Manage routing and channel rules",
}

// ─── Routing Rules ─────────────────────────────────────────────

var routingCmd = &cobra.Command{
	Use:   "routing",
	Short: "Manage topic routing rules",
}

var routingGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Get routing rules for a tenant",
	RunE: func(cmd *cobra.Command, _ []string) error {
		tenantID := resolveTenantFromCmd(cmd)
		if tenantID == "" {
			return errors.New("tenant ID required (use --tenant or set active tenant in context)")
		}

		c, err := newClient()
		if err != nil {
			return err
		}
		result, err := c.GetRoutingRules(cmd.Context(), tenantID)
		if err != nil {
			return fmt.Errorf("get routing rules: %w", err)
		}
		return printOutput(result, output)
	},
}

var routingSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Set routing rules for a tenant from a JSON file",
	RunE: func(cmd *cobra.Command, _ []string) error {
		tenantID := resolveTenantFromCmd(cmd)
		if tenantID == "" {
			return errors.New("tenant ID required (use --tenant or set active tenant in context)")
		}

		rulesFile, _ := cmd.Flags().GetString("file")
		data, err := os.ReadFile(rulesFile) //nolint:gosec // G304: CLI reads user-specified file path from --file flag
		if err != nil {
			return fmt.Errorf("read rules file: %w", err)
		}

		var req map[string]any
		if err := json.Unmarshal(data, &req); err != nil {
			return fmt.Errorf("parse rules file: %w", err)
		}

		c, err := newClient()
		if err != nil {
			return err
		}
		result, err := c.SetRoutingRules(cmd.Context(), tenantID, req)
		if err != nil {
			return fmt.Errorf("set routing rules: %w", err)
		}
		return printOutput(result, output)
	},
}

var routingDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete routing rules for a tenant",
	RunE: func(cmd *cobra.Command, _ []string) error {
		tenantID := resolveTenantFromCmd(cmd)
		if tenantID == "" {
			return errors.New("tenant ID required (use --tenant or set active tenant in context)")
		}

		c, err := newClient()
		if err != nil {
			return err
		}
		result, err := c.DeleteRoutingRules(cmd.Context(), tenantID)
		if err != nil {
			return fmt.Errorf("delete routing rules: %w", err)
		}
		return printOutput(result, output)
	},
}

var routingAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a single routing rule for a tenant",
	RunE: func(cmd *cobra.Command, _ []string) error {
		tenantID := resolveTenantFromCmd(cmd)
		if tenantID == "" {
			return errors.New("tenant ID required (use --tenant or set active tenant in context)")
		}

		pattern, _ := cmd.Flags().GetString("pattern")
		topicsRaw, _ := cmd.Flags().GetString("topics")
		priority, _ := cmd.Flags().GetInt("priority")

		topics, err := parseTopics(topicsRaw)
		if err != nil {
			return err
		}

		c, err := newClient()
		if err != nil {
			return err
		}

		result, err := c.AddRoutingRule(cmd.Context(), tenantID, client.RoutingRule{
			Pattern:  pattern,
			Topics:   topics,
			Priority: priority,
		})
		if err != nil {
			switch {
			case errors.Is(err, client.ErrDuplicateRoutingPattern):
				return fmt.Errorf("a routing rule with pattern %q already exists", pattern)
			case errors.Is(err, client.ErrDuplicateRoutingPriority):
				return fmt.Errorf("a routing rule with priority %d already exists", priority)
			default:
				return fmt.Errorf("add routing rule: %w", err)
			}
		}
		return printOutput(result, output)
	},
}

// parseTopics splits a comma-separated topics string, trims whitespace,
// and rejects empty entries (e.g. trailing comma or double comma).
func parseTopics(raw string) ([]string, error) {
	parts := strings.Split(raw, ",")
	topics := make([]string, 0, len(parts))
	for _, p := range parts {
		t := strings.TrimSpace(p)
		if t == "" {
			return nil, errors.New("topics must not contain empty entries (check for trailing commas)")
		}
		topics = append(topics, t)
	}
	if len(topics) == 0 {
		return nil, errors.New("at least one topic is required")
	}
	return topics, nil
}

// ─── Channel Rules ─────────────────────────────────────────────

var channelsCmd = &cobra.Command{
	Use:   "channels",
	Short: "Manage channel permission rules",
}

var channelsGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Get channel rules for a tenant",
	RunE: func(cmd *cobra.Command, _ []string) error {
		tenantID := resolveTenantFromCmd(cmd)
		if tenantID == "" {
			return errors.New("tenant ID required (use --tenant or set active tenant in context)")
		}

		c, err := newClient()
		if err != nil {
			return err
		}
		result, err := c.GetChannelRules(cmd.Context(), tenantID)
		if err != nil {
			return fmt.Errorf("get channel rules: %w", err)
		}
		return printOutput(result, output)
	},
}

var channelsSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Set channel rules for a tenant",
	RunE: func(cmd *cobra.Command, _ []string) error {
		tenantID := resolveTenantFromCmd(cmd)
		if tenantID == "" {
			return errors.New("tenant ID required (use --tenant or set active tenant in context)")
		}

		var req map[string]any
		rulesFile, _ := cmd.Flags().GetString("file")
		publicPatterns, _ := cmd.Flags().GetStringSlice("public")

		if rulesFile != "" && len(publicPatterns) > 0 {
			return errors.New("--file and --public are mutually exclusive")
		}

		if rulesFile != "" {
			data, err := os.ReadFile(rulesFile) //nolint:gosec // G304: CLI reads user-specified file path from --file flag
			if err != nil {
				return fmt.Errorf("read rules file: %w", err)
			}
			if err := json.Unmarshal(data, &req); err != nil {
				return fmt.Errorf("parse rules file: %w", err)
			}
		} else if len(publicPatterns) > 0 {
			// Schema field is "public" (SetChannelRulesRequest); the previous
			// "public_patterns" key was unknown to the API and silently saved
			// empty (deny-all) rules.
			req = channelRulesBodyFromPublic(publicPatterns)
		} else {
			return errors.New("either --file or --public is required")
		}

		c, err := newClient()
		if err != nil {
			return err
		}
		result, err := c.SetChannelRules(cmd.Context(), tenantID, req)
		if err != nil {
			return fmt.Errorf("set channel rules: %w", err)
		}
		return printOutput(result, output)
	},
}

var channelsDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete channel rules for a tenant",
	RunE: func(cmd *cobra.Command, _ []string) error {
		tenantID := resolveTenantFromCmd(cmd)
		if tenantID == "" {
			return errors.New("tenant ID required (use --tenant or set active tenant in context)")
		}

		c, err := newClient()
		if err != nil {
			return err
		}
		result, err := c.DeleteChannelRules(cmd.Context(), tenantID)
		if err != nil {
			return fmt.Errorf("delete channel rules: %w", err)
		}
		return printOutput(result, output)
	},
}

// ─── Test Access ───────────────────────────────────────────────

var rulesTestAccessCmd = &cobra.Command{
	Use:   "test-access",
	Short: "Test channel access for a subject",
	RunE: func(cmd *cobra.Command, _ []string) error {
		tenantID := resolveTenantFromCmd(cmd)
		if tenantID == "" {
			return errors.New("tenant ID required (use --tenant or set active tenant in context)")
		}

		sub, _ := cmd.Flags().GetString("sub")
		channel, _ := cmd.Flags().GetString("channel")
		groups, _ := cmd.Flags().GetStringSlice("group")

		req := map[string]any{}
		if sub != "" {
			req["sub"] = sub
		}
		if channel != "" {
			req["channel"] = channel
		}
		if len(groups) > 0 {
			req["groups"] = groups
		}

		c, err := newClient()
		if err != nil {
			return err
		}
		result, err := c.TestAccess(cmd.Context(), tenantID, req)
		if err != nil {
			return fmt.Errorf("test access: %w", err)
		}
		return printOutput(result, output)
	},
}

// resolveTenantFromCmd extracts tenant from --tenant flag, falling back to context.
func resolveTenantFromCmd(cmd *cobra.Command) string {
	tenantFlag, _ := cmd.Flags().GetString("tenant")
	return resolveTenant(tenantFlag)
}

// channelRulesBodyFromPublic builds the SetChannelRules request body for the
// --public flag path. Extracted for schema assertions in tests.
func channelRulesBodyFromPublic(patterns []string) map[string]any {
	return map[string]any{
		"public": patterns,
	}
}
