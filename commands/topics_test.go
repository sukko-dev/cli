package commands

import (
	"testing"

	"github.com/spf13/cobra"
)

// TestTopicsCommandWiring guards that the topics command tree and its flags are
// registered: a missing subcommand, or a --suffix flag that is not marked
// required, would ship a broken CLI.
func TestTopicsCommandWiring(t *testing.T) {
	t.Parallel()

	sub := map[string]bool{}
	for _, c := range topicsCmd.Commands() {
		sub[c.Name()] = true
	}
	for _, want := range []string{"list", "create", "delete"} {
		if !sub[want] {
			t.Errorf("topics is missing the %q subcommand", want)
		}
	}

	// --suffix present AND marked required on create and delete.
	for name, cmd := range map[string]*cobra.Command{"create": topicsCreateCmd, "delete": topicsDeleteCmd} {
		f := cmd.Flags().Lookup("suffix")
		if f == nil {
			t.Errorf("topics %s is missing the --suffix flag", name)
			continue
		}
		if _, required := f.Annotations[cobraRequiredAnnotation]; !required {
			t.Errorf("topics %s --suffix must be marked required", name)
		}
	}

	// --tenant accepted by every subcommand.
	if topicsListCmd.Flags().Lookup("tenant") == nil ||
		topicsCreateCmd.Flags().Lookup("tenant") == nil ||
		topicsDeleteCmd.Flags().Lookup("tenant") == nil {
		t.Error("every topics subcommand must accept --tenant")
	}
}
