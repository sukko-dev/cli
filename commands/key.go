package commands

import (
	"cmp"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/spf13/cobra"
)

var keysGenerate bool

func init() {
	rootCmd.AddCommand(keysCmd)
	keysCmd.AddCommand(keysCreateCmd, keysListCmd, keysRevokeCmd)
	markMutating(keysCreateCmd, keysRevokeCmd)

	// create flags
	keysCreateCmd.Flags().String("tenant", "", "Tenant ID (uses active tenant from context if not set)")
	keysCreateCmd.Flags().String("algorithm", "", "Signing algorithm: ES256, RS256, EdDSA")
	keysCreateCmd.Flags().String("public-key-file", "", "Path to PEM public key file")
	keysCreateCmd.Flags().String("key-id", "", "Key ID (optional, auto-generated if not set)")
	keysCreateCmd.Flags().String("expires-at", "", "Expiration time (RFC3339)")
	keysCreateCmd.Flags().BoolVar(&keysGenerate, "generate", false, "Generate ES256 key pair, register public key, save private key locally")

	// list flags
	keysListCmd.Flags().String("tenant", "", "Tenant ID (uses active tenant from context if not set)")

	// revoke flags
	keysRevokeCmd.Flags().String("tenant", "", "Tenant ID (uses active tenant from context if not set)")
	keysRevokeCmd.Flags().String("key-id", "", "Key ID (required)")
	_ = keysRevokeCmd.MarkFlagRequired("key-id")
}

var keysCmd = &cobra.Command{
	Use:   "keys",
	Short: "Manage JWT signing keys",
}

var keysCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Register a new public key or generate a key pair",
	RunE: func(cmd *cobra.Command, _ []string) error {
		tenantFlag, _ := cmd.Flags().GetString("tenant")
		tenantID := resolveTenant(tenantFlag)
		if tenantID == "" {
			return errors.New("tenant ID required (use --tenant or set active tenant in context)")
		}

		if keysGenerate {
			return runKeysGenerate(cmd, tenantID)
		}

		// Manual mode — require algorithm and public-key-file
		algorithm, _ := cmd.Flags().GetString("algorithm")
		pubKeyFile, _ := cmd.Flags().GetString("public-key-file")

		if algorithm == "" {
			return errors.New("--algorithm is required (or use --generate for ES256 key pair)")
		}
		if pubKeyFile == "" {
			return errors.New("--public-key-file is required (or use --generate for ES256 key pair)")
		}

		keyID, _ := cmd.Flags().GetString("key-id")
		expiresAt, _ := cmd.Flags().GetString("expires-at")

		pubKey, err := os.ReadFile(pubKeyFile) //nolint:gosec // G304: CLI reads user-specified file path from --public-key-file flag
		if err != nil {
			return fmt.Errorf("read public key file: %w", err)
		}

		req, _, err := buildCreateKeyRequest(algorithm, string(pubKey), keyID, expiresAt)
		if err != nil {
			return err
		}

		c, err := newClient()
		if err != nil {
			return err
		}
		result, err := c.CreateKey(cmd.Context(), tenantID, req)
		if err != nil {
			return fmt.Errorf("create key: %w", err)
		}
		return printOutput(result, output)
	},
}

func runKeysGenerate(cmd *cobra.Command, tenantID string) error {
	// Generate ES256 key pair
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate key pair: %w", err)
	}

	// Marshal public key to PEM
	pubBytes, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		return fmt.Errorf("marshal public key: %w", err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubBytes})

	// Register public key with provisioning. The CLI owns the key_id — the server requires
	// it and uses it as the globally-unique JWT kid — defaulting to a generated one when
	// --key-id is omitted.
	keyIDFlag, _ := cmd.Flags().GetString("key-id")
	req, keyID, err := buildCreateKeyRequest("ES256", string(pubPEM), keyIDFlag, "")
	if err != nil {
		return err
	}

	c, err := newClient()
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}
	if _, err := c.CreateKey(cmd.Context(), tenantID, req); err != nil {
		return fmt.Errorf("register public key: %w", err)
	}

	// Marshal private key to PEM
	privBytes, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		return fmt.Errorf("marshal private key: %w", err)
	}
	privPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privBytes})

	// Save the private key named after the key_id we registered, so `token generate`
	// auto-discovers it and derives the matching kid from the filename.
	if err := savePrivateKey(tenantID, keyID, privPEM); err != nil {
		return fmt.Errorf("save private key: %w", err)
	}

	path, _ := keysDir(tenantID)
	fmt.Fprintf(cmd.OutOrStdout(), "Key pair generated.\n")
	fmt.Fprintf(cmd.OutOrStdout(), "  Public key registered as %q for tenant %q\n", keyID, tenantID)
	fmt.Fprintf(cmd.OutOrStdout(), "  Private key saved to %s/%s.pem\n", path, keyID)
	fmt.Fprintln(cmd.OutOrStdout(), "\nGenerate a token with:")
	fmt.Fprintf(cmd.OutOrStdout(), "  sukko token generate --tenant %s --sub <user-id>\n", tenantID)

	return nil
}

