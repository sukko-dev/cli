//go:build e2e

// Package e2e drives the real `sukko` binary against a live, hermetic docker-compose
// stack. The stack runs platform images built from source with the `sukko_e2e` build
// tag (which embeds a dev license-signing key), and the license key injected via
// SUKKO_LICENSE_KEY is minted by the same dev key — so signature verification passes
// without any production key. The bootstrap (build dev images, mint the key, export
// SUKKO_LICENSE_KEY) is orchestrated by e2e/run.sh; these tests assume it has run.
//
// Every test gets an isolated HOME so it never touches the developer's real ~/.sukko
// config or context. The suite is heavy (boots Docker) and build-tagged `e2e`, so it
// is excluded from the normal `go test ./...` run and only executes via run.sh.
package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// projectSeq makes each cli's compose project name unique within a run.
var projectSeq atomic.Int64

// sukkoBin is the absolute path to the CLI binary built once in TestMain.
var sukkoBin string

// TestMain builds the sukko binary a single time for the whole suite, then runs it.
func TestMain(m *testing.M) {
	bin, cleanup, err := buildSukko()
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e: build sukko binary:", err)
		os.Exit(1)
	}
	sukkoBin = bin
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// buildSukko compiles the CLI from the repo root into a temp directory and returns the
// binary path plus a cleanup func. Building once keeps per-command latency to an exec.
func buildSukko() (bin string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "sukko-e2e-bin-")
	if err != nil {
		return "", nil, err
	}
	bin = filepath.Join(dir, "sukko")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = ".." // repo root; the suite lives in e2e/
	if out, buildErr := cmd.CombinedOutput(); buildErr != nil {
		_ = os.RemoveAll(dir)
		return "", nil, fmt.Errorf("go build: %w\n%s", buildErr, out)
	}
	return bin, func() { _ = os.RemoveAll(dir) }, nil
}

// cli is an isolated CLI invocation environment. The CLI splits its state across two
// roots: the project config + compose file are CWD-relative (./.sukko/), while the
// context store (the encrypted license key, provisioning URL) lives under the user's
// config dir (HOME-based). So isolation needs BOTH a fresh working directory and a
// fresh HOME, or a test would read/clobber the developer's real ~/.sukko and project.
// The working directory also names the compose project, so each cli gets its own stack.
type cli struct {
	t    *testing.T
	dir  string // working directory — holds ./.sukko/ and names the compose project
	home string // isolated HOME — backs the context store (os.UserConfigDir)
	env  []string
}

// newCLI returns a CLI bound to a fresh working directory and HOME. SUKKO_LICENSE_KEY
// (minted by run.sh) is inherited from the process environment so `sukko up` passes it
// to compose. XDG_CONFIG_HOME is pinned under HOME so os.UserConfigDir is deterministic
// on Linux regardless of the outer shell.
func newCLI(t *testing.T) *cli {
	t.Helper()
	dir := t.TempDir()
	home := t.TempDir()
	// Overriding HOME hides the Docker CLI plugin directory (~/.docker/cli-plugins),
	// so `docker compose` stops being recognized. Point DOCKER_CONFIG back at the real
	// ~/.docker (computed before the override) so the compose plugin is still found,
	// while the context store stays isolated under the temp HOME.
	dockerConfig := os.Getenv("DOCKER_CONFIG")
	if dockerConfig == "" {
		if realHome, err := os.UserHomeDir(); err == nil {
			dockerConfig = filepath.Join(realHome, ".docker")
		}
	}
	// The compose project name otherwise derives from the fixed ./.sukko config dir, so
	// every `sukko up` would share one project and one postgres volume — including the
	// developer's real local stack. A unique COMPOSE_PROJECT_NAME isolates each test's
	// stack and volumes (the CLI passes no -p and the compose declares no name:, so the
	// env var wins), which also gives provisioning a clean DB to register the admin key
	// into on first start. Teardown uses `down -v` to remove the per-test volume.
	project := fmt.Sprintf("sukko-e2e-%d-%d", os.Getpid(), projectSeq.Add(1))
	env := append(os.Environ(),
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"DOCKER_CONFIG="+dockerConfig,
		"COMPOSE_PROJECT_NAME="+project,
	)
	return &cli{t: t, dir: dir, home: home, env: env}
}

// run execs `sukko <args...>` with a default timeout and fails the test if it exits
// non-zero. It returns the combined stdout+stderr for assertions.
func (c *cli) run(args ...string) string {
	c.t.Helper()
	out, err := c.runErr(args...)
	if err != nil {
		c.t.Fatalf("sukko %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// runErr execs `sukko <args...>` and returns the combined output and any exit error,
// without failing the test — for commands whose failure the caller asserts on.
func (c *cli) runErr(args ...string) (string, error) {
	c.t.Helper()
	return c.runCtx(context.Background(), defaultTimeout, args...)
}

// up runs `sukko up` with the longer boot timeout (image start + health waits) and
// fails the test on error. Extra args (e.g. a message backend flag) are appended.
func (c *cli) up(args ...string) string {
	c.t.Helper()
	out, err := c.runCtx(context.Background(), upTimeout, append([]string{"up"}, args...)...)
	if err != nil {
		c.t.Fatalf("sukko up failed: %v\n%s", err, out)
	}
	return out
}

// runCtx is the core exec: it applies a timeout, the isolated HOME/env, and returns the
// combined output. A timeout surfaces as a clear error rather than a hung test.
func (c *cli) runCtx(parent context.Context, timeout time.Duration, args ...string) (string, error) {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, sukkoBin, args...)
	cmd.Env = c.env
	cmd.Dir = c.dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return buf.String(), fmt.Errorf("sukko %s timed out after %s", strings.Join(args, " "), timeout)
	}
	return buf.String(), err
}

const (
	// defaultTimeout bounds a single CLI command. `up` is the long pole (image start +
	// health waits), so it gets its own longer timeout via runCtx.
	defaultTimeout = 90 * time.Second
	upTimeout      = 5 * time.Minute
)

// requireLicenseKey skips the test if run.sh did not export a minted key — the suite is
// meaningless without a signature-valid license for the sukko_e2e stack.
func requireLicenseKey(t *testing.T) {
	t.Helper()
	if strings.TrimSpace(os.Getenv("SUKKO_LICENSE_KEY")) == "" {
		t.Skip("SUKKO_LICENSE_KEY not set — run via e2e/run.sh (mints a dev key)")
	}
}

// requireDocker skips the test if the Docker daemon is not reachable.
func requireDocker(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "info").Run(); err != nil {
		t.Skip("docker not available — skipping e2e")
	}
}
