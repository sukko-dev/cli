package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// chdir changes the working directory for the duration of a test.
// Must NOT be combined with t.Parallel() — os.Chdir is global state.
func chdir(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
}

func TestLoadProjectConfig_NotFound(t *testing.T) {
	chdir(t, t.TempDir())

	cfg, err := loadProjectConfig()
	if err != nil {
		t.Fatalf("expected nil error for missing config, got: %v", err)
	}
	if cfg != nil {
		t.Errorf("expected nil config for missing file, got: %+v", cfg)
	}
}

func TestLoadProjectConfig_CorruptJSON(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	configDir := filepath.Join(dir, sukkoConfigDir)
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, sukkoConfigFile), []byte("not-valid-json{{{"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := loadProjectConfig()
	if err == nil {
		t.Fatal("expected error for corrupt JSON")
	}
}

func TestLoadProjectConfig_Valid(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	configDir := filepath.Join(dir, sukkoConfigDir)
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	want := ProjectConfig{
		Database:       "postgres",
		Broadcast:      "valkey",
		MessageBackend: "kafka",
	}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, sukkoConfigFile), data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := loadProjectConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil config")
	}
	if *got != want {
		t.Errorf("config = %+v, want %+v", *got, want)
	}
}

func TestProjectConfig_BackwardCompat(t *testing.T) {
	t.Parallel()

	// Existing config without observability fields should unmarshal to false
	oldJSON := `{"database":"sqlite","broadcast":"valkey","message_backend":"direct"}`

	var cfg ProjectConfig
	if err := json.Unmarshal([]byte(oldJSON), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if cfg.Observability {
		t.Error("Observability should be false for old config")
	}
	if cfg.Tracing {
		t.Error("Tracing should be false for old config")
	}
	if cfg.Profiling {
		t.Error("Profiling should be false for old config")
	}
	if cfg.Database != "sqlite" {
		t.Errorf("Database = %q, want sqlite", cfg.Database)
	}
}

func TestProjectConfig_Roundtrip(t *testing.T) {
	t.Parallel()

	cfg := ProjectConfig{
		Database:       "postgres",
		Broadcast:      "valkey",
		MessageBackend: "kafka",
		Observability:  true,
		Tracing:        true,
		Profiling:      false,
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got ProjectConfig
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got != cfg {
		t.Errorf("roundtrip mismatch: got %+v, want %+v", got, cfg)
	}
}

func TestProjectConfig_OmitEmpty(t *testing.T) {
	t.Parallel()

	// When observability fields are false, omitempty should omit them
	cfg := ProjectConfig{
		Database:       "sqlite",
		Broadcast:      "valkey",
		MessageBackend: "direct",
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	jsonStr := string(data)
	for _, field := range []string{"observability", "tracing", "profiling"} {
		if strings.Contains(jsonStr, field) {
			t.Errorf("expected %q to be omitted from JSON, got: %s", field, jsonStr)
		}
	}
}
