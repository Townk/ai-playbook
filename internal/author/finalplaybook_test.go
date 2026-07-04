package author

import (
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/capture"
)

// FRESH mode: base == "" → distill the resolved session into a new reusable
// Literate-Config setup guide. The prompt must carry the title directive, the
// from-scratch prerequisites/configuration framing, the setup-guide (not diagnosis)
// intent, the exact {id=verify} mandate + the tagging-syntax example, and it must
// carry req.UserRequest + the context.
func TestFinalPlaybookPrompt_Fresh(t *testing.T) {
	req := sampleFailure() // UserRequest "fix my broken build"
	const ctx = "Root cause: missing gcc. Fix: install build-essential and pin CC."
	sys := FinalPlaybookPrompt(req, "", ctx)

	wants := []string{
		"# Playbook —",     // the title directive
		"prerequisites",    // prerequisites/configuration framing
		"from scratch",     // works FROM SCRATCH
		"DEPENDENCY ORDER", // dependency-order requirement
		"SETUP GUIDE",      // setup-guide intent
		"NOT a\n",          // ... NOT a debrief/diagnosis
		"{id=verify}",      // the exact verify mandate
		"keys success detection on `{id=verify}`", // why it's exact
		"```bash {id=verify}",                     // the tagging-syntax example
		"fix my broken build",                     // req.UserRequest carried
		ctx,                                       // the context carried
	}
	for _, w := range wants {
		if !strings.Contains(sys, w) {
			t.Errorf("fresh final-playbook prompt missing %q\n--- prompt ---\n%s", w, sys)
		}
	}
	// FRESH must not present a "base playbook" section.
	if strings.Contains(sys, "Base playbook") {
		t.Errorf("fresh mode must not carry a base-playbook section:\n%s", sys)
	}
}

// AMEND mode: base != "" → output the FULL UPDATED playbook with the change folded
// in, preserving existing steps in dependency order. The prompt must carry the base
// playbook, the change/context, the preserve/integrate/dependency-order instruction,
// and still mandate {id=verify}.
func TestFinalPlaybookPrompt_Amend(t *testing.T) {
	req := sampleFailure()
	const base = "# Playbook — set up the build\n\n```bash {id=verify}\nmake build\n```\n"
	const change = "Also configure the NDK path before building."
	sys := FinalPlaybookPrompt(req, base, change)

	wants := []string{
		base,               // the base playbook is interpolated
		change,             // the change/context is interpolated
		"Integrate",        // integrate the change
		"PRESERVE",         // preserve existing steps
		"DEPENDENCY ORDER", // in dependency order
		"FULL UPDATED",     // output the full updated playbook
		"{id=verify}",      // still mandates the exact verify tag
		"# Playbook —",     // keeps the title convention
	}
	for _, w := range wants {
		if !strings.Contains(sys, w) {
			t.Errorf("amend final-playbook prompt missing %q\n--- prompt ---\n%s", w, sys)
		}
	}
}

// The AMEND branch must carry the discard-if-rejected instruction so an outright
// rejection re-authors from scratch instead of patching the refused approach; the
// FRESH branch (empty base) has no base to discard, so it must NOT carry it.
func TestFinalPlaybookPrompt_AmendDiscardInstruction(t *testing.T) {
	req := sampleFailure()
	const discard = "if the change note rejects the current approach outright, discard the base playbook and re-author from scratch honoring the note — do not patch the rejected approach"

	const base = "# Playbook — set up the build\n\n```bash {id=verify}\nmake build\n```\n"
	amend := FinalPlaybookPrompt(req, base, "use podman instead of docker")
	if !strings.Contains(amend, discard) {
		t.Errorf("amend prompt missing the discard-if-rejected instruction %q\n--- prompt ---\n%s", discard, amend)
	}

	fresh := FinalPlaybookPrompt(req, "", "the resolved troubleshoot")
	if strings.Contains(fresh, discard) {
		t.Errorf("fresh prompt must NOT carry the discard-if-rejected instruction:\n%s", fresh)
	}
}

// Empty req fields fall back to the placeholders the other prompts use.
func TestFinalPlaybookPrompt_EmptyRequest(t *testing.T) {
	sys := FinalPlaybookPrompt(capture.Request{}, "", "")
	if !strings.Contains(sys, "<unknown>") {
		t.Errorf("empty request should fall back to <unknown> for the task:\n%s", sys)
	}
	if !strings.Contains(sys, "(unknown)") {
		t.Errorf("empty project root should fall back to (unknown):\n%s", sys)
	}
	if !strings.Contains(sys, "(none provided)") {
		t.Errorf("empty context should fall back to a placeholder:\n%s", sys)
	}
}

// appendSystemPromptArg returns the value following --append-system-prompt in the
// owned argv (the system prompt the harness was invoked with), or "" if absent.
func appendSystemPromptArg(args []string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--append-system-prompt" {
			return args[i+1]
		}
	}
	return ""
}
