package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// deviceClientID is the fixed public client identifier the CLI presents on the
// license service's device-grant endpoints (ADR-0008). It matches the server's
// allowed client; it is public (no secret).
const deviceClientID = "sukko-cli"

// maxDeviceResponseBytes caps a device-endpoint response body (defense in depth —
// SUKKO_LICENSE_API_URL is user-pointable); matches the sibling admin client.
const maxDeviceResponseBytes = 10 << 20

// Device-grant poll outcomes. The two non-terminal ones (pending/slow_down) and
// the two transient ones (rate-limited/transient) tell the caller to keep polling;
// the rest are terminal.
var (
	ErrDeviceAuthorizationPending = errors.New("authorization pending")
	ErrDeviceSlowDown             = errors.New("polling too frequently")
	ErrDeviceRateLimited          = errors.New("rate limited")                 // 429 — back off and retry
	ErrDeviceTransient            = errors.New("temporary device-login error") // transport/5xx — retry
	ErrDeviceExpired              = errors.New("device code expired")
	ErrDeviceAccessDenied         = errors.New("authorization request denied")
	ErrDeviceInvalidGrant         = errors.New("invalid or unknown device code")
	ErrDeviceInvalidClient        = errors.New("unrecognized client")
)

// DeviceAuthorization is the /device/authorize response.
type DeviceAuthorization struct {
	DeviceCode              string
	UserCode                string
	VerificationURI         string
	VerificationURIComplete string
	ExpiresIn               int
	Interval                int
}

// DeviceToken is the /device/token success response — the issued license key.
type DeviceToken struct {
	LicenseKey string
	Edition    string
	Exp        int64
}

// LicenseDeviceClient talks to the hosted license service's public device-grant
// endpoints (RFC 8628). It is unauthenticated: the device_code is the bearer.
type LicenseDeviceClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewLicenseDeviceClient targets the license service at baseURL (e.g.
// https://api.sukko.dev).
func NewLicenseDeviceClient(baseURL string) *LicenseDeviceClient {
	return &LicenseDeviceClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{Timeout: DefaultClientTimeout},
	}
}

// Authorize starts a device-authorization request and returns the codes + poll
// parameters. A non-201, an unparseable body, or a response missing the fields the
// flow needs is an error (defense against upstream drift).
func (c *LicenseDeviceClient) Authorize(ctx context.Context) (*DeviceAuthorization, error) {
	status, raw, err := c.post(ctx, "/api/v1/device/authorize", map[string]string{"client_id": deviceClientID})
	if err != nil {
		return nil, err
	}
	if status != http.StatusCreated {
		return nil, fmt.Errorf("device authorize: unexpected status %d", status)
	}
	var out struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURI         string `json:"verification_uri"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		ExpiresIn               int    `json:"expires_in"`
		Interval                int    `json:"interval"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode device authorize response: %w", err)
	}
	if out.DeviceCode == "" || out.UserCode == "" || out.VerificationURI == "" {
		return nil, errors.New("device authorize: incomplete response from license service")
	}
	return &DeviceAuthorization{
		DeviceCode:              out.DeviceCode,
		UserCode:                out.UserCode,
		VerificationURI:         out.VerificationURI,
		VerificationURIComplete: out.VerificationURIComplete,
		ExpiresIn:               out.ExpiresIn,
		Interval:                out.Interval,
	}, nil
}

// PollToken polls once. On success it returns the issued key. Non-terminal and
// transient outcomes return a retryable sentinel (ErrDeviceAuthorizationPending,
// ErrDeviceSlowDown, ErrDeviceRateLimited, ErrDeviceTransient); terminal failures
// return the matching terminal sentinel.
func (c *LicenseDeviceClient) PollToken(ctx context.Context, deviceCode string) (*DeviceToken, error) {
	status, raw, err := c.post(ctx, "/api/v1/device/token",
		map[string]string{"client_id": deviceClientID, "device_code": deviceCode})
	if err != nil {
		// transport/read error — transient; the caller retries a bounded number of times.
		return nil, fmt.Errorf("%w: %w", ErrDeviceTransient, err)
	}
	switch {
	case status == http.StatusOK:
		var out struct {
			LicenseKey string `json:"license_key"`
			Edition    string `json:"edition"`
			Exp        int64  `json:"exp"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("decode device token response: %w", err)
		}
		if out.LicenseKey == "" {
			return nil, errors.New("device token: license service returned an empty key")
		}
		return &DeviceToken{LicenseKey: out.LicenseKey, Edition: out.Edition, Exp: out.Exp}, nil
	case status == http.StatusTooManyRequests:
		return nil, ErrDeviceRateLimited
	case status >= http.StatusInternalServerError:
		return nil, fmt.Errorf("%w: status %d", ErrDeviceTransient, status)
	default:
		var e struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(raw, &e) // code may be absent; mapDeviceError handles ""
		return nil, mapDeviceError(status, e.Code)
	}
}

func mapDeviceError(status int, code string) error {
	switch code {
	case "AUTHORIZATION_PENDING":
		return ErrDeviceAuthorizationPending
	case "SLOW_DOWN":
		return ErrDeviceSlowDown
	case "EXPIRED_TOKEN":
		return ErrDeviceExpired
	case "ACCESS_DENIED":
		return ErrDeviceAccessDenied
	case "INVALID_GRANT":
		return ErrDeviceInvalidGrant
	case "INVALID_CLIENT":
		return ErrDeviceInvalidClient
	default:
		return fmt.Errorf("device token: unexpected status %d (code %q)", status, code)
	}
}

// post sends a JSON body and returns the status + the (size-capped) raw response
// body. A transport or read error is returned directly; HTTP status mapping is the
// caller's job.
func (c *LicenseDeviceClient) post(ctx context.Context, path string, body any) (status int, respBody []byte, err error) {
	b, err := json.Marshal(body)
	if err != nil {
		return 0, nil, fmt.Errorf("marshal %s body: %w", path, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(b))
	if err != nil {
		return 0, nil, fmt.Errorf("new request %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("%s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxDeviceResponseBytes))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("read %s response: %w", path, err)
	}
	return resp.StatusCode, raw, nil
}
