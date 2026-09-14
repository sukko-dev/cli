package commands

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	clicontext "github.com/sukko-dev/cli/context"
	"golang.org/x/term"
)

var contextCmd = &cobra.Command{
	Use:   "context",
	Short: "Manage CLI contexts (environments)",
	Long:  "Add, remove, switch, and list named contexts for different Sukko environments.",
}

var contextAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Add a new context",
	Args:  cobra.ExactArgs(1),
	RunE:  runContextAdd,
}

var contextUseCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Set the active context",
	Args:  cobra.ExactArgs(1),
	RunE:  runContextUse,
}

var contextListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List all contexts",
	RunE:    runContextList,
}

var contextRemoveCmd = &cobra.Command{
	Use:     "remove <name>",
	Aliases: []string{"rm"},
	Short:   "Remove a context",
	Args:    cobra.ExactArgs(1),
	RunE:    runContextRemove,
}

var contextCurrentCmd = &cobra.Command{
	Use:   "current",
	Short: "Show the active context",
	RunE:  runContextCurrent,
}

var ctxForce bool

var contextCreateCmd = &cobra.Command{
	Use:   "create [name]",
	Short: "Create a context interactively",
	Long: `Create a named context for a Sukko environment. Prompts for required
fields (name, provisioning URL, gateway URL, admin token) and optional fields.
Secrets are entered with masked input to avoid shell history exposure.

All fields can also be provided as flags for non-interactive/CI use.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runContextCreate,
}

// Flags for context add
var (
	ctxGatewayURL      string
	ctxProvisioningURL string
	ctxTesterURL       string
	ctxAdminToken      string
	ctxAPIKey          string
	ctxEnvironment     string
	ctxTenant          string
)

func init() {
	contextAddCmd.Flags().StringVar(&ctxGatewayURL, "gateway-url", "", "WebSocket gateway URL (e.g., ws://localhost:3000)")
	contextAddCmd.Flags().StringVar(&ctxProvisioningURL, "provisioning-url", "", "Provisioning API URL (e.g., http://localhost:8080)")
	contextAddCmd.Flags().StringVar(&ctxTesterURL, "tester-url", "", "Tester service URL (e.g., http://localhost:8090)")
	contextAddCmd.Flags().StringVar(&ctxAdminToken, "admin-token", "", "Admin authentication token")
	contextAddCmd.Flags().StringVar(&ctxAPIKey, "api-key", "", "API key for gateway access")
	contextAddCmd.Flags().StringVar(&ctxTenant, "tenant", "", "Default active tenant ID")

	contextCreateCmd.Flags().StringVar(&ctxGatewayURL, "gateway-url", "", "WebSocket gateway URL (e.g., wss://ws.example.com)")
	contextCreateCmd.Flags().StringVar(&ctxProvisioningURL, "provisioning-url", "", "Provisioning API URL (e.g., https://api.example.com)")
	contextCreateCmd.Flags().StringVar(&ctxTesterURL, "tester-url", "", "Tester service URL")
	contextCreateCmd.Flags().StringVar(&ctxAdminToken, "admin-token", "", "Admin authentication token")
	contextCreateCmd.Flags().StringVar(&ctxEnvironment, "environment", "", "Environment name (e.g., staging, production)")
	contextCreateCmd.Flags().StringVar(&ctxAPIKey, "api-key", "", "API key for gateway access")
	contextCreateCmd.Flags().StringVar(&ctxTenant, "tenant", "", "Default active tenant ID")
	contextCreateCmd.Flags().BoolVar(&ctxForce, "force", false, "Overwrite existing context without confirmation")

	contextCmd.AddCommand(contextAddCmd)
	contextCmd.AddCommand(contextCreateCmd)
	contextCmd.AddCommand(contextUseCmd)
	contextCmd.AddCommand(contextListCmd)
	contextCmd.AddCommand(contextRemoveCmd)
	contextCmd.AddCommand(contextCurrentCmd)
	rootCmd.AddCommand(contextCmd)
}

func runContextAdd(cmd *cobra.Command, args []string) error {
	name := args[0]

	store, err := clicontext.NewStore()
	if err != nil {
		return fmt.Errorf("init context store: %w", err)
	}

	ctx := &clicontext.Context{
		Name:            name,
		Type:            "remote",
		GatewayURL:      ctxGatewayURL,
		ProvisioningURL: ctxProvisioningURL,
		TesterURL:       ctxTesterURL,
		ActiveTenant:    ctxTenant,
	}

	if ctxAdminToken != "" {
		enc, err := store.EncryptSecret(ctxAdminToken)
		if err != nil {
			return fmt.Errorf("encrypt admin token: %w", err)
		}
		ctx.AdminTokenEnc = enc
	}

	if ctxAPIKey != "" {
		enc, err := store.EncryptSecret(ctxAPIKey)
		if err != nil {
			return fmt.Errorf("encrypt api key: %w", err)
		}
		ctx.APIKeyEnc = enc
	}

	if err := store.Add(ctx); err != nil {
		return fmt.Errorf("add context: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Context %q added.\n", name)
	return nil
}

func runContextUse(cmd *cobra.Command, args []string) error {
	name := args[0]

	store, err := clicontext.NewStore()
	if err != nil {
		return fmt.Errorf("init context store: %w", err)
	}

	if err := store.SetActive(name); err != nil {
		return fmt.Errorf("set active context: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Switched to context %q.\n", name)
	return nil
}

func runContextList(cmd *cobra.Command, _ []string) error {
	store, err := clicontext.NewStore()
	if err != nil {
		return fmt.Errorf("init context store: %w", err)
	}

	contexts, err := store.List()
	if err != nil {
		return fmt.Errorf("list contexts: %w", err)
	}

	activeName, _ := store.ActiveName() // no active context simply means no marker displayed

	if output == "json" {
		return printJSON(contexts)
	}

	if len(contexts) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No contexts configured. Use 'sukko context add' to create one.")
		return nil
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ACTIVE\tNAME\tENVIRONMENT\tGATEWAY\tPROVISIONING\tTENANT")
	for _, ctx := range contexts {
		marker := ""
		if ctx.Name == activeName {
			marker = "*"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", marker, ctx.Name, ctx.Environment, ctx.GatewayURL, ctx.ProvisioningURL, ctx.ActiveTenant)
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("flush context list: %w", err)
	}
	return nil
}

func runContextRemove(cmd *cobra.Command, args []string) error {
	name := args[0]

	store, err := clicontext.NewStore()
	if err != nil {
		return fmt.Errorf("init context store: %w", err)
	}

	if err := store.Remove(name); err != nil {
		return fmt.Errorf("remove context: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Context %q removed.\n", name)
	return nil
}

func runContextCurrent(cmd *cobra.Command, _ []string) error {
	store, err := clicontext.NewStore()
	if err != nil {
		return fmt.Errorf("init context store: %w", err)
	}

	ctx, err := store.Active()
	if err != nil {
		return fmt.Errorf("get active context: %w", err)
	}

	if output == "json" {
		return printJSON(ctx)
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "Name:\t%s\n", ctx.Name)
	fmt.Fprintf(w, "Gateway:\t%s\n", ctx.GatewayURL)
	fmt.Fprintf(w, "Provisioning:\t%s\n", ctx.ProvisioningURL)
	if ctx.TesterURL != "" {
		fmt.Fprintf(w, "Tester:\t%s\n", ctx.TesterURL)
	}
	if ctx.Environment != "" {
		fmt.Fprintf(w, "Environment:\t%s\n", ctx.Environment)
	}
	fmt.Fprintf(w, "Tenant:\t%s\n", ctx.ActiveTenant)
	fmt.Fprintf(w, "Admin Token:\t%s\n", secretIndicator(ctx.AdminTokenEnc))
	fmt.Fprintf(w, "API Key:\t%s\n", secretIndicator(ctx.APIKeyEnc))
	if err := w.Flush(); err != nil {
		return fmt.Errorf("flush context details: %w", err)
	}
	return nil
}

func secretIndicator(enc string) string {
	if enc != "" {
		return "(set)"
	}
	return "(not set)"
}

// --- context create ---

func runContextCreate(cmd *cobra.Command, args []string) error {
	store, err := clicontext.NewStore()
	if err != nil {
		return fmt.Errorf("init context store: %w", err)
	}

	// 1. Get name from arg or prompt
	var name string
	if len(args) > 0 {
		name = args[0]
	} else {
		name, err = promptText(cmd, "Context name", "", true)
		if err != nil {
			return err
		}
	}

	// 2. Check if context exists
	_, getErr := store.Get(name)
	if getErr != nil && !errors.Is(getErr, clicontext.ErrContextNotFound) {
		return fmt.Errorf("check context: %w", getErr)
	}
	if getErr == nil && !ctxForce {
		// Context exists — confirm overwrite
		overwrite, err := promptChoice(cmd, fmt.Sprintf("Context %q already exists. Overwrite?", name), []string{"yes", "no"}, "no")
		if err != nil {
			return err
		}
		if overwrite != "yes" {
			fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")
			return nil
		}
	}

	// 3. Prompt required fields
	provURL, err := promptText(cmd, "Provisioning URL", ctxProvisioningURL, true)
	if err != nil {
		return err
	}
	if err := validateURL(provURL); err != nil {
		return err
	}

	gwURL, err := promptText(cmd, "Gateway URL", ctxGatewayURL, true)
	if err != nil {
		return err
	}
	if err := validateURL(gwURL); err != nil {
		return err
	}

	adminToken, err := promptSecret(cmd, "Admin token", "admin-token", ctxAdminToken)
	if err != nil {
		return err
	}

	environment, err := promptText(cmd, "Environment", ctxEnvironment, true)
	if err != nil {
		return err
	}

	// 4. Prompt optional fields
	fmt.Fprintln(cmd.OutOrStdout(), "\nOptional (press Enter to skip):")

	testerURL, err := promptText(cmd, "  Tester URL", ctxTesterURL, false)
	if err != nil {
		return err
	}
	if testerURL != "" {
		if err := validateURL(testerURL); err != nil {
			return err
		}
	}

	apiKey, err := promptSecret(cmd, "  API key (required for subscribe/publish, Enter to skip)", "api-key", ctxAPIKey)
	if err != nil {
		return err
	}

	tenant, err := promptText(cmd, "  Default tenant", ctxTenant, false)
	if err != nil {
		return err
	}

	// 5. Build and save context
	ctx := &clicontext.Context{
		Name:            name,
		Type:            "remote",
		GatewayURL:      gwURL,
		ProvisioningURL: provURL,
		TesterURL:       testerURL,
		Environment:     environment,
		ActiveTenant:    tenant,
	}

	if adminToken != "" {
		enc, err := store.EncryptSecret(adminToken)
		if err != nil {
			return fmt.Errorf("encrypt admin token: %w", err)
		}
		ctx.AdminTokenEnc = enc
	}

	if apiKey != "" {
		enc, err := store.EncryptSecret(apiKey)
		if err != nil {
			return fmt.Errorf("encrypt api key: %w", err)
		}
		ctx.APIKeyEnc = enc
	}

	if err := store.Add(ctx); err != nil {
		return fmt.Errorf("save context: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "\nContext %q created.\n", name)

	// 6. Offer to set as active
	setActive, err := promptChoice(cmd, "Set as active context?", []string{"yes", "no"}, "yes")
	if err != nil {
		return err
	}
	if setActive == "yes" {
		if err := store.SetActive(name); err != nil {
			return fmt.Errorf("set active context: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Switched to context %q.\n", name)
	}

	return nil
}

// promptText prompts for a text value. Returns flagVal if non-empty (skips prompt).
// If required and input is empty, returns an error.
func promptText(cmd *cobra.Command, label, flagVal string, required bool) (string, error) {
	if flagVal != "" {
		return flagVal, nil
	}

	fmt.Fprintf(cmd.OutOrStdout(), "%s: ", label)

	var input string
	if _, err := fmt.Scanln(&input); err != nil || strings.TrimSpace(input) == "" {
		if required {
			return "", fmt.Errorf("%s is required", label)
		}
		return "", nil
	}

	return strings.TrimSpace(input), nil
}

// promptSecret prompts for a secret with masked input via term.ReadPassword.
// Returns flagVal if non-empty (with a shell history warning).
// In non-TTY mode, returns an error if flagVal is empty.
func promptSecret(cmd *cobra.Command, label, flagName, flagVal string) (string, error) {
	if flagVal != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "Tip: omit --%s to enter it securely without shell history\n", flagName)
		return flagVal, nil
	}

	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", fmt.Errorf("%s is required (use --%s in non-interactive mode)", label, flagName)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "%s: ", label)
	secret, err := term.ReadPassword(fd)
	fmt.Fprintln(cmd.OutOrStdout()) // newline after masked input
	if err != nil {
		return "", fmt.Errorf("read %s: %w", label, err)
	}

	return strings.TrimSpace(string(secret)), nil
}

// validateURL checks that the string is a valid URL with a scheme and host.
func validateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", raw, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("invalid URL %q: must include scheme (http/https/ws/wss) and host", raw)
	}
	return nil
}
