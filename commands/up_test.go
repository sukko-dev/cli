package commands

import (
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

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
