package author

import (
	"os/exec"
	"strings"
	"testing"

	"ai-playbook/internal/agentstream"
	"ai-playbook/internal/capture"
	"ai-playbook/internal/config"
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

// FinalPlaybook with a fake harness: it streams the canned playbook events and was
// invoked with FinalPlaybookPrompt(req, base, context) as the system prompt. The
// system prompt is the trailing-but-one positional in the owned argv
// (--append-system-prompt <sys> <user>); the Command seam captures the argv.
func TestFinalPlaybook_FakeHarnessAndSystemPrompt(t *testing.T) {
	bin := writeFakeHarness(t)

	cfg := config.Default()
	cfg.Agent.Harness = "claude"
	req := sampleFailure()
	const base = "# Playbook — x\n\n```bash {id=verify}\ntrue\n```\n"
	const ctx = "the resolved fix to fold in"

	var gotArgs []string
	events, wait, err := FinalPlaybook(req, base, ctx, AuthorOptions{
		Cfg: cfg,
		Command: func(b string, args []string) *exec.Cmd {
			gotArgs = args
			return exec.Command(bin, args...)
		},
	})
	if err != nil {
		t.Fatalf("FinalPlaybook: %v", err)
	}

	var got []agentstream.Event
	for e := range events {
		got = append(got, e)
	}
	if err := wait(); err != nil {
		t.Fatalf("process wait (reap) failed: %v", err)
	}

	// The canned stream surfaced as normalized events (final playbook included).
	if len(got) == 0 {
		t.Fatal("expected normalized events from the fake harness, got none")
	}
	var sawFinal bool
	for _, e := range got {
		if e.Kind == agentstream.Final {
			sawFinal = true
		}
	}
	if !sawFinal {
		t.Errorf("expected a Final event in the stream, got: %+v", got)
	}

	// The owned argv carried FinalPlaybookPrompt(req, base, ctx) as the system prompt.
	wantSys := FinalPlaybookPrompt(req, base, ctx)
	if sysArg := appendSystemPromptArg(gotArgs); sysArg != wantSys {
		t.Errorf("FinalPlaybook system prompt != FinalPlaybookPrompt(req, base, ctx)\n--- got ---\n%s", sysArg)
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
