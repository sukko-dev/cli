package commands

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	clicontext "github.com/sukko-dev/cli/context"
)

const (
	sukkoConfigDir   = ".sukko"
	sukkoConfigFile  = "config.json"
	sukkoComposeFile = "docker-compose.yml"
)

// composeFilePath returns the path to the compose file within the .sukko directory.
func composeFilePath() string {
	return filepath.Join(".", sukkoConfigDir, sukkoComposeFile)
}

// Default dev credentials read from environment, falling back to docker-compose defaults.
// These are NOT compiled secrets — they are well-known local dev values
// that match docker-compose.yml and are overridable via env vars.
var devAdminToken = envOrDefault("SUKKO_DEV_ADMIN_TOKEN", "sukko-dev-token")

var useDefaults bool

func init() {
	initCmd.Flags().BoolVar(&useDefaults, "defaults", false, "Skip prompts and use defaults (postgres + valkey + direct)")
	rootCmd.AddCommand(initCmd)
}

// ProjectConfig stores infrastructure selections from sukko init.
// Secrets (admin token) are NOT stored here — they live exclusively in the
// encrypted context store (~/.config/sukko/contexts/).
// Exception: CredentialsEncKey is stored here because it's a local dev-only
// key passed to the provisioning container via env var. The .sukko/ directory
// is gitignored and local to the project.
type ProjectConfig struct {
	Database          string `json:"database"`
	Broadcast         string `json:"broadcast"`
	MessageBackend    string `json:"message_backend"`
	Observability     bool   `json:"observability,omitempty"`
	Tracing           bool   `json:"tracing,omitempty"`
	Profiling         bool   `json:"profiling,omitempty"`
	CredentialsEncKey string `json:"credentials_encryption_key,omitempty"` // auto-generated 32-byte hex key for provisioning
}

// loadProjectConfig reads and unmarshals .sukko/config.json.
// Returns nil (not error) if the file doesn't exist.
func loadProjectConfig() (*ProjectConfig, error) {
	configPath := filepath.Join(".", sukkoConfigDir, sukkoConfigFile)
	data, err := os.ReadFile(configPath) //nolint:gosec // G304: path derived from fixed constant, not user input
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read project config: %w", err)
	}

	var cfg ProjectConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse project config: %w", err)
	}
	return &cfg, nil
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a local Sukko development environment",
	Long: `Initialize a local Sukko development environment.

Creates .sukko/config.json with infrastructure selections and sets up
a local context with dev credentials. Run 'sukko up' to start services.`,
	RunE: runInit,
}

func runInit(cmd *cobra.Command, _ []string) error {
	// Generate 32-byte credentials encryption key for provisioning
	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		return fmt.Errorf("generate credentials encryption key: %w", err)
	}

	cfg := ProjectConfig{
		Database:          "postgres",
		Broadcast:         "valkey",
		MessageBackend:    "direct",
		CredentialsEncKey: hex.EncodeToString(keyBytes),
	}

	if !useDefaults {
		var err error
		cfg.MessageBackend, err = promptChoice(cmd, "Message backend", []string{"direct", "kafka", "redpanda"}, "direct")
		if err != nil {
			return err
		}

		obs, err := promptChoice(cmd, "Observability (Prometheus, Grafana, Tempo, Pyroscope, AlertManager)", []string{"yes", "no"}, "no")
		if err != nil {
			return err
		}
		cfg.Observability = obs == "yes"

		if cfg.Observability {
			tracing, err := promptChoice(cmd, "  Distributed tracing", []string{"yes", "no"}, "yes")
			if err != nil {
				return err
			}
			cfg.Tracing = tracing == "yes"

			profiling, err := promptChoice(cmd, "  Continuous profiling", []string{"yes", "no"}, "yes")
			if err != nil {
				return err
			}
			cfg.Profiling = profiling == "yes"
		}
	}

	// Write .sukko/config.json
	configDir := filepath.Join(".", sukkoConfigDir)
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	configPath := filepath.Join(configDir, sukkoConfigFile)
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Config written to %s\n", configPath)

	// Create local context
	store, err := clicontext.NewStore()
	if err != nil {
		return fmt.Errorf("init context store: %w", err)
	}

	tokenEnc, err := store.EncryptSecret(devAdminToken)
	if err != nil {
		return fmt.Errorf("encrypt admin token: %w", err)
	}

	ctx := &clicontext.Context{
		Name:            "local",
		Type:            "local",
		GatewayURL:      defaultGatewayURL,
		ProvisioningURL: defaultAPIURL,
		TesterURL:       defaultTesterURL,
		AdminTokenEnc:   tokenEnc,
		Environment:     "local",
		ActiveTenant:    "demo",
	}

	if err := store.Add(ctx); err != nil {
		return fmt.Errorf("add local context: %w", err)
	}

	if err := store.SetActive("local"); err != nil {
		return fmt.Errorf("set active context: %w", err)
	}

	fmt.Fprintln(cmd.OutOrStdout(), "Local context created and set as active.")

	// Generate the admin keypair `sukko up` requires, so the documented
	// init→up quick-start works on a pristine machine (previously up failed
	// with "run 'sukko auth keygen' first" while its own hint pointed back at
	// init). Idempotent — an existing keypair is never regenerated.
	keypairDir := filepath.Join(store.Dir(), ctx.Name) // same dir resolveKeypairDir() derives for the active context
	created, _, err := ensureAdminKeypair(keypairDir)
	if err != nil {
		return fmt.Errorf("generate admin keypair: %w", err)
	}
	if created {
		fmt.Fprintln(cmd.OutOrStdout(), "Admin keypair generated (used to authenticate against provisioning).")
	} else {
		fmt.Fprintln(cmd.OutOrStdout(), "Admin keypair already present — kept.")
	}
	fmt.Fprintf(cmd.OutOrStdout(), "\nStack: %s + %s + %s\n", cfg.Database, cfg.Broadcast, cfg.MessageBackend)
	if os.Getenv("SUKKO_DEV_ADMIN_TOKEN") == "" {
		fmt.Fprintln(cmd.ErrOrStderr(), "Note: using default dev credentials. Set SUKKO_DEV_ADMIN_TOKEN for non-local environments.")
	}
	fmt.Fprintln(cmd.OutOrStdout(), "\nRun 'sukko up' to start the local environment.")

	return nil
}

func promptChoice(cmd *cobra.Command, label string, options []string, defaultVal string) (string, error) { //nolint:unparam // error return reserved for future validation; callers already handle it
	fmt.Fprintf(cmd.OutOrStdout(), "%s (%s) [%s]: ", label, joinOptions(options), defaultVal)

	var input string
	if _, err := fmt.Scanln(&input); err != nil || strings.TrimSpace(input) == "" {
		// Empty input or EOF (non-interactive/piped): use default. This is intentional
		// graceful degradation for non-TTY environments (Principle IV).
		return defaultVal, nil //nolint:nilerr // EOF/empty input is expected in non-TTY; returning default is intentional
	}

	if slices.Contains(options, input) {
		return input, nil
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Invalid choice %q, using default %q\n", input, defaultVal)
	return defaultVal, nil
}

func joinOptions(options []string) string {
	return strings.Join(options, "/")
}
