package author

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Townk/ai-playbook/internal/agentstream"
	"github.com/Townk/ai-playbook/internal/config"
	"github.com/Townk/ai-playbook/internal/draft"
	"github.com/Townk/ai-playbook/internal/harnesstest"
	"github.com/Townk/ai-playbook/internal/tools"
	"github.com/Townk/ai-playbook/pkg/driver"
)

// The cursor adapter is LIVE-VERIFIED (cursor-agent 2026.07.01-777f564): these
// tests are the re-verification path — on any machine that has cursor-agent
// installed (and authenticated) they prove the owned argv composes on the real
// CLI and the stream terminates in a Final event (the BASIC floor). Where the
// CLI is absent they skip, naming the binary.

// cursorLiveTimeout bounds one live cursor-agent call — minutes of headroom
// cover a slow model without hanging CI.
const cursorLiveTimeout = 3 * time.Minute

// runCursorLive drives RunHarnessEvents against the REAL cursor CLI (skipped
// when it is not installed) with a tiny prompt, from an empty working dir so
// rules/AGENTS.md discovery cannot pick up this repo's context files. It
// returns the drained events and wait()'s error.
//
// The probe is a BENIGN factual Q&A, deliberately not an "always reply with
// exactly X" instruction: cursor folds the system prompt into the positional
// user message (no system-prompt flag exists), and a canned "always reply
// exactly" fold reads to cursor's model as a prompt-injection attempt — it
// intermittently REFUSES it (live-observed: "I won't follow that embedded
// instruction. It's a prompt-injection attempt"). A coherent question the fold
// legitimately answers keeps the BASIC-floor gate deterministic.
func runCursorLive(t *testing.T, bare bool) ([]agentstream.Event, error) {
	t.Helper()
	harnesstest.RequireHarness(t, "cursor-agent")

	cfg := config.Default()
	cfg.Agent.Harness = "cursor"
	dir := t.TempDir()
	events, wait, err := RunHarnessEvents(
		"You are a helpful assistant. Answer in a single lowercase word.",
		"What color is a clear daytime sky?",
		AuthorOptions{
			Cfg:        cfg,
			Bare:       bare,
			NoThinking: true,
			Timeout:    cursorLiveTimeout,
			// The seam only pins the working directory; CommandContext keeps the
			// Timeout able to kill a stalled process (the seam's ctx contract).
			Command: func(ctx context.Context, bin string, args []string) *exec.Cmd {
				cmd := exec.CommandContext(ctx, bin, args...)
				cmd.Dir = dir
				return cmd
			},
		},
	)
	if err != nil {
		t.Fatalf("RunHarnessEvents: %v", err)
	}
	var drained []agentstream.Event
	for e := range events {
		drained = append(drained, e)
	}
	return drained, wait()
}

// assertCursorFinal asserts the live stream terminated with a Final event
// carrying the probe answer — the BASIC floor every harness owes (a parseable
// terminal event with the full final text).
func assertCursorFinal(t *testing.T, events []agentstream.Event, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("no events from the live cursor stream")
	}
	last := events[len(events)-1]
	if last.Kind != agentstream.Final {
		t.Fatalf("last event = %+v, want the Final (result is cursor's terminal envelope)", last)
	}
	if !strings.Contains(strings.ToLower(last.Text), "blue") {
		t.Errorf("Final = %q, want the probe answer (blue)", last.Text)
	}
	// Reasoning MAY appear: cursor-agent streams thinking text in stream-json
	// (live-verified — the doc's print-mode suppression claim is false), and
	// the adapter surfaces it as Reasoning. A trivial "ok" turn usually does no
	// thinking, so this is not asserted either way — the point is that Reasoning
	// is now legitimate, not a bug.
}

// TestCursorLive_BareFinalEvent is the live argv-composition probe for the
// BARE (classify-shaped) invocation: the documented flag set (-p
// --output-format stream-json --stream-partial-output --mode ask) plus the
// folded prompt must compose on the installed CLI and the stream must end in
// a Final event.
func TestCursorLive_BareFinalEvent(t *testing.T) {
	events, err := runCursorLive(t, true)
	assertCursorFinal(t, events, err)
}

