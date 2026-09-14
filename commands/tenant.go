package commands

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(tenantCmd)
	tenantCmd.AddCommand(tenantCreateCmd, tenantGetCmd, tenantListCmd, tenantUpdateCmd,
		tenantSuspendCmd, tenantReactivateCmd, tenantDeprovisionCmd, tenantDeleteCmd)

	// create flags — "slug" matches the provisioning API's CreateTenantRequest
	// field (#53: the old --id flag serialized to an "id" key the API never
	// read, so every create was rejected for an empty slug; --category mapped
	// to a field the API does not have — topic routing is configured via
	// routing rules, not tenant creation).
	tenantCreateCmd.Flags().String("slug", "", "Tenant slug — Kafka namespace and URL identifier, e.g. \"acme\" (required)")
	tenantCreateCmd.Flags().String("name", "", "Display name")
	tenantCreateCmd.Flags().String("consumer-type", "shared", "Consumer type (shared|dedicated)")
	_ = tenantCreateCmd.MarkFlagRequired("slug")

	// list flags
	tenantListCmd.Flags().Int("limit", 50, "Maximum results")
	tenantListCmd.Flags().Int("offset", 0, "Results offset")
	tenantListCmd.Flags().String("status", "", "Filter by status")

	// update flags
	tenantUpdateCmd.Flags().String("name", "", "New display name")
}

var tenantCmd = &cobra.Command{
	Use:   "tenant",
	Short: "Manage tenants",
}

var tenantCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new tenant",
	RunE: func(cmd *cobra.Command, _ []string) error {
		slug, _ := cmd.Flags().GetString("slug")
		name, _ := cmd.Flags().GetString("name")
		consumerType, _ := cmd.Flags().GetString("consumer-type")

		if consumerType != "shared" && consumerType != "dedicated" {
			return fmt.Errorf("invalid consumer-type %q: must be shared or dedicated", consumerType)
		}

		if name == "" {
			name = slug
		}

		c, err := newClient()
		if err != nil {
			return err
		}
		result, err := c.CreateTenant(cmd.Context(), buildTenantCreateRequest(slug, name, consumerType))
		if err != nil {
			return fmt.Errorf("create tenant: %w", err)
		}
		return printOutput(result, output)
	},
}

// buildTenantCreateRequest builds the provisioning CreateTenantRequest body.
// Field names MUST match the API schema (slug/name/consumer_type) — shared by
// `sukko tenant create` and `sukko up`'s demo-tenant provisioning so the two
// cannot drift (#53: a hand-rolled map here once sent "id", which the API
// silently ignored, rejecting every create for an empty slug).
func buildTenantCreateRequest(slug, name, consumerType string) map[string]any {
	return map[string]any{
		"slug":          slug,
		"name":          name,
		"consumer_type": consumerType,
	}
}

var tenantGetCmd = &cobra.Command{
	Use:   "get [tenant-id]",
	Short: "Get tenant details",
	Long:  "Get tenant details. If no tenant-id is provided, uses the active tenant from context.",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		tenantID := ""
		if len(args) > 0 {
			tenantID = args[0]
		}
		tenantID = resolveTenant(tenantID)
		if tenantID == "" {
			return errors.New("tenant ID required (provide as argument or set active tenant in context)")
		}

		c, err := newClient()
		if err != nil {
			return err
		}
		result, err := c.GetTenant(cmd.Context(), tenantID)
		if err != nil {
			return fmt.Errorf("get tenant: %w", err)
		}
		return printOutput(result, output)
	},
}

var tenantListCmd = &cobra.Command{
	Use:   "list",
	Short: "List tenants",
	RunE: func(cmd *cobra.Command, _ []string) error {
		limit, _ := cmd.Flags().GetInt("limit")
		offset, _ := cmd.Flags().GetInt("offset")
		status, _ := cmd.Flags().GetString("status")

		params := map[string]string{
			"limit":  strconv.Itoa(limit),
			"offset": strconv.Itoa(offset),
		}
		if status != "" {
			params["status"] = status
		}

		c, err := newClient()
		if err != nil {
			return err
		}
		result, err := c.ListTenants(cmd.Context(), params)
		if err != nil {
			return fmt.Errorf("list tenants: %w", err)
		}
		return printOutput(result, output)
	},
}

var tenantUpdateCmd = &cobra.Command{
	Use:   "update [tenant-id]",
	Short: "Update a tenant",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		req := map[string]any{}
		if name, _ := cmd.Flags().GetString("name"); name != "" {
			req["name"] = name
		}

		c, err := newClient()
		if err != nil {
			return err
		}
		result, err := c.UpdateTenant(cmd.Context(), args[0], req)
		if err != nil {
			return fmt.Errorf("update tenant: %w", err)
		}
		return printOutput(result, output)
	},
}

var tenantSuspendCmd = &cobra.Command{
	Use:   "suspend [tenant-id]",
	Short: "Suspend a tenant",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		result, err := c.SuspendTenant(cmd.Context(), args[0])
		if err != nil {
			return fmt.Errorf("suspend tenant: %w", err)
		}
		return printOutput(result, output)
	},
}

var tenantReactivateCmd = &cobra.Command{
	Use:   "reactivate [tenant-id]",
	Short: "Reactivate a suspended tenant",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		result, err := c.ReactivateTenant(cmd.Context(), args[0])
		if err != nil {
			return fmt.Errorf("reactivate tenant: %w", err)
		}
		return printOutput(result, output)
	},
}

var tenantDeprovisionCmd = &cobra.Command{
	Use:   "deprovision [tenant-id]",
	Short: "Initiate tenant deletion (grace period)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		result, err := c.DeprovisionTenant(cmd.Context(), args[0])
		if err != nil {
			return fmt.Errorf("deprovision tenant: %w", err)
		}
		return printOutput(result, output)
	},
}

// delete is an alias for deprovision
var tenantDeleteCmd = &cobra.Command{
	Use:   "delete [tenant-id]",
	Short: "Delete a tenant (alias for deprovision)",
	Args:  cobra.ExactArgs(1),
	RunE:  tenantDeprovisionCmd.RunE,
}
