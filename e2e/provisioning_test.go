//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestProvisioningAndDataPath is Phase 2a: it exercises the provisioning surface (the
// demo tenant auto-created by `sukko up`, plus a tenant create + read-back) and a
// data-path smoke — `sukko test smoke` drives the in-stack tester against the live
// gateway, so a green run proves the WebSocket path end to end, not just that containers
// are healthy. Like the lifecycle test it owns a stack and is not parallel.
func TestProvisioningAndDataPath(t *testing.T) {
	c, _ := bootCommunityStack(t)

	// `sukko up` provisions a `demo` tenant — the provisioning API is reachable and the
	// bootstrap ran.
	if list := c.run("tenant", "list"); !strings.Contains(list, "demo") {
		t.Errorf("tenant list is missing the auto-created demo tenant:\n%s", list)
	}

	// Create a tenant and read it back from a fresh list (write + read path).
	c.run("tenant", "create", "--slug", "e2e-acme", "--name", "E2E Acme")
	if list := c.run("tenant", "list"); !strings.Contains(list, "e2e-acme") {
		t.Errorf("tenant list is missing the created e2e-acme tenant:\n%s", list)
	}

	// Data-path smoke: the tester opens WebSocket connections through the gateway and
	// runs a quick connectivity/health check. A non-zero exit means the data path is
	// broken; that is the assertion (the exact metrics output is not pinned).
	if out, err := c.runCtx(context.Background(), dataPathTimeout, "test", "smoke"); err != nil {
		t.Fatalf("sukko test smoke failed (data path): %v\n%s", err, out)
	}
}

// dataPathTimeout bounds `sukko test smoke`, which runs a short connection test (default
// ~30s) plus tester setup.
const dataPathTimeout = 4 * time.Minute
