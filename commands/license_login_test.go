package commands

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/sukko-dev/cli/client"
	clicontext "github.com/sukko-dev/cli/context"
)

// fastPolls shrinks the poll waits so loop tests don't sleep. These tests are
// sequential (no t.Parallel), so mutating the package vars is race-free.
func fastPolls(t *testing.T) {
	t.Helper()
	origMin, origSlow := minPollInterval, slowDownIncrement
	t.Cleanup(func() { minPollInterval, slowDownIncrement = origMin, origSlow })
	minPollInterval, slowDownIncrement = time.Millisecond, time.Millisecond
}

// tokStatus replies with a bare status (no body) — for 429 (rate-limited) and 5xx
// (transient), which carry no {code}.
func tokStatus(status int) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) { w.WriteHeader(status) }
}

func pendingN(n int) []func(http.ResponseWriter) {
	out := make([]func(http.ResponseWriter), n)
	for i := range out {
		out[i] = tokErr("AUTHORIZATION_PENDING")
	}
	return out
}

// pollWith drives pollForLicenseKey against a fake server scripting the token
// replies (interval=0, floored to the test's tiny minPollInterval). It returns the
// key plus the number of token polls made (so tests can pin the retry bound) and
// the error.
func pollWith(ctx context.Context, t *testing.T, expiresIn int, replies ...func(http.ResponseWriter)) (key string, polls int, err error) {
	t.Helper()
	srv, calls := fakeLicenseServer(t, replies...)
	c := client.NewLicenseDeviceClient(srv.URL)
	key, err = pollForLicenseKey(ctx, c, &client.DeviceAuthorization{DeviceCode: "dev-code", Interval: 0, ExpiresIn: expiresIn})
	return key, int(calls.Load()), err
}

// validLicenseKey builds a CLI-parseable key (payload.signature, base64url) for the
// given claims — decodeLicenseClaims accepts it (it does not verify the signature).
func validLicenseKey(t *testing.T, edition, org string, exp int64) string {
	t.Helper()
	payload, err := json.Marshal(licenseClaims{Edition: edition, Org: org, Exp: exp})
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString([]byte("sig"))
}

// fakeLicenseServer serves /device/authorize (fixed) and /device/token (scripted
// replies, one per poll; the wait comes from the test's minPollInterval). It
// returns the server and a counter of /device/token calls, so tests can pin
// exactly how many polls happened (e.g. the retry bound, or zero on a pre-canceled
// context).
func fakeLicenseServer(t *testing.T, tokenReplies ...func(http.ResponseWriter)) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var tokenCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/device/authorize", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code": "dev-code", "user_code": "BCDF-GHJK",
			"verification_uri": "https://sukko.dev/device", "verification_uri_complete": "https://sukko.dev/device?code=BCDF-GHJK",
			"expires_in": 600, "interval": 0,
		})
	})
	mux.HandleFunc("POST /api/v1/device/token", func(w http.ResponseWriter, _ *http.Request) {
		i := int(tokenCalls.Add(1)) - 1
		if i < len(tokenReplies) {
			tokenReplies[i](w)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &tokenCalls
}

func tokErr(code string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": "x"})
	}
}

func tokOK(key, edition string, exp int64) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"license_key": key, "edition": edition, "exp": exp})
	}
}

// withActiveContext points resolvedStore/resolvedCtx at a fresh temp-dir store and
// returns the (store, ctx); it restores the globals on cleanup.
func withActiveContext(t *testing.T) (*clicontext.Store, *clicontext.Context) {
	t.Helper()
	store, err := clicontext.NewStoreWithDir(filepath.Join(t.TempDir(), "contexts"))
	if err != nil {
		t.Fatalf("NewStoreWithDir: %v", err)
	}
	ctx := &clicontext.Context{Name: "local"}
	if err := store.Add(ctx); err != nil {
		t.Fatalf("seed context: %v", err)
	}
	origCtx, origStore := resolvedCtx, resolvedStore
	t.Cleanup(func() { resolvedCtx, resolvedStore = origCtx, origStore })
	resolvedCtx, resolvedStore = ctx, store
	return store, ctx
}

func runLogin(t *testing.T) (*cobra.Command, *bytes.Buffer, error) {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	return cmd, &buf, runLicenseLogin(cmd, nil)
}

