package token

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTestKeyPair(t *testing.T) (privPath, pubPath string) {
	t.Helper()
	dir := t.TempDir()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	// Write private key
	privBytes, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	privPath = filepath.Join(dir, "private.pem")
	privPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privBytes})
	if err := os.WriteFile(privPath, privPEM, 0o600); err != nil {
		t.Fatalf("write private key: %v", err)
	}

	// Write public key
	pubBytes, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	pubPath = filepath.Join(dir, "public.pem")
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubBytes})
	if err := os.WriteFile(pubPath, pubPEM, 0o644); err != nil {
		t.Fatalf("write public key: %v", err)
	}

	return privPath, pubPath
}

func TestGenerateES256(t *testing.T) {
	t.Parallel()

	privPath, _ := writeTestKeyPair(t)

	tests := []struct {
		name    string
		cfg     GenerateConfig
		wantSub string
	}{
		{
			name: "basic ES256 token",
			cfg: GenerateConfig{
				Subject:   "user123",
				TenantID:  "acme",
				Algorithm: "ES256",
				KeyID:     "test-key",
				KeyFile:   privPath,
				TTL:       time.Hour,
			},
			wantSub: "user123",
		},
		{
			name: "with roles and groups",
			cfg: GenerateConfig{
				Subject:   "admin",
				TenantID:  "acme",
				Roles:     []string{"admin", "editor"},
				Groups:    []string{"team-a"},
				Algorithm: "ES256",
				KeyID:     "test-key",
				KeyFile:   privPath,
			},
			wantSub: "admin",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tokenStr, _, err := Generate(tt.cfg)
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}

			if tokenStr == "" {
				t.Fatal("empty token")
			}

			decoded, err := Decode(tokenStr)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}

			if !decoded.Valid {
				t.Errorf("expected valid token, got error: %s", decoded.Error)
			}

			if sub, ok := decoded.Claims["sub"]; ok {
				if sub != tt.wantSub {
					t.Errorf("sub = %v, want %v", sub, tt.wantSub)
				}
			} else if tt.wantSub != "" {
				t.Error("expected sub claim")
			}
		})
	}
}

