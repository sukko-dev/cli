package commands

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	clicontext "github.com/sukko-dev/cli/context"
)

func TestResolveWebhookToken(t *testing.T) {
	t.Parallel()

	// A persisted token (from `sukko init`) is returned verbatim.
	if got := resolveWebhookToken(ProjectConfig{WebhookInternalToken: "persisted-token"}); got != "persisted-token" {
		t.Errorf("resolveWebhookToken(persisted) = %q, want persisted-token", got)
	}

	// An empty config (e.g. created before the field existed) still yields a non-empty
	// ephemeral token, so a Pro/Enterprise stack boots rather than fatally requiring
	// WEBHOOK_INTERNAL_TOKEN.
	a := resolveWebhookToken(ProjectConfig{})
	b := resolveWebhookToken(ProjectConfig{})
	if a == "" || b == "" {
		t.Fatal("resolveWebhookToken(empty) returned an empty token — Pro/Enterprise would fail to boot")
	}
	if len(a) != 64 {
		t.Errorf("ephemeral token length = %d, want 64 hex chars (32 bytes)", len(a))
	}
	if a == b {
		t.Error("ephemeral tokens should differ between calls")
	}
}

func TestDefaultCatchAllRules(t *testing.T) {
	t.Parallel()

	body := defaultCatchAllRules()

	rulesRaw, ok := body["rules"]
	if !ok {
		t.Fatal("defaultCatchAllRules: missing 'rules' key")
	}

	rules, ok := rulesRaw.([]map[string]any)
	if !ok || len(rules) != 1 {
		t.Fatalf("defaultCatchAllRules: expected exactly 1 rule, got %v", rulesRaw)
	}

	rule := rules[0]

	// Assert the ingress topic is present and correct (ADR-0018: routing rules
	// carry one ingress topic, not a topics list).
	ingressRaw, ok := rule["ingress_topic"]
	if !ok {
		t.Fatal("rule missing 'ingress_topic' key")
	}
	if ingressRaw != "default" {
		t.Errorf("ingress_topic = %v, want \"default\"", ingressRaw)
	}
	if _, hasOld := rule["topics"]; hasOld {
		t.Errorf("rule still carries the removed 'topics' key: %v", rule)
	}

	// Assert priority is present and correct.
	priorityRaw, ok := rule["priority"]
	if !ok {
		t.Error("rule missing 'priority' key")
	} else if priorityRaw != 100 {
		t.Errorf("rule priority = %v, want 100", priorityRaw)
	}

	// Assert pattern is present and correct.
	if rule["pattern"] != "**" {
		t.Errorf("rule pattern = %v, want \"**\"", rule["pattern"])
	}

	// Assert topic_suffix is NOT present (old format must not appear).
	if _, ok := rule["topic_suffix"]; ok {
		t.Error("rule must not contain 'topic_suffix' key (stale format)")
	}
}

func TestValidateProjectConfig_Broadcast(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		broadcast string
		wantErr   bool
	}{
		{"empty is valid", "", false},
		{"valkey is valid", "valkey", false},
		{"unknown rejected", "unknown_value", true},
		{"legacy alias rejected", "redis", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateProjectConfig(ProjectConfig{Broadcast: tt.broadcast})
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for broadcast=%q, got nil", tt.broadcast)
				}
				if !strings.Contains(err.Error(), tt.broadcast) || !strings.Contains(err.Error(), "valkey") {
					t.Errorf("error %q must name the rejected value and the supported one", err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("expected broadcast=%q to be valid, got: %v", tt.broadcast, err)
			}
		})
	}
}