// generateKeyID mints a default signing-key id when the operator supplies none. It
// satisfies the provisioning CreateKeyRequest contract — 3-63 chars, starting with a
// lowercase letter, lowercase alphanumerics or hyphens — as `key-` plus 16 hex characters
// (8 random bytes = 64 bits, so collisions are negligible for a globally-unique id).
func generateKeyID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return "key-" + hex.EncodeToString(b), nil
}

// buildCreateKeyRequest assembles the provisioning CreateKey body and returns it alongside
// the effective key_id. key_id is required by the server (it is the globally-unique JWT
// kid), so an empty keyID is defaulted to a generated one rather than omitted. Returning
// the id lets the caller name the saved private key after it without relying on the server
// echoing it back. Extracted so key_test.go can assert the schema without a network call.
func buildCreateKeyRequest(algorithm, publicKeyPEM, keyID, expiresAt string) (body map[string]any, effectiveKeyID string, err error) {
	if keyID == "" {
		if keyID, err = generateKeyID(); err != nil {
			return nil, "", err
		}
	}
	req := map[string]any{
		"algorithm":  algorithm,
		"public_key": publicKeyPEM,
		"key_id":     keyID,
	}
	if expiresAt != "" {
		req["expires_at"] = expiresAt
	}
	return req, keyID, nil
}

// --- Key storage helpers ---

// keysDir returns the directory for stored private keys.
func keysDir(tenant string) (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("user config dir: %w", err)
	}
	return filepath.Join(configDir, "sukko", "keys", tenant), nil
}

// savePrivateKey saves a PEM-encoded private key for a tenant/key-id.
func savePrivateKey(tenant, keyID string, keyPEM []byte) error {
	dir, err := keysDir(tenant)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create keys dir: %w", err)
	}
	path := filepath.Join(dir, keyID+".pem")
	if err := os.WriteFile(path, keyPEM, 0o600); err != nil {
		return fmt.Errorf("write private key: %w", err)
	}
	return nil
}

// loadLatestPrivateKey finds the most recent private key for a tenant.
// Returns the key file path or empty string if none found.
func loadLatestPrivateKey(tenant string) (string, error) {
	dir, err := keysDir(tenant)
	if err != nil {
		return "", err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil // no keys directory — not an error
		}
		return "", fmt.Errorf("read keys dir: %w", err)
	}

	// Filter .pem files and sort by mod time (newest first)
	type keyFile struct {
		path    string
		modTime int64
	}
	var keys []keyFile
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".pem" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue // best-effort: skip unreadable files
		}
		keys = append(keys, keyFile{
			path:    filepath.Join(dir, entry.Name()),
			modTime: info.ModTime().UnixNano(),
		})
	}

	if len(keys) == 0 {
		return "", nil
	}

	slices.SortFunc(keys, func(a, b keyFile) int {
		return cmp.Compare(b.modTime, a.modTime) // descending: newest first
	})

	return keys[0].path, nil
}

var keysListCmd = &cobra.Command{
	Use:   "list",
	Short: "List keys for a tenant",
	RunE: func(cmd *cobra.Command, _ []string) error {
		tenantFlag, _ := cmd.Flags().GetString("tenant")
		tenantID := resolveTenant(tenantFlag)
		if tenantID == "" {
			return errors.New("tenant ID required (use --tenant or set active tenant in context)")
		}

		c, err := newClient()
		if err != nil {
			return err
		}
		result, err := c.ListKeys(cmd.Context(), tenantID)
		if err != nil {
			return fmt.Errorf("list keys: %w", err)
		}
		return printOutput(result, output)
	},
}

var keysRevokeCmd = &cobra.Command{
	Use:   "revoke",
	Short: "Revoke a key",
	RunE: func(cmd *cobra.Command, _ []string) error {
		tenantFlag, _ := cmd.Flags().GetString("tenant")
		tenantID := resolveTenant(tenantFlag)
		if tenantID == "" {
			return errors.New("tenant ID required (use --tenant or set active tenant in context)")
		}
		keyID, _ := cmd.Flags().GetString("key-id")

		c, err := newClient()
		if err != nil {
			return err
		}
		result, err := c.RevokeKey(cmd.Context(), tenantID, keyID)
		if err != nil {
			return fmt.Errorf("revoke key: %w", err)
		}
		return printOutput(result, output)
	},
}
