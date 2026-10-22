//go:build e2e

package e2e

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestLicenseLogin drives `sukko license login` (RFC 8628 device grant, ADR-0008) end to
// end against an in-test stub standing in for the license service — no Docker, no real
// license service. The stub serves the two device-grant endpoints; SUKKO_LICENSE_API_URL
// points the real binary at it. The CLI authorizes, polls once (pending) then receives a
// dev license key, decodes it, and stores it in the active context — which `license show`
// then reflects. This exercises the real subprocess, env plumbing, and encrypted context
// store, which the in-process unit test in commands/ cannot.
func TestLicenseLogin(t *testing.T) {
	exp := time.Now().Add(365 * 24 * time.Hour).Unix()
	key := makeLicenseKey(t, "community", "E2E Acme", exp)

	var tokenCalls atomic.Int32
	mux := http.NewServeMux()
	// Device authorize MUST return 201 with a non-empty device_code/user_code/verification_uri.
	mux.HandleFunc("POST /api/v1/device/authorize", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code": "dev-code", "user_code": "BCDF-GHJK",
			"verification_uri":          "https://sukko.dev/device",
			"verification_uri_complete": "https://sukko.dev/device?code=BCDF-GHJK",
			"expires_in":                600, "interval": 1,
		})
	})
	// First poll is pending (exercises the loop), second returns the key. Pending is a 400
	// with {"code":"AUTHORIZATION_PENDING"}; success is 200 with license_key.
	mux.HandleFunc("POST /api/v1/device/token", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if tokenCalls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"code": "AUTHORIZATION_PENDING"})
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"license_key": key, "edition": "community", "exp": exp})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newCLI(t)
	c.env = append(c.env, "SUKKO_LICENSE_API_URL="+srv.URL)

	// Login persists the fetched key into the active context, so one must exist.
	c.run("context", "add", "local", "--provisioning-url", "http://localhost:8080", "--gateway-url", "ws://localhost:3000")
	c.run("context", "use", "local")

	out := c.run("license", "login")
	for _, want := range []string{"Logged in", "Community", "E2E Acme"} {
		if !strings.Contains(out, want) {
			t.Errorf("license login output missing %q:\n%s", want, out)
		}
	}

	// The fetched key is stored — `license show` reflects its edition and org (it masks the
	// key itself).
	show := c.run("license", "show")
	if !strings.Contains(show, "Community") || !strings.Contains(show, "E2E Acme") {
		t.Errorf("license show does not reflect the logged-in key:\n%s", show)
	}
}

// makeLicenseKey builds a CLI-parseable license key: base64url(claims JSON) "."
// base64url(sig). decodeLicenseClaims only parses the payload (it does not verify the
// signature), so a placeholder signature drives the login flow. Claim field names match
// the CLI's licenseClaims schema (edition/org/exp).
func makeLicenseKey(t *testing.T, edition, org string, exp int64) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"edition": edition, "org": org, "exp": exp})
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString([]byte("sig"))
}