func TestBuildComposeConfig_Observability(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		cfg           ProjectConfig
		wantProfile   bool
		wantTracing   bool
		wantProfiling bool
	}{
		{
			name:        "observability disabled — no profile or env vars",
			cfg:         ProjectConfig{Database: "sqlite", Broadcast: "valkey", MessageBackend: "direct"},
			wantProfile: false,
		},
		{
			name:        "observability enabled — adds profile",
			cfg:         ProjectConfig{Database: "sqlite", Broadcast: "valkey", MessageBackend: "direct", Observability: true},
			wantProfile: true,
		},
		{
			name:        "tracing enabled — sets OTEL_TRACING_ENABLED",
			cfg:         ProjectConfig{Observability: true, Tracing: true},
			wantProfile: true,
			wantTracing: true,
		},
		{
			name:          "profiling enabled — sets PPROF and PYROSCOPE",
			cfg:           ProjectConfig{Observability: true, Profiling: true},
			wantProfile:   true,
			wantProfiling: true,
		},
		{
			name:          "all enabled",
			cfg:           ProjectConfig{Observability: true, Tracing: true, Profiling: true},
			wantProfile:   true,
			wantTracing:   true,
			wantProfiling: true,
		},
		{
			name:        "tracing without observability — no effect",
			cfg:         ProjectConfig{Tracing: true},
			wantProfile: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			profiles, envOverrides := buildComposeConfig(tt.cfg)

			hasProfile := slices.Contains(profiles, "observability")
			if hasProfile != tt.wantProfile {
				t.Errorf("observability profile: got %v, want %v", hasProfile, tt.wantProfile)
			}

			if tt.wantTracing {
				if envOverrides["OTEL_TRACING_ENABLED"] != "true" {
					t.Error("expected OTEL_TRACING_ENABLED=true")
				}
			} else if _, ok := envOverrides["OTEL_TRACING_ENABLED"]; ok {
				t.Error("unexpected OTEL_TRACING_ENABLED")
			}

			if tt.wantProfiling {
				if envOverrides["PPROF_ENABLED"] != "true" {
					t.Error("expected PPROF_ENABLED=true")
				}
				if envOverrides["PYROSCOPE_ENABLED"] != "true" {
					t.Error("expected PYROSCOPE_ENABLED=true")
				}
			} else {
				if _, ok := envOverrides["PPROF_ENABLED"]; ok {
					t.Error("unexpected PPROF_ENABLED")
				}
			}
		})
	}
}

func TestBuildComposeConfig_MessageBackend(t *testing.T) {
	tests := []struct {
		name        string
		backend     string
		wantProfile bool // "kafka" profile present
		wantEnv     bool // MESSAGE_BACKEND/KAFKA_BROKERS overrides present
	}{
		{"default empty stays direct", "", false, false},
		{"explicit direct", "direct", false, false},
		{"kafka", "kafka", true, true},
		{"redpanda maps to kafka profile+broker", "redpanda", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profiles, env := buildComposeConfig(ProjectConfig{Database: "sqlite", Broadcast: "valkey", MessageBackend: tt.backend})
			hasKafka := slices.Contains(profiles, "kafka")
			if hasKafka != tt.wantProfile {
				t.Errorf("kafka profile = %v, want %v (profiles=%v)", hasKafka, tt.wantProfile, profiles)
			}
			if slices.Contains(profiles, "redpanda") {
				t.Errorf("no service declares a %q profile; got %v", "redpanda", profiles)
			}
			if tt.wantEnv {
				if env["MESSAGE_BACKEND"] != "kafka" {
					t.Errorf("MESSAGE_BACKEND = %q, want kafka", env["MESSAGE_BACKEND"])
				}
				if env["KAFKA_BROKERS"] != "redpanda:9092" {
					t.Errorf("KAFKA_BROKERS = %q, want redpanda:9092", env["KAFKA_BROKERS"])
				}
			} else {
				if _, ok := env["MESSAGE_BACKEND"]; ok {
					t.Errorf("MESSAGE_BACKEND override present for backend %q; want none (Go default direct)", tt.backend)
				}
			}
		})
	}
}

