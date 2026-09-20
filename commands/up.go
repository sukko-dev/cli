package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/sukko-dev/cli/client"
	"github.com/sukko-dev/cli/compose"
)

const healthTimeout = 120 * time.Second

// Docker Compose internal service addresses — used by buildComposeConfig
// to wire services together inside the compose network.
const (
	composeDatabaseURL    = "postgres://sukko:sukko@postgres:5432/sukko_provisioning?sslmode=disable" //nolint:gosec // G101: not a credential — Docker Compose internal connection string with well-known dev defaults
	composeValkeyAddr     = "valkey:6379"
	composeRedpandaBroker = "redpanda:9092"
)

// Message backend selectors accepted in project config (sukko init). Both "kafka" and
// "redpanda" select the local Redpanda broker (Kafka-wire-compatible); see isKafkaFamilyBackend.
const (
	backendKafka    = "kafka"
	backendRedpanda = "redpanda"
)

// isKafkaFamilyBackend reports whether the message backend selects the Kafka/Redpanda
// broker (as opposed to the default "direct" backend). Both aliases are available on
// every edition (platform ADR-0009) — ingest, fan-out and publish all run license-free
// (channel-to-topic routing is Community per platform ADR-0014).
func isKafkaFamilyBackend(backend string) bool {
	return backend == backendKafka || backend == backendRedpanda
}

var pullImages bool

func init() {
	upCmd.Flags().BoolVar(&pullImages, "pull", false, "Always pull latest images before starting")
	rootCmd.AddCommand(upCmd)
}

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Start the local development environment",
	Long: `Start the local development environment using Docker Compose.

Reads .sukko/config.json (created by 'sukko init'), activates the selected
infrastructure profiles, waits for all services to become healthy, and
provisions a default tenant for immediate use.`,
	RunE: runUp,
}

func runUp(cmd *cobra.Command, _ []string) error {
	// Read project config
	configPath := filepath.Join(".", sukkoConfigDir, sukkoConfigFile)
	data, err := os.ReadFile(configPath) //nolint:gosec // G304: path derived from fixed constant, not user input
	if err != nil {
		return fmt.Errorf("read config (run 'sukko init' first): %w", err)
	}

	var cfg ProjectConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	if err := validateProjectConfig(cfg); err != nil {
		return err
	}

	// Verify credentials are available before starting services
	if resolvedCtx == nil {
		return errors.New("no active context found — run 'sukko init' first")
	}
	if _, _, err := resolveProvisioningConfig(); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "Hint: run 'sukko init' to set up admin keypair")
		return fmt.Errorf("resolve credentials: %w", err)
	}

	// Build profiles and env overrides from selections
	profiles, envOverrides := buildComposeConfig(cfg)

	// Credentials encryption key (required by provisioning for encrypting secrets in DB)
	if cfg.CredentialsEncKey != "" {
		envOverrides["CREDENTIALS_ENCRYPTION_KEY"] = cfg.CredentialsEncKey
	}

	// Admin bootstrap key — provisioning registers this on first startup for JWT auth
	adminPubKey := loadAdminPublicKey()
	if adminPubKey != "" {
		envOverrides["ADMIN_BOOTSTRAP_KEY"] = adminPubKey
	}

	// best-effort: license key passthrough is optional; decrypt failure is non-fatal
	if resolvedCtx != nil && resolvedStore != nil {
		if lk, err := resolvedCtx.LicenseKey(resolvedStore.Key()); err == nil && lk != "" {
			envOverrides["SUKKO_LICENSE_KEY"] = lk
		}
	}

	// Gateway push surface (sukko#244): the gateway registers its push routes only
	// when GATEWAY_PUSH_ENABLED=true at ITS start — flipping it later requires a
	// container recreate. Decide at boot from the same condition the push-service
	// reconciliation uses (push-capable edition + kafka-family backend), so the
	// routes and the service they proxy to come up together.
	applyGatewayPushEnv(envOverrides, resolveEffectiveLicenseKey(envOverrides), cfg.MessageBackend)

	fmt.Fprintf(cmd.OutOrStdout(), "Starting Sukko (postgres + %s + %s)...\n",
		cfg.Broadcast, cfg.MessageBackend)

	// Write embedded compose file to .sukko/
	if err := compose.WriteComposeFile(composeFilePath()); err != nil {
		return fmt.Errorf("write compose file: %w", err)
	}

	// Start Docker Compose
	fmt.Fprintln(cmd.OutOrStdout(), "\nStarting containers...")
	mgr, err := compose.NewManager(".", composeFilePath())
	if err != nil {
		return fmt.Errorf("create compose manager: %w", err)
	}
	if err := mgr.Up(cmd.Context(), profiles, envOverrides, pullImages); err != nil {
		return fmt.Errorf("start services: %w", err)
	}

	// Wait for core services to become healthy (delegates to docker compose ps)
	fmt.Fprintln(cmd.OutOrStdout(), "\nWaiting for services...")

	if err := mgr.WaitForHealth(cmd.Context(), cmd.OutOrStdout(), []string{"provisioning", "ws-gateway", "ws-server", "sukko-tester"}, healthTimeout); err != nil {
		return fmt.Errorf("health check: %w", err)
	}

	// Observability services — warn on error, don't fail
	if cfg.Observability {
		if err := mgr.WaitForHealth(cmd.Context(), cmd.OutOrStdout(), []string{"grafana", "prometheus"}, healthTimeout); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: observability services not healthy: %v\n", err)
		}
	}

	// Provision default tenant
	fmt.Fprintln(cmd.OutOrStdout(), "\nProvisioning default tenant...")
	provErr := provisionDefaultTenant(cmd)
	if provErr != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "Error: default tenant provisioning failed: %v\n", provErr)
		fmt.Fprintln(cmd.ErrOrStderr(), "  Services are running, but the demo tenant is NOT usable.")
		fmt.Fprintln(cmd.ErrOrStderr(), "  Create one manually with 'sukko tenant create' or re-run 'sukko up'.")
	}

	// Push-service reconciliation — start/stop based on final edition
	reconcilePushService(cmd, mgr, cfg)

	if provErr != nil {
		// §III: no silent failures — a broken demo tenant must not end in
		// "Sukko is ready!" with exit 0 (an audit caught exactly that: a 500
		// CREATE_FAILED buried under a success banner).
		return fmt.Errorf("default tenant provisioning: %w", provErr)
	}

	fmt.Fprintln(cmd.OutOrStdout(), "\nSukko is ready! Try:")
	fmt.Fprintln(cmd.OutOrStdout(), "  sukko status")
	fmt.Fprintln(cmd.OutOrStdout(), "  sukko tenant list")

	if cfg.Observability {
		fmt.Fprintln(cmd.OutOrStdout(), "\nObservability:")
		fmt.Fprintln(cmd.OutOrStdout(), "  Grafana:      http://localhost:3030")
		fmt.Fprintln(cmd.OutOrStdout(), "  Prometheus:   http://localhost:9091")
		fmt.Fprintln(cmd.OutOrStdout(), "  AlertManager: http://localhost:9093")
	}

	return nil
}

