package commands

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sukko-dev/cli/client"
)

func init() {
	rootCmd.AddCommand(topicsCmd)
	topicsCmd.AddCommand(topicsListCmd, topicsCreateCmd, topicsDeleteCmd)

	topicsListCmd.Flags().String("tenant", "", "Tenant ID (uses active tenant from context if not set)")

	topicsCreateCmd.Flags().String("tenant", "", "Tenant ID (uses active tenant from context if not set)")
	topicsCreateCmd.Flags().String("suffix", "", "Topic suffix to provision (e.g. analytics); lowercase alphanumeric and hyphens")
	_ = topicsCreateCmd.MarkFlagRequired("suffix")

	topicsDeleteCmd.Flags().String("tenant", "", "Tenant ID (uses active tenant from context if not set)")
	topicsDeleteCmd.Flags().String("suffix", "", "Topic suffix to delete")
	_ = topicsDeleteCmd.MarkFlagRequired("suffix")
}

var topicsCmd = &cobra.Command{
	Use:   "topics",
	Short: "Manage provisioned topics",
}

var topicsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List provisioned topics for a tenant",
	RunE: func(cmd *cobra.Command, _ []string) error {
		tenantID := resolveTenantFromCmd(cmd)
		if tenantID == "" {
			return errors.New("tenant ID required (use --tenant or set active tenant in context)")
		}

		c, err := newClient()
		if err != nil {
			return err
		}
		result, err := c.ListTopics(cmd.Context(), tenantID)
		if err != nil {
			return fmt.Errorf("list topics: %w", err)
		}
		return printOutput(result, output)
	},
}

var topicsCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a provisioned topic for a tenant",
	RunE: func(cmd *cobra.Command, _ []string) error {
		tenantID := resolveTenantFromCmd(cmd)
		if tenantID == "" {
			return errors.New("tenant ID required (use --tenant or set active tenant in context)")
		}
		suffix, _ := cmd.Flags().GetString("suffix")

		c, err := newClient()
		if err != nil {
			return err
		}
		result, err := c.CreateTopic(cmd.Context(), tenantID, suffix)
		if err != nil {
			if errors.Is(err, client.ErrTopicAlreadyExists) {
				return fmt.Errorf("a topic with suffix %q already exists", suffix)
			}
			return fmt.Errorf("create topic: %w", err)
		}
		return printOutput(result, output)
	},
}

var topicsDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete a provisioned topic for a tenant",
	RunE: func(cmd *cobra.Command, _ []string) error {
		tenantID := resolveTenantFromCmd(cmd)
		if tenantID == "" {
			return errors.New("tenant ID required (use --tenant or set active tenant in context)")
		}
		suffix, _ := cmd.Flags().GetString("suffix")

		c, err := newClient()
		if err != nil {
			return err
		}
		result, err := c.DeleteTopic(cmd.Context(), tenantID, suffix)
		if err != nil {
			switch {
			case errors.Is(err, client.ErrTopicNotFound):
				return fmt.Errorf("topic %q not found", suffix)
			case errors.Is(err, client.ErrTopicReferencedByRule):
				return fmt.Errorf("topic %q is still referenced by a routing rule; remove the rule first", suffix)
			default:
				return fmt.Errorf("delete topic: %w", err)
			}
		}
		return printOutput(result, output)
	},
}
