package commands

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	clicontext "github.com/sukko-dev/cli/context"
)

func newTempStore(t *testing.T) *clicontext.Store {
	t.Helper()
	store, err := clicontext.NewStoreWithDir(filepath.Join(t.TempDir(), "contexts"))
	if err != nil {
		t.Fatalf("NewStoreWithDir: %v", err)
	}
	return store
}

func newDiscardCmd() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	return cmd
}

// TestEnsureLocalContext_PreservesCredentialsOnReinit pins the data-loss fix: re-running init over
// a local context that already holds a license + API key must carry those credentials forward (the
// wholesale context rewrite previously wiped them). They are removed only by 'sukko license remove'.
func TestEnsureLocalContext_PreservesCredentialsOnReinit(t *testing.T) {
	store := newTempStore(t)

	licEnc, err := store.EncryptSecret(testLicenseKey(t, "pro"))
	if err != nil {
		t.Fatalf("encrypt license: %v", err)
	}
	apiEnc, err := store.EncryptSecret("api-key-abc123")
	if err != nil {
		t.Fatalf("encrypt api key: %v", err)
	}
	seed := &clicontext.Context{
		Name: "local", Type: "local",
		LicenseKeyEnc: licEnc, APIKeyEnc: apiEnc, ActiveTenant: "custom-tenant",
	}
	if err := store.Add(seed); err != nil {
		t.Fatalf("seed add: %v", err)
	}

	ctx, err := ensureLocalContext(newDiscardCmd(), store)
	if err != nil {
		t.Fatalf("ensureLocalContext: %v", err)
	}

	if ctx.LicenseKeyEnc != licEnc {
		t.Error("license key not preserved on the returned context")
	}
	if ctx.APIKeyEnc != apiEnc {
		t.Error("API key not preserved on the returned context")
	}
	// ActiveTenant is NOT a credential and is intentionally not preserved — re-init resets it to
	// the default "demo" (carrying a custom tenant across a volume reset would point at a tenant
	// that no longer exists).
	if ctx.ActiveTenant != "demo" {
		t.Errorf("active tenant = %q, want reset to default %q", ctx.ActiveTenant, "demo")
	}

	// And it is persisted + still decryptable (not just carried in-memory).
	got, err := store.Get("local")
	if err != nil {
		t.Fatalf("Get after re-init: %v", err)
	}
	if got.LicenseKeyEnc != licEnc {
		t.Fatal("license key was wiped in the store on re-init (data-loss regression)")
	}
	if lk, err := got.LicenseKey(store.Key()); err != nil || lk == "" {
		t.Errorf("preserved license key is not decryptable after re-init: lk=%q err=%v", lk, err)
	}
}

// TestEnsureLocalContext_FirstRun covers the first-run case: no existing context, so there is
// nothing to preserve — it must create the local context without error and leave creds empty.
func TestEnsureLocalContext_FirstRun(t *testing.T) {
	store := newTempStore(t)

	ctx, err := ensureLocalContext(newDiscardCmd(), store)
	if err != nil {
		t.Fatalf("ensureLocalContext (first run): %v", err)
	}
	if ctx.LicenseKeyEnc != "" || ctx.APIKeyEnc != "" {
		t.Errorf("fresh context should have no stored credentials, got license=%q api=%q", ctx.LicenseKeyEnc, ctx.APIKeyEnc)
	}
	if got, err := store.Get("local"); err != nil || got.Type != "local" {
		t.Errorf("local context not created: ctx=%v err=%v", got, err)
	}
}

// forceInteractive overrides the stdin-TTY check for the duration of a test.
func forceInteractive(t *testing.T, interactive bool) {
	t.Helper()
	prev := stdinIsInteractive
	stdinIsInteractive = func() bool { return interactive }
	t.Cleanup(func() { stdinIsInteractive = prev })
}

// feedStdin replaces os.Stdin with a pipe carrying input (for the fmt.Scanln in promptChoice).
func feedStdin(t *testing.T, input string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	prev := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = prev; _ = r.Close() })
	go func() { _, _ = w.WriteString(input); _ = w.Close() }()
}

func newInitTestCmd(t *testing.T) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	return cmd
}

