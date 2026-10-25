//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

// TestRemoteMutationConfirmation proves a state-changing command against a remote context is
// gated: non-interactively (as in CI) it fails fast and points at --yes rather than hanging
// on a prompt, and --yes lets it through. No Docker and no network: the context points at an
// unresolvable host and the command stops at the confirmation (or, with --yes, at the
// missing admin keypair) before dialing. The local case (not gated) is covered by the unit
// test TestConfirmRemoteMutation.
func TestRemoteMutationConfirmation(t *testing.T) {
	newRemoteCLI := func(t *testing.T) *cli {
		c := newCLI(t)
		c.run("context", "add", "prod",
			"--provisioning-url", "https://api.example.invalid",
			"--gateway-url", "wss://ws.example.invalid")
		c.run("context", "use", "prod")
		return c
	}

	t.Run("non-interactive mutation without --yes fails fast", func(t *testing.T) {
		c := newRemoteCLI(t)
		out, err := c.runErr("tenant", "delete", "some-tenant")
		if err == nil {
			t.Fatalf("a remote mutation without --yes should be refused:\n%s", out)
		}
		if !strings.Contains(out, "--yes") {
			t.Errorf("error should point the operator at --yes, got:\n%s", out)
		}
	})

	t.Run("--yes bypasses the confirmation", func(t *testing.T) {
		c := newRemoteCLI(t)
		// With --yes the confirmation is skipped; the command proceeds and stops at the
		// missing admin keypair, NOT at the confirmation gate.
		out, err := c.runErr("tenant", "delete", "some-tenant", "--yes")
		if err == nil {
			t.Fatalf("expected a later error (no keypair), got success:\n%s", out)
		}
		if strings.Contains(out, "without confirmation") {
			t.Errorf("--yes should have bypassed the confirmation, got:\n%s", out)
		}
		// Prove it got all the way past the gate to the expected next failure.
		if !strings.Contains(out, "admin keypair") {
			t.Errorf("expected the command to proceed to the missing-keypair error, got:\n%s", out)
		}
	})
}
