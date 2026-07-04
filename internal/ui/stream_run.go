package ui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/mattn/go-runewidth"

	"github.com/Townk/ai-playbook/internal/askbridge"
	"github.com/Townk/ai-playbook/internal/mux"
	"github.com/Townk/ai-playbook/internal/orchestrator"
	"github.com/Townk/ai-playbook/internal/reengage"
	"github.com/Townk/ai-playbook/pkg/driver"
)

// StreamOptions configure RunStream — the in-process render+drive path that
// consumes an arbitrary input STREAM (e.g. the authoring agent's stdout) rather
// than a file/stdin. It is the same render+drive path `run <file.md>` uses:
// the stream is parsed incrementally, rendered, and its run blocks are driven by
// the in-process orchestrator against the user's real shell.
type StreamOptions struct {
	Harness string // header label (default "agent")
	// Title, when set, is the WORKING pane header shown while the playbook is being
	// authored (the classify-supplied short label for an escalated request). A later
	// finalized-playbook H1 (on stream EOF) may still update it. Empty → the default
	// "ai-playbook — <harness>" header.
	Title string
	Cwd   string    // working dir for the in-process driver (default $PWD)
	Tee   io.Writer // if non-nil, every byte read from Src is mirrored here
	// Shell is the configured shell selector (cfg.Driver.Shell) threaded from the
	// launcher: "" | "auto" | "zsh" | "bash" | "sh". It is passed to driver.Open
	// when RunStream opens its OWN driver (opts.Driver == nil). "" preserves the
	// zsh default (no regression). When a session driver is supplied it is already
	// opened with the configured shell, so this field is unused on that path.
	Shell string
	// Driver, when non-nil, is the SESSION's shared shell driver — the same one
	// the authoring agent's tools backend drives, so the playbook's run blocks
	// execute in the exact shell the agent diagnosed in. When nil, RunStream opens
	// its own driver (the pre-stage-5 behavior). A supplied Driver is OWNED by the
	// caller: RunStream does NOT close it.
	Driver   *driver.Driver
	Reengage *reengage.Reengage // re-engagement context (regenerate/followup/finalplaybook); nil disables those kinds
	// Activity, when non-nil, is the agent's live tool-call feed (the session bridges
	// the tools backend's OnActivity hook to it). The model subscribes and shows the
	// latest summary next to the "Working…" spinner during the silent authoring wait.
	// nil → no activity line (the spinner still animates).
	Activity <-chan string
	// Asker, when non-nil, spawns the request-input float for the `f` keybind (spec
	// §D): proactive user-initiated amend. The session builds it from its
	// floatinput.Asker. nil (off-zellij / no selfExe) → `f` is a no-op.
	Asker AskFunc
	// AskBridge, when non-nil, routes the agent's `ask` tool to an in-viewer overlay
	// (the no-mux ask). The model drains pending asks and raises the dialog. nil → no
	// overlay (the mux-present float path, or tests). Only set on the interactive
	// null-mux path; the headless no-TTY branch below auto-cancels asks to avoid a
	// deadlock (it never raises the overlay).
	AskBridge *askbridge.Bridge
	// Structured, when true, switches the viewer into structured mode: the input
	// stream carries the agent's narration (not the playbook — the playbook arrives
	// via submit_playbook → OnPlaybook → Body), so narration is drained on each
	// textEvent and Body() is called at stream EOF to set m.md before the existing
	// finalDraft processing (preamble-strip, title, junk-guard) runs on it.
	// The captured structured playbook is treated as a final draft (w persists, r refines).
	Structured bool
	// Body, when non-nil, returns the captured rendered playbook at stream EOF in
	// structured mode (typically playbook.Render(session.lastPB)). Consumed once at EOF.
	Body func() string
}

