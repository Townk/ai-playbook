// degrade.go — the BASIC-tier degradation notes (ADR-0012 / the multi-harness
// spec's tier matrix). A harness without a tool loop (Capabilities().Tools ==
// false) cannot drive submit_playbook (structured drafting) or remember (the
// wrap-up knowledge fill), so those surfaces fall back to the existing TEXT
// paths — each with ONE visible note per session naming the missing capability,
// never a silent guess (the A5c doctrine). Everything tool-free (recall,
// classify, metadata, compaction, the validate AI review, followup, drift-regen)
// runs unchanged and never notes.
package launcher

import (
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/Townk/ai-playbook/internal/author"
	"github.com/Townk/ai-playbook/internal/config"
)

// The two degradation note classes (spec: "Notes are stderr/status-line
// one-liners, once per session, tested verbatim"). %s is the harness's
// DisplayName.
const (
	noteStructuredUnavailable = "structured drafting unavailable on %s — using text mode"
	noteKnowledgeUnavailable  = "knowledge capture unavailable on %s"
)

var (
	degradeMu    sync.Mutex
	degradeShown = map[string]bool{}
	// degradeOut is where the once-per-session notes print. Matches the existing
	// advisory-note convention (stderr one-liners, e.g. openSession's
	// "authoring without agent tools" notices). Var so tests capture it.
	degradeOut io.Writer = os.Stderr
)

// degradeNoteOnce prints one degradation note per class per session process.
func degradeNoteOnce(class, format string, args ...any) {
	degradeMu.Lock()
	defer degradeMu.Unlock()
	if degradeShown[class] {
		return
	}
	degradeShown[class] = true
	fmt.Fprintf(degradeOut, "ai-playbook: "+format+"\n", args...)
}

// textOnlyHarness reports whether the configured harness is BASIC-tier (no tool
// loop) and, if so, its display name for the note. An UNKNOWN harness returns
// false: the not-yet-supported error surfaces through the existing invocation
// error paths instead of a misleading degradation note.
func textOnlyHarness(cfg *config.Config) (displayName string, textOnly bool) {
	h, err := author.ConfiguredHarness(cfg)
	if err != nil {
		return "", false
	}
	return h.DisplayName(), !h.Capabilities().Tools
}
