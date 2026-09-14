package client

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testSigner(t *testing.T) *KeypairSigner {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate test keypair: %v", err)
	}
	return NewKeypairSigner(priv, "test-kid", "tester")
}

func TestAdminClient_Tenants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		serverStatus   int
		serverResponse map[string]any
		call           func(ctx context.Context, c *AdminClient) (map[string]any, error)
		wantMethod     string
		wantPath       string
		wantBody       map[string]any
		wantErr        bool
		wantErrContain string
		wantSentinel   error
	}{
		{
			name:         "CreateTenant sends POST /api/v1/tenants with body",
			serverStatus: http.StatusCreated,
			serverResponse: map[string]any{
				"id":   "tenant-1",
				"name": "Test Tenant",
			},
			call: func(ctx context.Context, c *AdminClient) (map[string]any, error) {
				return c.CreateTenant(ctx, map[string]any{
					"name": "Test Tenant",
					"tier": "standard",
				})
			},
			wantMethod: "POST",
			wantPath:   "/api/v1/tenants",
			wantBody: map[string]any{
				"name": "Test Tenant",
				"tier": "standard",
			},
		},
		{
			name:         "GetTenant sends GET /api/v1/tenants/{id}",
			serverStatus: http.StatusOK,
			serverResponse: map[string]any{
				"id":     "tenant-42",
				"name":   "My Tenant",
				"status": "active",
			},
			call: func(ctx context.Context, c *AdminClient) (map[string]any, error) {
				return c.GetTenant(ctx, "tenant-42")
			},
			wantMethod: "GET",
			wantPath:   "/api/v1/tenants/tenant-42",
		},
		{
			name:         "ListTenants sends GET /api/v1/tenants with query params",
			serverStatus: http.StatusOK,
			serverResponse: map[string]any{
				"tenants": []any{},
				"total":   float64(0),
			},
			call: func(ctx context.Context, c *AdminClient) (map[string]any, error) {
				return c.ListTenants(ctx, map[string]string{
					"status": "active",
				})
			},
			wantMethod: "GET",
			wantPath:   "/api/v1/tenants",
		},
		{
			name:         "SuspendTenant sends POST /api/v1/tenants/{id}/suspend",
			serverStatus: http.StatusOK,
			serverResponse: map[string]any{
				"id":     "tenant-99",
				"status": "suspended",
			},
			call: func(ctx context.Context, c *AdminClient) (map[string]any, error) {
				return c.SuspendTenant(ctx, "tenant-99")
			},
			wantMethod: "POST",
			wantPath:   "/api/v1/tenants/tenant-99/suspend",
		},
		{
			name:         "API error HTTP 400 returns error",
			serverStatus: http.StatusBadRequest,
			call: func(ctx context.Context, c *AdminClient) (map[string]any, error) {
				return c.CreateTenant(ctx, map[string]any{"name": ""})
			},
			wantMethod:   "POST",
			wantPath:     "/api/v1/tenants",
			wantErr:      true,
			wantSentinel: ErrAPIBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var gotMethod, gotPath string
			var gotBody map[string]any

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod = r.Method
				gotPath = r.URL.Path

				if r.Body != nil && r.ContentLength > 0 {
					if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
						t.Errorf("failed to decode request body: %v", err)
					}
				}

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.serverStatus)

				if tt.serverResponse != nil {
					if err := json.NewEncoder(w).Encode(tt.serverResponse); err != nil {
						t.Errorf("failed to encode response: %v", err)
					}
				} else if tt.wantErr {
					_, _ = w.Write([]byte(`{"error":"bad request"}`))
				}
			}))
			defer srv.Close()

			client, _ := New(Config{
				BaseURL: srv.URL,
				Signer:  testSigner(t),
			})

			result, err := tt.call(context.Background(), client)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error but got nil")
				}
				if tt.wantSentinel != nil && !errors.Is(err, tt.wantSentinel) {
					t.Errorf("error %q is not %q", err.Error(), tt.wantSentinel)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if gotMethod != tt.wantMethod {
				t.Errorf("method = %q, want %q", gotMethod, tt.wantMethod)
			}

			if gotPath != tt.wantPath {
				t.Errorf("path = %q, want %q", gotPath, tt.wantPath)
			}

			if tt.wantBody != nil {
				for k, wantVal := range tt.wantBody {
					gotVal, ok := gotBody[k]
					if !ok {
						t.Errorf("request body missing key %q", k)
						continue
					}
					if fmt.Sprintf("%v", gotVal) != fmt.Sprintf("%v", wantVal) {
						t.Errorf("request body[%q] = %v, want %v", k, gotVal, wantVal)
					}
				}
			}

			if tt.serverResponse != nil {
				for k, wantVal := range tt.serverResponse {
					gotVal, ok := result[k]
					if !ok {
						t.Errorf("response missing key %q", k)
						continue
					}
					if fmt.Sprintf("%v", gotVal) != fmt.Sprintf("%v", wantVal) {
						t.Errorf("response[%q] = %v, want %v", k, gotVal, wantVal)
					}
				}
			}
		})
	}
}

