package commands

import (
	"strings"
	"testing"

	clicontext "github.com/sukko-dev/cli/context"
)

// These tests mutate package-level resolvedCtx/resolvedStore — no t.Parallel()
// on subtests to avoid data races on shared globals.

func TestIsLocalContext(t *testing.T) {
	tests := []struct {
		name      string
		ctx       *clicontext.Context
		wantLocal bool
	}{
		{
			name:      "localhost gateway",
			ctx:       &clicontext.Context{GatewayURL: "ws://localhost:3000", ProvisioningURL: "http://remote:8080"},
			wantLocal: true,
		},
		{
			name:      "127.0.0.1 provisioning",
			ctx:       &clicontext.Context{GatewayURL: "wss://remote:3000", ProvisioningURL: "http://127.0.0.1:8080"},
			wantLocal: true,
		},
		{
			name:      "remote URLs",
			ctx:       &clicontext.Context{GatewayURL: "wss://ws.example.com", ProvisioningURL: "https://api.example.com"},
			wantLocal: false,
		},
		{
			name:      "nil context",
			ctx:       nil,
			wantLocal: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origCtx := resolvedCtx
			resolvedCtx = tt.ctx
			defer func() { resolvedCtx = origCtx }()

			if got := isLocalContext(); got != tt.wantLocal {
				t.Errorf("isLocalContext() = %v, want %v", got, tt.wantLocal)
			}
		})
	}
}

func TestBuildTestContext(t *testing.T) {
	tests := []struct {
		name           string
		ctx            *clicontext.Context
		store          *clicontext.Store
		kafkaBrokers   string
		wantNil        bool
		wantErr        bool
		wantErrContain string
	}{
		{
			name:    "nil context returns nil",
			ctx:     nil,
			wantNil: true,
		},
		{
			name:    "localhost returns nil",
			ctx:     &clicontext.Context{GatewayURL: "ws://localhost:3000", ProvisioningURL: "http://localhost:8080"},
			wantNil: true,
		},
		{
			name:    "nil store returns nil",
			ctx:     &clicontext.Context{GatewayURL: "wss://ws.example.com", ProvisioningURL: "https://api.example.com"},
			store:   nil,
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origCtx := resolvedCtx
			origStore := resolvedStore
			resolvedCtx = tt.ctx
			resolvedStore = tt.store
			defer func() {
				resolvedCtx = origCtx
				resolvedStore = origStore
			}()

			result, err := buildTestContext(tt.kafkaBrokers, "")

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error but got nil")
				}
				if tt.wantErrContain != "" && !strings.Contains(err.Error(), tt.wantErrContain) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.wantErrContain)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantNil {
				if result != nil {
					t.Errorf("expected nil, got %v", result)
				}
				return
			}
		})
	}
}

// TestBuildTestContext_KafkaBrokers verifies the per-run --kafka-brokers override
// is threaded into the context as kafka_brokers (for the kafka-ingest suite), and
// omitted when the flag is empty. Credentials are never included.
func TestBuildTestContext_KafkaBrokers(t *testing.T) {
	// No t.Parallel() — mutates package-level resolvedCtx/resolvedStore.
	store, err := clicontext.NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatalf("NewStoreWithDir: %v", err)
	}

	origCtx, origStore := resolvedCtx, resolvedStore
	resolvedCtx = &clicontext.Context{
		Name:            "remote",
		Type:            "remote",
		GatewayURL:      "wss://gw.example.com:3000",
		ProvisioningURL: "https://prov.example.com:8080",
		Environment:     "prod",
	}
	resolvedStore = store
	defer func() { resolvedCtx, resolvedStore = origCtx, origStore }()

	t.Run("override present", func(t *testing.T) {
		ctx, err := buildTestContext("broker1:9092,broker2:9092", "prod")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ctx == nil {
			t.Fatal("expected non-nil context")
		}
		if got := ctx["kafka_brokers"]; got != "broker1:9092,broker2:9092" {
			t.Errorf("kafka_brokers = %v, want %q", got, "broker1:9092,broker2:9092")
		}
		if got := ctx["kafka_topic_namespace"]; got != "prod" {
			t.Errorf("kafka_topic_namespace = %v, want %q", got, "prod")
		}
		if _, ok := ctx["message_backend_urls"]; ok {
			t.Error("retired message_backend_urls must not be present")
		}
	})

	t.Run("override empty omits fields", func(t *testing.T) {
		ctx, err := buildTestContext("", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := ctx["kafka_brokers"]; ok {
			t.Error("kafka_brokers must be absent when the flag is empty")
		}
		if _, ok := ctx["kafka_topic_namespace"]; ok {
			t.Error("kafka_topic_namespace must be absent when the flag is empty")
		}
	})

	t.Run("namespace without brokers threads independently", func(t *testing.T) {
		ctx, err := buildTestContext("", "prod")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := ctx["kafka_topic_namespace"]; got != "prod" {
			t.Errorf("kafka_topic_namespace = %v, want %q", got, "prod")
		}
		if _, ok := ctx["kafka_brokers"]; ok {
			t.Error("kafka_brokers must be absent when only the namespace is set")
		}
	})
}
