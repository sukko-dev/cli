package commands

import "testing"

func TestGrafanaCmd_Registration(t *testing.T) {
	t.Parallel()

	if grafanaCmd.Name() != "grafana" {
		t.Errorf("command name = %q, want grafana", grafanaCmd.Name())
	}

	if grafanaCmd.RunE == nil {
		t.Error("RunE should not be nil")
	}
}
