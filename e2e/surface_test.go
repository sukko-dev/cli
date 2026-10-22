//go:build e2e

package e2e

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// TestProvisioningSurface exercises the provisioning command surface Phase 2a left
// uncovered — API keys, signing keys + token generation/validation, routing & channel
// rules, and topics — all against a single Community stack on the auto-provisioned `demo`
// tenant. Each area is a subtest sharing the one boot; the subtests run sequentially and
// use distinct names, so they don't collide. Every operation here is Community-usable: no
// `--egress-topics` (Pro-gated), and well under the Community topic/tenant caps.
func TestProvisioningSurface(t *testing.T) {
	c, _ := bootCommunityStack(t)
	const tenant = "demo"

	// API keys: create → list (present, active) → revoke → list (inactive). The full
	// key_id (secret) is only returned on creation, so it is captured there.
	t.Run("api-keys", func(t *testing.T) {
		c := c.sub(t) // rebind failures to this subtest; the booted stack is shared
		var created map[string]any
		mustJSON(t, c.run("api-keys", "create", "--tenant", tenant, "--name", "e2e-api-key", "-o", "json"), &created)
		keyID, _ := created["key_id"].(string)
		if keyID == "" {
			t.Fatalf("api-keys create returned no key_id: %v", created)
		}
		if created["name"] != "e2e-api-key" {
			t.Errorf("api-keys create name = %v, want e2e-api-key", created["name"])
		}

		if got := findAPIKey(t, c.run("api-keys", "list", "--tenant", tenant, "-o", "json"), keyID); got == nil {
			t.Fatalf("created key %s absent from api-keys list", keyID)
		} else if active, _ := got["is_active"].(bool); !active {
			t.Errorf("new api key is_active = false, want true")
		}

		c.run("api-keys", "revoke", "--tenant", tenant, "--key-id", keyID)
		if got := findAPIKey(t, c.run("api-keys", "list", "--tenant", tenant, "-o", "json"), keyID); got != nil {
			if active, _ := got["is_active"].(bool); active {
				t.Errorf("revoked api key still is_active = true")
			}
		}
	})

	// Signing keys + tokens: register a tenant keypair, then mint and validate a JWT.
	// `token generate` signs locally with the key `keys create --generate` saved under the
	// isolated HOME, so it never calls the server.
	t.Run("keys-and-token", func(t *testing.T) {
		c := c.sub(t)
		c.run("keys", "create", "--generate", "--tenant", tenant)

		out := c.run("token", "generate", "--tenant", tenant, "--sub", "e2e-user")
		jwt := jwtRe.FindString(out)
		if jwt == "" {
			t.Fatalf("token generate produced no JWT:\n%s", out)
		}
		c.run("token", "validate", jwt)
	})

	// Routing & channel rules: add a routing rule (no --egress-topics — that is
	// Pro-gated) with a pattern/priority distinct from the `sukko up` catch-all
	// (`**`/100), and read it back; channel rules are seeded on demo, so get must return
	// them.
	t.Run("rules", func(t *testing.T) {
		c := c.sub(t)
		c.run("rules", "routing", "add",
			"--tenant", tenant, "--pattern", "e2e.surface", "--ingress-topic", "default", "--priority", "900")
		if got := c.run("rules", "routing", "get", "--tenant", tenant); !strings.Contains(got, "e2e.surface") {
			t.Errorf("routing get missing the added rule:\n%s", got)
		}

		if got := c.run("rules", "channels", "get", "--tenant", tenant); strings.TrimSpace(got) == "" {
			t.Errorf("channels get returned nothing")
		}
	})

	// Topics: create → list (present) → delete → list (absent). The topic is not
	// referenced by a routing rule, so delete is clean.
	t.Run("topics", func(t *testing.T) {
		c := c.sub(t)
		const suffix = "e2e-analytics"
		c.run("topics", "create", "--tenant", tenant, "--suffix", suffix)
		if got := c.run("topics", "list", "--tenant", tenant); !strings.Contains(got, suffix) {
			t.Errorf("topics list missing created topic %q:\n%s", suffix, got)
		}
		c.run("topics", "delete", "--tenant", tenant, "--suffix", suffix)
		if got := c.run("topics", "list", "--tenant", tenant); strings.Contains(got, suffix) {
			t.Errorf("topics list still shows deleted topic %q:\n%s", suffix, got)
		}
	})
}

// jwtRe matches a compact JWS (three base64url segments). `token generate` writes the JWT
// to stdout and a `jti:` line to stderr, which the harness merges — this isolates the JWT.
var jwtRe = regexp.MustCompile(`[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)

// mustJSON unmarshals the JSON object in s into v, failing the test with the raw output on
// error. It extracts the outermost `{…}` first, so any log line the CLI merges onto the
// stream alongside the JSON does not break the parse.
func mustJSON(t *testing.T, s string, v any) {
	t.Helper()
	i, j := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if i < 0 || j < i {
		t.Fatalf("no JSON object in output:\n%s", s)
	}
	if err := json.Unmarshal([]byte(s[i:j+1]), v); err != nil {
		t.Fatalf("output is not valid JSON (%v):\n%s", err, s)
	}
}

// findAPIKey parses a PaginatedAPIKeys list and returns the item with the given key_id, or
// nil if absent.
func findAPIKey(t *testing.T, listJSON, keyID string) map[string]any {
	t.Helper()
	var page struct {
		Items []map[string]any `json:"items"`
	}
	mustJSON(t, listJSON, &page)
	for _, it := range page.Items {
		if id, _ := it["key_id"].(string); id == keyID {
			return it
		}
	}
	return nil
}
