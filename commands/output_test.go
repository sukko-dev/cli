package commands

import (
	"testing"
)

func TestAsMap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    any
		wantLen  int
		wantNone bool
	}{
		{
			name:    "valid map",
			input:   map[string]any{"key": "value", "num": 42},
			wantLen: 2,
		},
		{
			name:     "non-map returns empty",
			input:    "string-value",
			wantLen:  0,
			wantNone: true,
		},
		{
			name:     "nil returns empty",
			input:    nil,
			wantLen:  0,
			wantNone: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := asMap(tt.input)
			if len(result) != tt.wantLen {
				t.Errorf("len(asMap()) = %d, want %d", len(result), tt.wantLen)
			}
		})
	}
}

func TestAsStr(t *testing.T) {
	t.Parallel()

	m := map[string]any{
		"name":   "test-tenant",
		"count":  42,
		"active": true,
	}

	tests := []struct {
		name string
		key  string
		want string
	}{
		{"string value", "name", "test-tenant"},
		{"int value", "count", "42"},
		{"bool value", "active", "true"},
		{"missing key", "nonexistent", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := asStr(m, tt.key)
			if got != tt.want {
				t.Errorf("asStr(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

func TestEnvOrDefault(t *testing.T) {
	// Not parallel: subtest uses t.Setenv which modifies the environment.

	t.Run("returns default when env not set", func(t *testing.T) { //nolint:paralleltest // subtests use t.Setenv which is incompatible with t.Parallel()
		result := envOrDefault("SUKKO_TEST_NONEXISTENT_KEY_XYZ", "fallback")
		if result != "fallback" {
			t.Errorf("envOrDefault() = %q, want %q", result, "fallback")
		}
	})

	t.Run("returns env when set", func(t *testing.T) {
		t.Setenv("SUKKO_TEST_CLI_ENV_VAR", "from-env")
		result := envOrDefault("SUKKO_TEST_CLI_ENV_VAR", "fallback")
		if result != "from-env" {
			t.Errorf("envOrDefault() = %q, want %q", result, "from-env")
		}
	})
}
