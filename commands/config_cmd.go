package commands

import (
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

const (
	configHTTPTimeout     = 5 * time.Second
	maxConfigResponseSize = 1 << 20 // 1MB
)

var configFormat string

func init() {
	configCmd.AddCommand(configDefaultsCmd, configViewCmd)
	configDefaultsCmd.Flags().StringVar(&configFormat, "format", "table", "Output format (table|env|json)")
	rootCmd.AddCommand(configCmd)
}

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Configuration management",
}

// configEntry represents a single configuration field.
type configEntry struct {
	EnvVar       string `json:"env_var"`
	DefaultValue string `json:"default_value"`
	Description  string `json:"description,omitempty"`
}

var configDefaultsCmd = &cobra.Command{
	Use:   "defaults",
	Short: "Show all configuration environment variables with defaults",
	Long:  "Outputs all environment variables from Go config struct tags (the single source of truth).",
	RunE: func(cmd *cobra.Command, _ []string) error {
		if configFormat != "table" && configFormat != "env" && configFormat != "json" {
			return fmt.Errorf("unsupported format %q: must be table, env, or json", configFormat)
		}

		entries := extractConfigEntries()

		if output == "json" || configFormat == "json" {
			return printJSON(entries)
		}

		if configFormat == "env" {
			for _, e := range entries {
				if e.DefaultValue != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "%s=%s\n", e.EnvVar, e.DefaultValue)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "# %s=\n", e.EnvVar)
				}
			}
			return nil
		}

		// Table format
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ENV VAR\tDEFAULT")
		for _, e := range entries {
			def := e.DefaultValue
			if def == "" {
				def = "(required)"
			}
			fmt.Fprintf(w, "%s\t%s\n", e.EnvVar, def)
		}
		return w.Flush()
	},
}

var configViewCmd = &cobra.Command{
	Use:   "view",
	Short: "Fetch active configuration from a running service",
	RunE: func(cmd *cobra.Command, _ []string) error {
		url, _, err := resolveProvisioningConfig()
		if err != nil {
			return err
		}
		configURL := strings.TrimRight(url, "/") + "/config"

		req, err := http.NewRequestWithContext(cmd.Context(), http.MethodGet, configURL, http.NoBody)
		if err != nil {
			return fmt.Errorf("create config request: %w", err)
		}

		httpClient := &http.Client{Timeout: configHTTPTimeout}
		resp, err := httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("fetch config from %s: %w", configURL, err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			return fmt.Errorf("fetch config: server returned %s: %s", resp.Status, string(body))
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, maxConfigResponseSize))
		if err != nil {
			return fmt.Errorf("read config response: %w", err)
		}

		fmt.Fprintln(cmd.OutOrStdout(), string(body))
		return nil
	},
}

// baseConfig mirrors platform.BaseConfig for config defaults display.
// Kept in sync manually — changes to BaseConfig in the monorepo should
// be reflected here.
type baseConfig struct {
	LogLevel    string `env:"LOG_LEVEL" envDefault:"info"`
	LogFormat   string `env:"LOG_FORMAT" envDefault:"json"`
	Environment string `env:"ENVIRONMENT" envDefault:"local"`
}

// extractConfigEntries uses reflection to read env struct tags from config types.
func extractConfigEntries() []configEntry {
	return extractFromType(reflect.TypeFor[baseConfig]())
}

func extractFromType(t reflect.Type) []configEntry {
	var entries []configEntry
	for field := range t.Fields() {

		// Handle embedded structs
		if field.Anonymous {
			entries = append(entries, extractFromType(field.Type)...)
			continue
		}

		envTag := field.Tag.Get("env")
		if envTag == "" {
			continue
		}

		defaultVal := field.Tag.Get("envDefault")

		entries = append(entries, configEntry{
			EnvVar:       envTag,
			DefaultValue: defaultVal,
		})
	}
	return entries
}
