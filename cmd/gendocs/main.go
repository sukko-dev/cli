// Command gendocs outputs the sukko CLI command tree as structured JSON.
// Used by the sukko-docs extraction pipeline to generate CLI reference pages.
//
// Usage: go run ./cmd/gendocs > cli-reference.json
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/sukko-dev/cli/commands"
)

// Command represents a cobra command in the JSON output.
type Command struct {
	Name     string    `json:"name"`
	Use      string    `json:"use"`
	Short    string    `json:"short"`
	Long     string    `json:"long,omitempty"`
	Example  string    `json:"example,omitempty"`
	Aliases  []string  `json:"aliases,omitempty"`
	Flags    []Flag    `json:"flags,omitempty"`
	Children []Command `json:"children,omitempty"`
}

// Flag represents a command flag.
type Flag struct {
	Name      string `json:"name"`
	Shorthand string `json:"shorthand,omitempty"`
	Type      string `json:"type"`
	Default   string `json:"default,omitempty"`
	Usage     string `json:"usage"`
}

func main() {
	root := commands.RootCmd()
	tree := walkCommand(root)

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(tree); err != nil {
		fmt.Fprintf(os.Stderr, "error encoding JSON: %v\n", err)
		os.Exit(1)
	}
}

func walkCommand(cmd *cobra.Command) Command {
	c := Command{
		Name:    cmd.Name(),
		Use:     cmd.Use,
		Short:   cmd.Short,
		Long:    cmd.Long,
		Example: cmd.Example,
		Aliases: cmd.Aliases,
	}

	// Extract local flags (not inherited from parent)
	cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		c.Flags = append(c.Flags, Flag{
			Name:      f.Name,
			Shorthand: f.Shorthand,
			Type:      f.Value.Type(),
			Default:   f.DefValue,
			Usage:     f.Usage,
		})
	})

	// Recurse into subcommands
	for _, sub := range cmd.Commands() {
		if sub.Hidden || sub.Name() == "help" {
			continue
		}
		c.Children = append(c.Children, walkCommand(sub))
	}

	return c
}
