package commands

import (
	"fmt"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/sukko-dev/cli/client"
)

var (
	subToken   string
	subAPIKey  string
	subGateway string
)

func init() {
	subscribeCmd.Flags().StringVar(&subToken, "token", "", "JWT token for authentication")
	subscribeCmd.Flags().StringVar(&subAPIKey, "api-key", "", "API key for authentication")
	subscribeCmd.Flags().StringVar(&subGateway, "gateway-url", "", "Gateway URL (overrides context)")
	rootCmd.AddCommand(subscribeCmd)
}

var subscribeCmd = &cobra.Command{
	Use:   "subscribe <channel> [channel...]",
	Short: "Subscribe to channels and stream messages to stdout",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		channels := args
		gatewayURL := resolveGatewayURL(subGateway)
		tok, apiKey := resolveWSAuth(subToken, subAPIKey)

		opts := wsDialOpts(tok, apiKey)

		ctx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()

		wsClient, err := client.Dial(ctx, gatewayURL, opts...)
		if err != nil {
			return fmt.Errorf("connect to gateway: %w", err)
		}
		defer wsClient.Close()

		// Close connection when context is canceled to unblock ReadMessage.
		var shutdownWg sync.WaitGroup
		shutdownWg.Go(func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "panic in shutdown goroutine: %v\n", r)
				}
			}()
			<-ctx.Done()
			wsClient.Close()
		})
		defer shutdownWg.Wait()

		if err := wsClient.Subscribe(channels); err != nil {
			return fmt.Errorf("subscribe: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Subscribed to %v. Press Ctrl+C to stop.\n", channels)

		start := time.Now()
		msgCount := 0

		for {
			msg, err := wsClient.ReadMessage()
			if err != nil {
				if ctx.Err() != nil {
					break // canceled — connection closed by shutdown goroutine
				}
				return fmt.Errorf("read: %w", err)
			}

			msgCount++

			if output == "json" {
				_ = printJSON(msg) // best-effort: stdout write failure is non-recoverable
			} else {
				if msg.Channel != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s\n", msg.Channel, string(msg.Data))
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s\n", msg.Type, string(msg.Data))
				}
			}
		}

		duration := time.Since(start)
		fmt.Fprintf(cmd.OutOrStdout(), "\n--- %d messages received in %s ---\n", msgCount, duration.Round(time.Millisecond))
		fmt.Fprintf(cmd.OutOrStdout(), "Channels: %v\n", channels)

		return nil
	},
}
