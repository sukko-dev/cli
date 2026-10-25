//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRemoteTLSEnforcement proves the CLI refuses to send credentials over plaintext to a
// non-loopback remote, at both boundaries (§IX): it blocks creating such a context, and it
// refuses to operate one that was planted by hand-editing the store (§II defense in depth).
// Loopback plaintext (a dev stack / port-forward) stays allowed. No Docker is required —
// every case fails (or passes) before any network dial.
func TestRemoteTLSEnforcement(t *testing.T) {
	t.Run("create rejects non-loopback plaintext", func(t *testing.T) {
		c := newCLI(t)
		out, err := c.runErr("context", "add", "prod-insecure",
			"--provisioning-url", "http://api.example.com",
			"--gateway-url", "wss://ws.example.com")
		if err == nil {
			t.Fatalf("context add with plaintext remote URL should have failed:\n%s", out)
		}
		if !strings.Contains(out, "cleartext") && !strings.Contains(out, "https") {
			t.Errorf("error should explain the TLS requirement, got:\n%s", out)
		}
		// Nothing should have been persisted.
		if list := c.run("context", "list"); strings.Contains(list, "prod-insecure") {
			t.Errorf("rejected context must not be saved:\n%s", list)
		}
	})

	t.Run("create allows https/wss remote", func(t *testing.T) {
		c := newCLI(t)
		c.run("context", "add", "prod-secure",
			"--provisioning-url", "https://api.example.com",
			"--gateway-url", "wss://ws.example.com")
		if list := c.run("context", "list"); !strings.Contains(list, "prod-secure") {
			t.Errorf("valid remote context should be saved:\n%s", list)
		}
	})

	t.Run("create allows loopback plaintext (tunnel/port-forward)", func(t *testing.T) {
		c := newCLI(t)
		c.run("context", "add", "tunnel",
			"--provisioning-url", "http://localhost:18080",
			"--gateway-url", "ws://127.0.0.1:13000")
		if list := c.run("context", "list"); !strings.Contains(list, "tunnel") {
			t.Errorf("loopback plaintext context should be allowed:\n%s", list)
		}
	})

	t.Run("dial seam refuses a --gateway-url override to a plaintext remote", func(t *testing.T) {
		// The real operator path: a stale env var or a pasted flag pointing off-box. The
		// override never touches a stored context, so only the dial-seam check catches it.
		c := newCLI(t)
		out, err := c.runErr("publish", "demo.x", "{}", "--gateway-url", "ws://ws.example.invalid:3000")
		if err == nil {
			t.Fatalf("publish to a plaintext remote gateway should be refused:\n%s", out)
		}
		if !strings.Contains(out, "cleartext") {
			t.Errorf("error should explain the cleartext refusal, got:\n%s", out)
		}
	})

	t.Run("provisioning seam refuses an --api-url override (admin JWT path)", func(t *testing.T) {
		// The admin-credential path: a context with a keypair, then a flag pointing the
		// provisioning API off-box over http. The signed admin JWT must not be dialed out.
		c := newCLI(t)
		c.run("context", "add", "lb", "--provisioning-url", "http://localhost:18080")
		c.run("context", "use", "lb")
		c.run("auth", "keygen")
		out, err := c.runErr("tenant", "list", "--api-url", "http://api.example.invalid:8080")
		if err == nil {
			t.Fatalf("tenant list with a plaintext --api-url override should be refused:\n%s", out)
		}
		if !strings.Contains(out, "cleartext") {
			t.Errorf("error should explain the cleartext refusal, got:\n%s", out)
		}
	})

	t.Run("use-time refuses a hand-edited plaintext remote even if Type is flipped to local", func(t *testing.T) {
		c := newCLI(t)
		// Create a valid remote context via the CLI (so it lands in the real store path for
		// this OS), then hand-edit the file to plaintext AND flip type->local — the one-field
		// bypass. The gate is by URL, not Type, so a dialing command must still refuse.
		c.run("context", "add", "handedited",
			"--provisioning-url", "https://api.example.com",
			"--gateway-url", "wss://ws.example.com")

		path := findContextFile(t, c.home, "handedited.json")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read context file: %v", err)
		}
		edited := strings.NewReplacer(
			"https://api.example.com", "http://api.example.com",
			`"type": "remote"`, `"type": "local"`,
		).Replace(string(data))
		if edited == string(data) {
			t.Fatalf("expected to rewrite the context file:\n%s", data)
		}
		if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
			t.Fatalf("write context file: %v", err)
		}

		// Selecting the context is a `context` subcommand, so it is allowed (the operator
		// must be able to switch to and fix it). The next command that dials must refuse —
		// the failure is the TLS refusal, not a connection error.
		c.run("context", "use", "handedited")
		out, err := c.runErr("edition")
		if err == nil {
			t.Fatalf("a command under a plaintext-remote context should be refused:\n%s", out)
		}
		if !strings.Contains(out, "cleartext") {
			t.Errorf("error should explain the cleartext refusal, got:\n%s", out)
		}
	})

	t.Run("init refuses a remote plaintext SUKKO_API_URL", func(t *testing.T) {
		// init seeds the local context from SUKKO_API_URL/SUKKO_GATEWAY_URL; pointing those
		// off-box over http must be refused at the persistence boundary, not silently stored.
		c := newCLI(t)
		c.env = append(c.env,
			"SUKKO_API_URL=http://devbox.example.invalid:18080",
			"SUKKO_GATEWAY_URL=ws://devbox.example.invalid:13000",
		)
		out, err := c.runErr("init", "--defaults")
		if err == nil {
			t.Fatalf("init with a remote plaintext SUKKO_API_URL should be refused:\n%s", out)
		}
		if !strings.Contains(out, "cleartext") {
			t.Errorf("error should explain the cleartext refusal, got:\n%s", out)
		}
	})
}

// findContextFile locates a context store file by name anywhere under the isolated HOME,
// so the test does not depend on the per-OS layout of os.UserConfigDir.
func findContextFile(t *testing.T, home, name string) string {
	t.Helper()
	var found string
	if err := filepath.WalkDir(home, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == name {
			found = p
		}
		return nil
	}); err != nil {
		t.Fatalf("walk %s: %v", home, err)
	}
	if found == "" {
		t.Fatalf("context file %q not found under %s", name, home)
	}
	return found
}