func TestResolveEffectiveLicenseKey(t *testing.T) {
	// Not parallel: mutates the process-wide SUKKO_LICENSE_KEY env var via t.Setenv.
	tests := []struct {
		name    string
		envOver map[string]string
		shell   string // shell-exported SUKKO_LICENSE_KEY ("" = unset)
		want    string
	}{
		{"neither set", map[string]string{}, "", ""},
		{"context-store only", map[string]string{"SUKKO_LICENSE_KEY": "ctx-token"}, "", "ctx-token"},
		{"shell env only (regression: must be honored)", map[string]string{}, "shell-token", "shell-token"},
		{"context store wins over shell", map[string]string{"SUKKO_LICENSE_KEY": "ctx-token"}, "shell-token", "ctx-token"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SUKKO_LICENSE_KEY", tt.shell)
			if got := resolveEffectiveLicenseKey(tt.envOver); got != tt.want {
				t.Errorf("resolveEffectiveLicenseKey(%v) with shell=%q = %q, want %q", tt.envOver, tt.shell, got, tt.want)
			}
		})
	}
}

// TestDefaultChannelRules asserts the seeded channel-rules body matches the
// provisioning SetChannelRulesRequest schema exactly. Regression guard: the
// previous body used the unknown key "public_patterns", which the API decoder
// silently dropped — saving EMPTY rules and leaving the demo tenant deny-all
// under provisioning-only channel authorization.
func TestDefaultChannelRules(t *testing.T) {
	t.Parallel()

	body := defaultChannelRules()

	for _, key := range []string{"public", "default", "publish_public"} {
		raw, ok := body[key]
		if !ok {
			t.Fatalf("defaultChannelRules: missing %q key", key)
		}
		patterns, ok := raw.([]string)
		if !ok || len(patterns) != 1 || patterns[0] != "*" {
			t.Errorf("defaultChannelRules[%q] = %v, want [\"*\"]", key, raw)
		}
	}

	// Regression: the old wrong key must never reappear — the API ignores
	// unknown fields, so it would silently seed a deny-all tenant again.
	if _, present := body["public_patterns"]; present {
		t.Error("defaultChannelRules must not use the unknown \"public_patterns\" key")
	}
	if len(body) != 3 {
		t.Errorf("defaultChannelRules has %d keys, want exactly 3 (public, default, publish_public): %v", len(body), body)
	}
}

// testLicenseKey builds an unverified-decodable license key with the given
// edition — the same payload.signature shape decodeLicenseClaims consumes.
func testLicenseKey(t *testing.T, edition string) string {
	t.Helper()
	payload, err := json.Marshal(licenseClaims{Edition: edition, Org: "Test Org"})
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString([]byte("fake-signature"))
}

// TestGatewayPushEnabled pins the boot-time gateway push decision (the sukko#244
// GATEWAY_PUSH_ENABLED companion): the gateway's push surface is enabled at `up`
// only when the effective license decodes to a push-capable edition AND the
// backend is kafka-family — the same condition the push-service reconciliation
// uses, so the gateway routes and the service they proxy to come up together.
func TestGatewayPushEnabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		key     string
		backend string
		want    bool
	}{
		{"pro + kafka", testLicenseKey(t, "pro"), backendKafka, true},
		{"enterprise + redpanda", testLicenseKey(t, "enterprise"), backendRedpanda, true},
		{"pro + direct (empty backend)", testLicenseKey(t, "pro"), "", false},
		{"community + kafka", testLicenseKey(t, "community"), backendKafka, false},
		{"no license + kafka", "", backendKafka, false},
		{"garbage license + kafka", "not-a-license", backendKafka, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := gatewayPushEnabled(tt.key, tt.backend); got != tt.want {
				t.Errorf("gatewayPushEnabled(%q backend, license %q) = %v, want %v", tt.backend, tt.name, got, tt.want)
			}
		})
	}
}

// TestApplyGatewayPushEnv pins the wiring semantics both ways: enabled sets the
// env to "true"; disabled leaves the key ABSENT (not "false") so the compose
// default — and an operator's own exported value — still apply.
func TestApplyGatewayPushEnv(t *testing.T) {
	t.Parallel()

	enabled := map[string]string{}
	applyGatewayPushEnv(enabled, testLicenseKey(t, "pro"), backendKafka)
	if got := enabled["GATEWAY_PUSH_ENABLED"]; got != "true" {
		t.Errorf("enabled case: env = %q, want true", got)
	}

	disabled := map[string]string{}
	applyGatewayPushEnv(disabled, testLicenseKey(t, "community"), backendKafka)
	if _, present := disabled["GATEWAY_PUSH_ENABLED"]; present {
		t.Error("disabled case: key must be absent, not set to false")
	}
}

