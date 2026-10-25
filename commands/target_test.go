package commands

import (
	"bytes"
	"strings"
	"testing"

	clicontext "github.com/sukko-dev/cli/context"
)

// withSentinelColors sets the shared color vars to recognizable sentinels for the duration
// of a test so coloring is asserted deterministically regardless of whether the test host
// is a terminal (output.go blanks them when stdout is not a TTY). It restores them after.
func withSentinelColors(t *testing.T) {
	t.Helper()
	origY, origB, origR := colorYellow, colorBold, colorReset
	colorYellow, colorBold, colorReset = "<Y>", "<B>", "<R>"
	t.Cleanup(func() { colorYellow, colorBold, colorReset = origY, origB, origR })
}

func TestFormatTargetBanner(t *testing.T) {
	withSentinelColors(t)

	const loopbackURL = "http://localhost:18080"
	const remoteURL = "https://api.example.com:8080"

	tests := []struct {
		name           string
		ctx            *clicontext.Context
		url            string
		wantName       string
		wantMode       string // the "(...)" label
		wantDanger     bool   // "⚠" marker + color wrapping
		wantOverridden bool   // "[endpoint overridden]" note
	}{
		{
			name: "no context against loopback default is quiet local",
			ctx:  nil, url: loopbackURL,
			wantName: "no context", wantMode: "local", wantDanger: false,
		},
		{
			name: "no context against a remote url is flagged",
			ctx:  nil, url: remoteURL,
			wantName: "no context", wantMode: "remote", wantDanger: true,
		},
		{
			name: "local context, loopback url",
			ctx:  &clicontext.Context{Name: "dev", Type: clicontext.TypeLocal}, url: loopbackURL,
			wantName: "dev", wantMode: "local", wantDanger: false,
		},
		{
			name: "local context overridden to a remote url is flagged",
			ctx:  &clicontext.Context{Name: "dev", Type: clicontext.TypeLocal}, url: remoteURL,
			wantName: "dev", wantMode: "local", wantDanger: true, wantOverridden: true,
		},
		{
			name: "remote context",
			ctx:  &clicontext.Context{Name: "prod", Type: clicontext.TypeRemote}, url: remoteURL,
			wantName: "prod", wantMode: "remote", wantDanger: true,
		},
		{
			name: "untyped context shown honestly, still flagged",
			ctx:  &clicontext.Context{Name: "legacy"}, url: remoteURL,
			wantName: "legacy", wantMode: "untyped", wantDanger: true,
		},
		{
			name: "unknown type shown verbatim",
			ctx:  &clicontext.Context{Name: "stg", Type: "staging"}, url: remoteURL,
			wantName: "stg", wantMode: "staging", wantDanger: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatTargetBanner(tt.ctx, seamProvisioning, tt.url)

			if !strings.HasSuffix(got, "\n") {
				t.Errorf("banner must end in a newline, got %q", got)
			}
			if !strings.Contains(got, tt.url) {
				t.Errorf("banner must show the effective URL %q, got %q", tt.url, got)
			}
			if !strings.Contains(got, seamProvisioning) {
				t.Errorf("banner must name the seam %q, got %q", seamProvisioning, got)
			}
			if !strings.Contains(got, "("+tt.wantMode+")") {
				t.Errorf("banner mode = want (%s) in %q", tt.wantMode, got)
			}
			if !strings.Contains(got, tt.wantName) {
				t.Errorf("banner must name the context %q, got %q", tt.wantName, got)
			}

			marker := "→"
			if tt.wantDanger {
				marker = "⚠"
			}
			if !strings.HasPrefix(strings.TrimPrefix(got, "<Y><B>"), marker) {
				t.Errorf("banner marker = want %q at start, got %q", marker, got)
			}

			if colored := strings.Contains(got, "<Y>") && strings.Contains(got, "<R>"); colored != tt.wantDanger {
				t.Errorf("banner colored = %v, want %v (danger); got %q", colored, tt.wantDanger, got)
			}
			if overridden := strings.Contains(got, "[endpoint overridden]"); overridden != tt.wantOverridden {
				t.Errorf("banner overridden-note = %v, want %v; got %q", overridden, tt.wantOverridden, got)
			}
		})
	}
}

// TestFormatTargetBanner_NoColorWhenBlanked proves the banner degrades to plain text when
// output.go has blanked the color vars (non-TTY), with no stray escape sequences.
func TestFormatTargetBanner_NoColorWhenBlanked(t *testing.T) {
	origY, origB, origR := colorYellow, colorBold, colorReset
	colorYellow, colorBold, colorReset = "", "", ""
	t.Cleanup(func() { colorYellow, colorBold, colorReset = origY, origB, origR })

	got := formatTargetBanner(&clicontext.Context{Name: "prod", Type: clicontext.TypeRemote}, seamProvisioning, "https://api.example.com")
	if strings.Contains(got, "\x1b") || strings.Contains(got, "\033") {
		t.Errorf("banner must be plain when colors are blanked, got %q", got)
	}
	if !strings.HasPrefix(got, "⚠ ") {
		t.Errorf("remote banner must still carry the ⚠ marker when plain, got %q", got)
	}
}

// TestEmitTargetBanner_WritesToBannerWriter covers the seam wiring: emitTargetBanner reads
// the active context and writes one line to bannerWriter (stderr in production).
func TestEmitTargetBanner_WritesToBannerWriter(t *testing.T) {
	origCtx, origW := resolvedCtx, bannerWriter
	t.Cleanup(func() { resolvedCtx, bannerWriter = origCtx, origW })

	var buf bytes.Buffer
	bannerWriter = &buf
	resolvedCtx = &clicontext.Context{Name: "prod", Type: clicontext.TypeRemote}

	emitTargetBanner(seamProvisioning, "https://api.example.com")

	out := buf.String()
	if strings.Count(out, "\n") != 1 {
		t.Errorf("expected exactly one banner line, got %q", out)
	}
	if !strings.Contains(out, "prod") || !strings.Contains(out, "(remote)") || !strings.Contains(out, "https://api.example.com") {
		t.Errorf("banner line = %q, want it to name the context, mode, and URL", out)
	}
}
