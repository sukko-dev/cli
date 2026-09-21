package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminClient_ListTopics(t *testing.T) {
	t.Parallel()

	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[{"suffix":"default"}],"total":1}`)
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL, Signer: testSigner(t)})
	res, err := c.ListTopics(context.Background(), "demo")
	if err != nil {
		t.Fatalf("ListTopics: %v", err)
	}
	if gotMethod != "GET" || gotPath != "/api/v1/tenants/demo/topics" {
		t.Errorf("got %s %s, want GET /api/v1/tenants/demo/topics", gotMethod, gotPath)
	}
	if res["total"].(float64) != 1 {
		t.Errorf("total = %v, want 1", res["total"])
	}
}

func TestAdminClient_CreateTopic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		serverStatus int
		serverBody   string
		wantErr      bool
		wantSentinel error
	}{
		{name: "201 success", serverStatus: http.StatusCreated, serverBody: `{"topic":{"suffix":"analytics"}}`},
		{name: "409 already exists", serverStatus: http.StatusConflict, serverBody: `{"code":"TOPIC_ALREADY_EXISTS","message":"topic already exists: analytics"}`, wantErr: true, wantSentinel: ErrTopicAlreadyExists},
		{name: "400 quota exceeded → bad request", serverStatus: http.StatusBadRequest, serverBody: `{"code":"TOO_MANY_TOPICS","message":"topic quota exceeded"}`, wantErr: true, wantSentinel: ErrAPIBadRequest},
		{name: "403 edition cap → forbidden", serverStatus: http.StatusForbidden, serverBody: `{"code":"EDITION_LIMIT","message":"limit reached"}`, wantErr: true, wantSentinel: ErrAPIForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var gotMethod, gotPath string
			var gotBody map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod, gotPath = r.Method, r.URL.Path
				if r.ContentLength > 0 {
					_ = json.NewDecoder(r.Body).Decode(&gotBody)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.serverStatus)
				fmt.Fprint(w, tt.serverBody)
			}))
			defer srv.Close()

			c, _ := New(Config{BaseURL: srv.URL, Signer: testSigner(t)})
			_, err := c.CreateTopic(context.Background(), "demo", "analytics")

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !errors.Is(err, tt.wantSentinel) {
					t.Errorf("error %q is not %v", err, tt.wantSentinel)
				}
				return
			}
			if err != nil {
				t.Fatalf("CreateTopic: %v", err)
			}
			if gotMethod != "POST" || gotPath != "/api/v1/tenants/demo/topics" {
				t.Errorf("got %s %s, want POST /api/v1/tenants/demo/topics", gotMethod, gotPath)
			}
			if gotBody["suffix"] != "analytics" {
				t.Errorf("body suffix = %v, want analytics", gotBody["suffix"])
			}
		})
	}
}

func TestAdminClient_DeleteTopic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		serverStatus int
		serverBody   string
		wantErr      bool
		wantSentinel error
	}{
		{name: "200 success", serverStatus: http.StatusOK, serverBody: `{"status":"deleted"}`},
		{name: "404 not found", serverStatus: http.StatusNotFound, serverBody: `{"code":"TOPIC_NOT_FOUND","message":"topic not found: analytics"}`, wantErr: true, wantSentinel: ErrTopicNotFound},
		{name: "409 referenced by rule", serverStatus: http.StatusConflict, serverBody: `{"code":"TOPIC_REFERENCED_BY_RULE","message":"topic referenced by routing rule: trade"}`, wantErr: true, wantSentinel: ErrTopicReferencedByRule},
		{name: "400 reserved → bad request", serverStatus: http.StatusBadRequest, serverBody: `{"code":"RESERVED_TOPIC_SUFFIX","message":"reserved"}`, wantErr: true, wantSentinel: ErrAPIBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var gotMethod, gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod, gotPath = r.Method, r.URL.Path
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.serverStatus)
				fmt.Fprint(w, tt.serverBody)
			}))
			defer srv.Close()

			c, _ := New(Config{BaseURL: srv.URL, Signer: testSigner(t)})
			_, err := c.DeleteTopic(context.Background(), "demo", "analytics")

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !errors.Is(err, tt.wantSentinel) {
					t.Errorf("error %q is not %v", err, tt.wantSentinel)
				}
				return
			}
			if err != nil {
				t.Fatalf("DeleteTopic: %v", err)
			}
			if gotMethod != "DELETE" || gotPath != "/api/v1/tenants/demo/topics/analytics" {
				t.Errorf("got %s %s, want DELETE /api/v1/tenants/demo/topics/analytics", gotMethod, gotPath)
			}
		})
	}
}

func TestAdminClient_DeleteTopic_EscapesSuffix(t *testing.T) {
	t.Parallel()

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"deleted"}`)
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL, Signer: testSigner(t)})
	// A hostile suffix must stay confined to the topics path segment, never
	// escape into another route or inject a query/fragment.
	if _, err := c.DeleteTopic(context.Background(), "demo", "a/b"); err != nil {
		t.Fatalf("DeleteTopic: %v", err)
	}
	if gotPath != "/api/v1/tenants/demo/topics/a%2Fb" {
		t.Errorf("escaped path = %q, want /api/v1/tenants/demo/topics/a%%2Fb", gotPath)
	}
}

func TestAdminClient_Topics_RequireSuffix(t *testing.T) {
	t.Parallel()

	// The empty-suffix guard must reject before any HTTP request is issued. A
	// server that flips `reached` lets the test discriminate the validation error
	// from an incidental transport failure (one-sided-green otherwise).
	reached := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()
	c, _ := New(Config{BaseURL: srv.URL, Signer: testSigner(t)})

	for _, tc := range []struct {
		name string
		call func() (map[string]any, error)
	}{
		{"create", func() (map[string]any, error) { return c.CreateTopic(context.Background(), "demo", "") }},
		{"delete", func() (map[string]any, error) { return c.DeleteTopic(context.Background(), "demo", "") }},
	} {
		_, err := tc.call()
		if err == nil || !strings.Contains(err.Error(), "suffix is required") {
			t.Errorf("%s empty suffix: err = %v, want it to mention \"suffix is required\"", tc.name, err)
		}
	}
	if reached {
		t.Error("empty suffix must be rejected before any HTTP request is issued")
	}
}
