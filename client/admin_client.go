// Package client provides a REST admin client for the provisioning API.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultClientTimeout is the default HTTP client timeout.
const DefaultClientTimeout = 30 * time.Second

// Sentinel errors for API responses.
var (
	ErrAPIBadRequest   = errors.New("API bad request")
	ErrAPIUnauthorized = errors.New("API unauthorized")
	ErrAPIForbidden    = errors.New("API forbidden")
	ErrAPINotFound     = errors.New("API not found")
	ErrAPIConflict     = errors.New("API conflict")
	ErrAPIRateLimited  = errors.New("API rate limited")
	ErrAPIInternal     = errors.New("API internal error")

	// Routing rule conflict errors.
	ErrDuplicateRoutingPattern  = errors.New("routing rule with this pattern already exists")
	ErrDuplicateRoutingPriority = errors.New("routing rule with this priority already exists")
)

// AdminClient communicates with the provisioning REST API.
type AdminClient struct {
	baseURL    string
	httpClient *http.Client
	signer     AuthSigner
}

// Config holds AdminClient configuration.
type Config struct {
	BaseURL string
	Timeout time.Duration
	Signer  AuthSigner
}

// New creates a new AdminClient.
func New(cfg Config) (*AdminClient, error) {
	if cfg.BaseURL == "" {
		return nil, errors.New("admin client: BaseURL is required")
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = DefaultClientTimeout
	}
	return &AdminClient{
		baseURL: cfg.BaseURL,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		signer: cfg.Signer,
	}, nil
}

// requireTenantID validates that a tenantID is not empty.
func requireTenantID(tenantID string) error {
	if tenantID == "" {
		return errors.New("tenantID is required")
	}
	return nil
}

// tenantPath builds a URL path for a tenant resource, escaping path components.
func tenantPath(tenantID string, subpath ...string) string {
	parts := make([]string, 0, 2+len(subpath))
	parts = append(parts, "/api/v1/tenants", url.PathEscape(tenantID))
	parts = append(parts, subpath...)
	return strings.Join(parts, "/")
}

// --- Tenants ---

// CreateTenant creates a new tenant via the provisioning API.
func (c *AdminClient) CreateTenant(ctx context.Context, req map[string]any) (map[string]any, error) {
	return c.doJSON(ctx, "POST", "/api/v1/tenants", req)
}

// GetTenant retrieves a tenant by ID.
func (c *AdminClient) GetTenant(ctx context.Context, tenantID string) (map[string]any, error) {
	if err := requireTenantID(tenantID); err != nil {
		return nil, err
	}
	return c.doJSON(ctx, "GET", tenantPath(tenantID), nil)
}

// ListTenants lists tenants with optional filter parameters.
func (c *AdminClient) ListTenants(ctx context.Context, params map[string]string) (map[string]any, error) {
	path := "/api/v1/tenants" + encodeParams(params)
	return c.doJSON(ctx, "GET", path, nil)
}

// UpdateTenant updates a tenant by ID.
func (c *AdminClient) UpdateTenant(ctx context.Context, tenantID string, req map[string]any) (map[string]any, error) {
	if err := requireTenantID(tenantID); err != nil {
		return nil, err
	}
	return c.doJSON(ctx, "PATCH", tenantPath(tenantID), req)
}

// SuspendTenant suspends a tenant by ID.
func (c *AdminClient) SuspendTenant(ctx context.Context, tenantID string) (map[string]any, error) {
	if err := requireTenantID(tenantID); err != nil {
		return nil, err
	}
	return c.doJSON(ctx, "POST", tenantPath(tenantID, "suspend"), nil)
}

// ReactivateTenant reactivates a suspended tenant.
func (c *AdminClient) ReactivateTenant(ctx context.Context, tenantID string) (map[string]any, error) {
	if err := requireTenantID(tenantID); err != nil {
		return nil, err
	}
	return c.doJSON(ctx, "POST", tenantPath(tenantID, "reactivate"), nil)
}

// DeprovisionTenant deprovisions a tenant by ID.
func (c *AdminClient) DeprovisionTenant(ctx context.Context, tenantID string) (map[string]any, error) {
	if err := requireTenantID(tenantID); err != nil {
		return nil, err
	}
	return c.doJSON(ctx, "DELETE", tenantPath(tenantID), nil)
}

// --- Keys ---

// CreateKey registers a new public key for a tenant.
func (c *AdminClient) CreateKey(ctx context.Context, tenantID string, req map[string]any) (map[string]any, error) {
	if err := requireTenantID(tenantID); err != nil {
		return nil, err
	}
	return c.doJSON(ctx, "POST", tenantPath(tenantID, "keys"), req)
}

