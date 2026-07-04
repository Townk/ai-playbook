// ask — the standalone themed-dialog binary. Dispatch, subcommands, flags, and
// help live in internal/askcli; this is a thin wrapper. See
// docs/specifications/ask-binary.md and ADR-0009 (interaction-toolkit surface).
package main

import (
	"os"

	"github.com/Townk/ai-playbook/internal/askcli"
)

func main() { os.Exit(askcli.Run(os.Args)) }
