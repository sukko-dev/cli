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

// isInsecureURL reports whether rawURL would send data in cleartext across the network. It
// is an allow-list and fails closed: only https/wss, or http/ws to a loopback host, are
// secure. Anything else — a plaintext scheme to a non-loopback host, an unknown scheme, a
// URL that does not parse, or one with no host — is insecure. The decision is purely about
// the URL, never a context's Type, because a Type field is user-editable and must not be
// able to turn a plaintext remote into an allowed one.
func isInsecureURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return true // fail closed: cannot prove it is secure
	}
	switch strings.ToLower(u.Scheme) {
	case "https", "wss":
		return false
	case "http", "ws":
		return !IsLoopback(rawURL) // loopback plaintext stays on the box — fine
	default:
		return true // unknown scheme: fail closed
	}
}

// RequireSecureURL returns an error if rawURL would send credentials in cleartext — the
// enforcement used at each dial seam against the *effective* URL (after flag/env/context
// precedence), so a --api-url/SUKKO_* override to a plaintext remote is refused too, not
// just a stored context (Constitution §IX). Loopback plaintext is allowed.
func RequireSecureURL(rawURL string) error {
	if isInsecureURL(rawURL) {
		return fmt.Errorf("%q would send credentials in cleartext — use https/wss (reach a private endpoint over a loopback tunnel instead)", rawURL)
	}
	return nil
}

// ValidateURLSecurity rejects a context whose configured URLs would send admin tokens,
// signed JWTs, or API keys over plaintext to a non-loopback host (§IX). It validates by URL,
// not by Type — a correctly built local context is loopback by construction, and editing the
// Type field cannot make a plaintext remote acceptable (§II). Enforced at the persistence
// boundary (Store.Add); the dial seams enforce the same rule on the effective URL. Empty
// optional URLs are skipped.
func (c *Context) ValidateURLSecurity() error {
	for _, f := range []struct{ label, rawURL string }{
		{"provisioning URL", c.ProvisioningURL},
		{"gateway URL", c.GatewayURL},
		{"tester URL", c.TesterURL},
	} {
		if f.rawURL == "" {
			continue
		}
		if err := RequireSecureURL(f.rawURL); err != nil {
			return fmt.Errorf("%s %w", f.label, err)
		}
	}
	return nil
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
