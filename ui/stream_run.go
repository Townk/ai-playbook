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

	"ai-playbook/driver"
	"ai-playbook/mux"
	"ai-playbook/orchestrator"
)

// StreamOptions configure RunStream — the in-process render+drive path that
// consumes an arbitrary input STREAM (e.g. the authoring agent's stdout) rather
// than a file/stdin/FIFO. It is the same render+drive path `run <file.md>` uses:
// the stream is parsed incrementally, rendered, and its run blocks are driven by
// the in-process orchestrator against the user's real shell.
type StreamOptions struct {
	Harness string    // header label (default "agent")
	Cwd     string    // working dir for the in-process driver (default $PWD)
	Tee     io.Writer // if non-nil, every byte read from Src is mirrored here
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
		fmt.Print(m.staticRender())
		return 0
	}
	defer tty.Close()

	// In-process drive: spin up the real shell driver + orchestrator. A failed
	// Open falls back to render-only (logged) rather than crashing.
	var orch *orchestrator.Orchestrator
	runCwd := opts.Cwd
	if runCwd == "" {
		runCwd, _ = os.Getwd()
	}
	d, derr := driver.Open(driver.Options{Cwd: runCwd})
	if derr != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook: driver.Open failed (%v); falling back to render-only\n", derr)
	} else {
		defer d.Close()
		orch = orchestrator.New(d, &cliMux{}).WithFloat(mux.NewZellij())
	}

	m := newModel(harness, "")
	m.orch = orch
	m.thinking = true
	m.streaming = true
	m.reader = bufio.NewReader(src)
	m.parser = parser
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