func TestValidateWithKeyFile(t *testing.T) {
	t.Parallel()

	privPath, pubPath := writeTestKeyPair(t)

	tokenStr, _, err := Generate(GenerateConfig{
		Subject:   "user",
		TenantID:  "acme",
		Algorithm: "ES256",
		KeyID:     "test-key",
		KeyFile:   privPath,
		TTL:       time.Hour,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	result, err := ValidateWithKeyFile(tokenStr, pubPath)
	if err != nil {
		t.Fatalf("ValidateWithKeyFile: %v", err)
	}

	if !result.Valid {
		t.Errorf("expected valid, got error: %s", result.Error)
	}
}

func TestValidateWithKeyFile_Tampered(t *testing.T) {
	t.Parallel()

	privPath, pubPath := writeTestKeyPair(t)

	tokenStr, _, err := Generate(GenerateConfig{
		Subject:   "user",
		Algorithm: "ES256",
		KeyID:     "test-key",
		KeyFile:   privPath,
		TTL:       time.Hour,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Tamper with the token
	tampered := tokenStr[:len(tokenStr)-5] + "XXXXX"

	result, err := ValidateWithKeyFile(tampered, pubPath)
	if err != nil {
		t.Fatalf("ValidateWithKeyFile: %v", err)
	}

	if result.Valid {
		t.Error("expected invalid for tampered token")
	}
}

func TestDecodeExpiredToken(t *testing.T) {
	t.Parallel()

	privPath, _ := writeTestKeyPair(t)

	tokenStr, _, err := Generate(GenerateConfig{
		Subject:   "user",
		Algorithm: "ES256",
		KeyID:     "test-key",
		KeyFile:   privPath,
		TTL:       -time.Hour, // already expired
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	decoded, err := Decode(tokenStr)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if decoded.Valid {
		t.Error("expected expired token to be invalid")
	}
	if decoded.Error != "token expired" {
		t.Errorf("error = %q, want 'token expired'", decoded.Error)
	}
}

func TestGenerateNoAlgorithm(t *testing.T) {
	t.Parallel()

	_, _, err := Generate(GenerateConfig{
		Subject: "user",
	})
	if err == nil {
		t.Error("expected error without algorithm")
	}
}

func TestGenerateNoKeyFile(t *testing.T) {
	t.Parallel()

	_, _, err := Generate(GenerateConfig{
		Subject:   "user",
		Algorithm: "ES256",
		KeyID:     "test-key",
	})
	if err == nil {
		t.Error("expected error without key file")
	}
}

func TestGenerateUnsupportedAlgorithm(t *testing.T) {
	t.Parallel()

	privPath, _ := writeTestKeyPair(t)

	_, _, err := Generate(GenerateConfig{
		Subject:   "user",
		Algorithm: "HS256",
		KeyFile:   privPath,
	})
	if err == nil {
		t.Error("expected error for HS256 (removed)")
	}
}

func TestGenerate_IncludesJTI(t *testing.T) {
	t.Parallel()

	privPath, _ := writeTestKeyPair(t)

	tokenStr, jti, err := Generate(GenerateConfig{
		Subject:   "user",
		TenantID:  "acme",
		Algorithm: "ES256",
		KeyID:     "test-key",
		KeyFile:   privPath,
		TTL:       time.Hour,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if jti == "" {
		t.Fatal("expected non-empty jti")
	}

	decoded, err := Decode(tokenStr)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	claimJTI, _ := decoded.Claims["jti"].(string)
	if claimJTI == "" {
		t.Fatal("expected jti claim in token")
	}
}

func TestGenerate_ReturnsJTI(t *testing.T) {
	t.Parallel()

	privPath, _ := writeTestKeyPair(t)

	tokenStr, jti, err := Generate(GenerateConfig{
		Subject:   "user",
		Algorithm: "ES256",
		KeyID:     "test-key",
		KeyFile:   privPath,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	decoded, err := Decode(tokenStr)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	claimJTI, _ := decoded.Claims["jti"].(string)
	if claimJTI != jti {
		t.Errorf("returned jti %q does not match claim jti %q", jti, claimJTI)
	}
}

// TestGenerate_SetsKidHeader pins the kid header. The Sukko gateway resolves a tenant's
// signing key by the token's `kid` and rejects a token without one ("missing kid header"),
// so a generated token that omits it is unusable against every deployment — the header is
// part of the contract, not an optional extra.
func TestGenerate_SetsKidHeader(t *testing.T) {
	t.Parallel()
	privPath, _ := writeTestKeyPair(t)

	tokenStr, _, err := Generate(GenerateConfig{
		Subject:   "user-1",
		TenantID:  "acme",
		KeyFile:   privPath,
		Algorithm: "ES256",
		KeyID:     "signing-key-1",
		TTL:       time.Hour,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	decoded, err := Decode(tokenStr)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	kid, ok := decoded.Header["kid"].(string)
	if !ok {
		t.Fatalf("header has no string kid; header = %v", decoded.Header)
	}
	if kid != "signing-key-1" {
		t.Errorf("kid = %q, want %q", kid, "signing-key-1")
	}
}

// TestGenerate_RequiresKeyID pins the loud failure. Emitting a kid-less token silently
// produces credentials that fail only later, at the gateway, with an error that points at
// the token rather than at the command that made it (§III: no silent failures).
func TestGenerate_RequiresKeyID(t *testing.T) {
	t.Parallel()
	privPath, _ := writeTestKeyPair(t)

	_, _, err := Generate(GenerateConfig{
		Subject:   "user-1",
		TenantID:  "acme",
		KeyFile:   privPath,
		Algorithm: "ES256",
		// KeyID deliberately omitted.
	})
	if err == nil {
		t.Fatal("Generate() with no KeyID returned nil error; want a key-id required error")
	}
	if !strings.Contains(err.Error(), "key id") {
		t.Errorf("error = %q, want it to name the missing key id", err)
	}
}
