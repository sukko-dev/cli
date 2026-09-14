package commands

import "testing"

func TestValidateURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"https valid", "https://api.example.com", false},
		{"wss valid", "wss://ws.example.com", false},
		{"http localhost with port", "http://localhost:8080", false},
		{"ws with path", "ws://gateway.example.com/ws", false},
		{"no scheme", "example.com", true},
		{"scheme only", "https://", true},
		{"empty string", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestContextCreateCmd_Registration(t *testing.T) {
	t.Parallel()

	if contextCreateCmd.Name() != "create" {
		t.Errorf("command name = %q, want create", contextCreateCmd.Name())
	}

	if contextCreateCmd.RunE == nil {
		t.Error("RunE should not be nil")
	}

	// Verify key flags exist
	for _, flagName := range []string{"provisioning-url", "gateway-url", "admin-token", "environment", "force"} {
		if contextCreateCmd.Flags().Lookup(flagName) == nil {
			t.Errorf("missing flag --%s", flagName)
		}
	}
}
