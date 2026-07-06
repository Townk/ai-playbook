package launcher

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Townk/ai-playbook/internal/author"
	"github.com/Townk/ai-playbook/internal/cache"
	"github.com/Townk/ai-playbook/internal/capture"
	"github.com/Townk/ai-playbook/internal/config"
	"github.com/Townk/ai-playbook/internal/reengage"
	"github.com/Townk/ai-playbook/internal/triage"
)

// ── the fake BASIC harness (ADR-0012 tier-degradation proof) ────────────────
//
// No BASIC harness ships in H1, so the tier matrix is proven by this fake: a
// registered harness with Capabilities{Tools:false} whose Argv records the
// FINAL folded system prompt (Argv runs before the process would start, so the
// prompt-hygiene assertions hold even though the fake's bin never exists and
// no process is ever spawned).

type fakeBasicHarness struct{ lastSys *string }

func (f fakeBasicHarness) Argv(systemPrompt, userMessage string, _ author.Invocation) []string {
	*f.lastSys = systemPrompt
	return []string{"-p", userMessage}
}
func (fakeBasicHarness) AdapterName() string               { return "text" }
func (fakeBasicHarness) Env(author.Invocation) []string    { return nil }
func (fakeBasicHarness) DisplayName() string               { return "Basic Fake" }
func (fakeBasicHarness) Capabilities() author.Capabilities { return author.Capabilities{} }
func (fakeBasicHarness) ToolTransport(author.Invocation, string, string) ([]string, []string, error) {
	return nil, nil, errors.New("BASIC harness has no tool transport")
}

var (
	registerBasicOnce sync.Once
	basicLastSys      = new(string)
)

// basicHarnessCfg registers the fake BASIC harness (once) and returns a config
// selecting it. The fake's bin is never created: invocations fail at exec with
// "executable file not found", proving no real process is spawned.
func basicHarnessCfg(t *testing.T) *config.Config {
	t.Helper()
	registerBasicOnce.Do(func() {
		author.RegisterHarness("basic-fake", fakeBasicHarness{lastSys: basicLastSys}, author.Defaults{})
	})
	cfg := config.Default()
	cfg.Agent.Harness = "basic-fake"
	return cfg
}

// resetDegradeNotes clears the once-per-session note latch and captures the
// note output, returning the buffer and a restore func.
func resetDegradeNotes(t *testing.T) *bytes.Buffer {
	t.Helper()
	degradeMu.Lock()
	degradeShown = map[string]bool{}
	var buf bytes.Buffer
	prev := degradeOut
	degradeOut = &buf
	degradeMu.Unlock()
	t.Cleanup(func() {
		degradeMu.Lock()
		degradeShown = map[string]bool{}
		degradeOut = prev
		degradeMu.Unlock()
	})
	return &buf
}

// notesIn returns the number of times note appears in buf.
func notesIn(buf *bytes.Buffer, note string) int {
	return strings.Count(buf.String(), note)
}

