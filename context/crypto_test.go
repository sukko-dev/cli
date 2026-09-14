package context

import (
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	t.Parallel()

	key, err := DeriveKey()
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}

	tests := []struct {
		name      string
		plaintext string
	}{
		{"simple string", "hello-world"},
		{"jwt token", "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"},
		{"empty string", ""},
		{"long secret", "sukko-dev-secret-minimum-32-bytes!!-with-extra-padding-for-good-measure"},
		{"special chars", `p@$$w0rd!#%&*(){}[]|;:'"<>,./~`},
		{"unicode", "日本語テスト-🚀"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			encrypted, err := Encrypt(key, tt.plaintext)
			if err != nil {
				t.Fatalf("Encrypt: %v", err)
			}

			if encrypted == tt.plaintext && tt.plaintext != "" {
				t.Error("encrypted should differ from plaintext")
			}

			decrypted, err := Decrypt(key, encrypted)
			if err != nil {
				t.Fatalf("Decrypt: %v", err)
			}

			if decrypted != tt.plaintext {
				t.Errorf("got %q, want %q", decrypted, tt.plaintext)
			}
		})
	}
}

func TestDecryptWrongKey(t *testing.T) {
	t.Parallel()

	key1 := make([]byte, 32)
	key2 := make([]byte, 32)
	key2[0] = 1 // different key

	encrypted, err := Encrypt(key1, "secret-data")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	_, err = Decrypt(key2, encrypted)
	if err == nil {
		t.Error("expected error decrypting with wrong key")
	}
}

func TestDecryptInvalidInput(t *testing.T) {
	t.Parallel()

	key := make([]byte, 32)

	tests := []struct {
		name  string
		input string
	}{
		{"invalid base64", "not-valid-base64!!!"},
		{"too short", "AQID"}, // 3 bytes, less than nonce
		{"empty", ""},         // empty base64
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := Decrypt(key, tt.input)
			if err == nil {
				t.Error("expected error for invalid input")
			}
		})
	}
}

func TestDeriveKeyDeterministic(t *testing.T) {
	t.Parallel()

	key1, err := DeriveKey()
	if err != nil {
		t.Fatalf("DeriveKey 1: %v", err)
	}

	key2, err := DeriveKey()
	if err != nil {
		t.Fatalf("DeriveKey 2: %v", err)
	}

	if len(key1) != keyLen {
		t.Errorf("key length = %d, want %d", len(key1), keyLen)
	}

	for i := range key1 {
		if key1[i] != key2[i] {
			t.Fatal("DeriveKey not deterministic: keys differ")
		}
	}
}

func TestEncryptProducesDifferentCiphertexts(t *testing.T) {
	t.Parallel()

	key, err := DeriveKey()
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}

	enc1, err := Encrypt(key, "same-plaintext")
	if err != nil {
		t.Fatalf("Encrypt 1: %v", err)
	}

	enc2, err := Encrypt(key, "same-plaintext")
	if err != nil {
		t.Fatalf("Encrypt 2: %v", err)
	}

	if enc1 == enc2 {
		t.Error("same plaintext should produce different ciphertexts (random nonce)")
	}
}