func TestAdminClient_Keys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		serverStatus   int
		serverResponse map[string]any
		call           func(ctx context.Context, c *AdminClient) (map[string]any, error)
		wantMethod     string
		wantPath       string
	}{
		{
			name:         "CreateKey sends POST /api/v1/tenants/{id}/keys",
			serverStatus: http.StatusCreated,
			serverResponse: map[string]any{
				"key_id": "key-abc",
			},
			call: func(ctx context.Context, c *AdminClient) (map[string]any, error) {
				return c.CreateKey(ctx, "tenant-1", map[string]any{
					"label": "my-key",
				})
			},
			wantMethod: "POST",
			wantPath:   "/api/v1/tenants/tenant-1/keys",
		},
		{
			name:         "ListKeys sends GET /api/v1/tenants/{id}/keys",
			serverStatus: http.StatusOK,
			serverResponse: map[string]any{
				"keys": []any{},
			},
			call: func(ctx context.Context, c *AdminClient) (map[string]any, error) {
				return c.ListKeys(ctx, "tenant-5")
			},
			wantMethod: "GET",
			wantPath:   "/api/v1/tenants/tenant-5/keys",
		},
		{
			name:         "RevokeKey sends DELETE /api/v1/tenants/{id}/keys/{keyId}",
			serverStatus: http.StatusOK,
			serverResponse: map[string]any{
				"revoked": true,
			},
			call: func(ctx context.Context, c *AdminClient) (map[string]any, error) {
				return c.RevokeKey(ctx, "tenant-5", "key-xyz")
			},
			wantMethod: "DELETE",
			wantPath:   "/api/v1/tenants/tenant-5/keys/key-xyz",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var gotMethod, gotPath string

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod = r.Method
				gotPath = r.URL.Path

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.serverStatus)
				if tt.serverResponse != nil {
					if err := json.NewEncoder(w).Encode(tt.serverResponse); err != nil {
						t.Errorf("failed to encode response: %v", err)
					}
				}
			}))
			defer srv.Close()

			client, _ := New(Config{
				BaseURL: srv.URL,
				Signer:  testSigner(t),
			})

			result, err := tt.call(context.Background(), client)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if gotMethod != tt.wantMethod {
				t.Errorf("method = %q, want %q", gotMethod, tt.wantMethod)
			}

			if gotPath != tt.wantPath {
				t.Errorf("path = %q, want %q", gotPath, tt.wantPath)
			}

			if result == nil {
				t.Fatal("expected non-nil result")
			}
		})
	}
}

func TestAdminClient_AuthSigning(t *testing.T) {
	t.Parallel()

	t.Run("signer present sends JWT", func(t *testing.T) {
		t.Parallel()
		var gotAuth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))
		defer srv.Close()

		c, _ := New(Config{BaseURL: srv.URL, Signer: testSigner(t)})
		_, err := c.GetTenant(context.Background(), "test-id")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.HasPrefix(gotAuth, "Bearer ") {
			t.Fatalf("auth = %q, want Bearer <jwt>", gotAuth)
		}
		jwt := strings.TrimPrefix(gotAuth, "Bearer ")
		if parts := strings.Split(jwt, "."); len(parts) != 3 {
			t.Errorf("JWT should have 3 parts, got %d", len(parts))
		}
	})

	t.Run("nil signer omits header", func(t *testing.T) {
		t.Parallel()
		var hasAuth bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hasAuth = r.Header.Get("Authorization") != ""
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))
		defer srv.Close()

		c, _ := New(Config{BaseURL: srv.URL})
		_, err := c.GetTenant(context.Background(), "test-id")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if hasAuth {
			t.Error("expected no Authorization header with nil signer")
		}
	})
}