// TestInitResolveEdition_UsesExistingLicense: when the context already holds a license (prior
// login or 'license set'), init gates on that edition without attempting a network login.
func TestInitResolveEdition_UsesExistingLicense(t *testing.T) {
	store := newTempStore(t)
	licEnc, err := store.EncryptSecret(testLicenseKey(t, "pro"))
	if err != nil {
		t.Fatalf("encrypt license: %v", err)
	}
	ctx := &clicontext.Context{Name: "local", LicenseKeyEnc: licEnc}

	edition, state, err := initResolveEdition(newDiscardCmd(), store, ctx)
	if err != nil {
		t.Fatalf("initResolveEdition: %v", err)
	}
	if edition != "pro" || state != licensePresent {
		t.Errorf("initResolveEdition = (%q, %d), want (pro, licensePresent=%d)", edition, state, licensePresent)
	}
}

// TestInitResolveEdition_DefaultsSkipsLogin: a non-interactive run (--defaults) with no license
// must not attempt a browser login — it reports the stack as unlicensed and lets init continue.
func TestInitResolveEdition_DefaultsSkipsLogin(t *testing.T) {
	store := newTempStore(t)
	ctx := &clicontext.Context{Name: "local"} // no license

	prev := useDefaults
	useDefaults = true
	defer func() { useDefaults = prev }()

	_, state, err := initResolveEdition(newDiscardCmd(), store, ctx)
	if err != nil {
		t.Fatalf("initResolveEdition: %v", err)
	}
	if state != licenseAbsent {
		t.Errorf("state = %d, want licenseAbsent=%d (no login attempted)", state, licenseAbsent)
	}
}

// TestInitResolveEdition_GateBlocksLoginWhenNonInteractive pins defense #1 (the TTY gate): even
// if stdin WOULD say "yes", a non-interactive stdin must stop the device-login before it starts.
// Feeding "yes" is what makes this discriminate — with the gate removed, the login would run.
func TestInitResolveEdition_GateBlocksLoginWhenNonInteractive(t *testing.T) {
	fastPolls(t)
	forceInteractive(t, false) // stdin is NOT a terminal
	feedStdin(t, "yes\nyes\n") // ...and even so, "yes" must not reach a login
	srv, tokenCalls := fakeLicenseServer(t, tokOK(testLicenseKey(t, "pro"), "pro", 0))
	t.Setenv("SUKKO_LICENSE_API_URL", srv.URL)
	store := newTempStore(t)
	ctx := &clicontext.Context{Name: "local"}

	_, state, err := initResolveEdition(newInitTestCmd(t), store, ctx)
	if err != nil {
		t.Fatalf("initResolveEdition: %v", err)
	}
	if state != licenseAbsent {
		t.Errorf("state = %d, want licenseAbsent", state)
	}
	if n := tokenCalls.Load(); n != 0 {
		t.Errorf("the TTY gate did not block a non-interactive login (%d token polls)", n)
	}
}

// TestInitResolveEdition_OfferDefaultsToNo pins defense #2 (the prompt default): an interactive
// operator who just hits Enter (EOF ⇒ default) is NOT dropped into a browser login. With the
// default flipped to "yes", EOF would start the login — so this discriminates that default.
func TestInitResolveEdition_OfferDefaultsToNo(t *testing.T) {
	fastPolls(t)
	forceInteractive(t, true) // a terminal...
	// ...but no stdin fed: promptChoice hits EOF and must use its "no" default.
	srv, tokenCalls := fakeLicenseServer(t, tokOK(testLicenseKey(t, "pro"), "pro", 0))
	t.Setenv("SUKKO_LICENSE_API_URL", srv.URL)
	store := newTempStore(t)
	ctx := &clicontext.Context{Name: "local"}

	_, state, err := initResolveEdition(newInitTestCmd(t), store, ctx)
	if err != nil {
		t.Fatalf("initResolveEdition: %v", err)
	}
	if state != licenseAbsent {
		t.Errorf("state = %d, want licenseAbsent", state)
	}
	if n := tokenCalls.Load(); n != 0 {
		t.Errorf("the login offer did not default to 'no' (%d token polls on EOF)", n)
	}
}

