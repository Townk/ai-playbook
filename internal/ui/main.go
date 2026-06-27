package ui

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/mattn/go-runewidth"

	"github.com/Townk/ai-playbook/internal/askbridge"
	"github.com/Townk/ai-playbook/internal/driver"
	"github.com/Townk/ai-playbook/internal/mux"
	"github.com/Townk/ai-playbook/internal/orchestrator"
)

// pendingReengage is the re-engagement context consumed by the next Main() call,
// set by SetReengage. The cached-replay path (serveCachedPlaybook) reuses ui.Main
// via os.Args reshaping and can't pass a struct through that seam, so it stashes
// the Reengage here; Main attaches it to the orchestrator and clears it. nil
// disables the regenerate/followup/wrapup kinds (their pre-4c-ii behavior).
var pendingReengage *orchestrator.Reengage

// SetReengage stashes the re-engagement context for the next ui.Main() invocation
// (used by the troubleshoot cached-replay path so the regenerate/followup/wrapup
// kinds can re-author in-process). It is consumed (and cleared) by Main.
func SetReengage(re *orchestrator.Reengage) { pendingReengage = re }

// pendingDriver is the session's shared shell driver consumed by the next Main()
// call, set by SetDriver. The cached-replay path (troubleshoot →
// serveCachedPlaybook → ui.Main via os.Args reshaping) can't pass a struct
// through that seam, so it stashes the driver here; Main reuses it for the
// playbook's run blocks (the same shell the tools backend exposes) instead of
// opening its own. A supplied driver is OWNED by the session — Main does NOT
// close it. nil → Main opens its own driver (the pre-stage-5 behavior).
var pendingDriver *driver.Driver

// SetDriver stashes the session's shared shell driver for the next ui.Main()
// invocation (the troubleshoot cached-replay path). Consumed (and cleared) by
// Main; the driver is not closed by Main.
func SetDriver(d *driver.Driver) { pendingDriver = d }

// pendingActivity is the agent's live activity feed consumed by the next Main()
// call, set by SetActivity. The cached-replay path stashes it here (same seam as
// the driver/reengage) so a re-engagement (regenerate / verify follow-up) during
// a cached replay can surface the agent's tool calls next to the spinner. nil →
// no activity line.
var pendingActivity <-chan string

// SetActivity stashes the agent activity feed for the next ui.Main() invocation
// (the troubleshoot cached-replay path). Consumed (and cleared) by Main.
func SetActivity(ch <-chan string) { pendingActivity = ch }

// pendingServedBase is the served playbook body consumed by the next Main() call,
// set by SetServedBase. On a cache HIT serveCachedPlaybook serves an existing
// playbook; it stashes that body here (same os.Args-reshaped seam as the
// driver/reengage/activity) so the model carries it as m.servedBase. Then a
// failing step → troubleshoot → confirm/`w`-generate AMENDS the served playbook
// (base=servedBase) rather than starting fresh (spec §C). "" → fresh.
var pendingServedBase string

// SetServedBase stashes the served playbook body for the next ui.Main() invocation
// (the cache-HIT serve path). Consumed (and cleared) by Main.
func SetServedBase(base string) { pendingServedBase = base }

// pendingAsker is the request-input-float asker consumed by the next Main() call,
// set by SetAsker. The cached-replay path (serveCachedPlaybook → ui.Main via the
// os.Args-reshaped seam) can't pass a closure through that seam, so it stashes the
// asker here; Main attaches it to the model (m.asker) and clears it. It backs the
// `f` keybind (spec §D): proactive user-initiated amend via the request float. nil →
// `f` is a no-op (off-zellij / no selfExe).
var pendingAsker AskFunc

// SetAsker stashes the request-input-float asker for the next ui.Main() invocation
// (the cache-HIT serve path, where `f` proactively amends the served playbook).
// Consumed (and cleared) by Main.
func SetAsker(a AskFunc) { pendingAsker = a }

// pendingAnswerRegen is the cached-ANSWER regenerate seam consumed by the next
// Main() call, set by SetAnswerRegen. The `answer` cached-serve path reshapes
// os.Args to the `run` entry (like serveCachedPlaybook) and can't pass a closure
// through that seam, so it stashes the regenerate function here; Main attaches it to
// the model (m.answerRegen) and clears it. When set, the cached pill's reload re-runs
// the cheap classify in place and replaces the prose (instead of the orchestrator's
// playbook-shaped Regenerate). nil → the answer path is not wired (the orchestrator
// path, or a flash-only no-op, applies).
var pendingAnswerRegen func() (io.ReadCloser, error)

// SetAnswerRegen stashes the cached-answer regenerate function for the next
// ui.Main() invocation (the `answer` cached-serve path). Consumed (and cleared) by
// Main. The returned reader streams the fresh prose; the closure also re-caches it.
func SetAnswerRegen(fn func() (io.ReadCloser, error)) { pendingAnswerRegen = fn }

