package commands

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDecodeLicenseClaims_Valid(t *testing.T) {
	t.Parallel()

	claims := licenseClaims{
		Edition: "pro",
		Org:     "Acme Corp",
		Exp:     time.Date(2027, 3, 25, 0, 0, 0, 0, time.UTC).Unix(),
	}
	payload, _ := json.Marshal(claims)
	key := base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString([]byte("fake-signature"))

	got, err := decodeLicenseClaims(key)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.Edition != "pro" {
		t.Errorf("edition = %q, want pro", got.Edition)
	}
	if got.Org != "Acme Corp" {
		t.Errorf("org = %q, want Acme Corp", got.Org)
	}
	if got.Exp != claims.Exp {
		t.Errorf("exp = %d, want %d", got.Exp, claims.Exp)
	}
}

func TestDecodeLicenseClaims_Errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		key  string
	}{
		{"no dot separator", "nodot"},
		{"bad base64 payload", "!!!.dGVzdA"},
		{"bad base64 signature", "dGVzdA.!!!"},
		{"invalid JSON payload", base64.RawURLEncoding.EncodeToString([]byte("not-json")) + ".dGVzdA"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := decodeLicenseClaims(tt.key)
			if err == nil {
				t.Error("expected error but got nil")
			}
		})
	}
}

func TestMaskKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"short", "***"},
		{"exactly12ch", "***"},
		{"abcdefghijklmnop.signature", "abcdefgh...ture"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			if got := maskKey(tt.input); got != tt.want {
				t.Errorf("maskKey(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFormatExpiry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		exp      int64
		contains string
	}{
		{"zero", 0, "none"},
		{"future", time.Now().Add(30 * 24 * time.Hour).Unix(), "days remaining"},
		{"past", time.Now().Add(-30 * 24 * time.Hour).Unix(), "expired"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := formatExpiry(tt.exp)
			if tt.contains != "" && !strings.Contains(got, tt.contains) {
				t.Errorf("formatExpiry(%d) = %q, want to contain %q", tt.exp, got, tt.contains)
			}
		})
	}
}

func TestLicenseCmd_Registration(t *testing.T) {
	t.Parallel()

	subs := licenseCmd.Commands()
	wantNames := map[string]bool{"set": false, "show": false, "remove": false, "push": false}

	for _, sub := range subs {
		if _, ok := wantNames[sub.Name()]; ok {
			wantNames[sub.Name()] = true
		}
	}

	for name, found := range wantNames {
		if !found {
			t.Errorf("missing subcommand %q", name)
		}
	}
}

func TestRunLicensePush_BadFormat(t *testing.T) {
	t.Parallel()

	err := runLicensePush(licensePushCmd, []string{"not-a-valid-key"})
	if err == nil {
		t.Fatal("expected error for bad format")
	}
	if !strings.Contains(err.Error(), "validate license key") {
		t.Errorf("error should contain 'validate license key': %v", err)
	}
}

func TestRunLicensePush_EmptyKey(t *testing.T) {
	t.Parallel()

	err := runLicensePush(licensePushCmd, []string{""})
	if err == nil {
		t.Fatal("expected error for empty key")
	}
	if !strings.Contains(err.Error(), "license key is required") {
		t.Errorf("error should contain 'license key is required': %v", err)
	}
}

// TestEditionSupportsPush — push-service boots with a Pro license (Web Push);
// FCM/APNs delivery additionally requires Enterprise, enforced server-side.
func TestEditionSupportsPush(t *testing.T) {
	t.Parallel()

	tests := []struct {
		edition string
		want    bool
	}{
		{"community", false},
		{"pro", true},
		{"enterprise", true},
		{"", false},
		{"unknown", false},
	}
	for _, tt := range tests {
		t.Run("edition="+tt.edition, func(t *testing.T) {
			t.Parallel()
			if got := editionSupportsPush(tt.edition); got != tt.want {
				t.Errorf("editionSupportsPush(%q) = %v, want %v", tt.edition, got, tt.want)
			}
		})
	}
}

// TestPushAction pins the compose orchestration decision for license
// transitions: start when entering a push-capable edition (Pro or Enterprise),
// ensure-healthy when staying push-capable, stop when leaving, none otherwise.
// An empty prev edition (pre-push GetEdition failed) is treated as not
// push-capable, so a Pro/Enterprise push still starts the service.
func TestPushAction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		prev string
		next string
		want pushOrchestration
	}{
		{"community to pro starts", "community", "pro", pushStart},
		{"community to enterprise starts", "community", "enterprise", pushStart},
		{"unknown prev to pro starts", "", "pro", pushStart},
		{"pro to enterprise ensures", "pro", "enterprise", pushEnsure},
		{"enterprise to pro ensures", "enterprise", "pro", pushEnsure},
		{"pro to pro ensures", "pro", "pro", pushEnsure},
		{"enterprise to enterprise ensures", "enterprise", "enterprise", pushEnsure},
		{"pro to community stops", "pro", "community", pushStop},
		{"enterprise to community stops", "enterprise", "community", pushStop},
		{"community to community none", "community", "community", pushNone},
		{"unknown to community none", "", "community", pushNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := pushAction(tt.prev, tt.next); got != tt.want {
				t.Errorf("pushAction(%q, %q) = %v, want %v", tt.prev, tt.next, got, tt.want)
			}
		})
	}
}

func TestParseExpiry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  int64
	}{
		{"valid RFC3339", "2027-04-08T00:00:00Z", 1807142400},
		{"invalid format", "not-a-date", 0},
		{"empty", "", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseExpiry(tt.input)
			if got != tt.want {
				t.Errorf("parseExpiry(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}
