package commands

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/sukko-dev/cli/client"
	clicontext "github.com/sukko-dev/cli/context"
)

// =============================================================================
// Quick-start repair tests. The documented flow is `sukko init` → `sukko up`
// (README), and `up` promises a ready-to-use demo tenant. An empirical audit
// (2026-08-31, cli-main binary against the published images) found the flow
// broken three ways on a pristine machine:
//   1. init never generated the admin keypair `up` hard-requires — and up's
//      hint pointed back at init (a circle);
//   2. provisionDefaultTenant sent {"id": ...} where the provisioning API has
//      only ever accepted {"slug": ...} (issue #53) — the demo tenant 500'd;
//   3. the 500 was swallowed as "(may already exist)" and `up` still printed
//      "Sukko is ready!" — a silent failure (§III).
// =============================================================================

// ensureAdminKeypair must create a keypair on first call and be idempotent —
// `sukko init` calls it, and re-running init on an existing context must not
// regenerate (invalidating the key provisioning already bootstrapped).
func TestEnsureAdminKeypair_CreatesThenIdempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	created, pub, err := ensureAdminKeypair(dir)
	if err != nil {
		t.Fatalf("first ensureAdminKeypair: %v", err)
	}
	if !created || pub == "" {
		t.Fatalf("first call: created=%v pub=%q, want created=true and non-empty pub", created, pub)
	}
	for _, f := range []string{"admin.key", "admin.pub", "admin.kid"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("missing %s after keygen: %v", f, err)
		}
	}
	// Private key must not be group/world readable.
	info, err := os.Stat(filepath.Join(dir, "admin.key"))
	if err != nil {
		t.Fatalf("stat admin.key: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("admin.key perms = %o, want 600", perm)
	}

	pubBytes, err := os.ReadFile(filepath.Join(dir, "admin.pub"))
	if err != nil {
		t.Fatalf("read admin.pub: %v", err)
	}

	created2, pub2, err := ensureAdminKeypair(dir)
	if err != nil {
		t.Fatalf("second ensureAdminKeypair: %v", err)
	}
	if created2 {
		t.Error("second call reports created=true — must be idempotent, never regenerate")
	}
	if pub2 == "" {
		t.Error("second call must still report the existing public key")
	}
	pubBytes2, _ := os.ReadFile(filepath.Join(dir, "admin.pub"))
	if !bytes.Equal(pubBytes, pubBytes2) {
		t.Error("admin.pub changed across calls — existing keypair was regenerated")
	}
}

// The tenant-create payload must use the provisioning API's field names —
// "slug", never "id" (issue #53: "id" unmarshals nowhere, the server sees an
// empty slug and rejects the create).
func TestBuildTenantCreateRequest_UsesSlug(t *testing.T) {
	t.Parallel()
	req := buildTenantCreateRequest("demo", "Demo", "shared")

	if got, ok := req["slug"]; !ok || got != "demo" {
		t.Errorf(`req["slug"] = %v (present=%v), want "demo"`, got, ok)
	}
	if _, hasID := req["id"]; hasID {
		t.Error(`req carries "id" — the API has no such field; it must not be sent`)
	}
	if got := req["name"]; got != "Demo" {
		t.Errorf(`req["name"] = %v, want "Demo"`, got)
	}
	if got := req["consumer_type"]; got != "shared" {
		t.Errorf(`req["consumer_type"] = %v, want "shared"`, got)
	}
}