func TestLicenseLogin_PendingThenStoresKey(t *testing.T) {
	fastPolls(t)
	exp := time.Now().Add(365 * 24 * time.Hour).Unix()
	key := validLicenseKey(t, "community", "Acme", exp)
	srv, _ := fakeLicenseServer(t, tokErr("AUTHORIZATION_PENDING"), tokOK(key, "community", exp))
	t.Setenv("SUKKO_LICENSE_API_URL", srv.URL)
	store, ctx := withActiveContext(t)

	_, buf, err := runLogin(t)
	if err != nil {
		t.Fatalf("runLicenseLogin: %v", err)
	}
	stored, err := ctx.LicenseKey(store.Key())
	if err != nil {
		t.Fatalf("decrypt stored key: %v", err)
	}
	if stored != key {
		t.Fatalf("stored key mismatch")
	}
	out := buf.String()
	for _, want := range []string{"https://sukko.dev/device", "BCDF-GHJK", "Logged in", "Community", "Acme"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
	// Secret hygiene: neither the issued key nor the device_code bearer may appear.
	if strings.Contains(out, key) {
		t.Error("license key leaked into login output")
	}
	if strings.Contains(out, "dev-code") {
		t.Error("device_code leaked into login output")
	}
}

func TestPollForLicenseKey_SlowDownThenSucceeds(t *testing.T) {
	fastPolls(t)
	key := validLicenseKey(t, "community", "Acme", time.Now().Add(time.Hour).Unix())
	got, _, err := pollWith(context.Background(), t, 600, tokErr("SLOW_DOWN"), tokOK(key, "community", 0))
	if err != nil || got != key {
		t.Fatalf("got (%q, %v), want the key after a slow_down", got, err)
	}
}

func TestPollForLicenseKey_RetriesTransientThenSucceeds(t *testing.T) {
	fastPolls(t)
	key := validLicenseKey(t, "community", "Acme", time.Now().Add(time.Hour).Unix())
	// A 500 then a 429, then success — both transient, retried, not aborted.
	got, _, err := pollWith(context.Background(), t, 600, tokStatus(500), tokStatus(429), tokOK(key, "community", 0))
	if err != nil || got != key {
		t.Fatalf("got (%q, %v), want the key after transient errors", got, err)
	}
}

// n500 returns n consecutive 500 replies.
func n500(n int) []func(http.ResponseWriter) {
	out := make([]func(http.ResponseWriter), n)
	for i := range out {
		out[i] = tokStatus(500)
	}
	return out
}

func TestPollForLicenseKey_AbortsAtExactlyTheBound(t *testing.T) {
	fastPolls(t)
	// The server falls through to 500 forever, so the abort is driven purely by the
	// client's bound — assert it aborts after EXACTLY maxConsecutiveTransientFailures
	// polls (pins the bound and the >= vs > off-by-one).
	_, calls, err := pollWith(context.Background(), t, 600, n500(maxConsecutiveTransientFailures)...)
	if err == nil || !strings.Contains(err.Error(), "could not reach the license service") {
		t.Fatalf("err = %v, want a 'could not reach' abort", err)
	}
	if calls != maxConsecutiveTransientFailures {
		t.Fatalf("polled %d times, want exactly %d before abort", calls, maxConsecutiveTransientFailures)
	}
}

func TestPollForLicenseKey_ToleratesTransientBelowBound(t *testing.T) {
	fastPolls(t)
	key := validLicenseKey(t, "community", "Acme", time.Now().Add(time.Hour).Unix())
	// One fewer than the bound, then success — must NOT abort (proves the bound is
	// not off-by-one low).
	replies := append(n500(maxConsecutiveTransientFailures-1), tokOK(key, "community", 0))
	got, _, err := pollWith(context.Background(), t, 600, replies...)
	if err != nil || got != key {
		t.Fatalf("got (%q, %v), want success after %d transient failures", got, err, maxConsecutiveTransientFailures-1)
	}
}

func TestPollForLicenseKey_ResetsTransientCounterOnProgress(t *testing.T) {
	fastPolls(t)
	key := validLicenseKey(t, "community", "Acme", time.Now().Add(time.Hour).Unix())
	// (bound-1) transients, a pending (progress → counter resets), then (bound-1)
	// more transients, then success. Without the reset this would abort on the
	// second run of transients.
	replies := append(n500(maxConsecutiveTransientFailures-1), tokErr("AUTHORIZATION_PENDING"))
	replies = append(replies, n500(maxConsecutiveTransientFailures-1)...)
	replies = append(replies, tokOK(key, "community", 0))
	got, _, err := pollWith(context.Background(), t, 600, replies...)
	if err != nil || got != key {
		t.Fatalf("got (%q, %v), want success — a pending between transient runs must reset the counter", got, err)
	}
}

func TestPollForLicenseKey_CanceledContextDoesNotPoll(t *testing.T) {
	fastPolls(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, calls, err := pollWith(ctx, t, 600, tokErr("AUTHORIZATION_PENDING"))
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("err = %v, want a 'canceled' error", err)
	}
	if calls != 0 {
		t.Fatalf("polled %d times on a pre-canceled context, want 0", calls)
	}
}

func TestPollForLicenseKey_CancelDuringWait(t *testing.T) {
	// A cancel that lands during the inter-poll wait (not before the first poll)
	// must end the loop cleanly. Interval > the ctx timeout so the wait is cut short.
	origMin, origSlow := minPollInterval, slowDownIncrement
	t.Cleanup(func() { minPollInterval, slowDownIncrement = origMin, origSlow })
	minPollInterval, slowDownIncrement = 200*time.Millisecond, time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, _, err := pollWith(ctx, t, 600, pendingN(5)...)
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("err = %v, want a 'canceled' error from the inter-poll wait", err)
	}
}

