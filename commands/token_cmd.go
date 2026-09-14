package commands

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	clitoken "github.com/sukko-dev/cli/token"
)

var (
	revokeJTI     string
	revokeSub     string
	revokeToken   string
	revokeTenant  string
	revokeExpires string
)

var (
	tokenSub       string
	tokenKeyID     string
	tokenTenant    string
	tokenRoles     []string
	tokenGroups    []string
	tokenScopes    []string
	tokenTTL       time.Duration
	tokenKeyFile   string
	tokenAlgorithm string
)

func init() {
	tokenCmd.AddCommand(tokenGenerateCmd, tokenValidateCmd, tokenRevokeCmd)

	tokenRevokeCmd.Flags().StringVar(&revokeJTI, "jti", "", "Revoke a specific session by JWT ID")
	tokenRevokeCmd.Flags().StringVar(&revokeSub, "sub", "", "Revoke all sessions for a user")
	tokenRevokeCmd.Flags().StringVar(&revokeToken, "token", "", "JWT to revoke (extracts jti and tenant_id)")
	tokenRevokeCmd.Flags().StringVar(&revokeTenant, "tenant", "", "Tenant ID")
	tokenRevokeCmd.Flags().StringVar(&revokeExpires, "expires", "", "Revocation expiry (duration like 2h or RFC3339 timestamp)")

	tokenGenerateCmd.Flags().StringVar(&tokenSub, "sub", "", "Subject (user ID)")
	tokenGenerateCmd.Flags().StringVar(&tokenTenant, "tenant", "", "Tenant ID")
	tokenGenerateCmd.Flags().StringSliceVar(&tokenRoles, "roles", nil, "Roles (repeatable)")
	tokenGenerateCmd.Flags().StringSliceVar(&tokenGroups, "groups", nil, "Groups (repeatable)")
	tokenGenerateCmd.Flags().StringSliceVar(&tokenScopes, "scopes", nil, "Scopes (repeatable)")
	tokenGenerateCmd.Flags().DurationVar(&tokenTTL, "ttl", time.Hour, "Token time-to-live (e.g., 1h, 30m, 24h)")
	tokenGenerateCmd.Flags().StringVar(&tokenKeyFile, "key-file", "", "Path to PEM private key")
	tokenGenerateCmd.Flags().StringVar(&tokenAlgorithm, "algorithm", "", "Signing algorithm (ES256, RS256, EdDSA)")
	tokenGenerateCmd.Flags().StringVar(&tokenKeyID, "key-id", "", "Registered signing-key id, emitted as the JWT kid header (defaults to the --key-file basename, which is how 'keys create' names stored keys)")

	tokenValidateCmd.Flags().StringVar(&tokenKeyFile, "key-file", "", "Path to PEM public key (for signature verification)")

	rootCmd.AddCommand(tokenCmd)
}

var tokenCmd = &cobra.Command{
	Use:   "token",
	Short: "JWT token helpers",
}

var tokenGenerateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generate a signed JWT token",
	RunE: func(cmd *cobra.Command, _ []string) error {
		algorithm := tokenAlgorithm
		keyFile := tokenKeyFile

		// Auto-discover stored key if no --algorithm and no --key-file
		if algorithm == "" && keyFile == "" {
			tenant := resolveTenant(tokenTenant)
			if tenant != "" {
				if path, err := loadLatestPrivateKey(tenant); err == nil && path != "" {
					algorithm = "ES256"
					keyFile = path
				}
			}
		}

		if algorithm == "" || keyFile == "" {
			return errors.New("specify --algorithm (ES256, RS256, EdDSA) and --key-file, or run 'sukko keys create --generate' first. HS256 is not supported by the Sukko gateway")
		}

		validAlgorithms := map[string]bool{
			"ES256": true, "RS256": true, "EdDSA": true,
		}
		if !validAlgorithms[algorithm] {
			return fmt.Errorf("invalid algorithm %q: must be one of ES256, RS256, EdDSA. HS256 is not supported by the Sukko gateway", algorithm)
		}

		tenant := resolveTenant(tokenTenant)

		keyID := resolveKeyID(tokenKeyID, keyFile)

		cfg := clitoken.GenerateConfig{
			Subject:   tokenSub,
			TenantID:  tenant,
			Roles:     tokenRoles,
			Groups:    tokenGroups,
			Scopes:    tokenScopes,
			TTL:       tokenTTL,
			KeyFile:   keyFile,
			Algorithm: algorithm,
			KeyID:     keyID,
		}

		tokenStr, jti, err := clitoken.Generate(cfg)
		if err != nil {
			return fmt.Errorf("generate token: %w", err)
		}

		fmt.Fprintf(cmd.ErrOrStderr(), "jti: %s\n", jti)
		fmt.Fprintln(cmd.OutOrStdout(), tokenStr)
		return nil
	},
}

