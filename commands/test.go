package commands

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const testHTTPTimeout = 30 * time.Second

var (
	testConnections         int
	testDuration            string
	testPublishRate         int
	testRampRate            int
	testSuite               string
	testLoadSuite           string // --suite for stress/soak (empty = default suite)
	testTesterURL           string
	testFollow              bool
	testKafkaBrokers        string
	testKafkaTopicNamespace string
	testTenantFlag          string
)

func init() {
	testCmd.AddCommand(testSmokeCmd, testLoadCmd, testStressCmd, testSoakCmd, testValidateCmd)

	// Common flags on all test subcommands
	for _, cmd := range []*cobra.Command{testLoadCmd, testStressCmd, testSoakCmd} {
		cmd.Flags().IntVar(&testConnections, "connections", 10, "Number of WebSocket connections")
		cmd.Flags().StringVar(&testDuration, "duration", "30s", "Test duration (e.g., 30s, 5m, 1h)")
		cmd.Flags().IntVar(&testPublishRate, "publish-rate", 10, "Messages per second")
		cmd.Flags().IntVar(&testRampRate, "ramp-rate", 5, "Connections per second during ramp-up")
	}

	testValidateCmd.Flags().StringVar(&testSuite, "suite", "auth", "Validation suite (fetch available suites from tester via --help)")
	for _, cmd := range []*cobra.Command{testStressCmd, testSoakCmd} {
		cmd.Flags().StringVar(&testLoadSuite, "suite", "", "Load suite to run (e.g. revocation); omit for default")
		_ = cmd.RegisterFlagCompletionFunc("suite", completeSuites)
	}
	_ = testValidateCmd.RegisterFlagCompletionFunc("suite", completeSuites)

	// Flags on all subcommands
	for _, cmd := range []*cobra.Command{testSmokeCmd, testLoadCmd, testStressCmd, testSoakCmd, testValidateCmd} {
		cmd.Flags().StringVar(&testTesterURL, "tester-url", "", "Tester service URL (overrides context)")
		cmd.Flags().BoolVarP(&testFollow, "follow", "f", false, "Stream metrics in real-time")
		cmd.Flags().StringVar(&testKafkaBrokers, "kafka-brokers", "", "Per-run Kafka broker override for the kafka-ingest suite (comma-separated); credentials come from the tester's environment")
		cmd.Flags().StringVar(&testKafkaTopicNamespace, "kafka-topic-namespace", "", "Per-run Kafka topic namespace override for the kafka-ingest suite; MUST match the server-under-test")
		cmd.Flags().StringVar(&testTenantFlag, "tenant", "", "Tenant ID (uses active tenant from context if not set)")
	}

	rootCmd.AddCommand(testCmd)
}

var testCmd = &cobra.Command{
	Use:   "test",
	Short: "Run tests against the Sukko platform",
	Long:  "Run smoke, load, stress, soak, or validation tests via the sukko-tester service.",
}

var testSmokeCmd = &cobra.Command{
	Use:   "smoke",
	Short: "Quick connectivity and health check",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runTest(cmd, "smoke", nil)
	},
}

var testLoadCmd = &cobra.Command{
	Use:   "load",
	Short: "Sustained load test with configurable connections",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runTest(cmd, "load", map[string]any{
			"connections":  testConnections,
			"duration":     testDuration,
			"publish_rate": testPublishRate,
			"ramp_rate":    testRampRate,
		})
	},
}

var testStressCmd = &cobra.Command{
	Use:   "stress",
	Short: "Push to maximum capacity and find limits",
	RunE: func(cmd *cobra.Command, _ []string) error {
		body := map[string]any{
			"connections":  testConnections,
			"duration":     testDuration,
			"publish_rate": testPublishRate,
			"ramp_rate":    testRampRate,
		}
		if testLoadSuite != "" {
			body["suite"] = testLoadSuite
		}
		return runTest(cmd, "stress", body)
	},
}

var testSoakCmd = &cobra.Command{
	Use:   "soak",
	Short: "Long-running stability test",
	RunE: func(cmd *cobra.Command, _ []string) error {
		body := map[string]any{
			"connections":  testConnections,
			"duration":     testDuration,
			"publish_rate": testPublishRate,
		}
		if testLoadSuite != "" {
			body["suite"] = testLoadSuite
		}
		return runTest(cmd, "soak", body)
	},
}

var testValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Run validation suites",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runTest(cmd, "validate", map[string]any{
			"suite": testSuite,
		})
	},
}

