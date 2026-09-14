package commands

import (
	"testing"
)

func TestRoutingAddCmd_Registration(t *testing.T) {
	t.Parallel()

	var found bool
	for _, sub := range routingCmd.Commands() {
		if sub.Use == "add" {
			found = true
			break
		}
	}
	if !found {
		t.Error("routingCmd does not have an 'add' subcommand")
	}
}

func TestRoutingAddCmd_Flags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		flag     string
		required bool
	}{
		{"tenant", false},
		{"pattern", true},
		{"topics", true},
		{"priority", true},
	}

	for _, tt := range tests {
		t.Run(tt.flag, func(t *testing.T) {
			t.Parallel()

			f := routingAddCmd.Flags().Lookup(tt.flag)
			if f == nil {
				t.Fatalf("--%s flag not found on routing add command", tt.flag)
			}

			_, isRequired := f.Annotations[cobraRequiredAnnotation]
			if isRequired != tt.required {
				t.Errorf("--%s required = %v, want %v", tt.flag, isRequired, tt.required)
			}
		})
	}
}

func TestParseTopics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    []string
		wantErr bool
	}{
		{
			name:  "single topic",
			input: "trades",
			want:  []string{"trades"},
		},
		{
			name:  "multiple topics",
			input: "trades,analytics",
			want:  []string{"trades", "analytics"},
		},
		{
			name:  "topics with whitespace trimmed",
			input: "trades, analytics",
			want:  []string{"trades", "analytics"},
		},
		{
			name:    "trailing comma → empty entry → error",
			input:   "trades,",
			wantErr: true,
		},
		{
			name:    "double comma → empty entry → error",
			input:   "trades,,analytics",
			wantErr: true,
		},
		{
			name:    "whitespace only entry → error",
			input:   "trades, ,analytics",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseTopics(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error but got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("topics = %v, want %v", got, tt.want)
			}
			for i, w := range tt.want {
				if got[i] != w {
					t.Errorf("topics[%d] = %q, want %q", i, got[i], w)
				}
			}
		})
	}
}
