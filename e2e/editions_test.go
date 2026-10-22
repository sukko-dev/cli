//go:build e2e

package e2e

import (
	"os"
	"strings"
	"testing"
)

// TestEditions boots a stack at each paid edition with a dev-minted key and asserts the
// CLI reports it — proving the edition-gating pipeline end to end: a dev-signed Pro /
// Enterprise key → the sukko_e2e services verify it and boot at that edition → `sukko
// edition` reflects it. (Community is covered by TestLifecycle.) The subtests are
// sequential: each owns its own docker-compose stack.
func TestEditions(t *testing.T) {
	cases := []struct {
		name   string
		envKey string // run.sh exports a dev key per edition
		want   string
	}{
		{"pro", "SUKKO_E2E_PRO_KEY", "pro"},
		{"enterprise", "SUKKO_E2E_ENT_KEY", "enterprise"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, upOut := bootStack(t, os.Getenv(tc.envKey))

			// The idempotent license re-push fix (platform v1.0.8) must hold at every
			// edition — a Pro/Enterprise boot must not warn on the reconcile push either.
			for _, bad := range []string{"REPLAY_DETECTED", "license push failed"} {
				if strings.Contains(upOut, bad) {
					t.Errorf("`sukko up` emitted %q at edition %s:\n%s", bad, tc.want, upOut)
				}
			}

			if ed := strings.ToLower(c.run("edition")); !strings.Contains(ed, tc.want) {
				t.Errorf("edition = %q, want %s", ed, tc.want)
			}
		})
	}
}
