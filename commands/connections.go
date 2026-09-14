package commands

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/sukko-dev/cli/client"
)

var (
	connToken   string
	connAPIKey  string
	connGateway string
	connTimeout time.Duration
)

func init() {
	connectionsCmd.AddCommand(connectionsTestCmd)
	connectionsTestCmd.Flags().StringVar(&connToken, "token", "", "JWT token for authentication")
	connectionsTestCmd.Flags().StringVar(&connAPIKey, "api-key", "", "API key for authentication")
	connectionsTestCmd.Flags().StringVar(&connGateway, "gateway-url", "", "Gateway URL (overrides context)")
	connectionsTestCmd.Flags().DurationVar(&connTimeout, "timeout", 10*time.Second, "Connection timeout")
	rootCmd.AddCommand(connectionsCmd)
}

var connectionsCmd = &cobra.Command{
	Use:   "connections",
	Short: "Connection management and testing",
}

var connectionsTestCmd = &cobra.Command{
	Use:   "test",
	Short: "Test WebSocket connectivity to the gateway (CLI-only, no tester needed)",
	Long: `Test WebSocket connectivity by opening a single connection directly from the CLI.

Verifies: WebSocket upgrade (101), subscribe, and basic message flow.
For full deployment validation (Kafka pipeline, multi-tenant isolation),
use 'sukko test smoke' which orchestrates the tester service.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		gatewayURL := resolveGatewayURL(connGateway)
		tok, apiKey := resolveWSAuth(connToken, connAPIKey)

		if tok == "" && apiKey == "" {
			return errors.New("authentication required (use --token or --api-key)")
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Testing connection to %s...\n", gatewayURL)

		opts := wsDialOpts(tok, apiKey)

		ctx, cancel := context.WithTimeout(cmd.Context(), connTimeout)
		defer cancel()

		// Step 1: Connect
		start := time.Now()
		wsClient, err := client.Dial(ctx, gatewayURL, opts...)
		if err != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "  Connect: %sFAIL%s (%v)\n", colorRed, colorReset, err)
			return errors.New("connection test failed")
		}
		defer wsClient.Close()
		connectTime := time.Since(start)
		fmt.Fprintf(cmd.OutOrStdout(), "  Connect: %sPASS%s (%s)\n", colorGreen, colorReset, connectTime.Round(time.Millisecond))

		// Step 2: Subscribe
		const defaultTestChannel = "sukko.test.connection"
		testChannel := defaultTestChannel
		if err := wsClient.Subscribe([]string{testChannel}); err != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "  Subscribe: %sFAIL%s (%v)\n", colorRed, colorReset, err)
			return errors.New("connection test failed")
		}

		// Read subscription acknowledgment (if any)
		msg, err := wsClient.ReadMessage()
		if err != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "  Subscribe: %sFAIL%s (no response: %v)\n", colorRed, colorReset, err)
			return errors.New("connection test failed")
		}

		if msg.Type == "error" || msg.Type == "subscribe_error" {
			fmt.Fprintf(cmd.OutOrStdout(), "  Subscribe: %sFAIL%s (%s)\n", colorRed, colorReset, string(msg.Data))
			return errors.New("connection test failed")
		}

		fmt.Fprintf(cmd.OutOrStdout(), "  Subscribe: %sPASS%s (channel: %s)\n", colorGreen, colorReset, testChannel)

		// Step 3: Summary
		authMode := "JWT"
		if apiKey != "" && tok == "" {
			authMode = "API Key (public channels only)"
		} else if apiKey != "" && tok != "" {
			authMode = "JWT + API Key"
		}

		fmt.Fprintf(cmd.OutOrStdout(), "\n%sConnection test passed%s\n", colorGreen, colorReset)
		fmt.Fprintf(cmd.OutOrStdout(), "  Gateway: %s\n", gatewayURL)
		fmt.Fprintf(cmd.OutOrStdout(), "  Auth: %s\n", authMode)
		fmt.Fprintf(cmd.OutOrStdout(), "  Latency: %s\n", connectTime.Round(time.Millisecond))

		return nil
	},
}
