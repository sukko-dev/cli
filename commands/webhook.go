package commands

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/sukko-dev/cli/client"
)

func init() {
	rootCmd.AddCommand(webhookCmd)
	webhookCmd.AddCommand(webhookCreateCmd, webhookListCmd, webhookGetCmd, webhookUpdateCmd, webhookDeleteCmd, webhookTestCmd)

	// Every webhook subcommand is tenant-scoped and operator-authenticated.
	for _, c := range []*cobra.Command{webhookCreateCmd, webhookListCmd, webhookGetCmd, webhookUpdateCmd, webhookDeleteCmd, webhookTestCmd} {
		c.Flags().String("tenant", "", "Tenant ID (uses active tenant from context if not set)")
	}

	webhookCreateCmd.Flags().String("url", "", "Destination URL (https required; http allowed only in local/dev)")
	webhookCreateCmd.Flags().String("channel-pattern", "", "Channel pattern the webhook fires on (e.g. orders.*)")
	webhookCreateCmd.Flags().String("secret", "", "HMAC signing secret shared with the destination")
	webhookCreateCmd.Flags().Int("max-retries", 0, "Max delivery retries (0 = provisioning default)")
	_ = webhookCreateCmd.MarkFlagRequired("url")
	_ = webhookCreateCmd.MarkFlagRequired("channel-pattern")
	_ = webhookCreateCmd.MarkFlagRequired("secret")

	for _, c := range []*cobra.Command{webhookGetCmd, webhookUpdateCmd, webhookDeleteCmd, webhookTestCmd} {
		c.Flags().String("id", "", "Webhook ID (required)")
		_ = c.MarkFlagRequired("id")
	}

	// update is a partial PATCH — only the flags the operator sets are sent.
	webhookUpdateCmd.Flags().String("url", "", "New destination URL")
	webhookUpdateCmd.Flags().String("channel-pattern", "", "New channel pattern")
	webhookUpdateCmd.Flags().String("secret", "", "New HMAC signing secret")
	webhookUpdateCmd.Flags().Int("max-retries", 0, "New max delivery retries")
	webhookUpdateCmd.Flags().String("status", "", "New status (enabled|degraded|suspended)")
}

var webhookCmd = &cobra.Command{
	Use:   "webhook",
	Short: "Manage webhook delivery registrations (operator, Pro+)",
	Long: `Manage a tenant's webhook registrations on its behalf.

Webhook management is operator-only: these commands authenticate with your admin
keypair and require a Pro or Enterprise license. Webhooks deliver channel events
to an external HTTPS endpoint, HMAC-signed with the shared secret.`,
}

var webhookCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Register a webhook for a tenant",
	RunE: func(cmd *cobra.Command, _ []string) error {
		tenantID := resolveTenantFromCmd(cmd)
		if tenantID == "" {
			return errors.New("tenant ID required (use --tenant or set active tenant in context)")
		}
		url, _ := cmd.Flags().GetString("url")
		pattern, _ := cmd.Flags().GetString("channel-pattern")
		secret, _ := cmd.Flags().GetString("secret")
		maxRetries, _ := cmd.Flags().GetInt("max-retries")

		c, err := newClient()
		if err != nil {
			return err
		}
		result, err := c.CreateWebhook(cmd.Context(), tenantID, client.CreateWebhookRequest{
			URL:            url,
			ChannelPattern: pattern,
			Secret:         secret,
			MaxRetries:     maxRetries,
		})
		if err != nil {
			return fmt.Errorf("create webhook: %w", err)
		}
		return printOutput(result, output)
	},
}

