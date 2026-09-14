package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"golang.org/x/term"
)

// ANSI color codes (only used when terminal supports it)
var (
	colorReset  = "\033[0m"
	colorGreen  = "\033[32m"
	colorRed    = "\033[31m"
	colorYellow = "\033[33m"
	colorCyan   = "\033[36m"
	colorBold   = "\033[1m"
)

func init() {
	if !isTTY() {
		colorReset = ""
		colorGreen = ""
		colorRed = ""
		colorYellow = ""
		colorCyan = ""
		colorBold = ""
	}
}

func isTTY() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// defaultWriter returns os.Stdout. Used as the default output target for print helpers.
func defaultWriter() io.Writer { return os.Stdout }

// printOutput renders data in the requested format.
func printOutput(data any, format string) error {
	switch format {
	case "json":
		return printJSON(data)
	default:
		return printTable(data)
	}
}

func printJSON(data any) error {
	return printJSONTo(defaultWriter(), data)
}

func printJSONTo(out io.Writer, data any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(data); err != nil {
		return fmt.Errorf("encode JSON: %w", err)
	}
	return nil
}

func printTable(data any) error {
	return printTableTo(defaultWriter(), data)
}

func printTableTo(out io.Writer, data any) error {
	m, ok := data.(map[string]any)
	if !ok {
		return printJSONTo(out, data)
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)

	// Handle lists
	if tenants, ok := m["tenants"].([]any); ok {
		_, _ = fmt.Fprintln(w, "ID\tNAME\tSTATUS\tCONSUMER_TYPE")
		for _, t := range tenants {
			tm := asMap(t)
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
				asStr(tm, "id"), asStr(tm, "name"), asStr(tm, "status"), asStr(tm, "consumer_type"))
		}
		_ = w.Flush()
		if total, ok := m["total"]; ok {
			_, _ = fmt.Fprintf(out, "\nTotal: %v\n", total)
		}
		return nil
	}

	if keys, ok := m["keys"].([]any); ok {
		_, _ = fmt.Fprintln(w, "KEY_ID\tTENANT_ID\tALGORITHM\tACTIVE")
		for _, k := range keys {
			km := asMap(k)
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%v\n",
				asStr(km, "key_id"), asStr(km, "tenant_id"), asStr(km, "algorithm"), km["is_active"])
		}
		_ = w.Flush()
		return nil
	}

	if apiKeys, ok := m["api_keys"].([]any); ok {
		_, _ = fmt.Fprintln(w, "KEY_ID\tTENANT_ID\tNAME\tACTIVE\tCREATED_AT")
		for _, k := range apiKeys {
			km := asMap(k)
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%v\t%s\n",
				asStr(km, "key_id"), asStr(km, "tenant_id"), asStr(km, "name"), km["is_active"], asStr(km, "created_at"))
		}
		_ = w.Flush()
		return nil
	}

	if topics, ok := m["topics"].([]any); ok {
		_, _ = fmt.Fprintln(w, "TOPIC")
		for _, t := range topics {
			_, _ = fmt.Fprintf(w, "%v\n", t)
		}
		_ = w.Flush()
		return nil
	}

	if entries, ok := m["entries"].([]any); ok {
		_, _ = fmt.Fprintln(w, "ACTION\tTENANT_ID\tACTOR\tCREATED_AT")
		for _, e := range entries {
			em := asMap(e)
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
				asStr(em, "action"), asStr(em, "tenant_id"), asStr(em, "actor"), asStr(em, "created_at"))
		}
		_ = w.Flush()
		return nil
	}

	// Single resource or status response — print key-value pairs
	for k, v := range m {
		_, _ = fmt.Fprintf(w, "%s\t%v\n", k, v)
	}
	_ = w.Flush()
	return nil
}

// printStatus prints a status table with colored status indicators.
func printStatus(items []StatusItem) {
	printStatusTo(defaultWriter(), items)
}

func printStatusTo(out io.Writer, items []StatusItem) {
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintf(w, "%sCOMPONENT\tSTATUS\tDETAILS%s\n", colorBold, colorReset)
	for _, item := range items {
		status := colorizeStatus(item.Status)
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", item.Name, status, item.Details)
	}
	_ = w.Flush()
}

// StatusItem represents a component's health/status.
type StatusItem struct {
	Name    string
	Status  string // "healthy", "unhealthy", "unknown"
	Details string
}

func colorizeStatus(status string) string {
	switch status {
	case "healthy", "running", "pass":
		return colorGreen + status + colorReset
	case "unhealthy", "error", "fail", "exited":
		return colorRed + status + colorReset
	case "starting", "degraded", "warning":
		return colorYellow + status + colorReset
	default:
		return status
	}
}

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func asStr(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		return fmt.Sprintf("%v", v)
	}
	return ""
}
