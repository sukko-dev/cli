package commands

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	clicontext "github.com/sukko-dev/cli/context"
)

func TestTargetIsRemote(t *testing.T) {
	tests := []struct {
		name string
		ctx  *clicontext.Context
		url  string
		want bool
	}{
		{name: "local context, loopback url", ctx: &clicontext.Context{Type: clicontext.TypeLocal}, url: "http://localhost:18080", want: false},
		{name: "remote context", ctx: &clicontext.Context{Type: clicontext.TypeRemote}, url: "https://api.example.com", want: true},
		{name: "remote context even over loopback tunnel", ctx: &clicontext.Context{Type: clicontext.TypeRemote}, url: "http://localhost:18080", want: true},
		{name: "no context, loopback default", ctx: nil, url: "http://localhost:18080", want: false},
		{name: "no context, remote url", ctx: nil, url: "https://api.example.com", want: true},
		{name: "local context overridden to a remote url", ctx: &clicontext.Context{Type: clicontext.TypeLocal}, url: "https://api.example.com", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orig := resolvedCtx
			resolvedCtx = tt.ctx
			defer func() { resolvedCtx = orig }()
			if got := targetIsRemote(tt.url); got != tt.want {
				t.Errorf("targetIsRemote(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}

func TestConfirmRemoteMutation(t *testing.T) {
	// A throwaway command whose CommandPath appears in messages; no parent needed.
	newCmd := func() *cobra.Command { return &cobra.Command{Use: "delete"} }

	t.Run("local target never prompts or errors", func(t *testing.T) {
		withResolvedContext(t, &clicontext.Context{Name: "dev", Type: clicontext.TypeLocal, ProvisioningURL: "http://localhost:18080"})
		forceInteractive(t, false) // even non-TTY: a local target must not be gated
		if err := confirmRemoteMutation(newCmd()); err != nil {
			t.Errorf("local target should pass, got %v", err)
		}
	})

	t.Run("remote target with --yes passes", func(t *testing.T) {
		withResolvedContext(t, &clicontext.Context{Name: "prod", Type: clicontext.TypeRemote, ProvisioningURL: "https://api.example.com"})
		withAssumeYes(t, true)
		forceInteractive(t, false)
		if err := confirmRemoteMutation(newCmd()); err != nil {
			t.Errorf("--yes should skip the prompt, got %v", err)
		}
	})

	t.Run("remote target non-interactive without --yes fails fast", func(t *testing.T) {
		withResolvedContext(t, &clicontext.Context{Name: "prod", Type: clicontext.TypeRemote, ProvisioningURL: "https://api.example.com"})
		withAssumeYes(t, false)
		forceInteractive(t, false)
		err := confirmRemoteMutation(newCmd())
		if err == nil {
			t.Fatal("non-TTY without --yes must fail, not proceed")
		}
		if !strings.Contains(err.Error(), "--yes") || !strings.Contains(err.Error(), "prod") {
			t.Errorf("error should name the context and point to --yes, got %v", err)
		}
	})

	t.Run("remote target interactive, answered yes, proceeds", func(t *testing.T) {
		withResolvedContext(t, &clicontext.Context{Name: "prod", Type: clicontext.TypeRemote, ProvisioningURL: "https://api.example.com"})
		withAssumeYes(t, false)
		forceInteractive(t, true)
		feedStdin(t, "yes\n")
		if err := confirmRemoteMutation(newCmd()); err != nil {
			t.Errorf("a yes answer should proceed, got %v", err)
		}
	})

	t.Run("interactive prompt goes to stderr, keeping stdout clean", func(t *testing.T) {
		withResolvedContext(t, &clicontext.Context{Name: "prod", Type: clicontext.TypeRemote, ProvisioningURL: "https://api.example.com"})
		withAssumeYes(t, false)
		forceInteractive(t, true)
		feedStdin(t, "yes\n")

		cmd := &cobra.Command{Use: "delete"}
		var out, errb bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&errb)
		if err := confirmRemoteMutation(cmd); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.Len() != 0 {
			t.Errorf("stdout must stay clean during confirm, got %q", out.String())
		}
		if !strings.Contains(errb.String(), "Continue?") {
			t.Errorf("the prompt must go to stderr, got %q", errb.String())
		}
	})

	t.Run("remote target interactive, answered no, aborts", func(t *testing.T) {
		withResolvedContext(t, &clicontext.Context{Name: "prod", Type: clicontext.TypeRemote, ProvisioningURL: "https://api.example.com"})
		withAssumeYes(t, false)
		forceInteractive(t, true)
		feedStdin(t, "no\n")
		err := confirmRemoteMutation(newCmd())
		if err == nil || !strings.Contains(err.Error(), "aborted") {
			t.Errorf("a no answer should abort, got %v", err)
		}
	})
}

// mutatingCommands is the exhaustive set of leaf commands that change state on a stack and
// must carry the remote-mutation confirmation. readOnlyOrLocalCommands is every other leaf:
// a read, a local-only op, or client-side crypto. Together they must cover EVERY leaf, so a
// newly added command that is left unclassified fails TestLeafCommandsAreClassified — which
// is what makes this an intent oracle, not a mirror of the code (a forgotten markMutating
// would otherwise be absent from both the code and a hand-written want list and pass).
var mutatingCommands = []string{
	"sukko tenant create", "sukko tenant update", "sukko tenant suspend",
	"sukko tenant reactivate", "sukko tenant deprovision", "sukko tenant delete",
	"sukko keys create", "sukko keys revoke",
	"sukko api-keys create", "sukko api-keys revoke",
	"sukko rules routing set", "sukko rules routing delete", "sukko rules routing add",
	"sukko rules channels set", "sukko rules channels delete",
	"sukko topics create", "sukko topics delete",
	"sukko webhook create", "sukko webhook update", "sukko webhook delete",
	"sukko quota update",
	"sukko license push",
	"sukko auth register", "sukko auth revoke",
	"sukko token revoke",
}

var readOnlyOrLocalCommands = []string{
	"sukko tenant get", "sukko tenant list",
	"sukko keys list",
	"sukko api-keys list",
	"sukko rules routing get", "sukko rules channels get", "sukko rules test-access",
	"sukko topics list",
	"sukko webhook list", "sukko webhook get", "sukko webhook test",
	"sukko quota get",
	"sukko license set", "sukko license show", "sukko license remove",
	"sukko license login", "sukko license logout",
	"sukko auth keygen", "sukko auth pubkey", "sukko auth list",
	"sukko token generate", "sukko token validate",
	"sukko edition", "sukko edition compare",
	"sukko config view", "sukko config defaults",
	"sukko publish", "sukko subscribe", "sukko connections test",
	"sukko grafana",
	"sukko test smoke", "sukko test load", "sukko test stress", "sukko test soak", "sukko test validate",
	"sukko up", "sukko down", "sukko status", "sukko init", "sukko logs", "sukko health",
	"sukko version",
	"sukko context add", "sukko context create", "sukko context use",
	"sukko context list", "sukko context remove", "sukko context current",
}

// runnableCommandPaths returns the CommandPath of every runnable command — one with a Run
// function, i.e. one that can actually dial a stack (including a runnable parent like
// `edition` that also has subcommands). Pure routing groups and cobra's auto-generated
// help/completion subtrees are excluded.
func runnableCommandPaths() []string {
	var cmds []string
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Name() == "help" || c.Name() == "completion" {
			return
		}
		if c.Runnable() {
			cmds = append(cmds, c.CommandPath())
		}
		for _, child := range c.Commands() {
			walk(child)
		}
	}
	walk(rootCmd)
	return cmds
}

// TestMutatingCommandsAreMarked checks the tagged set is exactly mutatingCommands.
func TestMutatingCommandsAreMarked(t *testing.T) {
	var got []string
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Annotations[annotMutates] == "true" {
			got = append(got, c.CommandPath())
		}
		for _, child := range c.Commands() {
			walk(child)
		}
	}
	walk(rootCmd)

	want := slices.Clone(mutatingCommands)
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(want, got) {
		t.Errorf("marked mutating commands mismatch:\n want: %v\n  got: %v", want, got)
	}
}

// TestLeafCommandsAreClassified fails if any leaf is in neither list (a new command nobody
// classified) or in both. This is the guard that forces a decision on every new command.
func TestLeafCommandsAreClassified(t *testing.T) {
	classified := map[string]int{}
	for _, c := range mutatingCommands {
		classified[c]++
	}
	for _, c := range readOnlyOrLocalCommands {
		classified[c]++
	}

	for _, leaf := range runnableCommandPaths() {
		switch classified[leaf] {
		case 0:
			t.Errorf("leaf %q is unclassified — add it to mutatingCommands (and markMutating) or readOnlyOrLocalCommands", leaf)
		case 1:
			// exactly one list — good
		default:
			t.Errorf("leaf %q appears in both classification lists", leaf)
		}
	}

	// Also catch a stale list entry that no longer matches a real leaf.
	leaves := map[string]bool{}
	for _, l := range runnableCommandPaths() {
		leaves[l] = true
	}
	for c := range classified {
		if !leaves[c] {
			t.Errorf("classified command %q is not a current leaf (stale entry?)", c)
		}
	}
}

// withResolvedContext sets the package-global active context for a test and restores it.
func withResolvedContext(t *testing.T, ctx *clicontext.Context) {
	t.Helper()
	orig := resolvedCtx
	resolvedCtx = ctx
	t.Cleanup(func() { resolvedCtx = orig })
}

// withAssumeYes sets the --yes flag state for a test and restores it.
func withAssumeYes(t *testing.T, v bool) {
	t.Helper()
	orig := assumeYes
	assumeYes = v
	t.Cleanup(func() { assumeYes = orig })
}
