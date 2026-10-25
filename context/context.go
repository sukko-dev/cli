package context

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// Context type values. Type is the single authoritative signal for whether a context
// operates the local compose stack or a remote one — see IsLocal. "local" is created by
// 'sukko init'; "remote" by 'sukko context add'/'create'.
const (
	TypeLocal  = "local"
	TypeRemote = "remote"
)

// Context represents a named environment configuration for the CLI.
type Context struct {
	Name            string `json:"name"`
	Type            string `json:"type,omitempty"` // "local" (compose-managed) or "remote" (K8s/staging/prod)
	GatewayURL      string `json:"gateway_url"`
	ProvisioningURL string `json:"provisioning_url"`
	TesterURL       string `json:"tester_url,omitempty"`
	AdminTokenEnc   string `json:"admin_token_encrypted,omitempty"`
	APIKeyEnc       string `json:"api_key_encrypted,omitempty"`
	LicenseKeyEnc   string `json:"license_key_encrypted,omitempty"`
	Environment     string `json:"environment,omitempty"`
	ActiveTenant    string `json:"active_tenant,omitempty"`
}

// IsLocal reports whether this context operates the local compose stack. It is the
// single authoritative local-vs-remote signal: it keys on the explicit Type, never on a
// URL heuristic, so a context's scope cannot silently disagree with what it was created
// as. A nil context, or any Type other than "local" (including an empty/legacy Type), is
// treated as non-local — the safe default for the banner, HTTPS, and confirmation gates.
func (c *Context) IsLocal() bool {
	return c != nil && c.Type == TypeLocal
}

// IsLoopback reports whether rawURL's host is a loopback address — localhost, a 127.0.0.0/8
// address, or ::1. A loopback endpoint is reachable from the CLI's own host but not from a
// remote tester container, and plaintext over loopback does not cross the network; both
// gaps (tester passthrough, remote TLS enforcement) need this distinction. A URL that does
// not parse, or has no host, is reported as non-loopback.
func IsLoopback(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	// "localhost" and, per RFC 6761, any name ending in ".localhost" resolve to loopback.
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// AdminToken decrypts and returns the admin token.
func (c *Context) AdminToken(key []byte) (string, error) {
	if c.AdminTokenEnc == "" {
		return "", nil
	}
	plaintext, err := Decrypt(key, c.AdminTokenEnc)
	if err != nil {
		return "", fmt.Errorf("decrypt admin token: %w", err)
	}
	return plaintext, nil
}

// LicenseKey decrypts and returns the license key.
func (c *Context) LicenseKey(key []byte) (string, error) {
	if c.LicenseKeyEnc == "" {
		return "", nil
	}
	plaintext, err := Decrypt(key, c.LicenseKeyEnc)
	if err != nil {
		return "", fmt.Errorf("decrypt license key: %w", err)
	}
	return plaintext, nil
}

// APIKey decrypts and returns the API key.
func (c *Context) APIKey(key []byte) (string, error) {
	if c.APIKeyEnc == "" {
		return "", nil
	}
	plaintext, err := Decrypt(key, c.APIKeyEnc)
	if err != nil {
		return "", fmt.Errorf("decrypt api key: %w", err)
	}
	return plaintext, nil
}
