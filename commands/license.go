package commands

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/sukko-dev/cli/client"
	"github.com/sukko-dev/cli/compose"
	clicontext "github.com/sukko-dev/cli/context"
)

func init() {
	licenseCmd.AddCommand(licenseSetCmd, licenseShowCmd, licenseRemoveCmd, licensePushCmd)
	rootCmd.AddCommand(licenseCmd)
}

var licenseCmd = &cobra.Command{
	Use:   "license",
	Short: "Manage Sukko license key",
	Long:  "Store, view, and remove the Sukko license key from the CLI context.",
}

// Edition identifiers as reported by provisioning's /edition and license endpoints.
const (
	editionPro        = "pro"
	editionEnterprise = "enterprise"
)

// pushComposeProfiles activates the push-service in the embedded compose file.
// The profile keeps its historical "enterprise" name even though push-service
// now boots with a Pro license (Web Push) — renaming is deferred until the
// platform compose (the sync source of truth) renames it in lockstep.
var pushComposeProfiles = []string{"enterprise"}

// editionSupportsPush reports whether the edition licenses the push service.
// Push-service boots with a Pro license (Web Push); FCM/APNs delivery
// additionally requires Enterprise — that gate is enforced server-side.
func editionSupportsPush(edition string) bool {
	return edition == editionPro || edition == editionEnterprise
}

// pushOrchestration is the compose action derived from a license transition.
type pushOrchestration int

const (
	pushNone   pushOrchestration = iota // neither edition push-capable — nothing to do
	pushStart                           // entered a push-capable edition — start push-service
	pushEnsure                          // still push-capable — ensure push-service is healthy
	pushStop                            // left push-capable editions — stop push-service
)

// pushAction decides the push-service orchestration for a license transition.
// An empty prevEdition (the pre-push edition lookup failed) counts as not
// push-capable, so pushing a Pro/Enterprise key still starts the service.
func pushAction(prevEdition, newEdition string) pushOrchestration {
	prevPush, newPush := editionSupportsPush(prevEdition), editionSupportsPush(newEdition)
	switch {
	case newPush && !prevPush:
		return pushStart
	case newPush && prevPush:
		return pushEnsure
	case prevPush:
		return pushStop
	default:
		return pushNone
	}
}

// licenseClaims represents the decoded payload of a license key.
type licenseClaims struct {
	Edition string `json:"edition"`
	Org     string `json:"org"`
	Exp     int64  `json:"exp"`
}

// decodeLicenseClaims splits a license key on ".", base64url-decodes the first
// segment (payload), and unmarshals the JSON claims. It does NOT verify the
// Ed25519 signature — the CLI doesn't have the public key.
func decodeLicenseClaims(key string) (*licenseClaims, error) {
	payloadSeg, sigSeg, ok := strings.Cut(key, ".")
	if !ok {
		return nil, errors.New("invalid license key format: expected payload.signature")
	}

	payload, err := base64.RawURLEncoding.DecodeString(payloadSeg)
	if err != nil {
		return nil, fmt.Errorf("decode license payload: %w", err)
	}

	// Validate signature segment is decodable (format check only)
	if _, err := base64.RawURLEncoding.DecodeString(sigSeg); err != nil {
		return nil, fmt.Errorf("decode license signature: %w", err)
	}

	var claims licenseClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("unmarshal license claims: %w", err)
	}

	return &claims, nil
}

// formatExpiry returns a human-readable expiry string with days remaining/since.
func formatExpiry(exp int64) string {
	if exp == 0 {
		return "none"
	}
	t := time.Unix(exp, 0)
	remaining := time.Until(t)
	if remaining > 0 {
		return fmt.Sprintf("%s (%d days remaining)", t.Format("2006-01-02"), int(remaining.Hours()/24))
	}
	return fmt.Sprintf("%s (expired %d days ago)", t.Format("2006-01-02"), int(-remaining.Hours()/24))
}

// --- sukko license set ---

var licenseSetCmd = &cobra.Command{
	Use:   "set [key]",
	Short: "Store a license key in the active context",
	Long: `Store a Sukko license key in the CLI context. The key is encrypted at rest.

If no key argument is provided, you will be prompted for input to avoid
the key appearing in shell history.`,
	RunE: runLicenseSet,
}

func runLicenseSet(cmd *cobra.Command, args []string) error {
	var key string
	if len(args) > 0 {
		key = args[0]
	} else {
		// Prompt to avoid shell history
		fmt.Fprint(cmd.OutOrStdout(), "License key: ")
		if _, err := fmt.Scanln(&key); err != nil {
			return fmt.Errorf("read license key: %w", err)
		}
	}

	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("license key is required")
	}

	// Validate format
	claims, err := decodeLicenseClaims(key)
	if err != nil {
		return fmt.Errorf("validate license key: %w", err)
	}

	// Warn if expired
	if claims.Exp > 0 && time.Unix(claims.Exp, 0).Before(time.Now()) {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"Warning: this license appears to be expired (%s). The platform will run as Community edition.\n",
			time.Unix(claims.Exp, 0).Format("2006-01-02"))
	}

	// Store encrypted in context
	store, ctx, err := requireActiveContext()
	if err != nil {
		return err
	}

	enc, err := store.EncryptSecret(key)
	if err != nil {
		return fmt.Errorf("encrypt license key: %w", err)
	}

	ctx.LicenseKeyEnc = enc
	if err := store.Add(ctx); err != nil {
		return fmt.Errorf("save context: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "License stored. Edition: %s, Org: %s, Expires: %s\n",
		capitalizeEdition(claims.Edition), claims.Org, formatExpiry(claims.Exp))

	return nil
}

