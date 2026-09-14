package commands

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/spf13/cobra"
)

func TestParseExpires_Duration(t *testing.T) {
	t.Parallel()

	before := time.Now().Add(2 * time.Hour)
	result, err := parseExpires("2h")
	after := time.Now().Add(2 * time.Hour)

	if err != nil {
		t.Fatalf("parseExpires(\"2h\"): %v", err)
	}

	parsed, err := time.Parse(time.RFC3339, result)
	if err != nil {
		t.Fatalf("parse result: %v", err)
	}

	if parsed.Before(before.Add(-time.Second)) || parsed.After(after.Add(time.Second)) {
		t.Errorf("result %v not within expected range [%v, %v]", parsed, before, after)
	}
}

func TestParseExpires_RFC3339(t *testing.T) {
	t.Parallel()

	input := "2026-12-31T23:59:59Z"
	result, err := parseExpires(input)
	if err != nil {
		t.Fatalf("parseExpires(%q): %v", input, err)
	}

	parsed, err := time.Parse(time.RFC3339, result)
	if err != nil {
		t.Fatalf("parse result: %v", err)
	}

	expected, _ := time.Parse(time.RFC3339, input)
	if !parsed.Equal(expected) {
		t.Errorf("result %v != expected %v", parsed, expected)
	}
}

func TestParseExpires_Invalid(t *testing.T) {
	t.Parallel()

	_, err := parseExpires("garbage")
	if err == nil {
		t.Error("expected error for invalid input")
	}
	if !strings.Contains(err.Error(), "invalid expires") {
		t.Errorf("error = %q, want to contain 'invalid expires'", err.Error())
	}
}

func TestRunTokenRevoke_MutualExclusivity(t *testing.T) {
	// Save and restore globals
	origJTI, origSub, origToken := revokeJTI, revokeSub, revokeToken
	defer func() { revokeJTI, revokeSub, revokeToken = origJTI, origSub, origToken }()

	revokeJTI = "abc"
	revokeSub = "user1"
	revokeToken = ""

	err := runTokenRevoke(nil, nil)
	if err == nil {
		t.Error("expected error when both --jti and --sub are set")
	}
	if !strings.Contains(err.Error(), "only one of") {
		t.Errorf("error = %q, want to contain 'only one of'", err.Error())
	}
}

func TestRunTokenRevoke_NoneProvided(t *testing.T) {
	origJTI, origSub, origToken := revokeJTI, revokeSub, revokeToken
	defer func() { revokeJTI, revokeSub, revokeToken = origJTI, origSub, origToken }()

	revokeJTI = ""
	revokeSub = ""
	revokeToken = ""

	err := runTokenRevoke(nil, nil)
	if err == nil {
		t.Error("expected error when no mode is provided")
	}
	if !strings.Contains(err.Error(), "one of --jti, --sub, or --token is required") {
		t.Errorf("error = %q, want to contain requirement message", err.Error())
	}
}

func TestRunTokenRevoke_TokenMissingJTI(t *testing.T) {
	// Create a JWT without jti
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":       "user1",
		"tenant_id": "acme",
		"exp":       time.Now().Add(time.Hour).Unix(),
	})
	tokenStr, err := token.SignedString([]byte("test-secret-at-least-32-bytes!!"))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	origJTI, origSub, origToken := revokeJTI, revokeSub, revokeToken
	defer func() { revokeJTI, revokeSub, revokeToken = origJTI, origSub, origToken }()

	revokeJTI = ""
	revokeSub = ""
	revokeToken = tokenStr

	err = runTokenRevoke(nil, nil)
	if err == nil {
		t.Error("expected error for token without jti")
	}
	if !strings.Contains(err.Error(), "does not contain a jti claim") {
		t.Errorf("error = %q, want to contain 'does not contain a jti claim'", err.Error())
	}
}

func TestRunTokenRevoke_TenantConflict(t *testing.T) {
	// Create a JWT with tenant_id and jti
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"jti":       "test-jti-123",
		"tenant_id": "acme",
		"exp":       time.Now().Add(time.Hour).Unix(),
	})
	tokenStr, err := token.SignedString([]byte("test-secret-at-least-32-bytes!!"))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	origJTI, origSub, origToken, origTenant := revokeJTI, revokeSub, revokeToken, revokeTenant
	defer func() {
		revokeJTI, revokeSub, revokeToken, revokeTenant = origJTI, origSub, origToken, origTenant
	}()

	revokeJTI = ""
	revokeSub = ""
	revokeToken = tokenStr
	revokeTenant = "different-tenant"

	err = runTokenRevoke(nil, nil)
	if err == nil {
		t.Error("expected error for tenant conflict")
	}
	if !strings.Contains(err.Error(), "tenant conflict") {
		t.Errorf("error = %q, want to contain 'tenant conflict'", err.Error())
	}
}

func TestRunTokenRevoke_TokenExtractsJTIAndTenant(t *testing.T) {
	// Create a JWT with known jti and tenant_id
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"jti":       "extract-me-jti",
		"tenant_id": "extract-me-tenant",
		"sub":       "user1",
		"exp":       time.Now().Add(time.Hour).Unix(),
	})
	tokenStr, err := token.SignedString([]byte("test-secret-at-least-32-bytes!!"))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	origJTI, origSub, origToken, origTenant := revokeJTI, revokeSub, revokeToken, revokeTenant
	defer func() {
		revokeJTI, revokeSub, revokeToken, revokeTenant = origJTI, origSub, origToken, origTenant
	}()

	revokeJTI = ""
	revokeSub = ""
	revokeToken = tokenStr
	revokeTenant = ""

	// runTokenRevoke will fail at newClient() since no context is loaded,
	// but we can verify extraction by checking the error occurs AFTER extraction
	// (i.e., it doesn't fail on jti/tenant extraction).
	cmd := &cobra.Command{}
	err = runTokenRevoke(cmd, nil)
	// The error should be about client creation, not about jti/tenant extraction
	if err == nil {
		t.Skip("no error — provisioning might be running")
	}
	if strings.Contains(err.Error(), "does not contain a jti") {
		t.Errorf("unexpected jti extraction error: %v", err)
	}
	if strings.Contains(err.Error(), "tenant required") {
		t.Errorf("unexpected tenant resolution error: %v", err)
	}
	if strings.Contains(err.Error(), "tenant conflict") {
		t.Errorf("unexpected tenant conflict error: %v", err)
	}
}

func TestRunTokenRevoke_TokenMalformedJWT(t *testing.T) {
	origJTI, origSub, origToken := revokeJTI, revokeSub, revokeToken
	defer func() { revokeJTI, revokeSub, revokeToken = origJTI, origSub, origToken }()

	revokeJTI = ""
	revokeSub = ""
	revokeToken = "not-a-valid-jwt"

	err := runTokenRevoke(nil, nil)
	if err == nil {
		t.Error("expected error for malformed JWT")
	}
	if !strings.Contains(err.Error(), "does not contain a jti claim") && !strings.Contains(err.Error(), "failed to decode token") {
		t.Errorf("error = %q, want decode failure or missing jti message", err.Error())
	}
}