// resolveEffectiveLicenseKey returns the license key that compose's ${SUKKO_LICENSE_KEY:-}
// interpolation will actually see: the context-store key (already placed in envOverrides) takes
// precedence, falling back to a shell-exported SUKKO_LICENSE_KEY (inherited by the compose
// subprocess via os.Environ() in compose.Manager.Up). Keeping this in sync with the real
// resolution prevents the push wiring from false-positiving on the documented
// `export SUKKO_LICENSE_KEY` workflow.
func resolveEffectiveLicenseKey(envOverrides map[string]string) string {
	if lk := envOverrides["SUKKO_LICENSE_KEY"]; lk != "" {
		return lk
	}
	return os.Getenv("SUKKO_LICENSE_KEY")
}

// validateProjectConfig rejects project config values that are not supported.
func validateProjectConfig(cfg ProjectConfig) error {
	if cfg.Broadcast != "" && cfg.Broadcast != "valkey" {
		return fmt.Errorf("broadcast %q is not supported: remove the broadcast field from .sukko/config.json or set it to \"valkey\"", cfg.Broadcast)
	}
	return nil
}

func buildComposeConfig(cfg ProjectConfig) (profiles []string, envOverrides map[string]string) {
	envOverrides = map[string]string{}

	// Postgres is the only supported database — always activate
	profiles = append(profiles, "postgres")
	envOverrides["DATABASE_DRIVER"] = "postgres"
	envOverrides["DATABASE_URL"] = composeDatabaseURL

	// Valkey is always the broadcast bus; wire its compose-internal address.
	// When cfg.MessageBackend is empty (direct mode — the Go default), no MESSAGE_BACKEND
	// env var is injected and the Go envDefault ("direct") takes effect natively.
	envOverrides["VALKEY_ADDRS"] = composeValkeyAddr

	// Kafka mode: "kafka" and "redpanda" both select the local Redpanda broker
	// (Kafka-wire-compatible) under the "kafka" compose profile — the only profile the
	// redpanda service declares. (Previously "kafka" wired the nonexistent host kafka:9092
	// and "redpanda" appended a "redpanda" profile no service defines — both broken.)
	switch cfg.MessageBackend {
	case backendKafka, backendRedpanda:
		profiles = append(profiles, "kafka")
		envOverrides["MESSAGE_BACKEND"] = "kafka"
		envOverrides["KAFKA_BROKERS"] = composeRedpandaBroker
	}

	if cfg.Observability {
		profiles = append(profiles, "observability")
		if cfg.Tracing {
			envOverrides["OTEL_TRACING_ENABLED"] = "true"
		}
		if cfg.Profiling {
			envOverrides["PPROF_ENABLED"] = "true"
			envOverrides["PYROSCOPE_ENABLED"] = "true"
		}
	}

	return profiles, envOverrides
}

