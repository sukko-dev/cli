package context

import "fmt"

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
