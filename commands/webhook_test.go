package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

func TestWebhookCmd_Registration(t *testing.T) {
	t.Parallel()

	want := map[string]bool{"create": false, "list": false, "get": false, "update": false, "delete": false, "test": false}
	for _, sub := range webhookCmd.Commands() {
		if _, ok := want[sub.Use]; ok {
			want[sub.Use] = true
		}
	}
	for use, found := range want {
		if !found {
			t.Errorf("webhookCmd missing %q subcommand", use)
		}
	}
}

func TestWebhookCmd_Flags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		cmd      *cobra.Command
		flag     string
		required bool
	}{
		{webhookCreateCmd, "tenant", false},
		{webhookCreateCmd, "url", true},
		{webhookCreateCmd, "channel-pattern", true},
		{webhookCreateCmd, "secret", false},
		{webhookCreateCmd, "secret-file", false},
		{webhookCreateCmd, "max-retries", false},
		{webhookGetCmd, "webhook-id", true},
		{webhookUpdateCmd, "webhook-id", true},
		{webhookDeleteCmd, "webhook-id", true},
		{webhookTestCmd, "webhook-id", true},
		{webhookUpdateCmd, "url", false},
		{webhookUpdateCmd, "channel-pattern", false},
		{webhookUpdateCmd, "max-retries", false},
		{webhookUpdateCmd, "status", false},
	}

	for _, tt := range tests {
		t.Run(tt.cmd.Use+"/"+tt.flag, func(t *testing.T) {
			t.Parallel()
			f := tt.cmd.Flags().Lookup(tt.flag)
			if f == nil {
				t.Fatalf("--%s not found on webhook %s", tt.flag, tt.cmd.Use)
			}
			_, isRequired := f.Annotations[cobraRequiredAnnotation]
			if isRequired != tt.required {
				t.Errorf("--%s required = %v, want %v", tt.flag, isRequired, tt.required)
			}
		})
	}
}

// TestWebhookUpdateBody verifies the partial-update body carries ONLY the flags the operator set,
// mapped to the API's JSON field names. An unset flag must not appear (it would overwrite the
// server value), and the flag→JSON renaming (channel-pattern→channel_pattern, max-retries→
// max_retries) must hold.
func TestWebhookUpdateBody(t *testing.T) {
	t.Parallel()

	newUpdateCmd := func() *cobra.Command {
		c := &cobra.Command{Use: "update", RunE: func(*cobra.Command, []string) error { return nil }}
		c.Flags().String("url", "", "")
		c.Flags().String("channel-pattern", "", "")
		c.Flags().Int("max-retries", 0, "")
		c.Flags().String("status", "", "")
		return c
	}

	t.Run("only changed flags are included, renamed to JSON keys", func(t *testing.T) {
		t.Parallel()
		c := newUpdateCmd()
		c.SetArgs([]string{"--channel-pattern", "orders.*", "--max-retries", "7"})
		if err := c.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		body := webhookUpdateBody(c)
		if len(body) != 2 {
			t.Fatalf("body = %v, want exactly 2 keys", body)
		}
		if body["channel_pattern"] != "orders.*" {
			t.Errorf("channel_pattern = %v, want orders.*", body["channel_pattern"])
		}
		if body["max_retries"] != 7 {
			t.Errorf("max_retries = %v, want 7", body["max_retries"])
		}
		for _, absent := range []string{"url", "status", "channel-pattern", "max-retries"} {
			if _, ok := body[absent]; ok {
				t.Errorf("body unexpectedly contains %q", absent)
			}
		}
	})

	t.Run("no flags set yields an empty body (the command rejects this)", func(t *testing.T) {
		t.Parallel()
		c := newUpdateCmd()
		c.SetArgs(nil)
		if err := c.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if body := webhookUpdateBody(c); len(body) != 0 {
			t.Errorf("body = %v, want empty", body)
		}
	})
}

// TestResolveWebhookSecret covers the --secret / --secret-file resolution: exactly one is required,
// --secret-file keeps the secret out of argv, and the two are mutually exclusive.
func TestResolveWebhookSecret(t *testing.T) {
	t.Parallel()

	newCreateCmd := func() *cobra.Command {
		c := &cobra.Command{Use: "create", RunE: func(*cobra.Command, []string) error { return nil }}
		c.Flags().String("secret", "", "")
		c.Flags().String("secret-file", "", "")
		return c
	}

	t.Run("--secret", func(t *testing.T) {
		t.Parallel()
		c := newCreateCmd()
		_ = c.Flags().Set("secret", "sek")
		got, err := resolveWebhookSecret(c)
		if err != nil || got != "sek" {
			t.Fatalf("got (%q, %v), want (\"sek\", nil)", got, err)
		}
	})

	t.Run("--secret-file is read and trimmed", func(t *testing.T) {
		t.Parallel()
		f := filepath.Join(t.TempDir(), "sek.txt")
		if err := os.WriteFile(f, []byte("  file-secret\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		c := newCreateCmd()
		_ = c.Flags().Set("secret-file", f)
		got, err := resolveWebhookSecret(c)
		if err != nil || got != "file-secret" {
			t.Fatalf("got (%q, %v), want (\"file-secret\", nil)", got, err)
		}
	})

	t.Run("both set is an error", func(t *testing.T) {
		t.Parallel()
		c := newCreateCmd()
		_ = c.Flags().Set("secret", "a")
		_ = c.Flags().Set("secret-file", "/x")
		if _, err := resolveWebhookSecret(c); err == nil {
			t.Error("want error when both --secret and --secret-file are set")
		}
	})

	t.Run("neither set is an error", func(t *testing.T) {
		t.Parallel()
		if _, err := resolveWebhookSecret(newCreateCmd()); err == nil {
			t.Error("want error when no secret is provided")
		}
	})
}
