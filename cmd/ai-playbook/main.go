// ai-playbook — unified terminal AI-assist / playbook binary. Dispatch,
// subcommands, and help live in internal/cli; this is a thin wrapper.
package main

import (
	"os"

	"github.com/Townk/ai-playbook/internal/cli"
)

func main() { os.Exit(cli.Run(os.Args)) }
