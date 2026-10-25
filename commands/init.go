package commands

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	clicontext "github.com/sukko-dev/cli/context"
	"golang.org/x/term"
)

// stdinIsInteractive reports whether stdin is a real terminal. It gates prompts that must NOT
// auto-fire on their default in a piped/CI run — notably the device-login offer, which would
// otherwise start a minutes-long network wait when bare `sukko init` is run non-interactively
// (promptChoice returns its default on EOF). Uses the same termios check as promptSecret
// (context.go) and isTTY (output.go) — a pipe, a regular file, and /dev/null all return false.
// A package var so tests can simulate interactive / non-interactive stdin.
var stdinIsInteractive = func() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

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
	// WebhookInternalToken is the provisioning↔webhook-worker shared secret. Required by
	// Pro/Enterprise editions (webhooks are Pro+), so provisioning fatals at boot without
	// it. Auto-generated here (local dev-only, like CredentialsEncKey) so `sukko up` boots
	// a Pro/Enterprise stack out of the box.
	WebhookInternalToken string `json:"webhook_internal_token,omitempty"`
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

Creates .sukko/config.json with infrastructure selections and sets up a local
context with dev credentials. If you are not logged in yet, init offers to fetch
your Sukko license (device login) so it can tailor the setup to your edition;
Pro-only features (e.g. distributed tracing) are offered only when your license
includes them. Re-running init keeps a stored license — it is removed only by
'sukko license remove'. Run 'sukko up' to start services.`,
	RunE: runInit,
}

// randomHexToken returns 32 cryptographically-random bytes as a hex string, used for the
// auto-generated local dev secrets.
func randomHexToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func runInit(cmd *cobra.Command, _ []string) error {
	// Generate 32-byte credentials encryption key for provisioning
	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		return fmt.Errorf("generate credentials encryption key: %w", err)
	}

	// Generate the provisioning↔webhook-worker shared secret (Pro/Enterprise require it).
	webhookToken, err := randomHexToken()
	if err != nil {
		return fmt.Errorf("generate webhook internal token: %w", err)
	}

	cfg := ProjectConfig{
		Database:             "postgres",
		Broadcast:            "valkey",
		MessageBackend:       "direct",
		CredentialsEncKey:    hex.EncodeToString(keyBytes),
		WebhookInternalToken: webhookToken,
	}

	// Set up the local context BEFORE the feature prompts, so we know the licensed edition (and
	// can offer to fetch a license first) and only prompt for features the license can actually
	// use — prompting for a gated feature a Community stack will refuse to boot with is pointless.
	store, err := clicontext.NewStore()
	if err != nil {
		return fmt.Errorf("init context store: %w", err)
	}
	ctx, err := ensureLocalContext(cmd, store)
	if err != nil {
		return err
	}
	edition, licState, err := initResolveEdition(cmd, store, ctx)
	if err != nil {
		return err
	}

	if !useDefaults {
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
			// Distributed tracing is Pro (features.go ConnectionTracing). Only offer it when the
			// license includes it; otherwise skip (off) with a one-line note rather than prompt
			// for something the stack can't run. Every gated feature is gated this way.
			if licState == licensePresent && editionAtLeast(edition, editionPro) {
				tracing, err := promptChoice(cmd, "  Distributed tracing", []string{"yes", "no"}, "yes")
				if err != nil {
					return err
				}
				cfg.Tracing = tracing == "yes"
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "  Distributed tracing — skipped (a Pro feature; run 'sukko license login' with a Pro license to enable it).")
			}

			// Continuous profiling is not edition-gated — always offered.
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
	fmt.Fprintf(cmd.OutOrStdout(), "\nStack: %s + %s + %s\n", cfg.Database, cfg.Broadcast, cfg.MessageBackend)
	if os.Getenv("SUKKO_DEV_ADMIN_TOKEN") == "" {
		fmt.Fprintln(cmd.ErrOrStderr(), "Note: using default dev credentials. Set SUKKO_DEV_ADMIN_TOKEN for non-local environments.")
	}
	fmt.Fprintln(cmd.OutOrStdout(), "\nRun 'sukko up' to start the local environment.")

	return nil
}

// ensureLocalContext creates (or idempotently updates) the "local" context and sets it active,
// returning the context. Re-running init PRESERVES existing credentials: a stored license key and
// API key are carried over from any existing local context, never wiped by the wholesale context
// rewrite — those are removed only by 'sukko license remove' (industry-standard idempotent init).
// The admin keypair `sukko up` requires is generated here too (idempotent).
func ensureLocalContext(cmd *cobra.Command, store *clicontext.Store) (*clicontext.Context, error) {
	tokenEnc, err := store.EncryptSecret(devAdminToken)
	if err != nil {
		return nil, fmt.Errorf("encrypt admin token: %w", err)
	}

	ctx := &clicontext.Context{
		Name:            "local",
		Type:            clicontext.TypeLocal,
		GatewayURL:      defaultGatewayURL,
		ProvisioningURL: defaultAPIURL,
		TesterURL:       defaultTesterURL,
		AdminTokenEnc:   tokenEnc,
		Environment:     "local",
		ActiveTenant:    "demo",
	}

	// Carry over credentials from an existing local context so re-init never destroys them
	// (they are removed only by 'sukko license remove'). A first run has no context yet
	// (ErrContextNotFound) — nothing to preserve. Any OTHER read error (corrupt JSON, EACCES)
	// must NOT be swallowed: the following store.Add would overwrite a file that may still hold a
	// recoverable key, re-introducing the very data loss this fixes — so fail loudly instead.
	switch existing, gerr := store.Get("local"); {
	case gerr == nil && existing != nil:
		ctx.LicenseKeyEnc = existing.LicenseKeyEnc
		ctx.APIKeyEnc = existing.APIKeyEnc
	case errors.Is(gerr, clicontext.ErrContextNotFound):
		// first run — nothing to preserve
	default:
		return nil, fmt.Errorf("read existing local context (not overwriting it): %w", gerr)
	}

	if err := store.Add(ctx); err != nil {
		return nil, fmt.Errorf("add local context: %w", err)
	}
	if err := store.SetActive("local"); err != nil {
		return nil, fmt.Errorf("set active context: %w", err)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "Local context ready and set as active.")

	keypairDir := filepath.Join(store.Dir(), ctx.Name) // same dir resolveKeypairDir() derives for the active context
	created, _, err := ensureAdminKeypair(keypairDir)
	if err != nil {
		return nil, fmt.Errorf("generate admin keypair: %w", err)
	}
	if created {
		fmt.Fprintln(cmd.OutOrStdout(), "Admin keypair generated (used to authenticate against provisioning).")
	} else {
		fmt.Fprintln(cmd.OutOrStdout(), "Admin keypair already present — kept.")
	}
	return ctx, nil
}

// initResolveEdition returns the effective license edition for gating init's feature prompts. If
// the context already holds a license (from a prior login or 'license set', preserved across
// re-init), that edition is used. Otherwise, when init is interactive, it offers to log in and
// fetch one now; a decline, a non-interactive run (--defaults), or a login error leaves the stack
// unlicensed — init continues and gated features are simply not offered.
func initResolveEdition(cmd *cobra.Command, store *clicontext.Store, ctx *clicontext.Context) (edition string, state licenseState, err error) {
	licenseKey, lkErr := ctx.LicenseKey(store.Key())
	if lkErr != nil {
		// A stored key that won't decrypt (e.g. a different machine/keyring) is not usable —
		// tell the operator rather than silently treating it as absent, then fall through to the
		// unlicensed path (a fresh login below recovers). Not fatal.
		fmt.Fprintf(cmd.ErrOrStderr(), "Note: a stored license key could not be read (%v); treating this stack as unlicensed.\n", lkErr)
		licenseKey = ""
	}
	edition, state = readLicenseEdition(licenseKey)
	if state == licensePresent {
		fmt.Fprintf(cmd.OutOrStdout(), "Using your %s license.\n", capitalizeEdition(edition))
		return edition, state, nil
	}

	// Offer a browser device-login ONLY when stdin is a real terminal — never auto-start a
	// minutes-long network wait on a piped/CI run (--defaults, or no TTY, where promptChoice
	// returns its default on EOF). The prompt defaults to "no" so an operator hitting Enter
	// through the defaults is not dropped into a browser flow either.
	if useDefaults || !stdinIsInteractive() {
		return edition, state, nil
	}
	// promptChoice never returns a non-nil error today (see its nolint:unparam); a prompt hiccup
	// must not block init, so the discarded error is intentional.
	choice, _ := promptChoice(cmd, "Log in to fetch your Sukko license now?", []string{"yes", "no"}, "no")
	if choice != "yes" {
		return edition, state, nil
	}

	claims, lerr := fetchAndStoreLicense(cmd.Context(), cmd.OutOrStdout(), store, ctx)
	if lerr != nil {
		// Ctrl-C means "stop" — abort init. Other failures (denied, expired, unreachable) are
		// non-fatal: continue unlicensed; they can run 'sukko license login' later.
		if errors.Is(lerr, context.Canceled) {
			return "", state, fmt.Errorf("init canceled during license login: %w", lerr)
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "License login did not complete (%v) — continuing without a license.\n", lerr)
		return edition, state, nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Logged in. Edition: %s, Org: %s, Expires: %s\n",
		capitalizeEdition(claims.Edition), claims.Org, formatExpiry(claims.Exp))
	return claims.Edition, licensePresent, nil
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
