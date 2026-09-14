// Package token provides JWT token generation and validation for the CLI.
package token

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const defaultTTL = time.Hour

// GenerateConfig configures token generation.
type GenerateConfig struct {
	Subject   string
	TenantID  string
	Roles     []string
	Groups    []string
	Scopes    []string
	TTL       time.Duration
	KeyFile   string // path to PEM private key
	Algorithm string // ES256, RS256, EdDSA

	// KeyID is the registered signing-key id, emitted as the JWT `kid` header.
	// REQUIRED: the gateway resolves the tenant's public key by `kid` and rejects
	// a token without one, so a kid-less token is unusable against any deployment.
	KeyID string
}

// DecodedToken represents a decoded (but not necessarily verified) JWT.
type DecodedToken struct {
	Header map[string]any `json:"header"`
	Claims map[string]any `json:"claims"`
	Valid  bool           `json:"valid"`
	Error  string         `json:"error,omitempty"`
}

// Generate creates a signed JWT token using an asymmetric private key.
// Returns the signed token string and the generated jti (JWT ID) for reference.
func Generate(cfg GenerateConfig) (tokenStr, jti string, err error) {
	if cfg.Algorithm == "" {
		return "", "", errors.New("algorithm is required (ES256, RS256, EdDSA)")
	}
	if cfg.KeyFile == "" {
		return "", "", errors.New("key file is required")
	}
	if cfg.KeyID == "" {
		return "", "", errors.New("key id is required: it becomes the JWT kid header, which the gateway uses to find the tenant's signing key")
	}
	if cfg.TTL == 0 {
		cfg.TTL = defaultTTL
	}

	jti = uuid.New().String()

	now := time.Now()
	claims := jwt.MapClaims{
		"jti": jti,
		"iat": now.Unix(),
		"exp": now.Add(cfg.TTL).Unix(),
	}

	if cfg.Subject != "" {
		claims["sub"] = cfg.Subject
	}
	if cfg.TenantID != "" {
		claims["tenant_id"] = cfg.TenantID
	}
	if len(cfg.Roles) > 0 {
		claims["roles"] = cfg.Roles
	}
	if len(cfg.Groups) > 0 {
		claims["groups"] = cfg.Groups
	}
	if len(cfg.Scopes) > 0 {
		claims["scopes"] = cfg.Scopes
	}

	method, err := signingMethod(cfg.Algorithm)
	if err != nil {
		return "", "", err
	}

	token := jwt.NewWithClaims(method, claims)
	token.Header["kid"] = cfg.KeyID

	key, err := loadPrivateKey(cfg.KeyFile)
	if err != nil {
		return "", "", err
	}

	signed, err := token.SignedString(key)
	if err != nil {
		return "", "", fmt.Errorf("sign token: %w", err)
	}

	return signed, jti, nil
}

// Decode decodes a JWT without verifying the signature.
func Decode(tokenString string) (*DecodedToken, error) {
	if tokenString == "" {
		return nil, errors.New("token string is required")
	}

	parser := jwt.NewParser()
	token, _, err := parser.ParseUnverified(tokenString, jwt.MapClaims{})
	if err != nil {
		return &DecodedToken{ //nolint:nilerr // parse error is returned as a field in DecodedToken, not as err
			Valid: false,
			Error: err.Error(),
		}, nil
	}

	claims, _ := token.Claims.(jwt.MapClaims)

	result := &DecodedToken{
		Header: token.Header,
		Claims: mapFromClaims(claims),
		Valid:  true,
	}

	// Check expiry
	if exp, ok := claims["exp"]; ok {
		if expFloat, ok := exp.(float64); ok {
			if time.Unix(int64(expFloat), 0).Before(time.Now()) {
				result.Valid = false
				result.Error = "token expired"
			}
		}
	}

	return result, nil
}

// ValidateWithKeyFile verifies a JWT using a PEM public key file.
// Determines the algorithm from the token header.
func ValidateWithKeyFile(tokenString, keyFilePath string) (*DecodedToken, error) {
	if tokenString == "" {
		return nil, errors.New("token string is required")
	}
	if keyFilePath == "" {
		return nil, errors.New("key file path is required")
	}

	pubKey, err := loadPublicKey(keyFilePath)
	if err != nil {
		return nil, fmt.Errorf("load public key: %w", err)
	}

	token, err := jwt.Parse(tokenString, func(t *jwt.Token) (any, error) {
		return pubKey, nil
	})

	result := &DecodedToken{
		Valid: err == nil && token.Valid,
	}

	if err != nil {
		result.Error = err.Error()
	}

	if token != nil {
		result.Header = token.Header
		if claims, ok := token.Claims.(jwt.MapClaims); ok {
			result.Claims = mapFromClaims(claims)
		}
	}

	return result, nil
}

func signingMethod(algorithm string) (jwt.SigningMethod, error) {
	switch algorithm {
	case "ES256":
		return jwt.SigningMethodES256, nil
	case "ES384":
		return jwt.SigningMethodES384, nil
	case "RS256":
		return jwt.SigningMethodRS256, nil
	case "RS384":
		return jwt.SigningMethodRS384, nil
	case "RS512":
		return jwt.SigningMethodRS512, nil
	case "EdDSA":
		return jwt.SigningMethodEdDSA, nil
	default:
		return nil, fmt.Errorf("unsupported algorithm: %s", algorithm)
	}
}

func loadPrivateKey(path string) (crypto.PrivateKey, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: CLI reads user-specified key file path
	if err != nil {
		return nil, fmt.Errorf("read key file: %w", err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found in %s", path)
	}

	// Try PKCS8 first, then EC, then RSA
	key, pkcs8Err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if pkcs8Err == nil {
		return key, nil
	}
	ecKey, ecErr := x509.ParseECPrivateKey(block.Bytes)
	if ecErr == nil {
		return ecKey, nil
	}
	rsaKey, rsaErr := x509.ParsePKCS1PrivateKey(block.Bytes)
	if rsaErr == nil {
		return rsaKey, nil
	}

	return nil, fmt.Errorf("unsupported key format in %s (pkcs8: %w, ec: %w, pkcs1: %w)", path, pkcs8Err, ecErr, rsaErr)
}

func loadPublicKey(path string) (crypto.PublicKey, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: CLI reads user-specified key file path
	if err != nil {
		return nil, fmt.Errorf("read public key file: %w", err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found in %s", path)
	}

	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse public key: %w", err)
	}

	return key, nil
}

func mapFromClaims(claims jwt.MapClaims) map[string]any {
	result := make(map[string]any, len(claims))
	for k, v := range claims {
		// Convert numeric types for clean JSON output
		if f, ok := v.(json.Number); ok {
			if i, err := f.Int64(); err == nil {
				result[k] = i
				continue
			}
		}
		result[k] = v
	}
	return result
}

// Exported key type checkers for referencing in validation code.
var (
	_ crypto.PrivateKey = (*ecdsa.PrivateKey)(nil)
	_ crypto.PrivateKey = (*rsa.PrivateKey)(nil)
	_ crypto.PrivateKey = ed25519.PrivateKey(nil)
)
