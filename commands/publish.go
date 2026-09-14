package commands

import (
	"encoding/json"
	"fmt"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/sukko-dev/cli/client"
)

var (
	pubToken    string
	pubAPIKey   string
	pubCount    int
	pubInterval time.Duration
	pubGateway  string
)

func init() {
	publishCmd.Flags().StringVar(&pubToken, "token", "", "JWT token for authentication")
	publishCmd.Flags().StringVar(&pubAPIKey, "api-key", "", "API key for authentication")
	publishCmd.Flags().IntVar(&pubCount, "count", 1, "Number of messages to publish")
	publishCmd.Flags().DurationVar(&pubInterval, "interval", 0, "Interval between messages (e.g., 100ms, 1s)")
	publishCmd.Flags().StringVar(&pubGateway, "gateway-url", "", "Gateway URL (overrides context)")
	rootCmd.AddCommand(publishCmd)
}

var publishCmd = &cobra.Command{
	Use:   "publish <channel> <message>",
	Short: "Publish a message to a channel via the gateway",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		channel := args[0]
		message := json.RawMessage(args[1])

		// Validate JSON
		if !json.Valid(message) {
			return fmt.Errorf("message is not valid JSON: %s", args[1])
		}

		gatewayURL := resolveGatewayURL(pubGateway)
		tok, apiKey := resolveWSAuth(pubToken, pubAPIKey)

		opts := wsDialOpts(tok, apiKey)

		ctx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()
		wsClient, err := client.Dial(ctx, gatewayURL, opts...)
		if err != nil {
			return fmt.Errorf("connect to gateway: %w", err)
		}
		defer wsClient.Close()

		for i := range pubCount {
			if err := wsClient.Publish(channel, message); err != nil {
				return fmt.Errorf("publish message %d: %w", i+1, err)
			}

			if pubCount > 1 {
				fmt.Fprintf(cmd.OutOrStdout(), "Published %d/%d to %s\n", i+1, pubCount, channel)
			}

			if pubInterval > 0 && i < pubCount-1 {
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(pubInterval):
				}
			}
		}

		if pubCount == 1 {
			fmt.Fprintf(cmd.OutOrStdout(), "Published to %s\n", channel)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "Published %d messages to %s\n", pubCount, channel)
		}

		return nil
	},
}

// wsDialOpts builds WebSocket dial options from resolved auth credentials.
func wsDialOpts(token, apiKey string) []client.DialOption {
	var opts []client.DialOption
	if token != "" {
		opts = append(opts, client.WithToken(token))
	}
	if apiKey != "" {
		opts = append(opts, client.WithAPIKey(apiKey))
	}
	return opts
}

// resolveWSAuth resolves WebSocket auth from flags, then context.
// API key is checked first (primary auth for CLI WebSocket connections).
// Admin token is NOT used — it's for the provisioning REST API, not gateway WebSocket.
func resolveWSAuth(tokenFlag, apiKeyFlag string) (tok, apiKey string) {
	tok = tokenFlag
	apiKey = apiKeyFlag

	// API key from context (primary auth for CLI WebSocket)
	if apiKey == "" && resolvedCtx != nil && resolvedStore != nil {
		if k, err := resolvedCtx.APIKey(resolvedStore.Key()); err == nil && k != "" {
			apiKey = k
		}
	}

	// No admin token fallback — admin token is for provisioning API only

	return tok, apiKey
}
