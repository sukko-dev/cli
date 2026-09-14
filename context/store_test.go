package context

import (
	"os"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	store, err := NewStoreWithDir(filepath.Join(dir, "contexts"))
	if err != nil {
		t.Fatalf("NewStoreWithDir: %v", err)
	}
	return store
}

func TestStoreAddAndGet(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)

	ctx := &Context{
		Name:            "test",
		GatewayURL:      "ws://localhost:3000",
		ProvisioningURL: "http://localhost:8080",
		ActiveTenant:    "local",
	}

	if err := store.Add(ctx); err != nil {
		t.Fatalf("Add: %v", err)
	}

	got, err := store.Get("test")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Name != ctx.Name {
		t.Errorf("Name = %q, want %q", got.Name, ctx.Name)
	}
	if got.GatewayURL != ctx.GatewayURL {
		t.Errorf("GatewayURL = %q, want %q", got.GatewayURL, ctx.GatewayURL)
	}
	if got.ProvisioningURL != ctx.ProvisioningURL {
		t.Errorf("ProvisioningURL = %q, want %q", got.ProvisioningURL, ctx.ProvisioningURL)
	}
	if got.ActiveTenant != ctx.ActiveTenant {
		t.Errorf("ActiveTenant = %q, want %q", got.ActiveTenant, ctx.ActiveTenant)
	}
}

func TestStoreAddWithEncryptedSecrets(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)

	tokenEnc, err := store.EncryptSecret("my-admin-token")
	if err != nil {
		t.Fatalf("EncryptSecret: %v", err)
	}

	ctx := &Context{
		Name:          "encrypted",
		GatewayURL:    "ws://localhost:3000",
		AdminTokenEnc: tokenEnc,
	}

	if err := store.Add(ctx); err != nil {
		t.Fatalf("Add: %v", err)
	}

	got, err := store.Get("encrypted")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	token, err := got.AdminToken(store.Key())
	if err != nil {
		t.Fatalf("AdminToken: %v", err)
	}

	if token != "my-admin-token" {
		t.Errorf("AdminToken = %q, want %q", token, "my-admin-token")
	}
}

func TestStoreAddEmptyName(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)

	ctx := &Context{Name: ""}
	if err := store.Add(ctx); err == nil {
		t.Error("expected error for empty name")
	}
}

func TestStoreGetNotFound(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)

	_, err := store.Get("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent context")
	}
}

func TestStoreList(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)

	for _, name := range []string{"alpha", "beta", "gamma"} {
		if err := store.Add(&Context{Name: name, GatewayURL: "ws://localhost"}); err != nil {
			t.Fatalf("Add %s: %v", name, err)
		}
	}

	list, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(list) != 3 {
		t.Errorf("List length = %d, want 3", len(list))
	}
}

func TestStoreRemove(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)

	if err := store.Add(&Context{Name: "removeme", GatewayURL: "ws://localhost"}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := store.Remove("removeme"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	_, err := store.Get("removeme")
	if err == nil {
		t.Error("expected error after removal")
	}
}

func TestStoreRemoveNotFound(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)

	err := store.Remove("nonexistent")
	if err == nil {
		t.Error("expected error removing nonexistent context")
	}
}

func TestStoreActiveContext(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)

	if err := store.Add(&Context{Name: "myctx", GatewayURL: "ws://localhost"}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := store.SetActive("myctx"); err != nil {
		t.Fatalf("SetActive: %v", err)
	}

	name, err := store.ActiveName()
	if err != nil {
		t.Fatalf("ActiveName: %v", err)
	}

	if name != "myctx" {
		t.Errorf("ActiveName = %q, want %q", name, "myctx")
	}

	ctx, err := store.Active()
	if err != nil {
		t.Fatalf("Active: %v", err)
	}

	if ctx.Name != "myctx" {
		t.Errorf("Active().Name = %q, want %q", ctx.Name, "myctx")
	}
}

func TestStoreSetActiveNonexistent(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)

	err := store.SetActive("nonexistent")
	if err == nil {
		t.Error("expected error setting nonexistent context as active")
	}
}

func TestStoreNoActiveContext(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)

	_, err := store.ActiveName()
	if err == nil {
		t.Error("expected error when no active context")
	}
}

