package commands

import (
	"testing"
)

// cobraRequiredAnnotation is the annotation key cobra uses to mark a flag as required.
const cobraRequiredAnnotation = "cobra_annotation_bash_completion_one_required_flag"

func TestAPIKeyCmd_Registration(t *testing.T) {
	t.Parallel()

	subs := apiKeyCmd.Commands()
	if len(subs) != 3 {
		t.Fatalf("apiKeyCmd has %d subcommands, want 3", len(subs))
	}

	wantUse := map[string]bool{
		"create": false,
		"list":   false,
		"revoke": false,
	}

	for _, sub := range subs {
		if _, ok := wantUse[sub.Use]; !ok {
			t.Errorf("unexpected subcommand Use=%q", sub.Use)
			continue
		}
		wantUse[sub.Use] = true
	}

	for use, found := range wantUse {
		if !found {
			t.Errorf("expected subcommand %q not found", use)
		}
	}
}

func TestAPIKeyCmd_CreateFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		flag     string
		required bool
	}{
		{"tenant flag is optional (context-aware)", "tenant", false},
		{"name flag is optional", "name", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := apiKeyCreateCmd.Flags().Lookup(tt.flag)
			if f == nil {
				t.Fatalf("--%s flag not found on create command", tt.flag)
			}

			_, isRequired := f.Annotations[cobraRequiredAnnotation]
			if isRequired != tt.required {
				t.Errorf("--%s required = %v, want %v", tt.flag, isRequired, tt.required)
			}
		})
	}
}

func TestAPIKeyCmd_ListFlags(t *testing.T) {
	t.Parallel()

	f := apiKeyListCmd.Flags().Lookup("tenant")
	if f == nil {
		t.Fatal("--tenant flag not found on list command")
	}

	// --tenant is now optional (resolved from context if not provided)
	if _, ok := f.Annotations[cobraRequiredAnnotation]; ok {
		t.Error("--tenant flag should be optional (context-aware)")
	}
}

func TestAPIKeyCmd_RevokeFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		flag     string
		required bool
	}{
		{"tenant flag is optional (context-aware)", "tenant", false},
		{"key-id flag is required", "key-id", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := apiKeyRevokeCmd.Flags().Lookup(tt.flag)
			if f == nil {
				t.Fatalf("--%s flag not found on revoke command", tt.flag)
			}

			_, isRequired := f.Annotations[cobraRequiredAnnotation]
			if isRequired != tt.required {
				t.Errorf("--%s required = %v, want %v", tt.flag, isRequired, tt.required)
			}
		})
	}
}