// writeBasicHarnessConfig points config.Load at a temp config.toml selecting
// the fake BASIC harness (for paths that load config themselves, like
// buildReengageEvents).
func writeBasicHarnessConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	cfgDir := filepath.Join(dir, "ai-playbook")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"),
		[]byte("[agent]\nharness = \"basic-fake\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestTextOnlyHarness pins the tier gate: FULL (claude) is not text-only, the
// fake BASIC harness is (with its display name for the note), and an unknown
// harness is NOT reported text-only (its clear not-yet-supported error must
// surface through the invocation paths instead of a misleading note).
func TestTextOnlyHarness(t *testing.T) {
	if name, textOnly := textOnlyHarness(config.Default()); textOnly {
		t.Errorf("claude (FULL) must not be text-only (name %q)", name)
	}
	cfg := basicHarnessCfg(t)
	name, textOnly := textOnlyHarness(cfg)
	if !textOnly || name != "Basic Fake" {
		t.Errorf("basic-fake: got (%q, %v), want (Basic Fake, true)", name, textOnly)
	}
	cfg.Agent.Harness = "unknown-harness"
	if _, textOnly := textOnlyHarness(cfg); textOnly {
		t.Error("an unknown harness must not be reported text-only")
	}
}

// TestCreateAuthor_BasicHarness_TextPathWithOneNote drives the create author
// path under the fake BASIC harness: the structured stream is NEVER opened (the
// seam fails the test if touched), the existing text path is taken, the
// "structured drafting unavailable" note prints EXACTLY once per session even
// across repeated authoring calls, and no real process is spawned (the fake's
// bin does not exist — the text path surfaces exec's start error).
func TestCreateAuthor_BasicHarness_TextPathWithOneNote(t *testing.T) {
	t.Setenv("AI_PLAYBOOK_DATA_DIR", t.TempDir())
	cfg := basicHarnessCfg(t)
	buf := resetDegradeNotes(t)

	origStream := createStreamFn
	t.Cleanup(func() { createStreamFn = origStream })
	createStreamFn = func(_ capture.Request, _ *session, _ *config.Config) (createStream, error) {
		t.Error("structured stream must not be opened under a BASIC harness")
		return createStream{}, errors.New("unreachable")
	}

	req := capture.Request{Kind: "prompt", UserRequest: "do the thing", CWD: t.TempDir()}
	d := triage.Decision{}
	c := cache.Open()

	code := createAuthorWithProgress(req, d, c, true, nil, cfg)
	// The fake harness's bin does not exist, so the TEXT path's author call
	// fails at exec start (no process is ever spawned) and the path exits 1.
	if code != 1 {
		t.Errorf("exit code = %d, want 1 (text path with a missing fake bin)", code)
	}
	note := "structured drafting unavailable on Basic Fake — using text mode"
	if n := notesIn(buf, note); n != 1 {
		t.Fatalf("note printed %d times, want exactly 1:\n%s", n, buf.String())
	}

	// A second authoring call in the SAME session process must not re-note.
	_ = createAuthorWithProgress(req, d, c, true, nil, cfg)
	if n := notesIn(buf, note); n != 1 {
		t.Errorf("note re-printed on a second call (%d times), want once per session", n)
	}
}

// TestReengageEvents_BasicHarness_NotesAndPromptHygiene drives the
// re-engagement EventsFunc under the fake BASIC harness and pins the tier
// matrix rows:
//
//   - finalplaybook (the wrap-up): the memory-fill fold is SKIPPED (the
//     `remember` tool does not exist) with the "knowledge capture unavailable"
//     note once, plus the structured-drafting note once (it is a structured
//     kind);
//   - the folded prompts never mention the run/ask/submit_playbook tools;
//   - followup (a text kind) runs unchanged: no notes, no tool mentions;
//   - notes never repeat within the session.
func TestReengageEvents_BasicHarness_NotesAndPromptHygiene(t *testing.T) {
	t.Setenv("AI_PLAYBOOK_DATA_DIR", t.TempDir())
	writeBasicHarnessConfig(t)
	_ = basicHarnessCfg(t) // ensure the fake is registered
	buf := resetDegradeNotes(t)

	req := capture.Request{Kind: "error", Command: "make", Exit: "2"}
	events := buildReengageEvents(req, nil)

	// The prompt FOLDS must never reach a BASIC harness: the tool instructions
	// (markdown + structured) and the wrap-up memory fill each teach run/ask/
	// remember/submit_playbook, which do not exist without a tool loop. (The
	// followup BASE prompt's own `run` guidance sentence is pre-existing base
	// text, kept byte-identical in H1 — see docs/BACKLOG.md.)
	assertNoToolFolds := func(label, sys string) {
		t.Helper()
		if sys == "" {
			t.Fatalf("%s: the fake harness recorded no system prompt", label)
		}
		for _, banned := range []string{
			"submit_playbook",                           // StructuredToolInstruction's deliverable
			"You have the tools",                        // both tool-instruction folds' opener
			"## Diagnosing in the user's environment",   // ToolInstruction heading
			"## Diagnosing and submitting the playbook", // StructuredToolInstruction heading
			"remember what you learned",                 // the memory-fill fold heading
		} {
			if strings.Contains(sys, banned) {
				t.Errorf("%s: BASIC prompt carries an unavailable-tool fold %q", label, banned)
			}
		}
	}

	// Wrap-up (finalplaybook): both notes, no tool mentions, no memory fill.
	*basicLastSys = ""
	_, _, err := events(reengage.KindReengageFinalPlaybook, "", "troubleshoot content", nil)
	if err == nil {
		t.Fatal("expected the invocation to fail at exec (the fake bin does not exist)")
	}
	if !strings.Contains(err.Error(), "executable file not found") {
		t.Fatalf("err = %v, want an exec start failure (proves no process ran)", err)
	}
	assertNoToolFolds("finalplaybook", *basicLastSys)
	structuredNote := "structured drafting unavailable on Basic Fake — using text mode"
	kbNote := "knowledge capture unavailable on Basic Fake"
	if n := notesIn(buf, structuredNote); n != 1 {
		t.Errorf("structured note printed %d times, want 1:\n%s", n, buf.String())
	}
	if n := notesIn(buf, kbNote); n != 1 {
		t.Errorf("knowledge note printed %d times, want 1:\n%s", n, buf.String())
	}

	// Regenerate (the other structured kind): no NEW notes (once per session).
	*basicLastSys = ""
	_, _, _ = events(reengage.KindReengageRegenerate, "", "", nil)
	assertNoToolFolds("regenerate", *basicLastSys)
	if n := notesIn(buf, structuredNote); n != 1 {
		t.Errorf("structured note repeated (%d times), want once per session", n)
	}

	// Followup is a TEXT kind — unchanged on BASIC: no notes at all beyond the
	// ones already latched, no tool mentions in its prompt.
	before := buf.Len()
	*basicLastSys = ""
	_, _, _ = events(reengage.KindReengageFollowup, "", "failed output", nil)
	assertNoToolFolds("followup", *basicLastSys)
	if buf.Len() != before {
		t.Errorf("followup under BASIC must not print notes; got %q", buf.String()[before:])
	}
}

// TestReengageEvents_FullHarness_NoDegradeNotes is the negative control: under
// the FULL default harness (claude) the degradation notes never print. The
// config pins [agent].bin to a nonexistent path so the invocation fails at exec
// start — no real harness is ever spawned — while the note gate (which runs
// BEFORE the exec) is still fully exercised.
func TestReengageEvents_FullHarness_NoDegradeNotes(t *testing.T) {
	t.Setenv("AI_PLAYBOOK_DATA_DIR", t.TempDir())
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	cfgDir := filepath.Join(dir, "ai-playbook")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Default harness (claude, FULL) with a bin that cannot exist: exec fails
	// at start, so nothing is ever launched or billed.
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"),
		[]byte("[agent]\nbin = \"/nonexistent/ai-playbook-test-harness-bin\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	buf := resetDegradeNotes(t)

	req := capture.Request{Kind: "error", Command: "make", Exit: "2"}
	events := buildReengageEvents(req, nil)
	if _, _, err := events(reengage.KindReengageFinalPlaybook, "", "content", nil); err == nil {
		t.Fatal("expected an exec start failure for the nonexistent bin")
	}
	if buf.Len() != 0 {
		t.Errorf("FULL harness printed degradation notes: %q", buf.String())
	}
}