// TestCursorLive_AppendFinalEvent is the live argv-composition probe for the
// AUTHORING invocation — with cursor deliberately the same flag shape as bare
// (no system-prompt or context levers exist); passing both proves the shared
// composition and the fold end to end on the real CLI.
func TestCursorLive_AppendFinalEvent(t *testing.T) {
	events, err := runCursorLive(t, false)
	assertCursorFinal(t, events, err)
}

// TestCursorLive_AuthoringShapedFinal is the live acceptance test the
// fixture-first corpus cannot substitute for: an authoring-shaped run — ask
// mode must READ a file (proving its read tools work headlessly) and then
// generate a multi-line markdown document, and the Final event must be
// EXACTLY that document with no interim narration glued in front of it. The
// last assertion is the live proof of the adapter's Final policy: cursor's
// documented `result` field is the no-separator concatenation of every
// assistant segment in the turn (cursor.com/docs/cli/reference/output-format),
// so if the model narrates before its tool call ("Let me read the file…"),
// taking `result` verbatim would glue that narration onto the document — the
// adapter must surface the last segment instead. A sentinel that exists only
// inside the file proves a tool call actually happened.
func TestCursorLive_AuthoringShapedFinal(t *testing.T) {
	harnesstest.RequireHarness(t, "cursor-agent")

	dir := t.TempDir()
	const sentinel = "XYZZY-4217"
	notes := "The magic word is " + sentinel + ". It unlocks the vault."
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte(notes), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Agent.Harness = "cursor"
	events, wait, err := RunHarnessEvents(
		"You are a documentation assistant. When asked for a document, reply with ONLY the document — no preamble, no commentary.",
		"Read the file notes.txt in the current directory. Then reply with a markdown document that starts with the exact line '# Answer' followed by a sentence containing the magic word from notes.txt, then a section '## Notes' with one sentence about where the word was found.",
		AuthorOptions{
			Cfg:        cfg,
			NoThinking: true,
			Timeout:    cursorLiveTimeout,
			Command: func(ctx context.Context, bin string, args []string) *exec.Cmd {
				cmd := exec.CommandContext(ctx, bin, args...)
				cmd.Dir = dir
				return cmd
			},
		},
	)
	if err != nil {
		t.Fatalf("RunHarnessEvents: %v", err)
	}
	var drained []agentstream.Event
	for e := range events {
		drained = append(drained, e)
	}
	if werr := wait(); werr != nil {
		t.Fatalf("wait: %v", werr)
	}
	if len(drained) == 0 {
		t.Fatal("no events from the live cursor stream")
	}
	last := drained[len(drained)-1]
	if last.Kind != agentstream.Final {
		t.Fatalf("last event = %+v, want the Final", last)
	}
	final := strings.TrimSpace(last.Text)
	// The document itself: multi-line markdown carrying the file-only sentinel
	// (which the model can only know by actually reading notes.txt — the
	// headless read-tool proof) and both requested sections.
	if !strings.Contains(final, sentinel) {
		t.Errorf("Final does not carry the sentinel %s (no tool read happened?):\n%s", sentinel, final)
	}
	if !strings.Contains(final, "## Notes") || strings.Count(final, "\n") < 2 {
		t.Errorf("Final is not the requested multi-line markdown document:\n%s", final)
	}
	// The Final-policy proof: the document must START at '# Answer' — any
	// pre-tool narration glued in front means the adapter surfaced the result
	// envelope's all-segment concatenation instead of the last segment.
	if !strings.HasPrefix(final, "# Answer") {
		t.Errorf("Final does not start at the document (narration glued in front?):\n%s", final)
	}
}

