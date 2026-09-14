package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(completionCmd)
}

var completionCmd = &cobra.Command{
	Use:   "completion [bash|zsh|fish]",
	Short: "Generate shell completion scripts",
	Long: `Generate shell completion scripts for sukko.

To load completions:

Bash:
  $ source <(sukko completion bash)
  # To load for each session, add to ~/.bashrc:
  $ echo 'source <(sukko completion bash)' >> ~/.bashrc

Zsh:
  $ source <(sukko completion zsh)
  # To load for each session, add to ~/.zshrc:
  $ echo 'source <(sukko completion zsh)' >> ~/.zshrc

Fish:
  $ sukko completion fish | source
  # To load for each session:
  $ sukko completion fish > ~/.config/fish/completions/sukko.fish
`,
	Args:      cobra.ExactArgs(1),
	ValidArgs: []string{"bash", "zsh", "fish"},
	RunE: func(_ *cobra.Command, args []string) error {
		switch args[0] {
		case "bash":
			return rootCmd.GenBashCompletion(os.Stdout)
		case "zsh":
			return rootCmd.GenZshCompletion(os.Stdout)
		case "fish":
			return rootCmd.GenFishCompletion(os.Stdout, true)
		default:
			return fmt.Errorf("unsupported shell %q (use bash, zsh, or fish)", args[0])
		}
	},
}