var tokenValidateCmd = &cobra.Command{
	Use:   "validate <token>",
	Short: "Decode and validate a JWT token",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		tokenStr := args[0]

		var result *clitoken.DecodedToken
		var err error
		verified := false
		if tokenKeyFile != "" {
			result, err = clitoken.ValidateWithKeyFile(tokenStr, tokenKeyFile)
			verified = true
		} else {
			result, err = clitoken.Decode(tokenStr)
		}
		if err != nil {
			return fmt.Errorf("validate token: %w", err)
		}

		if output == "json" {
			return printJSON(result)
		}

		// Human-readable output
		fmt.Fprintln(cmd.OutOrStdout(), "Header:")
		for k, v := range result.Header {
			fmt.Fprintf(cmd.OutOrStdout(), "  %s: %v\n", k, v)
		}

		fmt.Fprintln(cmd.OutOrStdout(), "\nClaims:")
		for k, v := range result.Claims {
			fmt.Fprintf(cmd.OutOrStdout(), "  %s: %v\n", k, v)
		}

		if result.Valid {
			fmt.Fprintf(cmd.OutOrStdout(), "\nStatus: %svalid%s", colorGreen, colorReset)
			if verified {
				fmt.Fprint(cmd.OutOrStdout(), " (signature verified)")
			} else {
				fmt.Fprint(cmd.OutOrStdout(), " (signature not verified — use --key-file to verify)")
			}
			fmt.Fprintln(cmd.OutOrStdout())
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "\nStatus: %sinvalid%s (%s)\n", colorRed, colorReset, result.Error)
		}

		return nil
	},
}

var tokenRevokeCmd = &cobra.Command{
	Use:   "revoke",
	Short: "Revoke a token by session (jti) or user (sub)",
	RunE:  runTokenRevoke,
}

func runTokenRevoke(cmd *cobra.Command, _ []string) error {
	// Step 1: validate mutual exclusivity
	modeCount := 0
	if revokeJTI != "" {
		modeCount++
	}
	if revokeSub != "" {
		modeCount++
	}
	if revokeToken != "" {
		modeCount++
	}
	if modeCount == 0 {
		return errors.New("one of --jti, --sub, or --token is required")
	}
	if modeCount > 1 {
		return errors.New("only one of --jti, --sub, or --token can be specified")
	}

	// Step 2: resolve mode and extract values
	var jti, sub, jwtTenantID string

	switch {
	case revokeJTI != "":
		jti = revokeJTI
	case revokeSub != "":
		sub = revokeSub
	case revokeToken != "":
		decoded, err := clitoken.Decode(revokeToken)
		if err != nil {
			return fmt.Errorf("failed to decode token: %w. Provide a valid JWT or use --jti directly", err)
		}
		if decoded.Claims == nil {
			return errors.New("failed to decode token: no claims found. Provide a valid JWT or use --jti directly")
		}

		jtiClaim, _ := decoded.Claims["jti"].(string)
		if jtiClaim == "" {
			return errors.New("token does not contain a jti claim — cannot revoke by token. Use --sub to revoke all sessions for this user instead")
		}
		jti = jtiClaim
		jwtTenantID, _ = decoded.Claims["tenant_id"].(string)
	}

	// Step 3: resolve tenant
	tenantID := revokeTenant
	if tenantID != "" && jwtTenantID != "" && tenantID != jwtTenantID {
		return fmt.Errorf("tenant conflict: --tenant %q does not match JWT tenant_id %q", tenantID, jwtTenantID)
	}
	if tenantID == "" {
		tenantID = jwtTenantID
	}
	if tenantID == "" {
		tenantID = resolveTenant("")
	}
	if tenantID == "" {
		return errors.New("tenant required: use --tenant, include tenant_id in the JWT, or set an active tenant in your context")
	}

	// Step 4: parse --expires
	var expiresAt string
	if revokeExpires != "" {
		var err error
		expiresAt, err = parseExpires(revokeExpires)
		if err != nil {
			return fmt.Errorf("parse expires: %w", err)
		}
	}

	// Step 5: build request body and call API
	body := map[string]any{}
	if jti != "" {
		body["jti"] = jti
	} else {
		body["sub"] = sub
	}
	if expiresAt != "" {
		body["exp"] = expiresAt
	}

	c, err := newClient()
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}

	result, err := c.RevokeToken(cmd.Context(), tenantID, body)
	if err != nil {
		return fmt.Errorf("revoke token: %w", err)
	}

	// Step 6: display result
	if output == "json" {
		return printJSON(result)
	}

	typ, _ := result["type"].(string)
	tenant, _ := result["tenant_id"].(string)
	expires, _ := result["expires_at"].(string)
	fmt.Fprintf(cmd.OutOrStdout(), "Revoked: type=%s tenant=%s expires=%s\n", typ, tenant, expires)
	return nil
}

// parseExpires parses a duration (e.g., "2h") or RFC3339 timestamp.
// Returns the absolute time as RFC3339 string.
func parseExpires(raw string) (string, error) {
	if d, err := time.ParseDuration(raw); err == nil {
		return time.Now().Add(d).Format(time.RFC3339), nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.Format(time.RFC3339), nil
	}
	return "", fmt.Errorf("invalid expires %q: must be a duration (e.g., 2h) or RFC3339 timestamp", raw)
}

// resolveKeyID picks the JWT `kid` for a generated token. The gateway finds a tenant's
// public key by `kid`, so every token needs one. An explicit --key-id always wins;
// otherwise fall back to the --key-file basename, because `keys create` stores a private
// key as <keyID>.pem — for store-managed keys (including the auto-discovered one) the
// basename IS the registered key id.
func resolveKeyID(explicit, keyFile string) string {
	if explicit != "" {
		return explicit
	}
	return strings.TrimSuffix(filepath.Base(keyFile), filepath.Ext(keyFile))
}
