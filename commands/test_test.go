package commands

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestRunTest_SuitePassedInBody verifies that runTest propagates the extra map
// (including suite) to the start-test endpoint via maps.Copy.
func TestRunTest_SuitePassedInBody(t *testing.T) {
	// No t.Parallel() — modifies package-level testTesterURL; depends on testLoadSuite == "".
	origSuite := testLoadSuite
	testLoadSuite = ""
	defer func() { testLoadSuite = origSuite }()

	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/tests":
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":     "t1",
				"status": "running",
				"config": map[string]any{},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	orig := testTesterURL
	testTesterURL = srv.URL
	defer func() { testTesterURL = orig }()

	err := runTest(nil, "stress", map[string]any{"suite": "revocation", "connections": 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody == nil {
		t.Fatal("no request body received at start-test endpoint")
	}
	if gotBody["suite"] != "revocation" {
		t.Errorf("body[suite] = %v, want %q", gotBody["suite"], "revocation")
	}
}

// TestLoadSuiteInjectedFromFlag verifies that the testLoadSuite package-level
// variable (bound to --suite by cobra) flows into the POST body when non-empty
// and is absent when empty. Covers both stress and soak RunE paths.
func TestLoadSuiteInjectedFromFlag(t *testing.T) {
	// No t.Parallel() — modifies package-level testTesterURL and testLoadSuite.
	tests := []struct {
		name    string
		cmd     *cobra.Command
		suite   string
		wantKey bool
	}{
		{"stress/suite set", testStressCmd, "revocation", true},
		{"stress/suite empty", testStressCmd, "", false},
		{"soak/suite set", testSoakCmd, "revocation", true},
		{"soak/suite empty", testSoakCmd, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotBody map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/tests":
					_ = json.NewDecoder(r.Body).Decode(&gotBody)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusCreated)
					_ = json.NewEncoder(w).Encode(map[string]any{
						"id": "t1", "status": "running", "config": map[string]any{},
					})
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()

			origURL := testTesterURL
			origSuite := testLoadSuite
			origCtx := tc.cmd.Context()
			testTesterURL = srv.URL
			testLoadSuite = tc.suite
			tc.cmd.SetContext(context.Background())
			defer func() {
				testTesterURL = origURL
				testLoadSuite = origSuite
				tc.cmd.SetContext(origCtx)
			}()
			err := tc.cmd.RunE(tc.cmd, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotBody == nil {
				t.Fatal("no request body received at start-test endpoint")
			}
			if tc.wantKey {
				if gotBody["suite"] != tc.suite {
					t.Errorf("body[suite] = %v, want %q", gotBody["suite"], tc.suite)
				}
			} else {
				if _, ok := gotBody["suite"]; ok {
					t.Errorf("body[suite] = %v, want key absent when --suite is empty", gotBody["suite"])
				}
			}
		})
	}
}

// TestRunTest_HintTextNoStatusCommand verifies that when --follow is false the
// hint line printed to cmd.OutOrStdout() contains "--follow" and does NOT contain
// "status --id". Regression guard: the old hint referenced a non-existent
// `sukko test status --id` command.
func TestRunTest_HintTextNoStatusCommand(t *testing.T) {
	// No t.Parallel() — modifies package-level flag vars.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/tests" {
			t.Errorf("unexpected request to %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "t1",
			"status": "running",
			"config": map[string]any{},
		})
	}))
	defer srv.Close()

	origURL := testTesterURL
	testTesterURL = srv.URL
	defer func() { testTesterURL = origURL }()

	origFollow := testFollow
	testFollow = false
	defer func() { testFollow = origFollow }()

	origOutput := output
	output = "" // ensure text path, not json
	defer func() { output = origOutput }()

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	var buf strings.Builder
	cmd.SetOut(&buf)

	if err := runTest(cmd, "smoke", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := buf.String()
	if !strings.Contains(got, "--follow") {
		t.Errorf("hint text missing '--follow': %q", got)
	}
	if strings.Contains(got, "status --id") {
		t.Errorf("hint text must not reference 'status --id': %q", got)
	}
}

func TestReportStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data string
		want string
	}{
		{"pass", `{"test_type":"validate","status":"pass","checks":[]}`, "pass"},
		{"fail", `{"status":"fail"}`, "fail"},
		{"error", `{"status":"error"}`, "error"},
		{"no status field", `{"test_type":"validate"}`, ""},
		{"not json (progress line)", `[elapsed] conns=1`, ""},
		{"empty", ``, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := reportStatus(tt.data); got != tt.want {
				t.Errorf("reportStatus(%q) = %q, want %q", tt.data, got, tt.want)
			}
		})
	}
}