func TestPollForLicenseKey_Deadline(t *testing.T) {
	// A short expiry with only pending replies must end with the expired message,
	// not spin forever. Use a modest interval so this takes ~1s, not thousands of polls.
	origMin, origSlow := minPollInterval, slowDownIncrement
	t.Cleanup(func() { minPollInterval, slowDownIncrement = origMin, origSlow })
	minPollInterval, slowDownIncrement = 100*time.Millisecond, time.Millisecond

	_, _, err := pollWith(context.Background(), t, 1, pendingN(50)...)
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("err = %v, want an 'expired' error at the local deadline", err)
	}
}

func TestLicenseLogin_Denied(t *testing.T) {
	srv, _ := fakeLicenseServer(t, tokErr("ACCESS_DENIED"))
	t.Setenv("SUKKO_LICENSE_API_URL", srv.URL)
	withActiveContext(t)

	_, _, err := runLogin(t)
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("err = %v, want a 'denied' error", err)
	}
}

func TestLicenseLogin_Expired(t *testing.T) {
	srv, _ := fakeLicenseServer(t, tokErr("EXPIRED_TOKEN"))
	t.Setenv("SUKKO_LICENSE_API_URL", srv.URL)
	withActiveContext(t)

	_, _, err := runLogin(t)
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("err = %v, want an 'expired' error", err)
	}
}

func TestLicenseLogin_NoActiveContext(t *testing.T) {
	origCtx, origStore := resolvedCtx, resolvedStore
	t.Cleanup(func() { resolvedCtx, resolvedStore = origCtx, origStore })
	resolvedCtx, resolvedStore = nil, nil

	_, _, err := runLogin(t)
	if err == nil || !strings.Contains(err.Error(), "active context") {
		t.Fatalf("err = %v, want a 'no active context' error", err)
	}
}

func TestLicenseLogout_RemovesStoredKey(t *testing.T) {
	store, ctx := withActiveContext(t)
	enc, err := store.EncryptSecret("payload.sig")
	if err != nil {
		t.Fatalf("encrypt seed key: %v", err)
	}
	ctx.LicenseKeyEnc = enc
	if err := store.Add(ctx); err != nil {
		t.Fatalf("seed key: %v", err)
	}

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	if err := licenseLogoutCmd.RunE(cmd, nil); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if ctx.LicenseKeyEnc != "" {
		t.Fatal("logout did not clear the stored license key")
	}
	if !strings.Contains(buf.String(), "removed") {
		t.Errorf("output missing removal confirmation:\n%s", buf.String())
	}
}

func TestLicenseLogin_RejectsMalformedIssuedKey(t *testing.T) {
	// The server returns a key the CLI cannot parse — login must fail, not store junk.
	srv, _ := fakeLicenseServer(t, tokOK("not-a-valid-key", "community", 0))
	t.Setenv("SUKKO_LICENSE_API_URL", srv.URL)
	store, ctx := withActiveContext(t)

	_, _, err := runLogin(t)
	if err == nil || !strings.Contains(err.Error(), "validate issued license key") {
		t.Fatalf("err = %v, want a validation error", err)
	}
	if stored, _ := ctx.LicenseKey(store.Key()); stored != "" {
		t.Fatalf("a malformed key must not be stored, got %q", stored)
	}
}