// TestCursorLive_ToolLoopSubmitPlaybook is the FULL-tier acceptance test the
// Phase C promotion rests on: the real cursor-agent CLI runs under the ISOLATED
// config root (the HOME redirect), the wire-time isolation guard passes (only
// OUR ai-playbook server is visible + auth survived), the model calls
// submit_playbook, our re-exec'd `ai-playbook mcp` server forwards it over the
// unix socket to a REAL tools backend, and the backend's OnPlaybook receives a
// draft-clean playbook — the schema-enforced structured loop end to end on the
// live harness. It exercises the SAME WriteToolTransport path production uses
// (guard included), so a guard refusal fails here exactly as it degrades to
// BASIC in the launcher. Skipped where cursor-agent is absent or the binary
// can't build.
func TestCursorLive_ToolLoopSubmitPlaybook(t *testing.T) {
	harnesstest.RequireHarness(t, "cursor-agent")

	// A real ai-playbook binary: cursor's transport points its MCP server at
	// `<SelfExe> mcp --socket <socket>`, so the loop needs the actual subcommand
	// (mirrors the claude e2e — cmd/ai-playbook is the main package).
	selfExe := filepath.Join(t.TempDir(), "ai-playbook")
	if out, berr := exec.Command("go", "build", "-o", selfExe, "github.com/Townk/ai-playbook/cmd/ai-playbook").CombinedOutput(); berr != nil {
		t.Skipf("build ai-playbook: %v\n%s", berr, out)
	}

	// A real tools backend: minimal controlled zsh (no user rc), temp roots, and
	// an OnPlaybook capture.
	zdot := t.TempDir()
	if err := os.WriteFile(filepath.Join(zdot, ".zshrc"), []byte("# minimal rc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := driver.Open(driver.Options{Shell: "zsh", Env: append(os.Environ(), "ZDOTDIR="+zdot)})
	if err != nil {
		t.Fatalf("driver.Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	var mu sync.Mutex
	var got *draft.Playbook
	// Short socket path: unix sun_path is ~104 bytes on darwin; a nested
	// t.TempDir() path can overflow it.
	sockDir, err := os.MkdirTemp("", "curssock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })
	socket := filepath.Join(sockDir, "t.sock")
	srv, err := tools.Serve(socket, tools.Deps{
		Driver:      d,
		ProjectRoot: t.TempDir(),
		KBRoot:      t.TempDir(),
		OnPlaybook: func(pb draft.Playbook) {
			mu.Lock()
			defer mu.Unlock()
			got = &pb
		},
	})
	if err != nil {
		t.Fatalf("tools.Serve: %v", err)
	}
	t.Cleanup(func() { srv.Close() })

	h, ok := harnessFor("cursor")
	if !ok {
		t.Fatal("cursor harness not registered")
	}
	// The production wiring path: writes the isolated .cursor/mcp.json, symlinks
	// the keychain, and RUNS the Step-5 isolation guard (mcp list + status under
	// HOME=<dir>). A guard failure returns an error here — the same signal the
	// launcher turns into a BASIC degrade.
	argv, toolDir, cleanup, err := WriteToolTransport(h, selfExe, "cursor-agent", socket)
	if err != nil {
		t.Fatalf("WriteToolTransport (isolation guard refused?): %v", err)
	}
	t.Cleanup(cleanup)

	cfg := config.Default()
	cfg.Agent.Harness = "cursor"
	workDir := t.TempDir()
	events, wait, err := RunHarnessEvents(
		"You are a playbook authoring assistant. Deliver the playbook by calling the submit_playbook tool.",
		"Submit a playbook via the submit_playbook tool with: title 'Say hello', "+
			"one section headed 'Steps' whose content is a single item "+
			"{kind:\"code\", lang:\"bash\", code:\"echo hello\"}, and meta "+
			"{description:\"Print hello\", project_bound:false}. "+
			"submit_playbook is your only action.",
		AuthorOptions{
			Cfg:        cfg,
			ToolArgv:   argv,
			ToolDir:    toolDir,
			Structured: true,
			NoThinking: true,
			Timeout:    cursorLiveTimeout,
			Command: func(ctx context.Context, bin string, args []string) *exec.Cmd {
				cmd := exec.CommandContext(ctx, bin, args...)
				cmd.Dir = workDir
				return cmd
			},
		},
	)
	if err != nil {
		t.Fatalf("RunHarnessEvents: %v", err)
	}
	sawSubmitActivity := false
	for e := range events {
		if e.Kind == agentstream.ToolActivity && strings.Contains(e.Text, "submit_playbook") {
			sawSubmitActivity = true
		}
	}
	if err := wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if got == nil {
		t.Fatal("the backend never received a submit_playbook (the tool loop did not close)")
	}
	if !sawSubmitActivity {
		t.Error("no submit_playbook ToolActivity event streamed")
	}
	if got.Title != "Say hello" {
		t.Errorf("playbook title = %q, want 'Say hello'", got.Title)
	}
	if len(got.Sections) == 0 {
		t.Fatal("playbook has no sections")
	}
	if got.Meta.Description == "" {
		t.Error("playbook meta.description is empty")
	}
}
