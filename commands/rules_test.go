package commands

import "testing"

// TestChannelRulesBodyFromPublic asserts the `rules channels set --public`
// request body uses the provisioning schema field "public". Regression guard:
// the previous body used the unknown key "public_patterns", which the API
// decoder silently dropped — saving EMPTY (deny-all) rules while reporting
// success to the operator.
func TestChannelRulesBodyFromPublic(t *testing.T) {
	t.Parallel()

	patterns := []string{"news.*", "alerts.*"}
	body := channelRulesBodyFromPublic(patterns)

	raw, ok := body["public"]
	if !ok {
		t.Fatal("body missing \"public\" key")
	}
	got, ok := raw.([]string)
	if !ok || len(got) != 2 || got[0] != "news.*" || got[1] != "alerts.*" {
		t.Errorf("body[\"public\"] = %v, want %v", raw, patterns)
	}

	if _, present := body["public_patterns"]; present {
		t.Error("body must not use the unknown \"public_patterns\" key (silently dropped by the API)")
	}
	if len(body) != 1 {
		t.Errorf("body has %d keys, want exactly 1: %v", len(body), body)
	}
}