// classifyProvisionError drives provisionDefaultTenant's outcome handling:
// only a 409 conflict may be treated as "already exists"; an edition gate is a
// clean skip (routing rules are not needed for the direct backend or
// kafka ingest); anything else is a real failure that must surface loudly
// (the audit found a 500 CREATE_FAILED reported as "(may already exist)").
func TestClassifyProvisionError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want provisionOutcome
	}{
		{"nil is ok", nil, provisionOK},
		{"409 conflict = already exists", fmt.Errorf("create: %w: {\"code\":\"ALREADY_EXISTS\"}", client.ErrAPIConflict), provisionExists},
		{"403 EDITION_LIMIT = edition gated", fmt.Errorf("rules: %w: {\"code\":\"EDITION_LIMIT\",\"message\":\"requires pro\"}", client.ErrAPIForbidden), provisionEditionGated},
		{"plain 403 is a failure, not a gate", fmt.Errorf("rules: %w: {\"code\":\"FORBIDDEN\"}", client.ErrAPIForbidden), provisionFailed},
		{"500 is a failure — never 'may already exist'", fmt.Errorf("create: %w (HTTP 500): {\"code\":\"CREATE_FAILED\"}", client.ErrAPIInternal), provisionFailed},
		{"400 is a failure", fmt.Errorf("create: %w (HTTP 400): bad", client.ErrAPIBadRequest), provisionFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := classifyProvisionError(tt.err); got != tt.want {
				t.Errorf("classifyProvisionError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// A 409 from the generic request path must surface as ErrAPIConflict so
// callers can discriminate "already exists" from real 4xx failures.
func TestConflictSentinelIsDistinct(t *testing.T) {
	t.Parallel()
	if errors.Is(client.ErrAPIConflict, client.ErrAPIBadRequest) {
		t.Error("ErrAPIConflict must be a distinct sentinel, not wrap ErrAPIBadRequest")
	}
}

// quickstartTestEnv points the command globals at a temp context store (with a
// generated admin keypair) and a fake provisioning server, restoring the
// originals on cleanup. Callers must NOT use t.Parallel() — package globals.
func quickstartTestEnv(t *testing.T, handler http.Handler) {
	t.Helper()

	dir := t.TempDir()
	store, err := clicontext.NewStoreWithDir(filepath.Join(dir, "contexts"))
	if err != nil {
		t.Fatalf("NewStoreWithDir: %v", err)
	}
	if _, _, err := ensureAdminKeypair(filepath.Join(store.Dir(), "local")); err != nil {
		t.Fatalf("ensureAdminKeypair: %v", err)
	}

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	origURL, origCtx, origStore := apiURL, resolvedCtx, resolvedStore
	t.Cleanup(func() { apiURL, resolvedCtx, resolvedStore = origURL, origCtx, origStore })
	apiURL = srv.URL
	resolvedCtx = &clicontext.Context{Name: "local"}
	resolvedStore = store
}

// newQuickstartCmd returns a throwaway command with captured output.
func newQuickstartCmd(t *testing.T) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetContext(context.Background())
	return cmd, &out
}

// TestProvisionDefaultTenant_Wiring pins the call-site behavior the audit
// found broken — not just the extracted helpers. A regression back to
// "swallow the error and continue" flips these outcomes.
func TestProvisionDefaultTenant_Wiring(t *testing.T) {
	// Not parallel: quickstartTestEnv mutates package globals.

	writeJSON := func(w http.ResponseWriter, status int, body string) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}

	t.Run("create 500 aborts loudly — never 'may already exist'", func(t *testing.T) {
		quickstartTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusInternalServerError, `{"code":"CREATE_FAILED","message":"boom"}`)
		}))
		cmd, out := newQuickstartCmd(t)
		err := provisionDefaultTenant(cmd)
		if err == nil {
			t.Fatalf("want error on 500 create, got nil (output: %s)", out.String())
		}
		if strings.Contains(out.String(), "may already exist") {
			t.Error("500 reported as 'may already exist' — the silent-failure regression")
		}
	})

	t.Run("409 create is an idempotent re-run; EDITION_LIMIT rules skip cleanly", func(t *testing.T) {
		quickstartTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/api/v1/tenants":
				writeJSON(w, http.StatusConflict, `{"code":"ALREADY_EXISTS"}`)
			case strings.HasSuffix(r.URL.Path, "/routing-rules"):
				writeJSON(w, http.StatusForbidden, `{"code":"EDITION_LIMIT","message":"requires pro"}`)
			default: // channel-rules
				writeJSON(w, http.StatusOK, `{}`)
			}
		}))
		cmd, out := newQuickstartCmd(t)
		if err := provisionDefaultTenant(cmd); err != nil {
			t.Fatalf("conflict + edition gate must succeed, got: %v", err)
		}
		for _, want := range []string{"already exists", "skipped", "Channel rules: set"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("output missing %q:\n%s", want, out.String())
			}
		}
	})

	t.Run("channel-rules failure aborts — a deny-all demo tenant is not ready", func(t *testing.T) {
		quickstartTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/api/v1/tenants":
				writeJSON(w, http.StatusCreated, `{"slug":"demo"}`)
			case strings.HasSuffix(r.URL.Path, "/routing-rules"):
				writeJSON(w, http.StatusOK, `{}`)
			default: // channel-rules
				writeJSON(w, http.StatusInternalServerError, `{"code":"INTERNAL"}`)
			}
		}))
		cmd, _ := newQuickstartCmd(t)
		if err := provisionDefaultTenant(cmd); err == nil {
			t.Fatal("want error when channel-rules seeding fails, got nil")
		}
	})

	t.Run("happy path", func(t *testing.T) {
		quickstartTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, `{}`)
		}))
		cmd, out := newQuickstartCmd(t)
		if err := provisionDefaultTenant(cmd); err != nil {
			t.Fatalf("happy path: %v", err)
		}
		if !strings.Contains(out.String(), "Tenant 'demo': created") {
			t.Errorf("output missing created line:\n%s", out.String())
		}
	})
}
