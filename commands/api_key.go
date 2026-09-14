package commands

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(apiKeyCmd)
	apiKeyCmd.AddCommand(apiKeyCreateCmd, apiKeyListCmd, apiKeyRevokeCmd)

	// create flags
	apiKeyCreateCmd.Flags().String("tenant", "", "Tenant ID (uses active tenant from context if not set)")
	apiKeyCreateCmd.Flags().String("name", "", "API key name (optional)")

	// list flags
	apiKeyListCmd.Flags().String("tenant", "", "Tenant ID (uses active tenant from context if not set)")

	// revoke flags
	apiKeyRevokeCmd.Flags().String("tenant", "", "Tenant ID (uses active tenant from context if not set)")
	apiKeyRevokeCmd.Flags().String("key-id", "", "API key ID (required)")
	_ = apiKeyRevokeCmd.MarkFlagRequired("key-id")
}

var apiKeyCmd = &cobra.Command{
	Use:   "api-keys",
	Short: "Manage API keys for tenant authentication",
}

var apiKeyCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new API key",
	RunE: func(cmd *cobra.Command, _ []string) error {
		tenantFlag, _ := cmd.Flags().GetString("tenant")
		tenantID := resolveTenant(tenantFlag)
		if tenantID == "" {
			return errors.New("tenant ID required (use --tenant or set active tenant in context)")
		}

		name, _ := cmd.Flags().GetString("name")

		req := map[string]any{}
		if name != "" {
			req["name"] = name
		}

		c, err := newClient()
		if err != nil {
			return err
		}
		result, err := c.CreateAPIKey(cmd.Context(), tenantID, req)
		if err != nil {
			return fmt.Errorf("create api key: %w", err)
		}
		return printOutput(result, output)
	},
}

var apiKeyListCmd = &cobra.Command{
	Use:   "list",
	Short: "List API keys for a tenant",
	RunE: func(cmd *cobra.Command, _ []string) error {
		tenantFlag, _ := cmd.Flags().GetString("tenant")
		tenantID := resolveTenant(tenantFlag)
		if tenantID == "" {
			return errors.New("tenant ID required (use --tenant or set active tenant in context)")
		}

		c, err := newClient()
		if err != nil {
			return err
		}
		result, err := c.ListAPIKeys(cmd.Context(), tenantID)
		if err != nil {
			return fmt.Errorf("list api keys: %w", err)
		}
		return printOutput(result, output)
	},
}

var apiKeyRevokeCmd = &cobra.Command{
	Use:   "revoke",
	Short: "Revoke an API key",
	RunE: func(cmd *cobra.Command, _ []string) error {
		tenantFlag, _ := cmd.Flags().GetString("tenant")
		tenantID := resolveTenant(tenantFlag)
		if tenantID == "" {
			return errors.New("tenant ID required (use --tenant or set active tenant in context)")
		}
		keyID, _ := cmd.Flags().GetString("key-id")

		c, err := newClient()
		if err != nil {
			return err
		}
		result, err := c.RevokeAPIKey(cmd.Context(), tenantID, keyID)
		if err != nil {
			return fmt.Errorf("revoke api key: %w", err)
		}
		return printOutput(result, output)
	},
}
