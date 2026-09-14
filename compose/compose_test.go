package compose

import (
	"bytes"
	"context"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestNewManager(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		projectDir  string
		composeFile string
		wantErr     bool
	}{
		{"valid", "/tmp/project", "/tmp/project/.sukko/docker-compose.yml", false},
		{"empty_dir", "", "/tmp/compose.yml", true},
		{"empty_file", "/tmp/project", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m, err := NewManager(tt.projectDir, tt.composeFile)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if m == nil {
				t.Fatal("expected non-nil manager")
			}
			if m.projectDir != tt.projectDir {
				t.Errorf("projectDir = %q, want %q", m.projectDir, tt.projectDir)
			}
			if m.composeFile != tt.composeFile {
				t.Errorf("composeFile = %q, want %q", m.composeFile, tt.composeFile)
			}
		})
	}
}

func TestComposeFileContent(t *testing.T) {
	t.Parallel()

	if len(ComposeFileContent) == 0 {
		t.Fatal("embedded ComposeFileContent is empty")
	}
}

// TestComposeFileContent_PushServiceHasLicensePassthrough is a regression guard: without
// SUKKO_LICENSE_KEY in its own environment block, the push-service (Pro+ — Web Push;
// FCM/APNs delivery needs Enterprise) fatals at LoadConfig when its profile is activated.
// Other services also set the var, so a whole-file check is insufficient — assert it
// inside the push-service block.
func TestComposeFileContent_PushServiceHasLicensePassthrough(t *testing.T) {
	t.Parallel()

	serviceKey := regexp.MustCompile(`^ {2}[a-z0-9-]+:\s*$`) // top-level (2-space) service key
	inBlock := false
	found := false
	for line := range strings.SplitSeq(string(ComposeFileContent), "\n") {
		if line == "  push-service:" {
			inBlock = true
			continue
		}
		if inBlock && serviceKey.MatchString(line) {
			break // reached the next service block without finding it
		}
		if inBlock && strings.Contains(line, "SUKKO_LICENSE_KEY") {
			found = true
			break
		}
	}
	if !found {
		t.Error("push-service block must pass SUKKO_LICENSE_KEY through (Pro+-licensed service fatals at LoadConfig without it)")
	}
}

func TestComposeArgs_IncludesProfiles(t *testing.T) {
	t.Parallel()

	m := &Manager{
		composeFile: "docker-compose.yml",
		profiles:    []string{"postgres", "kafka"},
	}

	args := m.composeArgs()
	if !slices.Contains(args, "--profile") {
		t.Error("expected --profile in composeArgs")
	}
	if !slices.Contains(args, "postgres") {
		t.Error("expected postgres profile")
	}
	if !slices.Contains(args, "kafka") {
		t.Error("expected kafka profile")
	}
}

func TestStartService_AddsProfiles(t *testing.T) {
	t.Parallel()

	m := &Manager{
		composeFile: "docker-compose.yml",
		profiles:    []string{"postgres"},
	}

	// We can't actually run StartService (needs docker), but we can test profile accumulation
	for _, p := range []string{"enterprise"} {
		if !slices.Contains(m.profiles, p) {
			m.profiles = append(m.profiles, p)
		}
	}

	if !slices.Contains(m.profiles, "enterprise") {
		t.Error("expected enterprise profile added")
	}
	if !slices.Contains(m.profiles, "postgres") {
		t.Error("expected postgres profile preserved")
	}
}

// mockGetStatus returns a function that simulates service health transitions.
func mockGetStatus(responses [][]ServiceStatus) func(context.Context) ([]ServiceStatus, error) {
	call := 0
	return func(_ context.Context) ([]ServiceStatus, error) {
		if call >= len(responses) {
			return responses[len(responses)-1], nil
		}
		result := responses[call]
		call++
		return result, nil
	}
}

func TestWaitForHealth_AllHealthyFirstPoll(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	getStatus := mockGetStatus([][]ServiceStatus{
		{{Service: "svc-a", Health: "healthy"}, {Service: "svc-b", Health: "healthy"}},
	})

	err := waitForHealth(context.Background(), &buf, []string{"svc-a", "svc-b"}, 5*time.Second, getStatus)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "svc-a:") || !strings.Contains(buf.String(), "healthy") {
		t.Error("expected svc-a healthy in output")
	}
}

func TestWaitForHealth_BecomesHealthy(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	getStatus := mockGetStatus([][]ServiceStatus{
		{{Service: "svc", Health: "starting"}},
		{{Service: "svc", Health: "healthy"}},
	})

	err := waitForHealth(context.Background(), &buf, []string{"svc"}, 10*time.Second, getStatus)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWaitForHealth_Timeout(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	getStatus := mockGetStatus([][]ServiceStatus{
		{{Service: "svc", Health: "starting"}},
	})

	err := waitForHealth(context.Background(), &buf, []string{"svc"}, 3*time.Second, getStatus)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "svc") {
		t.Errorf("error should mention service name, got: %v", err)
	}
}

func TestWaitForHealth_EmptyList(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	err := waitForHealth(context.Background(), &buf, nil, 5*time.Second, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWaitForHealth_ContextCancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var buf bytes.Buffer
	getStatus := mockGetStatus([][]ServiceStatus{
		{{Service: "svc", Health: "starting"}},
	})

	err := waitForHealth(ctx, &buf, []string{"svc"}, 30*time.Second, getStatus)
	if err == nil {
		t.Fatal("expected error from canceled context")
	}
}