// TestReconcileTracingEnv pins the Pro-gating of distributed tracing at `up`: ws-server fatals
// at boot if OTEL_TRACING_ENABLED=true on a non-Pro license, so `up` drops the flag and warns
// (graceful) rather than letting the stack fail cryptically. Pro/Enterprise keep it.
func TestReconcileTracingEnv(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		tracingOn   bool
		key         string
		wantEnabled bool   // OTEL_TRACING_ENABLED still "true" afterward
		wantWarn    bool   // a message was printed
		wantPhrase  string // a cause-specific phrase the message must contain (when wantWarn)
	}{
		{"pro keeps tracing", true, testLicenseKey(t, "pro"), true, false, ""},
		{"enterprise keeps tracing", true, testLicenseKey(t, "enterprise"), true, false, ""},
		{"community drops + explains upgrade", true, testLicenseKey(t, "community"), false, true, "does not include it"},
		{"no license drops + explains login", true, "", false, true, "no license is set"},
		{"garbage license drops + explains unreadable", true, "not-a-license", false, true, "could not be read"},
		{"tracing off is untouched", false, testLicenseKey(t, "community"), false, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := map[string]string{}
			if tt.tracingOn {
				env["OTEL_TRACING_ENABLED"] = "true"
			}
			var buf bytes.Buffer
			reconcileTracingEnv(&buf, env, tt.key)

			if got := env["OTEL_TRACING_ENABLED"] == "true"; got != tt.wantEnabled {
				t.Errorf("OTEL_TRACING_ENABLED enabled=%v, want %v", got, tt.wantEnabled)
			}
			if got := buf.Len() > 0; got != tt.wantWarn {
				t.Errorf("warned=%v, want %v (output=%q)", got, tt.wantWarn, buf.String())
			}
			if tt.wantPhrase != "" && !strings.Contains(buf.String(), tt.wantPhrase) {
				t.Errorf("message %q does not contain the cause-specific phrase %q", buf.String(), tt.wantPhrase)
			}
		})
	}
}

// TestExplainBootFailure pins the translation of known service startup fatals (the real strings
// from the platform's license/errors.go + config validation) into actionable operator guidance.
func TestExplainBootFailure(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		logs     string
		wantHint string // substring the explanation must contain
		wantOK   bool
	}{
		{"tracing pro gate", "ws-server-1  | FATAL config validation failed: connection tracing requires pro edition (current: community). Upgrade at https://x", "needs a Pro license", true},
		{"no license", "provisioning-1 | no license key: Community requires a free account-issued license — set SUKKO_LICENSE_KEY (get one at https://x)", "No license is set", true},
		{"invalid license", "provisioning-1 | provisioning requires a valid license: set SUKKO_LICENSE_KEY to an account-issued key", "No valid license", true},
		{"expired", "provisioning-1 | SUKKO_LICENSE_KEY is set but expired and no DB license is present; re-key to boot", "has expired", true},
		{"enterprise gate", "ws-server-1 | audit logging requires enterprise edition (current: pro). Upgrade at https://x", "Enterprise license", true},
		{"unmatched", "ws-server-1 | listening on :8080\nvalkey-1 | Ready to accept connections", "", false},
		{"empty", "", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			msg, ok := explainBootFailure(tc.logs)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v, want %v (msg=%q)", ok, tc.wantOK, msg)
			}
			if tc.wantOK {
				if !strings.Contains(msg, tc.wantHint) {
					t.Errorf("explanation %q missing hint %q", msg, tc.wantHint)
				}
				if !strings.Contains(msg, "failed to start") {
					t.Errorf("explanation %q missing the header + surfaced log line", msg)
				}
			}
		})
	}
}