func TestAdminClient_ErrorResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		serverStatus int
		serverBody   string
		wantSentinel error
	}{
		{
			name:         "HTTP 400 error",
			serverStatus: http.StatusBadRequest,
			serverBody:   `{"error":"invalid input"}`,
			wantSentinel: ErrAPIBadRequest,
		},
		{
			name:         "HTTP 500 error",
			serverStatus: http.StatusInternalServerError,
			serverBody:   `{"error":"internal failure"}`,
			wantSentinel: ErrAPIInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.serverStatus)
				_, _ = w.Write([]byte(tt.serverBody))
			}))
			defer srv.Close()

			client, _ := New(Config{
				BaseURL: srv.URL,
				Signer:  testSigner(t),
			})

			_, err := client.GetTenant(context.Background(), "any-id")
			if err == nil {
				t.Fatal("expected error but got nil")
			}

			if !errors.Is(err, tt.wantSentinel) {
				t.Errorf("error %q is not %q", err.Error(), tt.wantSentinel)
			}
		})
	}
}

func TestAdminClient_AddRoutingRule(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		serverStatus int
		serverBody   string
		wantErr      bool
		wantSentinel error
		wantMethod   string
		wantPath     string
		wantBodyKey  string // top-level key that must be present in the request body
	}{
		{
			name:         "sends POST to correct URL with rule envelope",
			serverStatus: http.StatusCreated,
			serverBody:   `{"rule":{"pattern":"trades.**","topics":["trades"],"priority":0}}`,
			wantMethod:   "POST",
			wantPath:     "/api/v1/tenants/demo/routing-rules",
			wantBodyKey:  "rule",
		},
		{
			name:         "409 ROUTING_RULE_DUPLICATE_PATTERN → typed error",
			serverStatus: http.StatusConflict,
			serverBody:   `{"code":"ROUTING_RULE_DUPLICATE_PATTERN","message":"already exists"}`,
			wantErr:      true,
			wantSentinel: ErrDuplicateRoutingPattern,
		},
		{
			name:         "409 ROUTING_RULE_DUPLICATE_PRIORITY → typed error",
			serverStatus: http.StatusConflict,
			serverBody:   `{"code":"ROUTING_RULE_DUPLICATE_PRIORITY","message":"already exists"}`,
			wantErr:      true,
			wantSentinel: ErrDuplicateRoutingPriority,
		},
		{
			name:         "409 unknown conflict code → generic bad request error",
			serverStatus: http.StatusConflict,
			serverBody:   `{"code":"UNKNOWN_CODE","message":"conflict"}`,
			wantErr:      true,
			wantSentinel: ErrAPIBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var gotMethod, gotPath string
			var gotBody map[string]any

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod = r.Method
				gotPath = r.URL.Path
				if r.Body != nil && r.ContentLength > 0 {
					if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
						t.Errorf("decode request body: %v", err)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.serverStatus)
				fmt.Fprint(w, tt.serverBody)
			}))
			defer srv.Close()

			c, _ := New(Config{BaseURL: srv.URL, Signer: testSigner(t)})

			result, err := c.AddRoutingRule(context.Background(), "demo", RoutingRule{
				Pattern:  "trades.**",
				Topics:   []string{"trades"},
				Priority: 0,
			})

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error but got nil")
				}
				if tt.wantSentinel != nil && !errors.Is(err, tt.wantSentinel) {
					t.Errorf("error %q is not %q", err.Error(), tt.wantSentinel)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotMethod != tt.wantMethod {
				t.Errorf("method = %q, want %q", gotMethod, tt.wantMethod)
			}
			if gotPath != tt.wantPath {
				t.Errorf("path = %q, want %q", gotPath, tt.wantPath)
			}
			if tt.wantBodyKey != "" {
				if _, ok := gotBody[tt.wantBodyKey]; !ok {
					t.Errorf("request body missing key %q; got %v", tt.wantBodyKey, gotBody)
				}
			}
			_ = result
		})
	}
}