// --- sukko license show ---

var licenseShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Display the stored license key and decoded claims",
	RunE:  runLicenseShow,
}

func runLicenseShow(cmd *cobra.Command, _ []string) error {
	store, ctx, err := requireActiveContext()
	if err != nil {
		return err
	}

	key, err := ctx.LicenseKey(store.Key())
	if err != nil {
		return fmt.Errorf("decrypt license key: %w", err)
	}
	if key == "" {
		fmt.Fprintln(cmd.OutOrStdout(), "No license key stored in the active context.")
		return nil
	}

	// Mask key
	masked := maskKey(key)
	fmt.Fprintf(cmd.OutOrStdout(), "Key:         %s\n", masked)

	claims, err := decodeLicenseClaims(key)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "Warning: could not decode claims: %v\n", err)
		return nil
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Edition:     %s\n", capitalizeEdition(claims.Edition))
	if claims.Org != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Org:         %s\n", claims.Org)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Expires:     %s\n", formatExpiry(claims.Exp))

	return nil
}

func maskKey(key string) string {
	if len(key) <= 12 {
		return "***"
	}
	return key[:8] + "..." + key[len(key)-4:]
}

// --- sukko license remove ---

var licenseRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove the stored license key from the active context",
	RunE:  runLicenseRemove,
}