// RunStream renders + drives a playbook from a live input stream in-process. It
// mirrors the interactive branch of Main(): spin up the driver + orchestrator,
// stream Src into the model, and drive run blocks against the real shell. When
// opts.Tee is set, the raw stream bytes are teed to it as they are consumed so
// the caller can persist the produced playbook on completion (the cache store).
//
// Returns an exit code; the caller owns os.Exit. On no TTY (tests/pipes) it
// drains the stream (teeing), renders once, and returns 0 — so a fake-agent test
// exercises the tee without a terminal.
func RunStream(src io.Reader, opts StreamOptions) int {
	os.Setenv("RUNEWIDTH_EASTASIAN", "0")
	runewidth.DefaultCondition.EastAsianWidth = false

	harness := opts.Harness
	if harness == "" {
		harness = "agent"
	}

	// Tee the stream so consumed bytes are mirrored to opts.Tee (the cache buffer).
	if opts.Tee != nil {
		src = io.TeeReader(src, opts.Tee)
	}

	parser := &streamParser{}

	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		// No TTY (tests / pipes): drain (teeing), strip control records, render
		// once, exit. The tee still receives the full raw stream.
		//
		// Deadlock guard: this branch never raises the ask overlay, so an agent
		// `ask` on the bridge would block the tools goroutine (and this drain loop)
		// forever. Auto-cancel every pending ask while draining so the agent always
		// gets a definite, non-hanging reply. The goroutine stops cleanly on return.
		if opts.AskBridge != nil {
			stop := make(chan struct{})
			defer close(stop)
			go drainAskCancel(opts.AskBridge, stop)
		}
		var b strings.Builder
		buf := make([]byte, 4096)
		rd := bufio.NewReader(src)
		for {
			n, rerr := rd.Read(buf)
			for _, ev := range parser.feed(buf[:n]) {
				if te, ok := ev.(textEvent); ok {
					b.WriteString(te.text)
				}
			}
			if rerr != nil {
				break
			}
		}
		m := newModel(harness, b.String())
		m.width = 100
		m.title = opts.Title
		fmt.Print(m.staticRender())
		return 0
	}
	defer tty.Close()

	// In-process drive: use the SESSION's shared driver when supplied (so the
	// playbook's run blocks execute in the exact shell the authoring agent
	// diagnosed in via its tools backend); else open our own. A failed Open falls
	// back to render-only (logged) rather than crashing. A caller-supplied driver
	// is owned by the caller — we don't close it here.
	var orch *orchestrator.Orchestrator
	var reeng *reengage.Engine
	d := opts.Driver
	if d == nil {
		runCwd := opts.Cwd
		if runCwd == "" {
			runCwd, _ = os.Getwd()
		}
		var derr error
		d, derr = driver.Open(driver.Options{Cwd: runCwd, Shell: opts.Shell})
		if derr != nil {
			fmt.Fprintf(os.Stderr, "ai-playbook: driver.Open failed (%v); falling back to render-only\n", derr)
			d = nil
		} else {
			defer d.Close()
		}
	}
	if d != nil {
		orch = orchestrator.New(d, &cliMux{}).WithFloat(mux.Load())
		// The engine is nil when no re-engagement context is wired; DriftTargetPath is
		// injected so the engine's drift-regenerate resolves paths without importing
		// the executor.
		reeng = reengage.New(opts.Reengage, orch.DriftTargetPath)
	}

	m := newModel(harness, "")
	m.title = opts.Title
	m.orch = orch
	m.reeng = reeng
	m.thinking = true
	m.streaming = true
	m.reader = bufio.NewReader(src)
	m.parser = parser
	m.activity = opts.Activity
	m.asker = opts.Asker
	m.askBridge = opts.AskBridge
	m.structured = opts.Structured
	m.bodyProvider = opts.Body
	if opts.Structured {
		m.finalDraft = true // the captured structured playbook is a final draft (w persists, r refines)
	}
	prog := tea.NewProgram(
		m,
		tea.WithInput(tty),
		tea.WithOutput(tty),
		tea.WithColorProfile(colorprofile.TrueColor),
	)
	if _, err := prog.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook: %v\n", err)
		return 1
	}
	// Drain and cancel any agent ask that arrives after the viewer exits so the
	// tools goroutine is never left blocked on an orphaned ask. A nil stop
	// channel keeps the goroutine running until process exit (bounded).
	if opts.AskBridge != nil {
		go drainAskCancel(opts.AskBridge, nil)
	}
	return 0
}