// ListKeys lists all keys for a tenant.
func (c *AdminClient) ListKeys(ctx context.Context, tenantID string) (map[string]any, error) {
	if err := requireTenantID(tenantID); err != nil {
		return nil, err
	}
	return c.doJSON(ctx, "GET", tenantPath(tenantID, "keys"), nil)
}

// RevokeKey revokes a key by tenant and key ID.
func (c *AdminClient) RevokeKey(ctx context.Context, tenantID, keyID string) (map[string]any, error) {
	if tenantID == "" || keyID == "" {
		return nil, errors.New("tenantID and keyID are required")
	}
	return c.doJSON(ctx, "DELETE", tenantPath(tenantID, "keys", url.PathEscape(keyID)), nil)
}

// --- API Keys ---

// CreateAPIKey creates a new API key for a tenant.
func (c *AdminClient) CreateAPIKey(ctx context.Context, tenantID string, req map[string]any) (map[string]any, error) {
	if err := requireTenantID(tenantID); err != nil {
		return nil, err
	}
	return c.doJSON(ctx, "POST", tenantPath(tenantID, "api-keys"), req)
}

// ListAPIKeys lists all API keys for a tenant.
func (c *AdminClient) ListAPIKeys(ctx context.Context, tenantID string) (map[string]any, error) {
	if err := requireTenantID(tenantID); err != nil {
		return nil, err
	}
	return c.doJSON(ctx, "GET", tenantPath(tenantID, "api-keys"), nil)
}

// RevokeAPIKey revokes an API key by tenant and key ID.
func (c *AdminClient) RevokeAPIKey(ctx context.Context, tenantID, keyID string) (map[string]any, error) {
	if tenantID == "" || keyID == "" {
		return nil, errors.New("tenantID and keyID are required")
	}
	return c.doJSON(ctx, "DELETE", tenantPath(tenantID, "api-keys", url.PathEscape(keyID)), nil)
}

// --- Token Revocation ---

// RevokeToken revokes a token by jti or sub for a tenant.
func (c *AdminClient) RevokeToken(ctx context.Context, tenantID string, req map[string]any) (map[string]any, error) {
	if err := requireTenantID(tenantID); err != nil {
		return nil, err
	}
	return c.doJSON(ctx, "POST", tenantPath(tenantID, "tokens", "revoke"), req)
}

// --- Routing Rules ---

// GetRoutingRules retrieves routing rules for a tenant.
func (c *AdminClient) GetRoutingRules(ctx context.Context, tenantID string) (map[string]any, error) {
	if err := requireTenantID(tenantID); err != nil {
		return nil, err
	}
	return c.doJSON(ctx, "GET", tenantPath(tenantID, "routing-rules"), nil)
}

// SetRoutingRules sets routing rules for a tenant.
func (c *AdminClient) SetRoutingRules(ctx context.Context, tenantID string, body any) (map[string]any, error) {
	if err := requireTenantID(tenantID); err != nil {
		return nil, err
	}
	return c.doJSON(ctx, "PUT", tenantPath(tenantID, "routing-rules"), body)
}

// DeleteRoutingRules deletes routing rules for a tenant.
func (c *AdminClient) DeleteRoutingRules(ctx context.Context, tenantID string) (map[string]any, error) {
	if err := requireTenantID(tenantID); err != nil {
		return nil, err
	}
	return c.doJSON(ctx, "DELETE", tenantPath(tenantID, "routing-rules"), nil)
}

// RoutingRule is the request body for AddRoutingRule.
type RoutingRule struct {
	Pattern  string   `json:"pattern"`
	Topics   []string `json:"topics"`
	Priority int      `json:"priority"`
}

// AddRoutingRule adds a single routing rule for a tenant via POST.
// Returns ErrDuplicateRoutingPattern or ErrDuplicateRoutingPriority on 409 conflicts.
func (c *AdminClient) AddRoutingRule(ctx context.Context, tenantID string, rule RoutingRule) (map[string]any, error) {
	if err := requireTenantID(tenantID); err != nil {
		return nil, err
	}
	status, resp, err := c.doJSONFull(ctx, "POST", tenantPath(tenantID, "routing-rules"), map[string]any{"rule": rule})
	if err != nil {
		return nil, err
	}
	if status == http.StatusConflict {
		code, _ := resp["code"].(string)
		switch code {
		case "ROUTING_RULE_DUPLICATE_PATTERN":
			return nil, fmt.Errorf("%w", ErrDuplicateRoutingPattern)
		case "ROUTING_RULE_DUPLICATE_PRIORITY":
			return nil, fmt.Errorf("%w", ErrDuplicateRoutingPriority)
		default:
			return nil, fmt.Errorf("%w (code=%s)", ErrAPIBadRequest, code)
		}
	}
	return resp, nil
}