// isLocalContext returns true if the context URLs point to localhost.
// Local dev contexts use Docker-internal URLs in the tester — context passthrough
// would send unreachable localhost URLs to the tester container.
func isLocalContext() bool {
	if resolvedCtx == nil {
		return false
	}
	for _, raw := range []string{resolvedCtx.GatewayURL, resolvedCtx.ProvisioningURL} {
		if u, err := url.Parse(raw); err == nil {
			host := u.Hostname()
			if host == "localhost" || host == "127.0.0.1" {
				return true
			}
		}
	}
	return false
}

// buildTestContext assembles the context block for the tester API.
// Returns nil if context should not be sent (local dev or incomplete).
func buildTestContext(kafkaBrokers, kafkaTopicNamespace string) (map[string]any, error) {
	if resolvedCtx == nil || resolvedStore == nil {
		return nil, nil // no context available — tester uses own env vars
	}
	if isLocalContext() {
		return nil, nil // local dev — tester uses Docker-internal URLs
	}

	// Validate core fields (all-or-nothing)
	if resolvedCtx.GatewayURL == "" {
		return nil, errors.New("incomplete context: gateway URL is required")
	}
	if resolvedCtx.ProvisioningURL == "" {
		return nil, errors.New("incomplete context: provisioning URL is required")
	}
	if resolvedCtx.Environment == "" {
		return nil, errors.New("incomplete context: environment is required (run 'sukko context create' to set it)")
	}

	ctx := map[string]any{
		"gateway_url":      resolvedCtx.GatewayURL,
		"provisioning_url": resolvedCtx.ProvisioningURL,
		"admin_key_path":   resolveAdminKeyPath(),
		"environment":      resolvedCtx.Environment,
	}

	// Optional per-run Kafka broker override for the kafka-ingest suite. Credentials
	// (SASL/TLS) are never sent by the CLI — they live in the tester's environment.
	if kafkaBrokers != "" {
		ctx["kafka_brokers"] = kafkaBrokers
	}
	// Optional per-run topic-namespace override — MUST match the server-under-test.
	if kafkaTopicNamespace != "" {
		ctx["kafka_topic_namespace"] = kafkaTopicNamespace
	}

	return ctx, nil
}

func runTest(cmd *cobra.Command, testType string, extra map[string]any) error {
	testerURL := resolveTesterURL(testTesterURL)
	tok := resolveTesterToken()

	ctx := context.Background()
	if cmd != nil {
		ctx = cmd.Context()
	}

	body := map[string]any{"type": testType}
	maps.Copy(body, extra)

	// Tenant passthrough
	if tenant := resolveTenant(testTenantFlag); tenant != "" {
		body["tenant_id"] = tenant
	}

	// Context passthrough (remote only)
	if testCtx, err := buildTestContext(testKafkaBrokers, testKafkaTopicNamespace); err != nil {
		return fmt.Errorf("build test context: %w", err)
	} else if testCtx != nil {
		body["context"] = testCtx
	}

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	client := &http.Client{Timeout: testHTTPTimeout}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, testerURL+"/api/v1/tests", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("start test: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated {
		var errResp map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&errResp) // best-effort: fall back to resp.Status if body isn't JSON
		msg := errResp["message"]
		if msg == "" {
			msg = resp.Status
		}
		return fmt.Errorf("start test failed: %s", msg)
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	testID, _ := result["id"].(string)
	if testID == "" {
		return errors.New("server response missing test ID")
	}

	var out io.Writer = os.Stdout
	if cmd != nil {
		out = cmd.OutOrStdout()
	}
	fmt.Fprintf(out, "Test started: %s (type: %s)\n", testID, testType)

	if !testFollow {
		if output == "json" {
			return printJSON(result)
		}
		fmt.Fprintf(out, "Use --follow (-f) to stream metrics in real-time. Test ID: %s\n", testID)
		return nil
	}

	// Stream metrics via SSE
	return streamTestMetrics(cmd, testerURL, testID, tok)
}

