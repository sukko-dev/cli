package commands

import (
	"regexp"
	"testing"
)

// serverKeyIDPattern mirrors the provisioning CreateKeyRequest contract: a key_id of 3-63
// characters, starting with a lowercase letter, containing lowercase alphanumerics or
// hyphens. The CLI MUST satisfy it — the server rejects anything else with KEY_INVALID.
var serverKeyIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{2,62}$`)

func TestGenerateKeyID(t *testing.T) {
	t.Parallel()

	a, err := generateKeyID()
	if err != nil {
		t.Fatalf("generateKeyID() error = %v", err)
	}
	if !serverKeyIDPattern.MatchString(a) {
		t.Errorf("generateKeyID() = %q, which the server would reject (want match %s)", a, serverKeyIDPattern)
	}

	b, err := generateKeyID()
	if err != nil {
		t.Fatalf("generateKeyID() second call error = %v", err)
	}
	if a == b {
		t.Errorf("generateKeyID() produced duplicate ids %q — key_id must be globally unique", a)
	}
}

func TestBuildCreateKeyRequest(t *testing.T) {
	t.Parallel()

	const pem = "-----BEGIN PUBLIC KEY-----\nMFkw...\n-----END PUBLIC KEY-----\n"

	// Empty key-id → a valid key_id is generated and returned so the caller can name the
	// saved private-key file after it (no dependence on the server echoing it back).
	req, keyID, err := buildCreateKeyRequest("ES256", pem, "", "")
	if err != nil {
		t.Fatalf("buildCreateKeyRequest(empty id) error = %v", err)
	}
	if keyID == "" || req["key_id"] != keyID {
		t.Fatalf("returned keyID %q must equal req[key_id] %v and be non-empty", keyID, req["key_id"])
	}
	if !serverKeyIDPattern.MatchString(keyID) {
		t.Errorf("generated key_id %q would be rejected by the server", keyID)
	}
	if req["algorithm"] != "ES256" || req["public_key"] != pem {
		t.Errorf("req missing algorithm/public_key: %v", req)
	}
	if _, ok := req["expires_at"]; ok {
		t.Errorf("expires_at must be absent when not supplied: %v", req)
	}

	// Explicit key-id is used verbatim; expires_at is carried through when set.
	req2, keyID2, err := buildCreateKeyRequest("ES256", pem, "my-signer", "2030-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("buildCreateKeyRequest(explicit id) error = %v", err)
	}
	if keyID2 != "my-signer" || req2["key_id"] != "my-signer" {
		t.Errorf("explicit key_id not preserved: keyID=%q req=%v", keyID2, req2)
	}
	if req2["expires_at"] != "2030-01-01T00:00:00Z" {
		t.Errorf("expires_at not carried through: %v", req2)
	}
}
