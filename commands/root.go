// Package commands implements the sukko CLI command tree.
package commands

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sukko-dev/cli/client"
	clicontext "github.com/sukko-dev/cli/context"
)

// CLI default values. Env vars take precedence via envOrDefault; CLI flags take precedence over env.
var (
	defaultAPIURL      = envOrDefault("SUKKO_API_URL", "http://localhost:18080")
	defaultGatewayURL  = envOrDefault("SUKKO_GATEWAY_URL", "ws://localhost:13000")
	defaultGatewayHTTP = envOrDefault("SUKKO_GATEWAY_HTTP_URL", "http://localhost:13000")
	defaultServerURL   = envOrDefault("SUKKO_SERVER_URL", "http://localhost:3005")
	defaultTesterURL   = envOrDefault("SUKKO_TESTER_URL", "http://localhost:8090")
)

const defaultOutput = "table"

var (
	apiURL      string
	token       string
	output      string
	contextName string
	verbose     bool

	// resolvedCtx is the active context loaded by PersistentPreRunE.
	resolvedCtx   *clicontext.Context
	resolvedStore *clicontext.Store
)

var rootCmd = &cobra.Command{
	Use:   "sukko",
	Short: "Sukko WebSocket platform CLI",
	Long:  "CLI tool for managing tenants, keys, rules, and operating the Sukko WebSocket platform.",
	PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
		// Skip context loading for context management commands and completion
		if isContextSubcommand(cmd) {
			return nil
		}
		if err := loadContext(cmd); err != nil {
			return err
		}
		if output != "json" && output != "table" {
			return fmt.Errorf("invalid output format %q: must be json or table", output)
		}
		return nil
	},
	SilenceUsage: true,
}

func init() {
	rootCmd.PersistentFlags().StringVar(&apiURL, "api-url", "", "Provisioning API base URL (overrides context)")
	rootCmd.PersistentFlags().StringVar(&token, "token", os.Getenv("SUKKO_TOKEN"), "Admin authentication token (overrides context)")
	rootCmd.PersistentFlags().StringVarP(&output, "output", "o", defaultOutput, "Output format (json|table)")
	rootCmd.PersistentFlags().StringVar(&contextName, "context", os.Getenv("SUKKO_CONTEXT"), "Context name (overrides active context)")
	rootCmd.PersistentFlags().BoolVar(&verbose, "verbose", false, "Enable verbose output")
}

// RootCmd returns the root cobra command for external tools (e.g., gendocs).
func RootCmd() *cobra.Command { return rootCmd }

// Execute runs the root command.
func Execute() error {
	if err := rootCmd.Execute(); err != nil {
		return fmt.Errorf("execute CLI: %w", err)
	}
	return nil
}