// provisionOutcome classifies a provisioning-step error for the demo-tenant
// flow. Only a 409 conflict means "already exists" (idempotent re-run of
// `sukko up`); only an EDITION_LIMIT rejection is a clean skip; everything
// else is a real failure that must surface (§III — an audit found a 500
// CREATE_FAILED reported as "(may already exist)" followed by "Sukko is
// ready!").
type provisionOutcome int

const (
	provisionOK           provisionOutcome = iota // no error
	provisionExists                               // 409 — already provisioned, idempotent re-run
	provisionEditionGated                         // 403 EDITION_LIMIT — feature above this edition
	provisionFailed                               // anything else — must surface loudly
)

// editionLimitCode is the provisioning API's error code for edition-gated
// features (matches httputil.ErrorResponse's "code" field).
const editionLimitCode = "EDITION_LIMIT"

func classifyProvisionError(err error) provisionOutcome {
	switch {
	case err == nil:
		return provisionOK
	case errors.Is(err, client.ErrAPIConflict):
		return provisionExists
	case errors.Is(err, client.ErrAPIForbidden) && strings.Contains(err.Error(), editionLimitCode):
		return provisionEditionGated
	default:
		return provisionFailed
	}
}

func provisionDefaultTenant(cmd *cobra.Command) error {
	c, err := newClient()
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}

	ctx := cmd.Context()

	// Create default tenant — a 409 means it already exists (re-run), which is
	// fine; any other error is a real failure and must not be shrugged off.
	_, err = c.CreateTenant(ctx, buildTenantCreateRequest("demo", "Demo", "shared"))
	switch classifyProvisionError(err) {
	case provisionOK:
		fmt.Fprintln(cmd.OutOrStdout(), "  Tenant 'demo': created")
	case provisionExists:
		fmt.Fprintln(cmd.OutOrStdout(), "  Tenant 'demo': already exists")
	default:
		return fmt.Errorf("create demo tenant: %w", err)
	}

	// Catch-all routing rules. Available on every edition (platform ADR-0014), so
	// this normally succeeds; the gated branch stays as a defensive classification
	// rather than a claim about editions. Rules gate client/REST publish INTO the
	// kafka backend only — the direct backend and kafka ingest need none.
	_, err = c.SetRoutingRules(ctx, "demo", defaultCatchAllRules())
	switch classifyProvisionError(err) {
	case provisionOK:
		fmt.Fprintln(cmd.OutOrStdout(), "  Routing rules: set (catch-all)")
	case provisionEditionGated:
		fmt.Fprintln(cmd.OutOrStdout(), "  Routing rules: skipped (not needed for the direct backend or kafka ingest)")
	default:
		return fmt.Errorf("set routing rules: %w", err)
	}

	// Seed channel rules. Channel authorization is provisioning-only — a tenant
	// with no rules is denied every subscribe and publish — so the demo tenant
	// must be seeded to be usable out of the box. Ungated on every edition:
	// any failure here leaves a deny-all tenant and must surface.
	if _, err = c.SetChannelRules(ctx, "demo", defaultChannelRules()); err != nil {
		return fmt.Errorf("seed channel rules: %w", err)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "  Channel rules: set (all channels open, subscribe + publish — override with 'sukko rules channels set')")

	return nil
}

// defaultChannelRules returns the channel-rules request body seeded for the
// demo tenant: all channels open for subscribe AND publish. Field names match
// the provisioning SetChannelRulesRequest schema (public/default/publish_public).
// The previous "public_patterns" key was unknown to the API and was silently
// dropped, saving EMPTY rules — a deny-all tenant. Extracted so up_test.go can
// assert the exact schema without network calls.
func defaultChannelRules() map[string]any {
	return map[string]any{
		"public":         []string{"*"},
		"default":        []string{"*"},
		"publish_public": []string{"*"},
	}
}

// defaultCatchAllRules returns the routing rules request body for the catch-all default rule.
// Extracted so up_test.go can assert the correct format without network calls.
func defaultCatchAllRules() map[string]any {
	return map[string]any{
		"rules": []map[string]any{
			{"pattern": "**", "ingress_topic": "default", "priority": 100},
		},
	}
}