// pendingAskBridge is the no-mux agent-ask bridge consumed by the next Main() call.
// The cached-serve path (serveCachedPlaybook) reshapes os.Args to the `run` entry and
// can't thread a value through that seam, so it stashes the bridge here; Main attaches
// it to the model (m.askBridge) and clears it. nil → no in-viewer ask overlay (the
// mux-present float path, or no bridge created).
var pendingAskBridge *askbridge.Bridge

// SetAskBridge stashes the no-mux ask bridge for the next ui.Main() invocation
// (the cached-serve path). Consumed (and cleared) by Main.
func SetAskBridge(b *askbridge.Bridge) { pendingAskBridge = b }

// pendingShell is the configured shell selector (cfg.Driver.Shell) consumed by the
// next Main() call. ui is config-agnostic — it receives the shell as DATA: the
// composition roots (cmd/ai-playbook for `run`, the launcher's cached-serve / inline
// answer paths that reshape os.Args to `run`) load config and stash it here. Main
// passes it to driver.Open when it opens its OWN driver. "" preserves the zsh
// default (no regression); a session-supplied driver (pendingDriver) ignores it.
var pendingShell string

// SetShell stashes the configured shell selector for the next ui.Main() invocation.
// Consumed (and cleared) by Main. Mirrors SetDriver/SetAskBridge.
func SetShell(s string) { pendingShell = s }

// OrchReady carries the lazily-opened orchestrator (and its request-input asker)
// delivered on the pendingReady channel by the async-startup path: main.go opens
// the shell driver + builds the orchestrator in the BACKGROUND while ui.Main
// renders the playbook IMMEDIATELY, then sends a single OrchReady once it is live.
// The Asker is the same AskFunc the `f` keybind uses (the float spawner), or nil.
// A nil Orch signals the background open FAILED: the UI clears the pending state
// and stays degraded (shell buttons remain disabled) rather than hanging.
type OrchReady struct {
	Orch  *orchestrator.Orchestrator
	Asker AskFunc
}

// pendingReady, when non-nil, switches ui.Main onto the ASYNC-orchestrator path:
// instead of opening the driver synchronously, Main renders the playbook first
// (shell buttons dimmed + inert via driverPending) and reads the single OrchReady
// off this channel through a startup tea.Cmd, enabling the buttons once it lands.
// Set by SetPendingReady; consumed (and cleared) by Main.
var pendingReady <-chan OrchReady

// SetPendingReady stashes the orchestrator-ready channel for the next ui.Main()
// invocation (the async-startup path). Consumed (and cleared) by Main. When set,
// Main does NOT open a driver / build an orch synchronously — it renders first and
// waits for the orchestrator on the channel. Mirrors SetReengage/SetDriver.
func SetPendingReady(ch <-chan OrchReady) { pendingReady = ch }

// BuildOrch constructs the in-process orchestrator the way ui.Main does, wired to
// the ui-internal cliMux + the active float mux. The async-startup path (main.go's
// serveCachedPlaybook) can't build this itself — the cliMux is unexported — so it
// hands the driver + re-engagement context here off the background goroutine and
// delivers the result over OrchReady. When re is non-nil the orchestrator is wired
// for re-engagement (the cached replay's regenerate/wrap-up). This is the SINGLE
// construction site: ui.Main's sync path calls it too.
func BuildOrch(d *driver.Driver, re *orchestrator.Reengage) *orchestrator.Orchestrator {
	orch := orchestrator.New(d, &cliMux{}).WithFloat(mux.Load())
	if re != nil {
		orch.WithReengage(re)
	}
	return orch
}

// loadPlaybookSource reads a finalized-playbook file (run-from-file / cached-serve),
// strips any leading YAML front matter AND any preamble above the first H1 title,
// and returns a reader over the stripped body, the playbook title (front-matter
// `name` when present, else the H1), and the front-matter `description` as a
// subtitle (empty when the file carries no front-matter description). A file with
// no front matter and no H1 is returned unchanged with empty title/subtitle (it's
// a transcript, not a playbook).
func loadPlaybookSource(file string) (r io.Reader, title, subtitle string, err error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, "", "", err
	}
	title, subtitle, body := loadPlaybookDocument(string(raw))
	return strings.NewReader(body), title, subtitle, nil
}