// --- Quotas ---

// GetQuota retrieves the quota for a tenant.
func (c *AdminClient) GetQuota(ctx context.Context, tenantID string) (map[string]any, error) {
	if err := requireTenantID(tenantID); err != nil {
		return nil, err
	}
	return c.doJSON(ctx, "GET", tenantPath(tenantID, "quotas"), nil)
}

// UpdateQuota updates the quota for a tenant.
func (c *AdminClient) UpdateQuota(ctx context.Context, tenantID string, req map[string]any) (map[string]any, error) {
	if err := requireTenantID(tenantID); err != nil {
		return nil, err
	}
	return c.doJSON(ctx, "PATCH", tenantPath(tenantID, "quotas"), req)
}

// --- Channel Rules ---

// GetChannelRules retrieves channel rules for a tenant.
func (c *AdminClient) GetChannelRules(ctx context.Context, tenantID string) (map[string]any, error) {
	if err := requireTenantID(tenantID); err != nil {
		return nil, err
	}
	return c.doJSON(ctx, "GET", tenantPath(tenantID, "channel-rules"), nil)
}

// SetChannelRules sets channel rules for a tenant.
func (c *AdminClient) SetChannelRules(ctx context.Context, tenantID string, req map[string]any) (map[string]any, error) {
	if err := requireTenantID(tenantID); err != nil {
		return nil, err
	}
	return c.doJSON(ctx, "PUT", tenantPath(tenantID, "channel-rules"), req)
}

// DeleteChannelRules deletes channel rules for a tenant.
func (c *AdminClient) DeleteChannelRules(ctx context.Context, tenantID string) (map[string]any, error) {
	if err := requireTenantID(tenantID); err != nil {
		return nil, err
	}
	return c.doJSON(ctx, "DELETE", tenantPath(tenantID, "channel-rules"), nil)
}

// --- Test Access ---

// TestAccess tests channel access for a tenant with the given parameters.
func (c *AdminClient) TestAccess(ctx context.Context, tenantID string, req map[string]any) (map[string]any, error) {
	if err := requireTenantID(tenantID); err != nil {
		return nil, err
	}
	return c.doJSON(ctx, "POST", tenantPath(tenantID, "test-access"), req)
}

// --- Edition ---

// EditionResponse represents the GET /edition response from the provisioning service.
type EditionResponse struct {
	Edition   string        `json:"edition"`
	Org       string        `json:"org"`
	ExpiresAt string        `json:"expires_at"`
	Expired   bool          `json:"expired"`
	Limits    EditionLimits `json:"limits"`
	Usage     EditionUsage  `json:"usage"`
}

// EditionLimits holds the hard limits for the current edition.
// A value of 0 means unlimited.
type EditionLimits struct {
	MaxTenants               int `json:"max_tenants"`
	MaxTotalConnections      int `json:"max_total_connections"`
	MaxShards                int `json:"max_shards"`
	MaxTopicsPerTenant       int `json:"max_topics_per_tenant"`
	MaxRoutingRulesPerTenant int `json:"max_routing_rules_per_tenant"`
}

// EditionUsage holds live resource usage counts. Nil fields indicate
// the data source did not provide that metric.
type EditionUsage struct {
	Tenants     *int `json:"tenants"`
	Connections *int `json:"connections"`
	Shards      *int `json:"shards"`
}

// GetEdition fetches the edition status from GET /edition.
// This endpoint does not require authentication — the method makes a direct
// HTTP call without the Bearer token (unlike doJSON which always adds it).
func (c *AdminClient) GetEdition(ctx context.Context) (*EditionResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/edition", http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("create edition request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch edition: %w", err)
	}
	defer func() { _ = resp.Body.Close() }() // close error inconsequential for completed HTTP response

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096)) // best-effort: read error body for diagnostics
		return nil, fmt.Errorf("fetch edition: server returned %s: %s", resp.Status, string(body))
	}

	var edition EditionResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(&edition); err != nil {
		return nil, fmt.Errorf("decode edition response: %w", err)
	}

	return &edition, nil
}

// --- Admin Key Management ---

// RegisterAdminKey registers a new admin public key.
func (c *AdminClient) RegisterAdminKey(ctx context.Context, name, algorithm, publicKey string) (map[string]any, error) {
	return c.doJSON(ctx, http.MethodPost, "/api/v1/admin/keys", map[string]string{
		"name":       name,
		"algorithm":  algorithm,
		"public_key": publicKey,
	})
}

// RevokeAdminKey revokes an admin key by ID.
func (c *AdminClient) RevokeAdminKey(ctx context.Context, keyID string) error {
	_, err := c.doJSON(ctx, http.MethodDelete, "/api/v1/admin/keys/"+keyID, nil)
	return err
}

