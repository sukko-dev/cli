package commands

import (
	"fmt"
	"io"
	"os"

	clicontext "github.com/sukko-dev/cli/context"
)

// Dial-seam labels for the context banner — which API a command is about to reach.
const (
	seamProvisioning = "provisioning API"
	seamGateway      = "gateway"
	seamTester       = "tester"
)

// bannerWriter is where the target banner goes. It is stderr so stdout stays clean for
// -o json and piping; it is a package var so tests can capture the line.
var bannerWriter io.Writer = os.Stderr

// emitTargetBanner writes a one-line indicator of which stack a command is about to
// operate against (#8b gap 1), so local-vs-remote is unmistakable before a mutation lands.
// It is advisory — written to stderr, never affecting stdout — and shows the EFFECTIVE URL
// after flag/env/context precedence, not the raw context URL, so it cannot claim to reach
// one endpoint while the command dials another.
func emitTargetBanner(seam, effectiveURL string) {
	fmt.Fprint(bannerWriter, formatTargetBanner(resolvedCtx, seam, effectiveURL))
}

// formatTargetBanner renders the banner line (with trailing newline). It is pure so the
// wording is directly testable.
//
// The mode label reports the context's real Type (never inferred) — "local", "remote",
// "untyped" for a legacy/hand-edited context, or the raw Type otherwise — and for no active
// context it is derived from the effective URL, the only signal available then.
//
// The danger treatment — a "⚠" marker and color — flags anything that is not a known-local
// dial, so it can never reassure an operator who is about to hit a remote stack. It fires
// when the context is non-local, OR when the effective URL is not loopback (a remote
// endpoint reached with no context, or a local context whose URL was overridden by
// --api-url/SUKKO_* to point off-box). The URL check is advisory on the display only; Type
// stays authoritative for the mode label and for every behavioral gate (see IsLocal).
//
// Color uses the shared colorYellow/colorBold/colorReset, which output.go blanks when
// stdout is not a terminal, keeping one color policy across the CLI.
func formatTargetBanner(ctx *clicontext.Context, seam, effectiveURL string) string {
	remoteURL := !clicontext.IsLoopback(effectiveURL)

	var name, mode string
	var danger, overridden bool
	switch {
	case ctx == nil:
		// No context: there is no Type to trust, so judge the effective URL itself.
		name = "no context"
		mode, danger = "local", false
		if remoteURL {
			mode, danger = "remote", true
		}
	case ctx.Type == clicontext.TypeLocal:
		name, mode = ctx.Name, "local"
		// A local context's URLs are loopback by construction; a non-loopback effective
		// URL means a flag/env override is sending this off-box — flag it plainly.
		if remoteURL {
			danger, overridden = true, true
		}
	case ctx.Type == clicontext.TypeRemote:
		name, mode, danger = ctx.Name, "remote", true
	case ctx.Type == "":
		name, mode, danger = ctx.Name, "untyped", true
	default:
		name, mode, danger = ctx.Name, ctx.Type, true
	}

	marker := "→"
	if danger {
		marker = "⚠"
	}
	body := fmt.Sprintf("%s %s: %s (%s) · %s", marker, seam, name, mode, effectiveURL)
	if overridden {
		body += " [endpoint overridden]"
	}
	if danger {
		body = colorYellow + colorBold + body + colorReset
	}
	return body + "\n"
}