// loadAdminPublicKey reads the admin public key (raw base64) from the context directory.
// Returns empty string if no keypair exists (user hasn't run 'sukko auth keygen').
func loadAdminPublicKey() string {
	keyPath := resolveAdminKeyPath()
	if keyPath == "" {
		return ""
	}
	pubPath := strings.TrimSuffix(keyPath, filepath.Ext(keyPath)) + ".pub"
	data, err := os.ReadFile(pubPath) //nolint:gosec // G304: path derived from context directory, not user input
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

const pushServiceTimeout = 30 * time.Second

// gatewayPushEnabled decides whether the gateway should boot with its push
// surface enabled (GATEWAY_PUSH_ENABLED, sukko#244): the effective license
// decodes to a push-capable edition AND the backend is kafka-family — the same
// condition reconcilePushService uses to start push-service. The decode is
// UNVERIFIED (decodeLicenseClaims; the CLI holds no public key), which is safe
// for an orchestration hint: the server enforces the license for real, so a
// forged edition claim only exposes gateway routes whose backing service will
// refuse to serve — no capability is granted by the routes existing.
func gatewayPushEnabled(licenseKey, messageBackend string) bool {
	if licenseKey == "" || !isKafkaFamilyBackend(messageBackend) {
		return false
	}
	claims, err := decodeLicenseClaims(licenseKey)
	if err != nil {
		return false
	}
	return editionSupportsPush(claims.Edition)
}

// applyGatewayPushEnv sets GATEWAY_PUSH_ENABLED=true in the compose env when the
// gateway should boot with its push surface. When disabled the key is left ABSENT
// (not "false") so the embedded compose's `${GATEWAY_PUSH_ENABLED:-false}` default
// applies — an operator's own exported value is then still honored by compose.
func applyGatewayPushEnv(envOverrides map[string]string, licenseKey, messageBackend string) {
	if gatewayPushEnabled(licenseKey, messageBackend) {
		envOverrides["GATEWAY_PUSH_ENABLED"] = "true"
	}
}

// reconcilePushService starts or stops push-service based on the final edition.
// Uses findLocalContext for license key and provisioning URL.
// Precondition: runUp has already verified resolvedCtx != nil, which guarantees resolvedStore != nil.
func reconcilePushService(cmd *cobra.Command, mgr *compose.Manager, cfg ProjectConfig) {
	localCtx, err := resolvedStore.FindLocalContext()
	if err != nil || localCtx == nil {
		return // no local context — skip reconciliation
	}

	// Decrypt license key from local context
	licenseKey, err := localCtx.LicenseKey(resolvedStore.Key())
	if err != nil || licenseKey == "" {
		return // no license key — skip reconciliation
	}

	// Create client targeting local provisioning
	localSigner, err := loadAdminSigner()
	if err != nil {
		return // no admin keypair — skip silently
	}

	c, err := client.New(client.Config{
		BaseURL: localCtx.ProvisioningURL,
		Signer:  localSigner,
	})
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "Warning: could not create provisioning client: %v — push-service not reconciled.\n", err)
		return
	}

	// Push license explicitly (idempotent — provisioning may have auto-loaded it)
	resp, err := c.PushLicense(cmd.Context(), licenseKey)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "Warning: license push failed: %v — push-service not started. Run 'sukko license push <key>' to retry.\n", err)
		return
	}

	edition := resp.Edition
	compatibleBackend := isKafkaFamilyBackend(cfg.MessageBackend)

	if editionSupportsPush(edition) && compatibleBackend {
		if !pushServiceHealthy(cmd.Context(), mgr) {
			fmt.Fprintln(cmd.OutOrStdout(), "\nStarting push-service (Web Push, Pro+)...")
			if err := mgr.StartService(cmd.Context(), cmd.OutOrStdout(), "push-service", pushComposeProfiles, pushServiceTimeout); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: push-service failed to start: %v\n", err)
			}
		}
	} else if !editionSupportsPush(edition) {
		if pushServiceRunning(cmd.Context(), mgr) {
			fmt.Fprintln(cmd.OutOrStdout(), "\nStopping push-service (edition below Pro)...")
			if err := mgr.StopService(cmd.Context(), "push-service"); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: failed to stop push-service: %v\n", err)
			}
		}
	}
}

// pushServiceHealthy returns true if push-service reports Health=="healthy" via docker compose ps.
func pushServiceHealthy(ctx context.Context, mgr *compose.Manager) bool {
	statuses, err := mgr.Status(ctx)
	if err != nil {
		return false
	}
	for _, s := range statuses {
		if s.Service == "push-service" && s.Health == "healthy" {
			return true
		}
	}
	return false
}

// pushServiceRunning returns true if push-service has a running container.
func pushServiceRunning(ctx context.Context, mgr *compose.Manager) bool {
	statuses, err := mgr.Status(ctx)
	if err != nil {
		return false
	}
	for _, s := range statuses {
		if s.Service == "push-service" && s.State == "running" {
			return true
		}
	}
	return false
}