var webhookListCmd = &cobra.Command{
	Use:   "list",
	Short: "List a tenant's webhooks",
	RunE: func(cmd *cobra.Command, _ []string) error {
		tenantID := resolveTenantFromCmd(cmd)
		if tenantID == "" {
			return errors.New("tenant ID required (use --tenant or set active tenant in context)")
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		result, err := c.ListWebhooks(cmd.Context(), tenantID)
		if err != nil {
			return fmt.Errorf("list webhooks: %w", err)
		}
		return printOutput(result, output)
	},
}

var webhookGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Get a webhook by ID",
	RunE: func(cmd *cobra.Command, _ []string) error {
		tenantID := resolveTenantFromCmd(cmd)
		if tenantID == "" {
			return errors.New("tenant ID required (use --tenant or set active tenant in context)")
		}
		id, _ := cmd.Flags().GetString("id")
		c, err := newClient()
		if err != nil {
			return err
		}
		result, err := c.GetWebhook(cmd.Context(), tenantID, id)
		if err != nil {
			return fmt.Errorf("get webhook: %w", err)
		}
		return printOutput(result, output)
	},
}

var webhookUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update a webhook (only the flags you set are changed)",
	RunE: func(cmd *cobra.Command, _ []string) error {
		tenantID := resolveTenantFromCmd(cmd)
		if tenantID == "" {
			return errors.New("tenant ID required (use --tenant or set active tenant in context)")
		}
		id, _ := cmd.Flags().GetString("id")

		// Partial update: send only the fields the operator actually set.
		body := webhookUpdateBody(cmd)
		if len(body) == 0 {
			return errors.New("nothing to update: set at least one of --url, --channel-pattern, --secret, --max-retries, --status")
		}

		c, err := newClient()
		if err != nil {
			return err
		}
		result, err := c.UpdateWebhook(cmd.Context(), tenantID, id, body)
		if err != nil {
			return fmt.Errorf("update webhook: %w", err)
		}
		return printOutput(result, output)
	},
}

var webhookDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete a webhook by ID",
	RunE: func(cmd *cobra.Command, _ []string) error {
		tenantID := resolveTenantFromCmd(cmd)
		if tenantID == "" {
			return errors.New("tenant ID required (use --tenant or set active tenant in context)")
		}
		id, _ := cmd.Flags().GetString("id")
		c, err := newClient()
		if err != nil {
			return err
		}
		result, err := c.DeleteWebhook(cmd.Context(), tenantID, id)
		if err != nil {
			return fmt.Errorf("delete webhook: %w", err)
		}
		return printOutput(result, output)
	},
}

var webhookTestCmd = &cobra.Command{
	Use:   "test",
	Short: "Send a single synchronous test delivery to a webhook",
	RunE: func(cmd *cobra.Command, _ []string) error {
		tenantID := resolveTenantFromCmd(cmd)
		if tenantID == "" {
			return errors.New("tenant ID required (use --tenant or set active tenant in context)")
		}
		id, _ := cmd.Flags().GetString("id")
		c, err := newClient()
		if err != nil {
			return err
		}
		result, err := c.TestWebhook(cmd.Context(), tenantID, id)
		if err != nil {
			return fmt.Errorf("test webhook: %w", err)
		}
		return printOutput(result, output)
	},
}

// webhookUpdateBody builds the PATCH body for `webhook update` from only the flags the operator
// changed, mapping CLI flag names to the API's JSON field names. Extracted for unit testing the
// partial-update semantics.
func webhookUpdateBody(cmd *cobra.Command) map[string]any {
	body := map[string]any{}
	if cmd.Flags().Changed("url") {
		v, _ := cmd.Flags().GetString("url")
		body["url"] = v
	}
	if cmd.Flags().Changed("channel-pattern") {
		v, _ := cmd.Flags().GetString("channel-pattern")
		body["channel_pattern"] = v
	}
	if cmd.Flags().Changed("secret") {
		v, _ := cmd.Flags().GetString("secret")
		body["secret"] = v
	}
	if cmd.Flags().Changed("max-retries") {
		v, _ := cmd.Flags().GetInt("max-retries")
		body["max_retries"] = v
	}
	if cmd.Flags().Changed("status") {
		v, _ := cmd.Flags().GetString("status")
		body["status"] = v
	}
	return body
}
