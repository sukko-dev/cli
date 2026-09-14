package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// TesterCapabilities describes the tester's API surface.
type TesterCapabilities struct {
	TestTypes     []TesterTestType     `json:"test_types"`
	Suites        []TesterSuite        `json:"suites"`
	ContextFields []TesterContextField `json:"context_fields"`
}

// TesterTestType describes a supported test type.
type TesterTestType struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// TesterSuite describes a validation suite.
type TesterSuite struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// TesterContextField describes a TestContext field.
type TesterContextField struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description"`
}

// TesterClient communicates with the sukko-tester service.
type TesterClient struct {
	baseURL string
	http    *http.Client
}

// NewTesterClient creates a client for the given tester URL.
func NewTesterClient(baseURL string) *TesterClient {
	return &TesterClient{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

// Capabilities fetches the tester's capabilities.
// Returns an error if the tester is unreachable.
func (c *TesterClient) Capabilities(ctx context.Context) (*TesterCapabilities, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v1/capabilities", http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("create capabilities request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to tester at %s. Is it running? (%w)", c.baseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("tester capabilities: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var caps TesterCapabilities
	if err := json.NewDecoder(resp.Body).Decode(&caps); err != nil {
		return nil, fmt.Errorf("tester capabilities: decode response: %w", err)
	}

	return &caps, nil
}
