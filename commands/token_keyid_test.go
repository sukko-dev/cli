package commands

import "testing"

// TestResolveKeyID pins how a generated token's `kid` is chosen. The gateway resolves a
// tenant's signing key by `kid`, so getting this wrong produces a token that authenticates
// nowhere — and the failure surfaces at connect time, far from the command that caused it.
func TestResolveKeyID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		explicit string
		keyFile  string
		want     string
	}{
		{
			name:     "explicit key id wins over the file name",
			explicit: "registered-id",
			keyFile:  "/keys/acme/other-name.pem",
			want:     "registered-id",
		},
		{
			name:    "falls back to the store file name, which is the registered key id",
			keyFile: "/keys/acme/benchkey.pem",
			want:    "benchkey",
		},
		{
			name:    "bare file name without a directory",
			keyFile: "benchkey.pem",
			want:    "benchkey",
		},
		{
			name:    "file with no extension",
			keyFile: "/keys/acme/benchkey",
			want:    "benchkey",
		},
		{
			name:    "dots inside the key id are preserved",
			keyFile: "/keys/acme/key.v2.pem",
			want:    "key.v2",
		},
		{
			name:     "explicit wins even when the key file is empty",
			explicit: "registered-id",
			want:     "registered-id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := resolveKeyID(tt.explicit, tt.keyFile); got != tt.want {
				t.Errorf("resolveKeyID(%q, %q) = %q, want %q", tt.explicit, tt.keyFile, got, tt.want)
			}
		})
	}
}
