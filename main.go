// CLI tool for Sukko WS provisioning management.
package main

import (
	"os"

	"github.com/sukko-dev/cli/commands"
)

func main() {
	if err := commands.Execute(); err != nil {
		os.Exit(1)
	}
}
