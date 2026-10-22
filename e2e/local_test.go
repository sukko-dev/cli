//go:build e2e

package e2e

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestOfflineCommands exercises the CLI commands that work without a running stack — the
// local context store, static config defaults, shell completion, and admin keypair
// management. No Docker is required; each subtest gets its own isolated HOME via newCLI so
// its context store is clean and independent.
func TestOfflineCommands(t *testing.T) {
	// context: add → list (present) → use → current (active) → remove → list (absent).
	t.Run("context", func(t *testing.T) {
		c := newCLI(t)
		const name = "e2e-ctx"
		c.run("context", "add", name,
			"--provisioning-url", "http://localhost:8080", "--gateway-url", "ws://localhost:3000")
		if got := c.run("context", "list"); !strings.Contains(got, name) {
			t.Errorf("context list missing %q:\n%s", name, got)
		}
		c.run("context", "use", name)
		if got := c.run("context", "current"); !strings.Contains(got, name) {
			t.Errorf("context current = %q, want it to name %q", got, name)
		}
		c.run("context", "remove", name)
		if got := c.run("context", "list"); strings.Contains(got, name) {
			t.Errorf("context list still shows removed %q:\n%s", name, got)
		}
	})

	// config defaults is a static table of env-var defaults — no server needed. It must
	// produce output and be valid JSON in json format.
	t.Run("config-defaults", func(t *testing.T) {
		c := newCLI(t)
		if got := strings.TrimSpace(c.run("config", "defaults")); got == "" {
			t.Error("config defaults produced no output")
		}
		var parsed any
		if err := json.Unmarshal([]byte(c.run("config", "defaults", "--format", "json")), &parsed); err != nil {
			t.Errorf("config defaults --format json is not valid JSON: %v", err)
		}
	})

	// completion emits a shell script for the named shell.
	t.Run("completion", func(t *testing.T) {
		c := newCLI(t)
		if got := c.run("completion", "bash"); !strings.Contains(got, "complete") {
			t.Errorf("completion bash does not look like a bash completion script:\n%s", got)
		}
		c.run("completion", "zsh") // exits 0
	})

	// auth keygen generates an admin keypair in the active context; auth pubkey recovers
	// the public key. keygen needs an active context and refuses to overwrite, so start
	// from a fresh context (not `init`, which pre-generates a keypair).
	t.Run("auth-keygen", func(t *testing.T) {
		c := newCLI(t)
		c.run("context", "add", "e2e-auth", "--provisioning-url", "http://localhost:8080")
		c.run("context", "use", "e2e-auth")
		if got := c.run("auth", "keygen"); !strings.Contains(got, "Keypair generated") {
			t.Errorf("auth keygen did not report a generated keypair:\n%s", got)
		}
		if got := strings.TrimSpace(c.run("auth", "pubkey")); got == "" {
			t.Error("auth pubkey printed nothing after keygen")
		}
	})
}

// TestLogs boots a Community stack and reads container logs through the CLI — proving
// `sukko logs` discovers the running compose project and returns output (non-follow, so it
// exits). It owns its stack and is not parallel.
func TestLogs(t *testing.T) {
	c, _ := bootCommunityStack(t)
	if got := strings.TrimSpace(c.run("logs", "provisioning")); got == "" {
		t.Error("sukko logs provisioning returned no output for a running stack")
	}
}