func TestAdminClient_EmptyBaseURL(t *testing.T) {
	t.Parallel()

	_, err := New(Config{BaseURL: ""})
	if err == nil {
		t.Fatal("expected error for empty BaseURL, got nil")
	}
}

func TestAdminClient_EmptyTenantID(t *testing.T) {
	t.Parallel()

	c, _ := New(Config{BaseURL: "http://localhost:9999"})

	_, err := c.GetTenant(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty tenantID, got nil")
	}
}

func TestAdminClient_EmptyKeyID(t *testing.T) {
	t.Parallel()

	c, _ := New(Config{BaseURL: "http://localhost:9999"})

	_, err := c.RevokeKey(context.Background(), "tenant-1", "")
	if err == nil {
		t.Fatal("expected error for empty keyID, got nil")
	}
}

func TestAdminClient_GetEdition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		serverStatus int
		serverBody   string
		wantErr      bool
		wantEdition  string
		wantExpired  bool
	}{
		{
			name:         "valid pro response",
			serverStatus: http.StatusOK,
			serverBody:   `{"edition":"pro","org":"Acme Corp","expires_at":"2027-03-25T00:00:00Z","expired":false,"limits":{"max_tenants":50,"max_total_connections":10000,"max_shards":8,"max_topics_per_tenant":50,"max_routing_rules_per_tenant":100},"usage":{"tenants":5,"connections":1200,"shards":2}}`,
			wantEdition:  "pro",
		},
		{
			name:         "expired license",
			serverStatus: http.StatusOK,
			serverBody:   `{"edition":"community","org":"Acme Corp","expires_at":"2026-01-15T00:00:00Z","expired":true,"limits":{"max_tenants":3,"max_total_connections":500,"max_shards":1},"usage":{"tenants":2}}`,
			wantEdition:  "community",
			wantExpired:  true,
		},
		{
			name:         "community no org",
			serverStatus: http.StatusOK,
			serverBody:   `{"edition":"community","expired":false,"limits":{"max_tenants":3,"max_total_connections":500,"max_shards":1},"usage":{}}`,
			wantEdition:  "community",
		},
		{
			name:         "server error",
			serverStatus: http.StatusInternalServerError,
			serverBody:   `{"error":"internal"}`,
			wantErr:      true,
		},
		{
			name:         "invalid JSON",
			serverStatus: http.StatusOK,
			serverBody:   `not-json`,
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var gotPath string
			var gotAuth string

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotAuth = r.Header.Get("Authorization")

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.serverStatus)
				fmt.Fprint(w, tt.serverBody)
			}))
			defer srv.Close()

			c, _ := New(Config{BaseURL: srv.URL, Signer: testSigner(t)})

			resp, err := c.GetEdition(context.Background())

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if gotPath != "/edition" {
				t.Errorf("path = %q, want /edition", gotPath)
			}

			if gotAuth != "" {
				t.Error("GetEdition should not send Authorization header")
			}

			if resp.Edition != tt.wantEdition {
				t.Errorf("edition = %q, want %q", resp.Edition, tt.wantEdition)
			}

			if resp.Expired != tt.wantExpired {
				t.Errorf("expired = %v, want %v", resp.Expired, tt.wantExpired)
			}
		})
	}
}

func TestAdminClient_GetEdition_Unreachable(t *testing.T) {
	t.Parallel()

	c, _ := New(Config{BaseURL: "http://127.0.0.1:1"}) // port 1 — connection refused
	_, err := c.GetEdition(context.Background())
	if err == nil {
		t.Fatal("expected error for unreachable server, got nil")
	}
}

func TestAdminClient_DefaultTimeout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		timeout     time.Duration
		wantTimeout time.Duration
	}{
		{
			name:        "zero timeout defaults to 30s",
			timeout:     0,
			wantTimeout: 30 * time.Second,
		},
		{
			name:        "explicit timeout is respected",
			timeout:     10 * time.Second,
			wantTimeout: 10 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client, _ := New(Config{
				BaseURL: "http://localhost:9999",
				Timeout: tt.timeout,
			})

			if client.httpClient.Timeout != tt.wantTimeout {
				t.Errorf("timeout = %v, want %v", client.httpClient.Timeout, tt.wantTimeout)
			}
		})
	}
}

