package commands

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTesterClient_Capabilities_Success(t *testing.T) {
	t.Parallel()

	caps := TesterCapabilities{
		TestTypes: []TesterTestType{
			{Name: "smoke", Description: "Smoke test"},
		},
		Suites: []TesterSuite{
			{Name: "auth", Description: "Auth validation"},
			{Name: "pubsub", Description: "Pub-sub validation"},
			{Name: "kafka-ingest", Description: "Direct-to-Kafka ingestion"},
		},
		ContextFields: []TesterContextField{
			{Name: "gateway_url", Type: "string", Required: true, Description: "Gateway URL"},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(caps)
	}))
	defer srv.Close()

	client := NewTesterClient(srv.URL)
	got, err := client.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities(): %v", err)
	}

	if len(got.Suites) != 3 {
		t.Errorf("suites = %d, want 3", len(got.Suites))
	}
	if got.Suites[0].Name != "auth" {
		t.Errorf("suites[0].name = %q, want %q", got.Suites[0].Name, "auth")
	}
}

func TestTesterClient_Capabilities_Unreachable(t *testing.T) {
	t.Parallel()

	client := NewTesterClient("http://localhost:59999")
	_, err := client.Capabilities(context.Background())
	if err == nil {
		t.Fatal("expected error for unreachable tester")
	}
	if !strings.Contains(err.Error(), "cannot connect to tester") {
		t.Errorf("error = %q, want to contain 'cannot connect to tester'", err.Error())
	}
}

func TestTesterClient_Capabilities_InvalidJSON(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	client := NewTesterClient(srv.URL)
	_, err := client.Capabilities(context.Background())
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "decode response") {
		t.Errorf("error = %q, want to contain 'decode response'", err.Error())
	}
}