// ListAdminKeys lists active admin keys.
func (c *AdminClient) ListAdminKeys(ctx context.Context) (map[string]any, error) {
	return c.doJSON(ctx, http.MethodGet, "/api/v1/admin/keys", nil)
}

// --- Internal ---

// doJSONFull performs an HTTP request and returns (statusCode, parsedBody, error).
// Unlike doJSON, it returns 4xx/5xx responses as (statusCode, parsedBody, nil) so
// callers can inspect the response code field for fine-grained error mapping.
// Network and JSON parsing errors are returned as (0, nil, error).
func (c *AdminClient) doJSONFull(ctx context.Context, method, path string, body any) (status int, parsed map[string]any, err error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return 0, nil, fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return 0, nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.signer != nil {
		c.signer.SignRequest(req)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return 0, nil, fmt.Errorf("read response: %w", err)
	}

	var result map[string]any
	if len(respBody) > 0 {
		if err := json.Unmarshal(respBody, &result); err != nil {
			return resp.StatusCode, nil, fmt.Errorf("unmarshal response: %w", err)
		}
	}
	return resp.StatusCode, result, nil
}

func (c *AdminClient) doJSON(ctx context.Context, method, path string, body any) (map[string]any, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.signer != nil {
		c.signer.SignRequest(req)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }() // close error inconsequential for completed HTTP response

	// Limit response body to 10MB to prevent OOM from malicious/broken servers
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		body := string(respBody)
		switch {
		case resp.StatusCode == http.StatusUnauthorized:
			return nil, fmt.Errorf("%w: %s", ErrAPIUnauthorized, body)
		case resp.StatusCode == http.StatusForbidden:
			return nil, fmt.Errorf("%w: %s", ErrAPIForbidden, body)
		case resp.StatusCode == http.StatusNotFound:
			return nil, fmt.Errorf("%w: %s", ErrAPINotFound, body)
		case resp.StatusCode == http.StatusConflict:
			// Distinct sentinel: callers treat "already exists" differently
			// from real 4xx failures (e.g. sukko up's idempotent demo tenant).
			return nil, fmt.Errorf("%w: %s", ErrAPIConflict, body)
		case resp.StatusCode >= 400 && resp.StatusCode < 500:
			return nil, fmt.Errorf("%w (HTTP %d): %s", ErrAPIBadRequest, resp.StatusCode, body)
		default:
			return nil, fmt.Errorf("%w (HTTP %d): %s", ErrAPIInternal, resp.StatusCode, body)
		}
	}

	var result map[string]any
	if len(respBody) > 0 {
		if err := json.Unmarshal(respBody, &result); err != nil {
			return nil, fmt.Errorf("unmarshal response: %w", err)
		}
	}

	return result, nil
}

func encodeParams(params map[string]string) string {
	if len(params) == 0 {
		return ""
	}
	v := url.Values{}
	for key, val := range params {
		v.Set(key, val)
	}
	return "?" + v.Encode()
}

// LicenseResponse holds the server response from a license push.
type LicenseResponse struct {
	Edition   string `json:"edition"`
	Org       string `json:"org"`
	ExpiresAt string `json:"expires_at"`
	Status    string `json:"status"`
}

// PushLicense posts a license key to the provisioning service.
// Returns the applied license details on success, or an actionable error.
// Handles 429 (rate limited) with Retry-After header extraction.
func (c *AdminClient) PushLicense(ctx context.Context, key string) (*LicenseResponse, error) {
	body := map[string]string{"key": key}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/license", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.signer != nil {
		c.signer.SignRequest(req)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		respStr := string(respBody)

		switch resp.StatusCode {
		case http.StatusUnauthorized:
			return nil, fmt.Errorf("%w: %s", ErrAPIUnauthorized, respStr)
		case http.StatusTooManyRequests:
			retryAfter := resp.Header.Get("Retry-After")
			if retryAfter != "" {
				return nil, fmt.Errorf("%w: retry after %s seconds", ErrAPIRateLimited, retryAfter)
			}
			return nil, fmt.Errorf("%w: %s", ErrAPIRateLimited, respStr)
		case http.StatusForbidden:
			return nil, fmt.Errorf("%w: %s", ErrAPIForbidden, respStr)
		default:
			if resp.StatusCode < 500 {
				return nil, fmt.Errorf("%w (HTTP %d): %s", ErrAPIBadRequest, resp.StatusCode, respStr)
			}
			return nil, fmt.Errorf("%w (HTTP %d): %s", ErrAPIInternal, resp.StatusCode, respStr)
		}
	}

	var result LicenseResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return &result, nil
}
