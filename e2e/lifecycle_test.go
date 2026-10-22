//go:build e2e

package e2e

import (
	"os"
	"strings"
	"testing"
)

// TestLifecycle is the Phase-1 walking skeleton: it drives the core local-dev loop end
// to end against a real stack — init, set the dev license, boot, inspect, tear down —
// and guards the `sukko up` license-push regression.
//
// It is NOT parallel: it owns a docker-compose stack. t.Cleanup always tears the stack
// down, even on a failed assertion, so a crash never leaves containers running.
func TestLifecycle(t *testing.T) {
	requireDocker(t)
	requireLicenseKey(t)

	c := newCLI(t)
	t.Cleanup(func() {
		// Best-effort teardown including the per-test volume (-v), so the next run gets
		// a clean DB and no stack is left running on a failed assertion.
		if out, err := c.runErr("down", "-v"); err != nil {
			t.Logf("sukko down -v (cleanup) failed: %v\n%s", err, out)
		}
	})

	// 1. Initialize a local project (SQLite + Valkey + direct messaging).
	initOut := c.run("init", "--defaults")
	if !strings.Contains(initOut, ".sukko") {
		t.Errorf("init output did not mention the .sukko config:\n%s", initOut)
	}

	// 2. Set the dev-minted Community license into the active context.
	c.run("license", "set", os.Getenv("SUKKO_LICENSE_KEY"))

	// 3. Boot the stack. This pulls nothing (local sukko_e2e images satisfy the compose
	//    refs) and waits for health.
	upOut := c.up()

	// REGRESSION GUARD (parked bug replay-idempotent-reload): `sukko up` re-pushes the
	// boot-loaded license to reconcile the push-service; the platform's replay check
	// rejects the identical key with REPLAY_DETECTED. This assertion FAILS until the
	// platform idempotent-reload fix ships — it is the e2e's first proven catch.
	for _, bad := range []string{"REPLAY_DETECTED", "license push failed", "replay rejected"} {
		if strings.Contains(upOut, bad) {
			t.Errorf("`sukko up` emitted a license-push replay warning (%q) — the reconcile push collides with the boot-loaded key:\n%s", bad, upOut)
		}
	}

	// 4. Services are up and licensed.
	health := c.run("health")
	if !containsAny(strings.ToLower(health), "healthy", "ok", "ready") {
		t.Errorf("health did not report healthy:\n%s", health)
	}

	// 5. Edition reports Community (the dev key's edition), not an empty/failed license.
	edition := c.run("edition")
	if !strings.Contains(strings.ToLower(edition), "community") {
		t.Errorf("edition did not report community:\n%s", edition)
	}

	// 6. status and version run cleanly against the live stack.
	c.run("status")
	version := c.run("version")
	if strings.TrimSpace(version) == "" {
		t.Error("version produced no output")
	}

	// 7. Teardown is exercised explicitly (and again, defensively, in t.Cleanup).
	c.run("down")
}

// containsAny reports whether s contains any of subs (used for tolerant output matching
// while the exact human-facing strings are calibrated on the first real run).
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
