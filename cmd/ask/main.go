// ask — the standalone themed-dialog binary. Dispatch, subcommands, flags, and
// help live in internal/askcli; this is a thin wrapper. See
// docs/specifications/ask-binary.md and ADR-0009 (interaction-toolkit surface).
//
// DEPRECATED since the troupe consolidation (ADR-0009 superseded by troupe's
// ADR-0001): the dialog widgets now live in github.com/Townk/troupe, and
// `troupe` is the standalone binary — same verbs, same flags, TROUPE_* env
// with ASK_* fallback. This wrapper survives one release with the stderr
// notice below, then retires.
package main

import (
	"fmt"
	"os"

	"golang.org/x/term"

	"github.com/Townk/ai-playbook/internal/askcli"
)

func main() {
	// Only when stderr is NOT a tty: the dialogs render their TUI on the
	// terminal, and an unconditional first line would smear the frame. A
	// redirected stderr (scripts, logs) gets the notice harmlessly.
	if !term.IsTerminal(int(os.Stderr.Fd())) {
		fmt.Fprintln(os.Stderr, "ask: deprecated — use `troupe` (same verbs/flags; TROUPE_* env, ASK_* still honored)")
	}
	os.Exit(askcli.Run(os.Args))
}