// TestLocalStackContext verifies that a local-stack command resolves the LOCAL context
// regardless of which context is active — the guarantee that keeps `sukko up` from
// provisioning a remote/prod stack — and reports when a remote context is active.
func TestLocalStackContext(t *testing.T) {
	// Not parallel: mutates package-level resolvedStore/resolvedCtx.
	store, err := clicontext.NewStoreWithDir(t.TempDir() + "/contexts")
	if err != nil {
		t.Fatalf("NewStoreWithDir: %v", err)
	}
	local := &clicontext.Context{Name: "local", Type: clicontext.TypeLocal, ProvisioningURL: "http://localhost:18080"}
	remote := &clicontext.Context{Name: "prod", Type: clicontext.TypeRemote, ProvisioningURL: "https://api.example.com"}
	for _, c := range []*clicontext.Context{local, remote} {
		if err := store.Add(c); err != nil {
			t.Fatalf("seed %s: %v", c.Name, err)
		}
	}

	origStore, origCtx := resolvedStore, resolvedCtx
	t.Cleanup(func() { resolvedStore, resolvedCtx = origStore, origCtx })

	t.Run("no store errors", func(t *testing.T) {
		resolvedStore, resolvedCtx = nil, nil
		if _, _, err := localStackContext(); err == nil {
			t.Error("expected error with no store")
		}
	})

	t.Run("resolves local when a remote context is active", func(t *testing.T) {
		resolvedStore, resolvedCtx = store, remote
		got, activeIsRemote, err := localStackContext()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got == nil || got.Name != "local" {
			t.Errorf("want the local context, got %+v", got)
		}
		if !activeIsRemote {
			t.Error("activeIsRemote should be true when a remote context is active")
		}
	})

	t.Run("local active is not flagged remote", func(t *testing.T) {
		resolvedStore, resolvedCtx = store, local
		got, activeIsRemote, err := localStackContext()
		if err != nil || got == nil || got.Name != "local" {
			t.Fatalf("want local context, got %+v err %v", got, err)
		}
		if activeIsRemote {
			t.Error("activeIsRemote should be false when the local context is active")
		}
	})

	t.Run("no active context still resolves local, not flagged", func(t *testing.T) {
		resolvedStore, resolvedCtx = store, nil
		got, activeIsRemote, err := localStackContext()
		if err != nil || got == nil || got.Name != "local" {
			t.Fatalf("want local context, got %+v err %v", got, err)
		}
		if activeIsRemote {
			t.Error("activeIsRemote should be false when no context is active")
		}
	})

	t.Run("no local context errors", func(t *testing.T) {
		emptyStore, err := clicontext.NewStoreWithDir(t.TempDir() + "/empty")
		if err != nil {
			t.Fatalf("NewStoreWithDir: %v", err)
		}
		resolvedStore, resolvedCtx = emptyStore, remote
		if _, _, err := localStackContext(); err == nil {
			t.Error("expected error when no local context exists")
		}
	})
}

