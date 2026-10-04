package commands

import (
	"errors"
	"fmt"
	"os"
	"strings"

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
	webhookCreateCmd.Flags().String("secret", "", "HMAC signing secret (or use --secret-file to avoid argv exposure)")
	webhookCreateCmd.Flags().String("secret-file", "", "Path to a file containing the HMAC signing secret")
	webhookCreateCmd.Flags().Int("max-retries", 0, "Max delivery retries (0 = provisioning default)")
	_ = webhookCreateCmd.MarkFlagRequired("url")
	_ = webhookCreateCmd.MarkFlagRequired("channel-pattern")

	for _, c := range []*cobra.Command{webhookGetCmd, webhookUpdateCmd, webhookDeleteCmd, webhookTestCmd} {
		c.Flags().String("webhook-id", "", "Webhook ID (required)")
		_ = c.MarkFlagRequired("webhook-id")
	}

	// update is a partial PATCH — only the flags the operator sets are sent. --secret (or
	// --secret-file) rotates the HMAC signing secret in place, keeping the webhook ID and history.
	webhookUpdateCmd.Flags().String("url", "", "New destination URL")
	webhookUpdateCmd.Flags().String("channel-pattern", "", "New channel pattern")
	webhookUpdateCmd.Flags().Int("max-retries", 0, "New max delivery retries (1–10)")
	webhookUpdateCmd.Flags().String("status", "", "New status (enabled|suspended)")
	webhookUpdateCmd.Flags().String("secret", "", "Rotate the HMAC signing secret (or use --secret-file)")
	webhookUpdateCmd.Flags().String("secret-file", "", "Rotate the HMAC signing secret, read from a file (keeps it out of argv)")
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
		secret, err := resolveWebhookSecret(cmd)
		if err != nil {
			return err
		}
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
		id, _ := cmd.Flags().GetString("webhook-id")
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
		id, _ := cmd.Flags().GetString("webhook-id")

		// Partial update: send only the fields the operator actually set.
		body := webhookUpdateBody(cmd)
		// A secret rotation is optional on update: include it only when provided.
		secret, provided, err := resolveWebhookSecretOptional(cmd)
		if err != nil {
			return err
		}
		if provided {
			body["secret"] = secret
		}
		if len(body) == 0 {
			return errors.New("nothing to update: set at least one of --url, --channel-pattern, --max-retries, --status, --secret")
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
		id, _ := cmd.Flags().GetString("webhook-id")
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
		id, _ := cmd.Flags().GetString("webhook-id")
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

// resolveWebhookSecret reads the HMAC signing secret from --secret or --secret-file (exactly one
// required). --secret-file keeps the secret out of argv/shell history; --secret is convenient for
// scripts that inject it via an environment variable.
func resolveWebhookSecret(cmd *cobra.Command) (string, error) {
	s, ok, err := resolveWebhookSecretOptional(cmd)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errors.New("a webhook secret is required (use --secret or --secret-file)")
	}
	return s, nil
}

// resolveWebhookSecretOptional resolves --secret/--secret-file when the operator provided one.
// It returns (secret, true, nil) when exactly one flag is set, ("", false, nil) when neither is
// set (the caller decides whether that is an error — create requires one, update treats it as
// "no rotation"), and an error when both are set or the file is unreadable/empty.
func resolveWebhookSecretOptional(cmd *cobra.Command) (secret string, provided bool, err error) {
	flagSecret, _ := cmd.Flags().GetString("secret")
	secretFile, _ := cmd.Flags().GetString("secret-file")
	switch {
	case flagSecret != "" && secretFile != "":
		return "", false, errors.New("--secret and --secret-file are mutually exclusive")
	case secretFile != "":
		data, readErr := os.ReadFile(secretFile) //nolint:gosec // G304: CLI reads user-specified file path from --secret-file
		if readErr != nil {
			return "", false, fmt.Errorf("read secret file: %w", readErr)
		}
		s := strings.TrimSpace(string(data))
		if s == "" {
			return "", false, fmt.Errorf("secret file %s is empty", secretFile)
		}
		return s, true, nil
	case flagSecret != "":
		return flagSecret, true, nil
	default:
		return "", false, nil
	}
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
