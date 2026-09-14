package commands

import (
	"strings"
	"testing"

	"github.com/sukko-dev/cli/client"
)

func TestEditionCmd_Registration(t *testing.T) {
	t.Parallel()

	subs := editionCmd.Commands()
	if len(subs) != 1 {
		t.Fatalf("have %d subcommands, want 1 (compare)", len(subs))
	}
	if subs[0].Name() != "compare" {
		t.Errorf("subcommand name = %q, want compare", subs[0].Name())
	}
}

func TestCapitalizeEdition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"community", "Community"},
		{"pro", "Pro"},
		{"enterprise", "Enterprise"},
		{"", ""},
	}

	for _, tt := range tests {
		if got := capitalizeEdition(tt.input); got != tt.want {
			t.Errorf("capitalizeEdition(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestEditionCompare_TableWidth(t *testing.T) {
	t.Parallel()

	// Verify all rows fit within 80 columns
	// Format: "%-24s %-14s %-14s %-14s" = 24+14+14+14 = 66 chars max
	for _, row := range editionMatrix {
		if row.dimension == "" {
			continue
		}
		// Each column: value + padding to width
		lineLen := len(row.dimension)
		if lineLen > 24 {
			t.Errorf("dimension %q is %d chars, max 24 for 80-col table", row.dimension, lineLen)
		}
		for _, val := range []string{row.community, row.pro, row.enterprise} {
			if len(val) > 14 {
				t.Errorf("value %q is %d chars, max 14 for 80-col table", val, len(val))
			}
		}
	}
}

func TestEditionCompare_DimensionCount(t *testing.T) {
	t.Parallel()

	// Count non-separator rows (5 limits + 14 features under the platform ADR-0009 edition model)
	count := 0
	for _, row := range editionMatrix {
		if row.dimension != "" {
			count++
		}
	}
	if count != 19 {
		t.Errorf("editionMatrix has %d dimensions, want 19", count)
	}
}

// TestEditionMatrix_EditionModel pins the platform edition model: the full data
// path (kafka backend, message history, live gap recovery, REST publish) is
// Community, and so is channel-to-topic routing — it is the sole channel-to-topic
// mapping, so gating it would leave the Community kafka backend unusable for
// publish (platform ADR-0014); Web Push and push analytics are Pro; mobile push (FCM/APNs) and
// audit logging are Enterprise. It also guards against resurrecting rows that
// misrepresented the model (tenant isolation is a security property of every
// edition, never a paid feature; no edition gates the database driver).
func TestEditionMatrix_EditionModel(t *testing.T) {
	t.Parallel()

	rows := map[string][3]string{}
	for _, row := range editionMatrix {
		if row.dimension != "" {
			rows[row.dimension] = [3]string{row.community, row.pro, row.enterprise}
		}
	}

	want := map[string][3]string{
		"Kafka Backend":          {"Yes", "Yes", "Yes"},
		"Message History":        {"Yes", "Yes", "Yes"},
		"Live Gap Recovery":      {"Yes", "Yes", "Yes"},
		"REST Publish":           {"Yes", "Yes", "Yes"},
		"Channel-Topic Routing":  {"Yes", "Yes", "Yes"},
		"Tenant Limits & Quotas": {"No", "Yes", "Yes"},
		"SSE Transport":          {"No", "Yes", "Yes"},
		"Web Push":               {"No", "Yes", "Yes"},
		"Push Analytics":         {"No", "Yes", "Yes"},
		"Mobile Push (FCM/APNs)": {"No", "No", "Yes"},
		"Audit Logging":          {"No", "No", "Yes"},
	}
	for dim, vals := range want {
		got, ok := rows[dim]
		if !ok {
			t.Errorf("editionMatrix missing dimension %q", dim)
			continue
		}
		if got != vals {
			t.Errorf("dimension %q = %v, want %v", dim, got, vals)
		}
	}

	for _, banned := range []string{"Per-Tenant Isolation", "Message Backend", "Database"} {
		if _, ok := rows[banned]; ok {
			t.Errorf("editionMatrix still contains retired dimension %q", banned)
		}
	}
}

// TestComparisonData_EditionModel keeps the JSON comparison payload consistent
// with the editionMatrix under the platform ADR-0009 edition model.
func TestComparisonData_EditionModel(t *testing.T) {
	t.Parallel()

	data := comparisonData()
	editions, ok := data["editions"].([]map[string]any)
	if !ok || len(editions) != 3 {
		t.Fatalf("editions field missing or wrong shape: %v", data["editions"])
	}

	wantFeatures := map[string][3]bool{
		"kafka_backend":         {true, true, true},
		"message_history":       {true, true, true},
		"live_gap_recovery":     {true, true, true},
		"rest_publish":          {true, true, true},
		"channel_topic_routing": {true, true, true},
		"tenant_limits_quotas":  {false, true, true},
		"alerting":              {false, true, true},
		"sse_transport":         {false, true, true},
		"webhooks":              {false, true, true},
		"admin_ui":              {false, true, true},
		"web_push":              {false, true, true},
		"push_analytics":        {false, true, true},
		"mobile_push":           {false, false, true},
		"audit_logging":         {false, false, true},
	}

	for i, e := range editions {
		features, ok := e["features"].(map[string]any)
		if !ok {
			t.Fatalf("edition[%d] features missing or wrong type", i)
		}
		if len(features) != len(wantFeatures) {
			t.Errorf("edition[%d] has %d features, want %d", i, len(features), len(wantFeatures))
		}
		for name, editionsWant := range wantFeatures {
			got, ok := features[name].(bool)
			if !ok {
				t.Errorf("edition[%d] feature %q missing or not bool", i, name)
				continue
			}
			if got != editionsWant[i] {
				t.Errorf("edition[%d] feature %q = %v, want %v", i, name, got, editionsWant[i])
			}
		}
	}
}

func TestComparisonData_JSON(t *testing.T) {
	t.Parallel()

	data := comparisonData()
	editions, ok := data["editions"].([]map[string]any)
	if !ok {
		t.Fatal("editions field missing or wrong type")
	}
	if len(editions) != 3 {
		t.Fatalf("have %d editions, want 3", len(editions))
	}

	names := []string{"community", "pro", "enterprise"}
	for i, e := range editions {
		name, _ := e["name"].(string)
		if name != names[i] {
			t.Errorf("edition[%d] name = %q, want %q", i, name, names[i])
		}
	}

	url, _ := data["upgrade_url"].(string)
	if !strings.Contains(url, "sukko.dev") {
		t.Errorf("upgrade_url = %q, want sukko.dev URL", url)
	}
}

func TestMergeEditionUsage(t *testing.T) {
	t.Parallel()

	intPtr := func(v int) *int { return &v }

	tests := []struct {
		name        string
		dst         client.EditionUsage
		src         client.EditionUsage
		wantConns   *int
		wantShards  *int
		wantTenants *int
	}{
		{
			name:        "fills nil connections and shards from src",
			dst:         client.EditionUsage{Tenants: intPtr(5)},
			src:         client.EditionUsage{Connections: intPtr(1200), Shards: intPtr(2)},
			wantTenants: intPtr(5),
			wantConns:   intPtr(1200),
			wantShards:  intPtr(2),
		},
		{
			name:       "does not overwrite non-nil dst fields",
			dst:        client.EditionUsage{Connections: intPtr(999), Shards: intPtr(4)},
			src:        client.EditionUsage{Connections: intPtr(1200), Shards: intPtr(2)},
			wantConns:  intPtr(999),
			wantShards: intPtr(4),
		},
		{
			name:       "both nil — stays nil",
			dst:        client.EditionUsage{},
			src:        client.EditionUsage{},
			wantConns:  nil,
			wantShards: nil,
		},
		{
			name:       "src nil — dst unchanged",
			dst:        client.EditionUsage{Connections: intPtr(500)},
			src:        client.EditionUsage{},
			wantConns:  intPtr(500),
			wantShards: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mergeEditionUsage(&tt.dst, &tt.src)

			checkIntPtr(t, "Connections", tt.dst.Connections, tt.wantConns)
			checkIntPtr(t, "Shards", tt.dst.Shards, tt.wantShards)
			if tt.wantTenants != nil {
				checkIntPtr(t, "Tenants", tt.dst.Tenants, tt.wantTenants)
			}
		})
	}
}

func checkIntPtr(t *testing.T, name string, got, want *int) {
	t.Helper()
	if got == nil && want == nil {
		return
	}
	if got == nil || want == nil {
		t.Errorf("%s: got %v, want %v", name, got, want)
		return
	}
	if *got != *want {
		t.Errorf("%s: got %d, want %d", name, *got, *want)
	}
}