// TestEnsureLocalContext_CorruptStoreFailsLoud pins the §III fix: when the existing local context
// file can't be read (corrupt JSON), init must fail loudly WITHOUT overwriting it — otherwise a
// recoverable license in that file would be destroyed (the data-loss class this change fixes).
func TestEnsureLocalContext_CorruptStoreFailsLoud(t *testing.T) {
	store := newTempStore(t)
	path := filepath.Join(store.Dir(), "local.json")
	junk := []byte("{ this is not valid json")
	if err := os.WriteFile(path, junk, 0o600); err != nil {
		t.Fatalf("seed corrupt file: %v", err)
	}

	_, err := ensureLocalContext(newDiscardCmd(), store)
	if err == nil || !strings.Contains(err.Error(), "not overwriting") {
		t.Fatalf("want a loud error that does not overwrite, got: %v", err)
	}
	after, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatalf("read back: %v", rerr)
	}
	if !bytes.Equal(after, junk) {
		t.Errorf("corrupt local.json was overwritten (data-loss regression): now %q", after)
	}
}

// TestInitResolveEdition_InteractiveLoginSuccess: interactive + "yes" → device login runs against
// the (fake) service and the resolved edition reflects the issued key.
func TestInitResolveEdition_InteractiveLoginSuccess(t *testing.T) {
	fastPolls(t)
	forceInteractive(t, true)
	feedStdin(t, "yes\n")
	key := testLicenseKey(t, "pro")
	srv, _ := fakeLicenseServer(t, tokOK(key, "pro", 0))
	t.Setenv("SUKKO_LICENSE_API_URL", srv.URL)
	store := newTempStore(t)
	ctx := &clicontext.Context{Name: "local"}
	if err := store.Add(ctx); err != nil {
		t.Fatalf("seed ctx: %v", err)
	}

	edition, state, err := initResolveEdition(newInitTestCmd(t), store, ctx)
	if err != nil {
		t.Fatalf("initResolveEdition: %v", err)
	}
	if edition != "pro" || state != licensePresent {
		t.Errorf("after login = (%q, %d), want (pro, licensePresent)", edition, state)
	}
	if stored, _ := ctx.LicenseKey(store.Key()); stored != key {
		t.Error("issued license key was not stored into the context")
	}
}

// TestInitResolveEdition_InteractiveLoginErrorIsNonFatal: a denied/failed login must not abort
// init — it continues unlicensed (err nil, state absent).
func TestInitResolveEdition_InteractiveLoginErrorIsNonFatal(t *testing.T) {
	fastPolls(t)
	forceInteractive(t, true)
	feedStdin(t, "yes\n")
	srv, _ := fakeLicenseServer(t, tokErr("ACCESS_DENIED"))
	t.Setenv("SUKKO_LICENSE_API_URL", srv.URL)
	store := newTempStore(t)
	ctx := &clicontext.Context{Name: "local"}

	_, state, err := initResolveEdition(newInitTestCmd(t), store, ctx)
	if err != nil {
		t.Fatalf("a failed login must be non-fatal, got err: %v", err)
	}
	if state != licenseAbsent {
		t.Errorf("state = %d, want licenseAbsent after a failed login", state)
	}
}

// TestInitResolveEdition_CancelIsFatal: Ctrl-C during the login (a canceled context) aborts init.
func TestInitResolveEdition_CancelIsFatal(t *testing.T) {
	fastPolls(t)
	forceInteractive(t, true)
	feedStdin(t, "yes\n")
	srv, _ := fakeLicenseServer(t, tokOK(testLicenseKey(t, "pro"), "pro", 0))
	t.Setenv("SUKKO_LICENSE_API_URL", srv.URL)
	store := newTempStore(t)
	ctx := &clicontext.Context{Name: "local"}

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	canceled, cancel := context.WithCancel(context.Background())
	cancel() // simulate Ctrl-C before the device flow completes
	cmd.SetContext(canceled)

	_, _, err := initResolveEdition(cmd, store, ctx)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Errorf("cancel during login should be fatal (wrap context.Canceled), got err=%v", err)
	}
}
