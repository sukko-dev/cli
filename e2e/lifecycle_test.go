//go:build e2e

package e2e

import (
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
	// Boot a licensed Community stack (init --defaults, license set, up). Teardown is
	// registered inside the helper.
	c, upOut := bootCommunityStack(t)

	// REGRESSION GUARD: `sukko up` re-pushes the boot-loaded license to reconcile the
	// push-service; before the idempotent-reload fix (platform v1.0.8) the replay check
	// rejected the identical key with REPLAY_DETECTED and aborted the reconcile. This
	// asserts `up` is now clean; it reproduced the bug end to end before the fix.
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
