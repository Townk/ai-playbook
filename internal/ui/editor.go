package ui

import (
	"os"
	"strings"
)

// resolveEditor returns the user's preferred editor command string by checking
// $VISUAL first, then $EDITOR, then falling back to "vi". The raw env value is
// returned as-is; callers that need an argv slice should split it with
// strings.Fields (e.g. "code -w" → ["code", "-w"]).
func resolveEditor() string {
	for _, k := range []string{"VISUAL", "EDITOR"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return "vi"
}