func TestStoreRemoveClearsActive(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)

	if err := store.Add(&Context{Name: "todelete", GatewayURL: "ws://localhost"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := store.SetActive("todelete"); err != nil {
		t.Fatalf("SetActive: %v", err)
	}

	if err := store.Remove("todelete"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	_, err := store.ActiveName()
	if err == nil {
		t.Error("expected error after removing active context")
	}
}

func TestLicenseKeyRoundTrip(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)

	// Encrypt and store
	licenseKey := "eyJlZGl0aW9uIjoicHJvIn0.c2lnbmF0dXJl"
	enc, err := store.EncryptSecret(licenseKey)
	if err != nil {
		t.Fatalf("EncryptSecret: %v", err)
	}

	ctx := &Context{
		Name:          "license-test",
		GatewayURL:    "ws://localhost",
		LicenseKeyEnc: enc,
	}
	if err := store.Add(ctx); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Retrieve and decrypt
	got, err := store.Get("license-test")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	decrypted, err := got.LicenseKey(store.Key())
	if err != nil {
		t.Fatalf("LicenseKey: %v", err)
	}
	if decrypted != licenseKey {
		t.Errorf("LicenseKey = %q, want %q", decrypted, licenseKey)
	}
}

func TestLicenseKeyEmpty(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	ctx := &Context{Name: "no-license", GatewayURL: "ws://localhost"}
	if err := store.Add(ctx); err != nil {
		t.Fatalf("Add: %v", err)
	}

	got, err := store.Get("no-license")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	val, err := got.LicenseKey(store.Key())
	if err != nil {
		t.Fatalf("LicenseKey: %v", err)
	}
	if val != "" {
		t.Errorf("LicenseKey = %q, want empty", val)
	}
}

func TestStoreFilePermissions(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)

	if err := store.Add(&Context{Name: "perms", GatewayURL: "ws://localhost"}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	path := filepath.Join(store.dir, "perms.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != contextFilePerms {
		t.Errorf("file permissions = %o, want %o", perm, contextFilePerms)
	}
}

func TestFindLocalContext_OneLocal(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	_ = store.Add(&Context{Name: "local", Type: "local", GatewayURL: "ws://localhost:3000"})
	_ = store.Add(&Context{Name: "staging", Type: "remote", GatewayURL: "ws://staging:3000"})

	ctx, err := store.FindLocalContext()
	if err != nil {
		t.Fatalf("FindLocalContext: %v", err)
	}
	if ctx == nil {
		t.Fatal("expected local context, got nil")
	}
	if ctx.Name != "local" {
		t.Errorf("Name = %q, want %q", ctx.Name, "local")
	}
	if ctx.Type != "local" {
		t.Errorf("Type = %q, want %q", ctx.Type, "local")
	}
}

func TestFindLocalContext_NoLocal(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)

	ctx, err := store.FindLocalContext()
	if err != nil {
		t.Fatalf("FindLocalContext: %v", err)
	}
	if ctx != nil {
		t.Errorf("expected nil, got %+v", ctx)
	}
}

func TestFindLocalContext_OnlyRemote(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	_ = store.Add(&Context{Name: "staging", Type: "remote", GatewayURL: "ws://staging:3000"})
	_ = store.Add(&Context{Name: "prod", Type: "remote", GatewayURL: "ws://prod:3000"})

	ctx, err := store.FindLocalContext()
	if err != nil {
		t.Fatalf("FindLocalContext: %v", err)
	}
	if ctx != nil {
		t.Errorf("expected nil, got %+v", ctx)
	}
}

func TestStoreList_SkipsCorruptContext(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)

	if err := store.Add(&Context{Name: "valid", GatewayURL: "ws://localhost"}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Write a corrupt JSON file directly into the contexts directory
	corruptPath := filepath.Join(store.Dir(), "corrupt.json")
	if err := os.WriteFile(corruptPath, []byte("not-valid-json{{{"), 0o600); err != nil {
		t.Fatalf("WriteFile corrupt: %v", err)
	}

	list, err := store.List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("List length = %d, want 1 (corrupt entry should be skipped)", len(list))
	}
	if list[0].Name != "valid" {
		t.Errorf("List[0].Name = %q, want %q", list[0].Name, "valid")
	}
}
