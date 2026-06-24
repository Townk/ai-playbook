package ui

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/mattn/go-runewidth"
)

// Main is the entrypoint for the `ai-playbook run` subcommand. It parses flags
// from os.Args[2:] (os.Args[1] is the "run" subcommand) and returns an exit
// code; the caller is responsible for os.Exit.
func Main() int {
	// Force narrow (1-cell) accounting for East-Asian-ambiguous characters
	// (em-dash, ellipsis, smart quotes, nerd-font icons).  The terminal renders
	// them as 1 cell; without this setting go-runewidth counts them as 2,
	// causing admonition/code background fills to come up short.
	// Must run before any lipgloss/bubbletea call: charmbracelet/x/ansi reads
	// RUNEWIDTH_EASTASIAN in its package init, so the env var must be set first.
	os.Setenv("RUNEWIDTH_EASTASIAN", "0")
	runewidth.DefaultCondition.EastAsianWidth = false

	fs := flag.NewFlagSet("run", flag.ExitOnError)
	var harness string
	fs.StringVar(&harness, "harness", "agent", "harness label for the header")
	var fifoPath string
	fs.StringVar(&fifoPath, "actions-fifo", "", "FIFO path to write button actions to")
	var inputFifo string
	fs.StringVar(&inputFifo, "input-fifo", "", "FIFO path to read the input stream from (else stdin)")
	var thinkingLabel string
	fs.StringVar(&thinkingLabel, "thinking-label", "Working…", "default spinner label")
	var resultsFifo string
	fs.StringVar(&resultsFifo, "results-fifo", "", "FIFO of run-result records (consumed in Stage 2b; drained here)")
	var cachedStr string
	fs.StringVar(&cachedStr, "cached", "", "ISO-8601 timestamp: when set, show a 'cached' badge pill in the header (cache replay)")
	// os.Args[1] is the "run" subcommand (dispatched from the root main); flags
	// start at os.Args[2:]. Guard for direct/odd invocations.
	argv := os.Args[2:]
	if len(os.Args) < 2 {
		argv = nil
	}
	fs.Parse(argv)

	var cachedAt time.Time
	isCached := false
	if cachedStr != "" {
		if t, err := time.Parse(time.RFC3339, cachedStr); err == nil {
			cachedAt = t
			isCached = true
		}
	}

	// Input source: the named FIFO (opens for read; blocks until a writer
	// connects), an optional positional <file.md> argument, or stdin. Content
	// streams in; keys come from /dev/tty.
	var src io.Reader = os.Stdin
	if inputFifo != "" {
		f, err := os.OpenFile(inputFifo, os.O_RDONLY, 0)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ai-playbook run: %v\n", err)
			return 1
		}
		defer f.Close()
		src = f
	} else if file := fs.Arg(0); file != "" {
		// `ai-playbook run <file.md>` — render a playbook artifact from a file.
		// The file is used as the same input stream a FIFO/stdin would provide.
		f, err := os.Open(file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ai-playbook run: %v\n", err)
			return 1
		}
		defer f.Close()
		src = f
	}
	parser := &streamParser{}

	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		// No TTY (tests / pipes): drain the stream, strip control records, render
		// once, and exit.
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
		m.fifoPath = fifoPath
		m.isCached = isCached
		m.cachedAt = cachedAt
		fmt.Print(m.staticRender())
		return 0
	}
	defer tty.Close()

	// Force TrueColor: zellij's alt-screen pane underreports the color profile
	// during bubbletea's auto-detection, causing colors to be downsampled.
	// The UI targets a truecolor Catppuccin terminal, so we pin it explicitly.
	m := newModel(harness, "")
	m.fifoPath = fifoPath
	m.inputFifoPath = inputFifo
	m.defaultLabel = thinkingLabel
	m.thinkLabel = thinkingLabel
	m.isCached = isCached
	m.cachedAt = cachedAt
	m.thinking = true // implicit thinking at launch (spec)
	m.streaming = true
	m.reader = bufio.NewReader(src)
	m.parser = parser
	prog := tea.NewProgram(
		m,
		tea.WithInput(tty),
		tea.WithOutput(tty),
		tea.WithColorProfile(colorprofile.TrueColor),
	)
	// Start the results reader goroutine after the program is constructed so we
	// can call prog.Send. The blocking os.Open is fine inside a goroutine — the
	// UI is unaffected until the broker opens the write end.
	if resultsFifo != "" {
		go func() {
			f, err := os.OpenFile(resultsFifo, os.O_RDWR, 0)
			if err != nil {
				return
			}
			defer f.Close()
			parseResults(f, prog.Send)
		}()
	}
	if _, err := prog.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook run: %v\n", err)
		return 1
	}
	return 0
}
