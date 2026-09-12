package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAuthPubkey covers retrieving the admin public key AFTER generation. The value is
// required to bootstrap every deployment (ADMIN_BOOTSTRAP_KEY / helm
// provisioning.adminBootstrapKey), but `auth keygen` prints it only once, at creation, and
// refuses to re-run — so without this command the only way to recover it is reading a
// platform-specific file path by hand.
func TestAuthPubkey(t *testing.T) {
	const key = "voKFbYdGTzNtSbg8kuBPtESTONLYtESTONLYtESTONLY="

	t.Run("prints the stored key bare, so it is pipeable", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("HOME", dir)
		if err := os.MkdirAll(filepath.Join(dir, ".sukko"), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		// keygen writes the key with a trailing newline.
		if err := os.WriteFile(filepath.Join(dir, ".sukko", "admin.pub"), []byte(key+"\n"), 0o600); err != nil {
			t.Fatalf("write pub: %v", err)
		}

		var out bytes.Buffer
		authPubkeyCmd.SetOut(&out)
		authPubkeyCmd.SetErr(&out)
		if err := authPubkeyCmd.RunE(authPubkeyCmd, nil); err != nil {
			t.Fatalf("RunE: %v", err)
		}

		// Exactly the key plus one newline — no labels, no surrounding prose, so
		// ADMIN_BOOTSTRAP_KEY="$(sukko auth pubkey)" is correct without trimming.
		if got := out.String(); got != key+"\n" {
			t.Errorf("output = %q, want %q", got, key+"\n")
		}
	})

	t.Run("missing key points at keygen instead of failing opaquely", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("HOME", dir)

		var out bytes.Buffer
		authPubkeyCmd.SetOut(&out)
		authPubkeyCmd.SetErr(&out)
		err := authPubkeyCmd.RunE(authPubkeyCmd, nil)
		if err == nil {
			t.Fatal("RunE with no keypair returned nil error; want a not-found error")
		}
		if !strings.Contains(err.Error(), "auth keygen") {
			t.Errorf("error = %q, want it to name 'auth keygen' as the fix", err)
		}
	})
}