func runLicenseRemove(cmd *cobra.Command, _ []string) error {
	store, ctx, err := requireActiveContext()
	if err != nil {
		return err
	}

	if ctx.LicenseKeyEnc == "" {
		fmt.Fprintln(cmd.OutOrStdout(), "No license key stored.")
		return nil
	}

	ctx.LicenseKeyEnc = ""
	if err := store.Add(ctx); err != nil {
		return fmt.Errorf("save context: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "License key removed from context %q.\n", ctx.Name)
	return nil
}

// --- sukko license push ---

var licensePushCmd = &cobra.Command{
	Use:   "push [key]",
	Short: "Push a license key to a running deployment",
	Long: `Push a Sukko license key to a running provisioning service via
POST /api/v1/license. The license is applied immediately without
restarting services.

If no key argument is provided, you will be prompted for input to avoid
the key appearing in shell history.

This command does NOT modify the local context — use 'sukko license set'
to persist the key for future 'sukko up' invocations.`,
	RunE: runLicensePush,
}

func runLicensePush(cmd *cobra.Command, args []string) error {
	// 1. Get key from args or prompt (matching license set pattern)
	var key string
	if len(args) > 0 {
		key = args[0]
	} else {
		fmt.Fprint(cmd.OutOrStdout(), "License key: ")
		if _, err := fmt.Scanln(&key); err != nil {
			return fmt.Errorf("read license key: %w", err)
		}
	}

	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("license key is required")
	}

	// 2. Local format validation
	claims, err := decodeLicenseClaims(key)
	if err != nil {
		return fmt.Errorf("validate license key: %w", err)
	}

	// 3. Warn if expired (don't fail — server is authoritative)
	if claims.Exp > 0 && time.Unix(claims.Exp, 0).Before(time.Now()) {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"Warning: this license appears to be expired (%s). The server's validation is authoritative.\n",
			time.Unix(claims.Exp, 0).Format("2006-01-02"))
	}

	// 4. Create admin-authenticated client (fails fast if no keypair)
	c, err := newClient()
	if err != nil {
		return err
	}

	// 5. Capture pre-push edition for transition detection
	prevEdition, err := c.GetEdition(cmd.Context())
	if err != nil {
		// Non-fatal — can't detect transitions but can still push
		prevEdition = nil
	}

	// 6. Push license to provisioning
	resp, err := c.PushLicense(cmd.Context(), key)
	if err != nil {
		if errors.Is(err, client.ErrAPIUnauthorized) {
			return errors.New("admin authentication failed — ensure your admin key is registered via 'sukko auth register' or ADMIN_BOOTSTRAP_KEY")
		}
		if errors.Is(err, client.ErrAPIRateLimited) {
			return fmt.Errorf("license endpoint is rate-limited — %w", err)
		}
		return fmt.Errorf("push license: %w", err)
	}

	// 7. Report success
	fmt.Fprintf(cmd.OutOrStdout(), "License applied. Edition: %s, Org: %s, Expires: %s\n",
		capitalizeEdition(resp.Edition), resp.Org, formatExpiry(parseExpiry(resp.ExpiresAt)))

	// 8. Compose orchestration — local contexts only. Push-service is
	// Pro+ (Web Push); FCM/APNs delivery additionally requires Enterprise.
	if resolvedCtx == nil || resolvedCtx.Type != "local" {
		if editionSupportsPush(resp.Edition) {
			fmt.Fprintln(cmd.OutOrStdout(), "To start push-service in K8s: helm upgrade --set push-service.enabled=true")
		}
		return nil
	}

	// Detect edition transition
	prevEditionStr := ""
	if prevEdition != nil {
		prevEditionStr = prevEdition.Edition
	}

	action := pushAction(prevEditionStr, resp.Edition)
	if action == pushNone {
		return nil
	}

	// Starting (or ensuring) push-service requires a kafka-family backend for
	// the push-service container. The license is already applied at this point
	// and push is only one of many Pro/Enterprise features, so an incompatible
	// backend is a note, not a failure. Stopping needs no backend check.
	if action != pushStop {
		projCfg, err := loadProjectConfig()
		switch {
		case err != nil:
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: push-service not started — load project config: %v\n", err)
			return nil
		case projCfg == nil:
			fmt.Fprintln(cmd.ErrOrStderr(), "Note: push-service not started — no project config found. Run 'sukko init' if you want push notifications locally.")
			return nil
		case !isKafkaFamilyBackend(projCfg.MessageBackend):
			fmt.Fprintf(cmd.ErrOrStderr(), "Note: push-service not started — it requires the kafka or redpanda message backend (current: %s). Run 'sukko init' to reconfigure, then 'sukko up', if you want push notifications locally.\n", projCfg.MessageBackend)
			return nil
		}
	}

	// Write compose file and create manager once — reused across all branches below
	if err := compose.WriteComposeFile(composeFilePath()); err != nil {
		return fmt.Errorf("write compose file: %w", err)
	}
	mgr, err := compose.NewManager(".", composeFilePath())
	if err != nil {
		return fmt.Errorf("create compose manager: %w", err)
	}

	switch action {
	case pushStart:
		// Newly push-capable — start push-service
		fmt.Fprintln(cmd.OutOrStdout(), "\nStarting push-service (Web Push, Pro+)...")
		if err := mgr.StartService(cmd.Context(), cmd.OutOrStdout(), "push-service", pushComposeProfiles, pushServiceTimeout); err != nil {
			return fmt.Errorf("start push-service: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Push-service is healthy.")
		// The gateway registers its push routes only when GATEWAY_PUSH_ENABLED=true
		// at ITS start (sukko#244). On a not-push→push transition the running
		// gateway was booted without it, and recreating it from here would need the
		// full `up` environment — so point at the safe path instead.
		fmt.Fprintln(cmd.OutOrStdout(), "Note: the gateway's push routes (subscribe/vapid-key) enable on the next 'sukko up' — the running gateway was started without its push surface.")
	case pushEnsure:
		// Idempotent — still push-capable
		if pushServiceHealthy(cmd.Context(), mgr) {
			fmt.Fprintln(cmd.OutOrStdout(), "Push-service already running and healthy.")
		} else {
			// Push-service unhealthy/absent — retry start
			fmt.Fprintln(cmd.OutOrStdout(), "\nRestarting push-service...")
			if err := mgr.StartService(cmd.Context(), cmd.OutOrStdout(), "push-service", pushComposeProfiles, pushServiceTimeout); err != nil {
				return fmt.Errorf("start push-service: %w", err)
			}
		}
	case pushStop:
		// Downgrade below Pro
		fmt.Fprintln(cmd.OutOrStdout(), "\nStopping push-service (edition below Pro)...")
		if err := mgr.StopService(cmd.Context(), "push-service"); err != nil {
			return fmt.Errorf("stop push-service: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Push-service stopped.")
		// The gateway's push routes stay registered until its next recreate; with
		// the service gone they answer 503, which is harmless — the next 'sukko up'
		// boots the gateway without the push surface (404s, per sukko#244).
		fmt.Fprintln(cmd.OutOrStdout(), "Note: the gateway's push routes disable on the next 'sukko up'.")
	default:
		// pushNone — unreachable: it returned above, before the compose file
		// write. Present only to satisfy switch exhaustiveness.
	}

	return nil
}

// parseExpiry converts an RFC3339 expiry string to a Unix timestamp.
// Returns 0 if parsing fails (formatExpiry handles 0 as "none").
func parseExpiry(s string) int64 {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return 0
	}
	return t.Unix()
}

// requireActiveContext returns the store and active context, or an error.
func requireActiveContext() (*clicontext.Store, *clicontext.Context, error) {
	if resolvedStore == nil || resolvedCtx == nil {
		return nil, nil, errors.New("no active context — run 'sukko init' first")
	}
	return resolvedStore, resolvedCtx, nil
}