func streamTestMetrics(cmd *cobra.Command, testerURL, testID, tok string) error {
	ctx := context.Background()
	if cmd != nil {
		ctx = cmd.Context()
	}
	var out io.Writer = os.Stdout
	if cmd != nil {
		out = cmd.OutOrStdout()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, testerURL+"/api/v1/tests/"+url.PathEscape(testID)+"/metrics", http.NoBody)
	if err != nil {
		return fmt.Errorf("create stream request: %w", err)
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	client := &http.Client{Timeout: 0} // no timeout for SSE
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("connect to metrics stream: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("metrics stream returned %s", resp.Status)
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "event: complete") {
			// Next data line is the final report
			if scanner.Scan() {
				reportLine := scanner.Text()
				reportData := strings.TrimPrefix(reportLine, "data: ")
				if output == "json" {
					fmt.Fprintln(out, reportData)
				} else {
					printTestReport(out, reportData)
				}
				// Exit non-zero when the report indicates failure so CI and scripts can gate
				// on it. This catches report-level fail/error; individual skipped checks keep
				// the report status "pass", so a skip is NOT a failure here (assert on the
				// specific check if a skip must fail — e.g. the kafka-ingest e2e target).
				if status := reportStatus(reportData); status != "" && status != "pass" {
					return fmt.Errorf("test reported status %q", status)
				}
			}
			return nil
		}

		if after, ok := strings.CutPrefix(line, "data: "); ok {
			metricsData := after
			if output == "json" {
				fmt.Fprintln(out, metricsData)
			} else {
				printMetricsLine(out, metricsData)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read metrics stream: %w", err)
	}
	return nil
}

func printMetricsLine(out io.Writer, data string) {
	var m map[string]any
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		return // best-effort display: malformed metrics line is non-fatal
	}

	elapsed, _ := m["elapsed"].(string)
	conns, _ := m["connections_active"].(float64)
	sent, _ := m["messages_sent"].(float64)
	recv, _ := m["messages_received"].(float64)
	errTotal, _ := m["errors_total"].(float64)

	fmt.Fprintf(out, "\r[%s] conns=%d sent=%d recv=%d errors=%d",
		elapsed, int(conns), int(sent), int(recv), int(errTotal))
}

// reportStatus extracts the top-level "status" field from a test report JSON line
// ("pass", "fail", or "error"). Returns "" when the data is not a parseable report so
// callers treat an unreadable report as non-fatal rather than a false failure.
func reportStatus(data string) string {
	var report struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(data), &report); err != nil {
		return ""
	}
	return report.Status
}

func printTestReport(out io.Writer, data string) {
	var report map[string]any
	if err := json.Unmarshal([]byte(data), &report); err != nil {
		fmt.Fprintln(out, "\n"+data)
		return
	}

	status, _ := report["status"].(string)
	testType, _ := report["test_type"].(string)

	fmt.Fprintf(out, "\n\n=== Test Report: %s ===\n", testType)

	statusColor := colorGreen
	if status != "pass" {
		statusColor = colorRed
	}
	fmt.Fprintf(out, "Status: %s%s%s\n", statusColor, status, colorReset)

	if checks, ok := report["checks"].([]any); ok && len(checks) > 0 {
		fmt.Fprintln(out, "\nChecks:")
		for _, c := range checks {
			check, ok := c.(map[string]any)
			if !ok {
				continue
			}
			name, _ := check["name"].(string)
			checkStatus, _ := check["status"].(string)
			indicator := colorGreen + "PASS" + colorReset
			if checkStatus != "pass" {
				indicator = colorRed + "FAIL" + colorReset
			}
			fmt.Fprintf(out, "  [%s] %s", indicator, name)
			if latency, ok := check["latency"].(string); ok && latency != "" {
				fmt.Fprintf(out, " (%s)", latency)
			}
			if errMsg, ok := check["error"].(string); ok && errMsg != "" {
				fmt.Fprintf(out, " — %s", errMsg)
			}
			fmt.Fprintln(out)
		}
	}

	if metrics, ok := report["metrics"].(map[string]any); ok {
		fmt.Fprintln(out, "\nMetrics:")
		connsActive, _ := metrics["connections_active"].(float64)
		connsTotal, _ := metrics["connections_total"].(float64)
		connsFailed, _ := metrics["connections_failed"].(float64)
		sent, _ := metrics["messages_sent"].(float64)
		recv, _ := metrics["messages_received"].(float64)
		dropped, _ := metrics["messages_dropped"].(float64)
		fmt.Fprintf(out, "  Connections: %d active, %d total, %d failed\n",
			int(connsActive), int(connsTotal), int(connsFailed))
		fmt.Fprintf(out, "  Messages: %d sent, %d received, %d dropped\n",
			int(sent), int(recv), int(dropped))
	}

	if errs, ok := report["errors"].([]any); ok && len(errs) > 0 {
		fmt.Fprintln(out, "\nErrors:")
		for _, e := range errs {
			fmt.Fprintf(out, "  - %s\n", e)
		}
	}
}

// completeSuites provides dynamic tab completion for --suite by fetching
// capabilities from the running tester.
func completeSuites(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	testerURL := resolveTesterURL(testTesterURL)
	if testerURL == "" {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	client := NewTesterClient(testerURL)
	caps, err := client.Capabilities(context.Background())
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	names := make([]string, len(caps.Suites))
	for i, s := range caps.Suites {
		names[i] = s.Name
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}