// newClient creates an AdminClient with keypair JWT auth.
func newClient() (*client.AdminClient, error) {
	url, signer, err := resolveProvisioningConfig()
	if err != nil {
		return nil, fmt.Errorf("resolve provisioning config: %w", err)
	}

	c, err := client.New(client.Config{
		BaseURL: url,
		Signer:  signer,
		Timeout: client.DefaultClientTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("create admin client: %w", err)
	}
	return c, nil
}

// resolveProvisioningConfig returns the provisioning URL and admin auth signer.
func resolveProvisioningConfig() (string, client.AuthSigner, error) {
	url := apiURL
	if url == "" && resolvedCtx != nil {
		url = resolvedCtx.ProvisioningURL
	}
	if url == "" {
		url = defaultAPIURL
	}

	signer, err := loadAdminSigner()
	if err != nil {
		return "", nil, fmt.Errorf("load admin keypair: %w", err)
	}

	return url, signer, nil
}

// resolveTesterToken returns the tester API auth token (separate from provisioning admin auth).
func resolveTesterToken() string {
	return envOrDefault("SUKKO_TESTER_TOKEN", "")
}

// loadAdminSigner loads the admin Ed25519 private key from the active context directory.
func loadAdminSigner() (client.AuthSigner, error) {
	keyPath := resolveAdminKeyPath()
	if keyPath == "" {
		return nil, errors.New("admin keypair not found — run 'sukko auth keygen' first")
	}

	data, err := os.ReadFile(keyPath) //nolint:gosec // G304: path derived from context directory, not user input
	if err != nil {
		return nil, fmt.Errorf("read admin key %s: %w", keyPath, err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("invalid PEM in %s", keyPath)
	}

	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse admin private key: %w", err)
	}

	edKey, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("admin key is not Ed25519")
	}

	// Key ID: required for JWT kid header — must match what provisioning registered
	kidPath := strings.TrimSuffix(keyPath, filepath.Ext(keyPath)) + ".kid"
	kidData, err := os.ReadFile(kidPath) //nolint:gosec // G304: path derived from context directory
	if err != nil {
		return nil, fmt.Errorf("read admin key ID %s: %w (run 'sukko auth keygen' to regenerate)", kidPath, err)
	}
	kid := strings.TrimSpace(string(kidData))
	if kid == "" {
		return nil, fmt.Errorf("admin key ID file is empty: %s", kidPath)
	}

	name := "admin"
	if resolvedCtx != nil {
		name = resolvedCtx.Name
	}

	return client.NewKeypairSigner(edKey, kid, name), nil
}

// resolveAdminKeyPath returns the path to the admin private key file.
func resolveAdminKeyPath() string {
	if resolvedCtx != nil && resolvedStore != nil {
		keyPath := filepath.Join(resolvedStore.Dir(), resolvedCtx.Name, "admin.key")
		if _, err := os.Stat(keyPath); err == nil {
			return keyPath
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	keyPath := filepath.Join(home, ".sukko", "admin.key")
	if _, err := os.Stat(keyPath); err == nil {
		return keyPath
	}

	return ""
}

// resolveTenant returns the tenant ID from: --tenant flag > context > empty.
func resolveTenant(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if resolvedCtx != nil {
		return resolvedCtx.ActiveTenant
	}
	return ""
}

// resolveGatewayURL returns the gateway URL from: flag > context > default.
func resolveGatewayURL(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if resolvedCtx != nil && resolvedCtx.GatewayURL != "" {
		return resolvedCtx.GatewayURL
	}
	return defaultGatewayURL
}

// wsToHTTP converts a WebSocket URL scheme to HTTP.
func wsToHTTP(wsURL string) string {
	if after, ok := strings.CutPrefix(wsURL, "wss://"); ok {
		return "https://" + after
	}
	if after, ok := strings.CutPrefix(wsURL, "ws://"); ok {
		return "http://" + after
	}
	return wsURL
}

// resolveTesterURL returns the tester URL from: flag > context > default.
func resolveTesterURL(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if resolvedCtx != nil && resolvedCtx.TesterURL != "" {
		return resolvedCtx.TesterURL
	}
	return defaultTesterURL
}

// loadContext attempts to load the active context. Non-fatal if no context is configured.
func loadContext(cmd *cobra.Command) error {
	store, err := clicontext.NewStore()
	if err != nil {
		if verbose {
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: could not init context store: %v\n", err)
		}
		return nil // non-fatal
	}
	resolvedStore = store

	name := contextName
	if name == "" {
		name, _ = store.ActiveName() // ignore error — no active context is fine
	}
	if name == "" {
		return nil
	}

	ctx, err := store.Get(name)
	if err != nil {
		if verbose {
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: could not load context %q: %v\n", name, err)
		}
		return nil // non-fatal
	}
	resolvedCtx = ctx

	return nil
}

// isContextSubcommand returns true for commands that should skip context loading.
func isContextSubcommand(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		if c.Name() == "context" || c.Name() == "completion" || c.Name() == "init" {
			return true
		}
	}
	return false
}

func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
