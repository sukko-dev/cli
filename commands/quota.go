package commands

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(quotaCmd)
	quotaCmd.AddCommand(quotaGetCmd, quotaUpdateCmd)

	quotaGetCmd.Flags().String("tenant", "", "Tenant ID (uses active context if not set)")

	quotaUpdateCmd.Flags().String("tenant", "", "Tenant ID (uses active context if not set)")
	quotaUpdateCmd.Flags().Int("max-topics", 0, "Maximum topics")
	quotaUpdateCmd.Flags().Int("max-partitions", 0, "Maximum partitions")
	quotaUpdateCmd.Flags().Int64("max-storage-bytes", 0, "Maximum storage in bytes")
	quotaUpdateCmd.Flags().Int64("producer-byte-rate", 0, "Producer throughput limit (bytes/sec)")
	quotaUpdateCmd.Flags().Int64("consumer-byte-rate", 0, "Consumer throughput limit (bytes/sec)")
	quotaUpdateCmd.Flags().Int("max-connections", 0, "Maximum WebSocket connections")
}

var quotaCmd = &cobra.Command{
	Use:   "quota",
	Short: "Manage tenant quotas",
}

var quotaGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Get quotas for a tenant",
	RunE: func(cmd *cobra.Command, _ []string) error {
		tenantID := resolveTenantFromCmd(cmd)
		if tenantID == "" {
			return errors.New("tenant ID required (use --tenant or set active tenant in context)")
		}

		c, err := newClient()
		if err != nil {
			return err
		}
		result, err := c.GetQuota(cmd.Context(), tenantID)
		if err != nil {
			return fmt.Errorf("get quota: %w", err)
		}
		return printOutput(result, output)
	},
}

var quotaUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update quotas for a tenant",
	RunE: func(cmd *cobra.Command, _ []string) error {
		tenantID := resolveTenantFromCmd(cmd)
		if tenantID == "" {
			return errors.New("tenant ID required (use --tenant or set active tenant in context)")
		}

		req := map[string]any{}
		if cmd.Flags().Changed("max-topics") {
			v, _ := cmd.Flags().GetInt("max-topics")
			req["max_topics"] = v
		}
		if cmd.Flags().Changed("max-partitions") {
			v, _ := cmd.Flags().GetInt("max-partitions")
			req["max_partitions"] = v
		}
		if cmd.Flags().Changed("max-storage-bytes") {
			v, _ := cmd.Flags().GetInt64("max-storage-bytes")
			req["max_storage_bytes"] = v
		}
		if cmd.Flags().Changed("producer-byte-rate") {
			v, _ := cmd.Flags().GetInt64("producer-byte-rate")
			req["producer_byte_rate"] = v
		}
		if cmd.Flags().Changed("consumer-byte-rate") {
			v, _ := cmd.Flags().GetInt64("consumer-byte-rate")
			req["consumer_byte_rate"] = v
		}
		if cmd.Flags().Changed("max-connections") {
			v, _ := cmd.Flags().GetInt("max-connections")
			req["max_connections"] = v
		}

		if len(req) == 0 {
			return errors.New("at least one quota field must be specified")
		}

		c, err := newClient()
		if err != nil {
			return err
		}
		result, err := c.UpdateQuota(cmd.Context(), tenantID, req)
		if err != nil {
			return fmt.Errorf("update quota: %w", err)
		}
		return printOutput(result, output)
	},
}
