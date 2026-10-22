//go:build e2e

package e2e

import (
	"os"
	"strings"
	"testing"
)

// TestWebhooks exercises the Pro-gated webhook surface from both sides of the edition
// gate: a Pro stack performs the full CRUD lifecycle, and a Community stack is refused with
// EDITION_LIMIT. Webhooks are a Pro+ feature enforced server-side (the CLI has no local
// gate), so this proves the gate end to end. Each subtest owns its own stack.
func TestWebhooks(t *testing.T) {
	const tenant = "demo"
	const hookURL = "https://example.invalid/hook" // https + domain: provisioning validates the scheme (WEBHOOK_ALLOW_HTTP=false) and never dials it on create

	// Pro: create → list (present) → get → update (suspend) → delete → list (absent).
	t.Run("pro-crud", func(t *testing.T) {
		c, _ := bootStack(t, os.Getenv("SUKKO_E2E_PRO_KEY"))

		var created map[string]any
		mustJSON(t, c.run("webhook", "create",
			"--tenant", tenant, "--url", hookURL,
			"--channel-pattern", "orders.*", "--secret", "e2e-webhook-secret", "-o", "json"), &created)
		id, _ := created["id"].(string)
		if id == "" {
			t.Fatalf("webhook create returned no id: %v", created)
		}

		if got := c.run("webhook", "list", "--tenant", tenant, "-o", "json"); !strings.Contains(got, id) {
			t.Errorf("created webhook %s absent from list:\n%s", id, got)
		}

		var got map[string]any
		mustJSON(t, c.run("webhook", "get", "--tenant", tenant, "--webhook-id", id, "-o", "json"), &got)
		if got["url"] != hookURL {
			t.Errorf("webhook get url = %v, want %s", got["url"], hookURL)
		}

		// Partial update: suspend it and read the new status back.
		var updated map[string]any
		mustJSON(t, c.run("webhook", "update", "--tenant", tenant, "--webhook-id", id, "--status", "suspended", "-o", "json"), &updated)
		if updated["status"] != "suspended" {
			t.Errorf("after update status = %v, want suspended", updated["status"])
		}

		c.run("webhook", "delete", "--tenant", tenant, "--webhook-id", id)
		if got := c.run("webhook", "list", "--tenant", tenant, "-o", "json"); strings.Contains(got, id) {
			t.Errorf("deleted webhook %s still present in list:\n%s", id, got)
		}
	})

	// Community: the same create is refused at the edition gate — and specifically with
	// EDITION_LIMIT, so we're discriminating on the cause, not just any failure.
	t.Run("community-gated", func(t *testing.T) {
		c, _ := bootCommunityStack(t)
		out, err := c.runErr("webhook", "create",
			"--tenant", tenant, "--url", hookURL,
			"--channel-pattern", "orders.*", "--secret", "e2e-webhook-secret")
		if err == nil {
			t.Fatalf("webhook create succeeded on a Community stack — the Pro gate is not enforced:\n%s", out)
		}
		if !strings.Contains(out, "EDITION_LIMIT") {
			t.Errorf("webhook create on Community failed without EDITION_LIMIT (wrong cause?):\n%s", out)
		}
	})
}