// TestUseLocalStackContext proves that pointing a local-stack command at the local stack
// actually reassigns the globals the rest of runUp reads: the provisioning URL and admin key
// path resolve to the local context even when a remote context is active, a note is printed,
// and a non-loopback --api-url override is refused (it must not redirect `up` off-box).
func TestUseLocalStackContext(t *testing.T) {
	// Not parallel: mutates package-level globals (resolvedStore/resolvedCtx/apiURL).
	store, err := clicontext.NewStoreWithDir(t.TempDir() + "/contexts")
	if err != nil {
		t.Fatalf("NewStoreWithDir: %v", err)
	}
	local := &clicontext.Context{Name: "local", Type: clicontext.TypeLocal, ProvisioningURL: "http://localhost:18080"}
	remote := &clicontext.Context{Name: "prod", Type: clicontext.TypeRemote, ProvisioningURL: "https://api.example.com"}
	for _, c := range []*clicontext.Context{local, remote} {
		if err := store.Add(c); err != nil {
			t.Fatalf("seed %s: %v", c.Name, err)
		}
	}
	// A file at <store>/local/admin.key so resolveAdminKeyPath resolves to the local key.
	if err := os.MkdirAll(filepath.Join(store.Dir(), "local"), 0o755); err != nil {
		t.Fatalf("mkdir local key dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(store.Dir(), "local", "admin.key"), []byte("x"), 0o600); err != nil {
		t.Fatalf("seed admin key: %v", err)
	}

	origStore, origCtx, origAPI := resolvedStore, resolvedCtx, apiURL
	t.Cleanup(func() { resolvedStore, resolvedCtx, apiURL = origStore, origCtx, origAPI })

	t.Run("reassigns globals to local when a remote context is active", func(t *testing.T) {
		resolvedStore, resolvedCtx, apiURL = store, remote, ""
		var buf bytes.Buffer
		if err := useLocalStackContext(&buf); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resolvedCtx == nil || resolvedCtx.Name != "local" {
			t.Fatalf("resolvedCtx should be the local context, got %+v", resolvedCtx)
		}
		if got := resolveProvisioningURL(); got != "http://localhost:18080" {
			t.Errorf("provisioning URL = %q, want the local URL", got)
		}
		if got := resolveAdminKeyPath(); !strings.HasSuffix(got, filepath.Join("local", "admin.key")) {
			t.Errorf("admin key path = %q, want it under the local context dir", got)
		}
		if !strings.Contains(buf.String(), "not the local context") {
			t.Errorf("expected a note about the active context, got %q", buf.String())
		}
	})

	t.Run("refuses a non-loopback --api-url override", func(t *testing.T) {
		resolvedStore, resolvedCtx, apiURL = store, local, "https://api.prod.example"
		err := useLocalStackContext(&bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "--api-url") {
			t.Errorf("non-loopback --api-url should be refused, got %v", err)
		}
	})

	t.Run("allows a loopback --api-url override", func(t *testing.T) {
		resolvedStore, resolvedCtx, apiURL = store, local, "http://localhost:19999"
		if err := useLocalStackContext(&bytes.Buffer{}); err != nil {
			t.Errorf("loopback --api-url should be allowed, got %v", err)
		}
	})
}

// TestLocalStackProbeURLs verifies status probes the local context (or loopback defaults),
// never the active context.
func TestLocalStackProbeURLs(t *testing.T) {
	store, err := clicontext.NewStoreWithDir(t.TempDir() + "/contexts")
	if err != nil {
		t.Fatalf("NewStoreWithDir: %v", err)
	}
	local := &clicontext.Context{Name: "local", Type: clicontext.TypeLocal, ProvisioningURL: "http://localhost:18080", GatewayURL: "ws://localhost:13000"}
	remote := &clicontext.Context{Name: "prod", Type: clicontext.TypeRemote, ProvisioningURL: "https://api.example.com", GatewayURL: "wss://ws.example.com"}
	for _, c := range []*clicontext.Context{local, remote} {
		if err := store.Add(c); err != nil {
			t.Fatalf("seed %s: %v", c.Name, err)
		}
	}
	origStore, origCtx := resolvedStore, resolvedCtx
	t.Cleanup(func() { resolvedStore, resolvedCtx = origStore, origCtx })

	t.Run("remote active still probes local URLs", func(t *testing.T) {
		resolvedStore, resolvedCtx = store, remote
		prov, gw, _ := localStackProbeURLs()
		if prov != "http://localhost:18080" || gw != "http://localhost:13000" {
			t.Errorf("probe URLs = %q,%q; want the local context's, not prod", prov, gw)
		}
	})

	t.Run("no store falls back to defaults", func(t *testing.T) {
		resolvedStore, resolvedCtx = nil, nil
		prov, _, _ := localStackProbeURLs()
		if prov != defaultAPIURL {
			t.Errorf("probe prov URL = %q, want default %q", prov, defaultAPIURL)
		}
	})
}
