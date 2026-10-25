package commands

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	clicontext "github.com/sukko-dev/cli/context"
)

// annotMutates marks a command that changes state on a stack (create/update/delete/
// revoke/…). Commands carrying it are confirmed before they run against a remote target.
const annotMutates = "sukko_mutates"

// assumeYes is set by the persistent --yes/-y flag and skips the remote-mutation
// confirmation (for scripts and CI).
var assumeYes bool

// markMutating tags commands as state-changing so PersistentPreRunE confirms them against a
// remote target. Called from each command group's init. The companion test asserts the full
// set is tagged, so a new write that forgets this fails CI rather than skipping the gate.
func markMutating(cmds ...*cobra.Command) {
	for _, c := range cmds {
		if c.Annotations == nil {
			c.Annotations = map[string]string{}
		}
		c.Annotations[annotMutates] = "true"
	}
}

// targetIsRemote reports whether the effective provisioning endpoint is anything other than
// a known-local dial — the same notion the target banner flags. A non-local context is
// remote even over a loopback tunnel; and a local (or absent) context whose URL was
// overridden off-box is remote too. Type stays authoritative for the context's own label;
// the URL check only widens the net so an override cannot dodge the prompt.
func targetIsRemote(effectiveURL string) bool {
	if resolvedCtx != nil && !resolvedCtx.IsLocal() {
		return true
	}
	return !clicontext.IsLoopback(effectiveURL)
}

// confirmRemoteMutation gates a state-changing command when it targets a remote stack, so a
// destructive op against production is never a single keystroke. Local targets pass
// silently. With --yes it passes. Otherwise it prompts on a terminal, and on a non-TTY
// (script/CI) it fails fast with a clear message instead of hanging on a prompt or silently
// assuming an answer.
func confirmRemoteMutation(cmd *cobra.Command) error {
	url := resolveProvisioningURL()
	if !targetIsRemote(url) {
		return nil
	}

	// A plaintext remote is refused outright (§IX) — don't prompt for something we will
	// reject at the dial seam anyway. This keeps the confirm and the TLS gate consistent.
	if err := clicontext.RequireSecureURL(url); err != nil {
		return fmt.Errorf("refusing insecure provisioning target: %w", err)
	}

	action := cmd.CommandPath()
	name := "no context"
	if resolvedCtx != nil {
		name = resolvedCtx.Name
	}

	if assumeYes {
		return nil
	}
	if !stdinIsInteractive() {
		return fmt.Errorf("refusing to run %q against remote context %q (%s) without confirmation — re-run with --yes", action, name, url)
	}

	// Prompt on stderr so -o json / piped stdout stays clean.
	fmt.Fprintf(cmd.ErrOrStderr(), "About to run %q against remote context %q (%s).\n", action, name, url)
	answer, err := promptChoiceW(cmd.ErrOrStderr(), "Continue?", []string{"yes", "no"}, "no")
	if err != nil {
		return fmt.Errorf("read confirmation: %w", err)
	}
	if answer != "yes" {
		return errors.New("aborted")
	}
	return nil
}