// effectiveTitle resolves the pager header title: an explicit --title flag wins
// (it OVERRIDES the H1/front-matter-derived title — used by the answer/escalate
// panes, where the classify supplies a short label and a prose answer has no H1),
// otherwise the title derived from the playbook document (empty → default header).
func effectiveTitle(flagTitle, derived string) string {
	if strings.TrimSpace(flagTitle) != "" {
		return flagTitle
	}
	return derived
}

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
	var titleFlag string
	fs.StringVar(&titleFlag, "title", "", "explicit pane header title (overrides the H1/front-matter title)")
	var thinkingLabel string
	fs.StringVar(&thinkingLabel, "thinking-label", "Working…", "default spinner label")
	var cachedStr string
	fs.StringVar(&cachedStr, "cached", "", "ISO-8601 timestamp: when set, show a 'cached' badge pill in the header (cache replay)")
	var cwd string
	fs.StringVar(&cwd, "cwd", "", "working dir for the in-process shell driver (default: dir of <file.md>, else $PWD)")
	var fileFlag string
	fs.StringVar(&fileFlag, "file", "", "playbook file to render (alternative to the positional arg)")
	var adaptedFrom string
	fs.StringVar(&adaptedFrom, "adapted-from", "", "source slug: render an 'adapted from <slug>' banner + enable the `d` diff overlay")
	var origFile string
	fs.StringVar(&origFile, "orig-file", "", "original (pre-adaptation) playbook file backing the `d` original→adapted diff")
	// os.Args[1] is the "run" subcommand (dispatched from the root main); flags
	// start at os.Args[2:]. Guard for direct/odd invocations.
	argv := os.Args[2:]
	if len(os.Args) < 2 {
		argv = nil
	}
	_ = fs.Parse(argv) // flag.ExitOnError: Parse never returns a non-nil error

	// Source file: --file takes precedence over the bare positional. The positional
	// stays supported for back-compat (serveCachedPlaybook/show migrate to --file via
	// the launcher reshape; a direct `run <file.md>` still works). When both are set,
	// --file wins.
	file := fileFlag
	if file == "" {
		file = fs.Arg(0)
	}

	var cachedAt time.Time
	isCached := false
	if cachedStr != "" {
		if t, err := time.Parse(time.RFC3339, cachedStr); err == nil {
			cachedAt = t
			isCached = true
		}
	}

	// Input source: an optional positional <file.md> argument, or stdin. Content
	// streams in; keys come from /dev/tty.
	var src io.Reader = os.Stdin
	// playbookTitle is the finalized-playbook title for the pager header (▓▓▓
	// <title>), set when the input is a saved playbook file (run-from-file /
	// cached-serve). Empty for stdin streams (an authoring transcript keeps the
	// default "ai-playbook — <harness>" header).
	playbookTitle := ""
	// playbookSubtitle is the front-matter `description` shown under the title for a
	// finalized/served playbook that carries front matter. Empty for stdin streams
	// and for files without a front-matter description.
	playbookSubtitle := ""
	if file != "" {
		// `ai-playbook run <file.md>` — render a finalized playbook artifact from a
		// file (also the cached-serve path). Read it fully, strip any preamble above
		// the H1 title, and use the playbook title as the pager header. The stripped
		// body is the document stream (saved playbooks are plain markdown, no control
		// records). Stripping here also cleans EXISTING saved files that still carry
		// preamble. A file with no H1 is left unchanged (title stays empty).
		r, title, subtitle, err := loadPlaybookSource(file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ai-playbook run: %v\n", err)
			return 1
		}
		playbookTitle = title
		playbookSubtitle = subtitle
		src = r
	}

	// Adapt-on-run (Task 9): --adapted-from <slug> renders the "adapted from <slug>"
	// banner in the subtitle slot (when the document carries no description of its
	// own) and enables the `d` original→adapted diff overlay. --orig-file backs that
	// diff with the pre-adaptation body. Both are empty for a normal render.
	origDoc := ""
	if origFile != "" {
		if raw, rerr := os.ReadFile(origFile); rerr == nil {
			_, _, origDoc = loadPlaybookDocument(string(raw))
		}
	}
	if adaptedFrom != "" && playbookSubtitle == "" {
		playbookSubtitle = adaptedBanner(adaptedFrom)
	}
	parser := &streamParser{}

	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		// No TTY (tests / pipes): drain the stream, strip control records, render
		// once, and exit.
		//
		// Deadlock guard (mirrors RunStream): this branch never raises the ask
		// overlay, so auto-cancel any pending agent ask on the bridge while draining
		// so a re-engagement ask never blocks the tools goroutine forever.
		if pendingAskBridge != nil {
			stop := make(chan struct{})
			defer close(stop)
			go drainAskCancel(pendingAskBridge, stop)
			pendingAskBridge = nil
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
		m.isCached = isCached
		m.cachedAt = cachedAt
		m.title = effectiveTitle(titleFlag, playbookTitle)
		m.subtitle = playbookSubtitle
		m.adaptedFrom = adaptedFrom
		m.origDoc = origDoc
		fmt.Print(m.staticRender())
		return 0
	}
	defer tty.Close()

	// In-process mode: when we have a playbook file to run, drive the real shell
	// directly via the orchestrator. The driver's working dir is --cwd, else the
	// dir of <file.md>, else $PWD. A failed driver.Open falls back to the
	// (no-orch) render-only behavior with a logged note rather than crashing.
	// Done only on the interactive path (after a real TTY) so render-only
	// invocations never spawn a shell.
	var orch *orchestrator.Orchestrator
	// Async-orchestrator path (consume-once): when a ready-channel is stashed, do NOT
	// open a driver or build an orch synchronously. Render the playbook IMMEDIATELY
	// with the shell-action buttons disabled (driverPending), and let a startup
	// tea.Cmd read the background-opened orchestrator off readyCh → orchReadyMsg, which
	// enables the buttons. Keeps blank-pane startup off the critical path entirely.
	readyCh := pendingReady
	pendingReady = nil // consume once
	driverPending := false
	// Skip the shell driver entirely for a cached ANSWER (pendingAnswerRegen set): an
	// answer has no run blocks and its reload is a cheap-model call (ClassifyRequest),
	// not a shell command. Opening a driver here spawns a shell that sources the user's
	// full profile — seconds of blank-pane startup — for nothing. (The cached-PLAYBOOK
	// path reuses the session's already-open driver, so it never pays this.)
	if readyCh != nil {
		// ASYNC: the orchestrator is delivered later on readyCh. Leave orch nil and mark
		// the driver pending; the OTHER pending seams (servedBase/asker/answerRegen/…)
		// are still consumed below — they don't need the driver.
		driverPending = true
	} else if pendingAnswerRegen == nil {
		if file != "" {
			// Reuse the session's shared driver when stashed (the troubleshoot
			// cached-replay path), so run blocks execute in the shell the tools
			// backend exposes; else open our own. A session-supplied driver is owned
			// by the session — we don't close it here.
			d := pendingDriver
			if d == nil {
				runCwd := cwd
				if runCwd == "" {
					if abs, aerr := filepath.Abs(file); aerr == nil {
						runCwd = filepath.Dir(abs)
					}
				}
				if runCwd == "" {
					runCwd, _ = os.Getwd()
				}
				var derr error
				d, derr = driver.Open(driver.Options{Cwd: runCwd, Shell: pendingShell})
				if derr != nil {
					fmt.Fprintf(os.Stderr, "ai-playbook run: driver.Open failed (%v); falling back to render-only\n", derr)
					d = nil
				} else {
					defer d.Close()
				}
			}
			if d != nil {
				orch = BuildOrch(d, pendingReengage)
			}
		}
	}
	activity := pendingActivity
	servedBase := pendingServedBase
	askerFn := pendingAsker
	answerRegen := pendingAnswerRegen
	askBridge := pendingAskBridge
	pendingReengage = nil    // consume once, regardless of whether an orch was built
	pendingDriver = nil      // ditto: the session owns the driver's lifecycle
	pendingActivity = nil    // ditto: the session owns the activity channel's lifecycle
	pendingServedBase = ""   // ditto: served-base amend stash is consume-once
	pendingAsker = nil       // ditto: the `f` asker stash is consume-once
	pendingAnswerRegen = nil // ditto: the cached-answer regenerate stash is consume-once
	pendingAskBridge = nil   // ditto: the no-mux ask-bridge stash is consume-once
	pendingShell = ""        // ditto: the configured-shell stash is consume-once

	// Force TrueColor: zellij's alt-screen pane underreports the color profile
	// during bubbletea's auto-detection, causing colors to be downsampled.
	// The UI targets a truecolor Catppuccin terminal, so we pin it explicitly.
	m := newModel(harness, "")
	m.title = effectiveTitle(titleFlag, playbookTitle)
	m.subtitle = playbookSubtitle
	m.adaptedFrom = adaptedFrom
	m.origDoc = origDoc
	m.orch = orch
	m.driverPending = driverPending
	m.readyCh = readyCh
	m.defaultLabel = thinkingLabel
	m.thinkLabel = thinkingLabel
	m.isCached = isCached
	m.cachedAt = cachedAt
	m.thinking = true // implicit thinking at launch (spec)
	m.streaming = true
	m.reader = bufio.NewReader(src)
	m.parser = parser
	m.activity = activity
	m.servedBase = servedBase
	m.asker = askerFn
	m.answerRegen = answerRegen
	m.askBridge = askBridge
	prog := tea.NewProgram(
		m,
		tea.WithInput(tty),
		tea.WithOutput(tty),
		tea.WithColorProfile(colorprofile.TrueColor),
	)
	if _, err := prog.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook run: %v\n", err)
		return 1
	}
	// Drain and cancel any agent ask that arrives after the viewer exits so the
	// tools goroutine is never left blocked on an orphaned ask. A nil stop
	// channel keeps the goroutine running until process exit (bounded).
	if askBridge != nil {
		go drainAskCancel(askBridge, nil)
	}
	return 0
}