func TestAdminClient_RevokeToken(t *testing.T) {
	t.Parallel()

	var gotMethod, gotPath string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":     "revoked",
			"type":       "token",
			"tenant_id":  "acme",
			"expires_at": "2026-04-15T00:00:00Z",
		})
	}))
	defer srv.Close()

	c, err := New(Config{BaseURL: srv.URL, Signer: testSigner(t)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := c.RevokeToken(context.Background(), "acme", map[string]any{"jti": "abc-123"})
	if err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/api/v1/tenants/acme/tokens/revoke" {
		t.Errorf("path = %q, want /api/v1/tenants/acme/tokens/revoke", gotPath)
	}
	if gotBody["jti"] != "abc-123" {
		t.Errorf("body jti = %v, want abc-123", gotBody["jti"])
	}
	if result["status"] != "revoked" {
		t.Errorf("result status = %v, want revoked", result["status"])
	}
}

func TestAdminClient_RevokeToken_EmptyTenant(t *testing.T) {
	t.Parallel()

	c, err := New(Config{BaseURL: "http://localhost", Signer: testSigner(t)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = c.RevokeToken(context.Background(), "", map[string]any{"jti": "abc"})
	if err == nil {
		t.Error("expected error for empty tenantID")
	}
}

func TestPushLicense(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		status      int
		body        string
		headers     map[string]string
		wantErr     error
		wantEdition string
	}{
		{
			name:        "200 success",
			status:      http.StatusOK,
			body:        `{"edition":"pro","org":"Acme","expires_at":"2027-04-08T00:00:00Z","status":"reloaded"}`,
			wantEdition: "pro",
		},
		{
			name:    "401 unauthorized",
			status:  http.StatusUnauthorized,
			body:    `{"code":"UNAUTHORIZED","message":"invalid admin credentials"}`,
			wantErr: ErrAPIUnauthorized,
		},
		{
			name:    "429 rate limited with Retry-After",
			status:  http.StatusTooManyRequests,
			body:    `{"code":"RATE_LIMITED","message":"too many requests"}`,
			headers: map[string]string{"Retry-After": "6"},
			wantErr: ErrAPIRateLimited,
		},
		{
			name:    "429 rate limited without Retry-After",
			status:  http.StatusTooManyRequests,
			body:    `{"code":"RATE_LIMITED","message":"too many requests"}`,
			wantErr: ErrAPIRateLimited,
		},
		{
			name:    "400 invalid signature",
			status:  http.StatusBadRequest,
			body:    `{"code":"INVALID_SIGNATURE","message":"Ed25519 signature verification failed"}`,
			wantErr: ErrAPIBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/license" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}

				// Verify request body
				var req map[string]string
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Errorf("decode request body: %v", err)
				}
				if req["key"] == "" {
					t.Error("missing key in request body")
				}

				for k, v := range tt.headers {
					w.Header().Set(k, v)
				}
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			}))
			defer srv.Close()

			c, err := New(Config{BaseURL: srv.URL})
			if err != nil {
				t.Fatalf("create client: %v", err)
			}

			resp, err := c.PushLicense(context.Background(), "test-license-key")

			if tt.wantErr != nil {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("error = %v, want %v", err, tt.wantErr)
				}
				// Check Retry-After is included in 429 error message
				if tt.headers != nil && tt.headers["Retry-After"] != "" {
					if !strings.Contains(err.Error(), tt.headers["Retry-After"]) {
						t.Errorf("error should contain Retry-After value %q: %v", tt.headers["Retry-After"], err)
					}
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if resp.Edition != tt.wantEdition {
				t.Errorf("edition = %q, want %q", resp.Edition, tt.wantEdition)
			}
			if resp.Org != "Acme" {
				t.Errorf("org = %q, want %q", resp.Org, "Acme")
			}
			if resp.Status != "reloaded" {
				t.Errorf("status = %q, want %q", resp.Status, "reloaded")
			}
		})
	}
}
