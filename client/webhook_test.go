package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestAdminClient_Webhooks pins the HTTP contract (method + path, and request body for writes) of
// each webhook client method against a capturing test server.
func TestAdminClient_Webhooks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		wantMethod   string
		wantPath     string
		wantBodyKeys []string
		call         func(ctx context.Context, c *AdminClient) (map[string]any, error)
	}{
		{
			name: "create", wantMethod: "POST", wantPath: "/api/v1/tenants/demo/webhooks",
			wantBodyKeys: []string{"url", "channel_pattern", "secret", "max_retries"},
			call: func(ctx context.Context, c *AdminClient) (map[string]any, error) {
				return c.CreateWebhook(ctx, "demo", CreateWebhookRequest{
					URL: "https://example.com/hook", ChannelPattern: "orders.*", Secret: "sek", MaxRetries: 3,
				})
			},
		},
		{
			name: "list", wantMethod: "GET", wantPath: "/api/v1/tenants/demo/webhooks",
			call: func(ctx context.Context, c *AdminClient) (map[string]any, error) {
				return c.ListWebhooks(ctx, "demo")
			},
		},
		{
			name: "get", wantMethod: "GET", wantPath: "/api/v1/tenants/demo/webhooks/wh-1",
			call: func(ctx context.Context, c *AdminClient) (map[string]any, error) {
				return c.GetWebhook(ctx, "demo", "wh-1")
			},
		},
		{
			name: "update", wantMethod: "PATCH", wantPath: "/api/v1/tenants/demo/webhooks/wh-1",
			wantBodyKeys: []string{"status"},
			call: func(ctx context.Context, c *AdminClient) (map[string]any, error) {
				return c.UpdateWebhook(ctx, "demo", "wh-1", map[string]any{"status": "suspended"})
			},
		},
		{
			name: "delete", wantMethod: "DELETE", wantPath: "/api/v1/tenants/demo/webhooks/wh-1",
			call: func(ctx context.Context, c *AdminClient) (map[string]any, error) {
				return c.DeleteWebhook(ctx, "demo", "wh-1")
			},
		},
		{
			name: "test", wantMethod: "POST", wantPath: "/api/v1/tenants/demo/webhooks/wh-1/test",
			call: func(ctx context.Context, c *AdminClient) (map[string]any, error) {
				return c.TestWebhook(ctx, "demo", "wh-1")
			},
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
					_ = json.NewDecoder(r.Body).Decode(&gotBody)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer srv.Close()

			c, _ := New(Config{BaseURL: srv.URL, Signer: testSigner(t)})
			if _, err := tt.call(context.Background(), c); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotMethod != tt.wantMethod {
				t.Errorf("method = %q, want %q", gotMethod, tt.wantMethod)
			}
			if gotPath != tt.wantPath {
				t.Errorf("path = %q, want %q", gotPath, tt.wantPath)
			}
			for _, k := range tt.wantBodyKeys {
				if _, ok := gotBody[k]; !ok {
					t.Errorf("request body missing key %q (got %v)", k, gotBody)
				}
			}
		})
	}
}

// TestAdminClient_Webhooks_Guards verifies the tenant/webhook-ID guards reject empty inputs before
// any HTTP request is made.
func TestAdminClient_Webhooks_Guards(t *testing.T) {
	t.Parallel()

	c, _ := New(Config{BaseURL: "http://unused.invalid", Signer: testSigner(t)})
	ctx := context.Background()

	if _, err := c.ListWebhooks(ctx, ""); err == nil {
		t.Error("ListWebhooks(\"\") = nil error, want tenant-required error")
	}
	if _, err := c.GetWebhook(ctx, "demo", ""); err == nil {
		t.Error("GetWebhook(demo, \"\") = nil error, want webhook-ID-required error")
	}
	if _, err := c.DeleteWebhook(ctx, "demo", ""); err == nil {
		t.Error("DeleteWebhook(demo, \"\") = nil error, want webhook-ID-required error")
	}
	if _, err := c.TestWebhook(ctx, "demo", ""); err == nil {
		t.Error("TestWebhook(demo, \"\") = nil error, want webhook-ID-required error")
	}
}
