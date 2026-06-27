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

	"ai-playbook/internal/driver"
	"ai-playbook/internal/mux"
	"ai-playbook/internal/orchestrator"
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
	// Driver, when non-nil, is the SESSION's shared shell driver — the same one
	// the authoring agent's tools backend drives, so the playbook's run blocks
	// execute in the exact shell the agent diagnosed in. When nil, RunStream opens
	// its own driver (the pre-stage-5 behavior). A supplied Driver is OWNED by the
	// caller: RunStream does NOT close it.
	Driver   *driver.Driver
	Reengage *orchestrator.Reengage // re-engagement context (regenerate/followup/finalplaybook); nil disables those kinds
	// Activity, when non-nil, is the agent's live tool-call feed (the session bridges
	// the tools backend's OnActivity hook to it). The model subscribes and shows the
	// latest summary next to the "Working…" spinner during the silent authoring wait.
	// nil → no activity line (the spinner still animates).
	Activity <-chan string
	// Asker, when non-nil, spawns the request-input float for the `f` keybind (spec
	// §D): proactive user-initiated amend. The session builds it from its
	// floatinput.Asker. nil (off-zellij / no selfExe) → `f` is a no-op.
	Asker AskFunc
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
	d := opts.Driver
	if d == nil {
		runCwd := opts.Cwd
		if runCwd == "" {
			runCwd, _ = os.Getwd()
		}
		var derr error
		d, derr = driver.Open(driver.Options{Cwd: runCwd})
		if derr != nil {
			fmt.Fprintf(os.Stderr, "ai-playbook: driver.Open failed (%v); falling back to render-only\n", derr)
			d = nil
		} else {
			defer d.Close()
		}
	}
	if d != nil {
		orch = orchestrator.New(d, &cliMux{}).WithFloat(mux.Load())
		if opts.Reengage != nil {
			orch.WithReengage(opts.Reengage)
		}
	}

	m := newModel(harness, "")
	m.title = opts.Title
	m.orch = orch
	m.thinking = true
	m.streaming = true
	m.reader = bufio.NewReader(src)
	m.parser = parser
	m.activity = opts.Activity
	m.asker = opts.Asker
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
	return 0
}
