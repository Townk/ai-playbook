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
	"github.com/Townk/ai-playbook/internal/mux"
	"github.com/Townk/ai-playbook/internal/orchestrator"
	"github.com/Townk/ai-playbook/internal/reengage"
	"github.com/Townk/ai-playbook/internal/runlog"
	"github.com/Townk/ai-playbook/pkg/driver"
	"github.com/Townk/ai-playbook/pkg/playbook/frontmatter"
)

// Options configures Run — the in-process render+drive viewer for a finalized
// playbook (or a served/cached/prose artifact). It is the single seam the
// launcher configures the viewer through: every field carries what one of the
// former pending* package globals carried, and each is consumed once per Run
// call (Options is per-invocation, so there is no cross-call clear block). The
// zero value is a valid render-only viewer over File/stdin (no driver, no
// re-engagement — every AI affordance degrades to an inert no-op).
type Options struct {
	// File is the finalized-playbook file to render (run-from-file / cached-serve /
	// stored show / prose answer). Read fully, front matter + any preamble above the
	// first H1 stripped, and used as the document stream + pager title. "" → read the
	// document from os.Stdin (an authoring transcript keeps the default header). Both
	// the `--file` flag and a bare positional resolve into this one field.
	File string
	// Cwd is the working dir for the in-process shell driver Run opens (its run blocks
	// execute here). "" → the dir of File, else $PWD.
	Cwd string
	// Title, when set, OVERRIDES the H1/front-matter-derived pager header (used by the
	// answer/escalate panes, where the classify supplies a short label and a prose
	// answer has no H1). "" → the title derived from the playbook document (empty →
	// default header).
	Title string
	// Harness is the header label. "" → "agent".
	Harness string
	// ThinkingLabel is the default spinner label. "" → "Working…".
	ThinkingLabel string
	// Cached, when true, shows a "cached" badge pill in the header (cache replay).
	Cached bool
	// CachedAt is the badge timestamp shown when Cached is true.
	CachedAt time.Time

	// Reengage is the re-engagement context (the troubleshoot cached-replay / create
	// paths use it so the regenerate/followup/wrapup kinds can re-author in-process).
	// nil disables those kinds (their pre-4c-ii behavior).
	Reengage *reengage.Reengage
	// AutoRollback is the --auto-rollback opt-in: when true, a step failure auto-fires
	// the rollback chain instead of only showing the manual "Rollback playbook" button.
	AutoRollback bool
	// Assisted is the --assisted opt-in. GUIDED-fullscreen mode rides the same viewer
	// path as the default (interactive) run; the assisted behavior itself is wired by
	// later Plan 2 tasks — this plumbing only stashes the opt-in onto the model.
	Assisted bool
	// Driver, when non-nil, is the session's shared shell driver: Run reuses it for the
	// playbook's run blocks (the same shell the tools backend exposes) instead of
	// opening its own. A supplied driver is OWNED by the session — Run does NOT close
	// it. nil → Run opens its own driver (the pre-stage-5 behavior).
	Driver *driver.Driver
	// ServedBase is the served playbook body for a cache HIT (serveCachedPlaybook): the
	// model carries it as m.servedBase, so a failing step → troubleshoot →
	// confirm/`w`-generate AMENDS the served playbook (base=ServedBase) rather than
	// starting fresh (spec §C). "" → fresh.
	ServedBase string
	// Asker, when non-nil, is the request-input-float asker backing the `f` keybind
	// (spec §D): proactive user-initiated amend via the request float. nil → `f` is a
	// no-op (off-zellij / no selfExe).
	Asker AskFunc
	// AnswerRegen is the cached-ANSWER regenerate seam (the `answer` cached-serve
	// path). When set, the cached pill's reload re-runs the cheap classify in place and
	// replaces the prose (instead of the orchestrator's playbook-shaped Regenerate);
	// the returned reader streams the fresh prose and the closure re-caches it. nil →
	// the answer path is not wired (the orchestrator path, or a flash-only no-op).
	AnswerRegen func() (io.ReadCloser, error)
	// AskBridge, when non-nil, is the no-mux agent-ask bridge: Run attaches it to the
	// model so the agent's `ask` reaches the in-viewer overlay. nil → no in-viewer ask
	// overlay (the mux-present float path, or no bridge created).
	AskBridge *askbridge.Bridge
	// OriginPane is the mux pane id of the shell the request ORIGINATED from
	// (capture's ZELLIJ_PANE_ID → "terminal_<n>", persisted in the session
	// doc's Origin). The play button (⏵) types the block's command into this
	// pane, focus-independent, with no trailing CR. "" → no origin pane (off-
	// zellij, or a viewer running in the user's own pane) — play degrades to
	// the clipboard with a status note.
	OriginPane string
	// Shell is the configured shell selector (cfg.Driver.Shell). ui is config-agnostic
	// — it receives the shell as DATA and passes it to driver.Open when it opens its
	// OWN driver. "" preserves the zsh default (no regression); a session-supplied
	// Driver ignores it (already opened with the configured shell).
	Shell string
	// ProjectRoot is the heuristic project root for a project_bound playbook run: the
	// driver exports PROJECT_ROOT=<root> so the playbook's portable $PROJECT_ROOT
	// references resolve. "" → no PROJECT_ROOT injected.
	ProjectRoot string
	// SourcePath is the on-disk .md path of a stored playbook (set by ShowMain) so the
	// viewer model can offer an [edit] button for file-backed playbooks. "" →
	// temp-file / generated path, no [edit].
	SourcePath string
	// FinalDraft, when true, starts the model with finalDraft=true (committed=false):
	// the create flow sets it so the viewer treats the already-rendered structured
	// playbook as a FINAL DRAFT — `w` PERSISTS it (CommitPlaybook via the injected
	// metadata seam), and the EOF branch sets the pager title from the playbook's H1 —
	// instead of running a final-playbook GENERATION pass.
	FinalDraft bool
	// JournalPath is the run-journal file this run persists to
	// (internal/runlog). The LAUNCHER resolves it (data root + project key +
	// run key) together with the playbook identity below — ui stays
	// store/path-agnostic and receives all three as data. "" = journaling off
	// (every pre-journal caller is unaffected).
	JournalPath string
	// JournalPlaybookPath is the journaled playbook identity: the absolute
	// path of the source .md the journal names.
	JournalPlaybookPath string
	// JournalContentHash is the playbook content sha256
	// (runlog.ContentHash), the retry content-drift gate's identity.
	JournalContentHash string
	// RetrySeed is the `run --retry` pre-seed (runlog.Seed.PreSeeded, resolved
	// by the launcher's gate ladder): block id → the previous run's ok record.
	// Each listed block starts satisfied — Status "ok" with the PreviousRun
	// marker ("✓ done — previous run"), its needs= edges met, still manually
	// re-runnable — and its record is installed into the lazy journal skeleton
	// so the first real result persists the full picture. nil = a fresh run.
	RetrySeed map[string]runlog.BlockRecord
	// Ready, when non-nil, switches Run onto the ASYNC-orchestrator path: instead of
	// opening the driver synchronously, Run renders the playbook first (shell buttons
	// dimmed + inert via driverPending) and reads the single OrchReady off this channel
	// through a startup tea.Cmd, enabling the buttons once it lands. nil → the sync
	// path (the orchestrator is built before the program starts).
	Ready <-chan OrchReady
}

// OrchReady carries the lazily-opened orchestrator (and its request-input asker)
// delivered on the Options.Ready channel by the async-startup path: the launcher
// opens the shell driver + builds the orchestrator in the BACKGROUND while Run
// renders the playbook IMMEDIATELY, then sends a single OrchReady once it is live.
// The Asker is the same AskFunc the `f` keybind uses (the float spawner), or nil.
// A nil Orch signals the background open FAILED: the UI clears the pending state
// and stays degraded (shell buttons remain disabled) rather than hanging.
type OrchReady struct {
	Orch  *orchestrator.Orchestrator
	Reeng *reengage.Engine
	Asker AskFunc
}

// BuildOrch constructs the in-process executor + the AI-layer re-engagement engine
// the way Run does, wired to the ui-internal cliMux + the active float mux. The
// async-startup path (the launcher's serveCachedPlaybook) can't build this itself —
// the cliMux is unexported — so it hands the driver + re-engagement context here off
// the background goroutine and delivers the pair over OrchReady. When re is non-nil
// the engine is wired for re-engagement (the cached replay's regenerate/wrap-up),
// with the executor's DriftTargetPath injected as the drift-target resolver so the
// engine never imports the orchestrator. The engine is nil when re is nil. This is
// the SINGLE construction site: Run's sync path calls it too. originPane is the
// request's origin shell pane (Options.OriginPane) — the play button's target.
func BuildOrch(d *driver.Driver, re *reengage.Reengage, originPane string) (*orchestrator.Orchestrator, *reengage.Engine) {
	fl := mux.Load()
	orch := orchestrator.New(d, newCLIMux(originPane, fl)).WithFloat(fl)
	return orch, reengage.New(re, orch.DriftTargetPath)
}

// loadPlaybookSource reads a finalized-playbook file (run-from-file / cached-serve),
// strips any leading YAML front matter AND any preamble above the first H1 title,
// and returns a reader over the stripped body, the playbook title (front-matter
// `name` when present, else the H1), the front-matter `description` as a subtitle
// (empty when the file carries no front-matter description), and the declared env
// map (nil when no front matter or no env block). A file with no front matter and
// no H1 is returned unchanged with empty title/subtitle (it's a transcript, not a
// playbook).
func loadPlaybookSource(file string) (r io.Reader, title, subtitle string, env map[string]frontmatter.EnvValue, err error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, "", "", nil, err
	}
	title, subtitle, body, env := loadPlaybookDocument(string(raw))
	return strings.NewReader(body), title, subtitle, env, nil
}

// effectiveTitle resolves the pager header title: an explicit Title wins (it
// OVERRIDES the H1/front-matter-derived title — used by the answer/escalate panes,
// where the classify supplies a short label and a prose answer has no H1),
// otherwise the title derived from the playbook document (empty → default header).
func effectiveTitle(flagTitle, derived string) string {
	if strings.TrimSpace(flagTitle) != "" {
		return flagTitle
	}
	return derived
}

// Main is the thin argv wrapper for the `ai-playbook run` subcommand: it parses
// flags from os.Args[2:] (os.Args[1] is the "run" subcommand) into Options and
// hands off to Run — preserving the documented `apb`/`ai-playbook run` argv
// contract. The caller is responsible for os.Exit.
func Main() int {
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
	// os.Args[1] is the "run" subcommand (dispatched from the root main); flags
	// start at os.Args[2:]. Guard for direct/odd invocations.
	argv := os.Args[2:]
	if len(os.Args) < 2 {
		argv = nil
	}
	_ = fs.Parse(argv) // flag.ExitOnError: Parse never returns a non-nil error

	// Source file: --file takes precedence over the bare positional. The positional
	// stays supported for back-compat (a direct `run <file.md>` still works). When
	// both are set, --file wins.
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

	return Run(Options{
		File:          file,
		Cwd:           cwd,
		Title:         titleFlag,
		Harness:       harness,
		ThinkingLabel: thinkingLabel,
		Cached:        isCached,
		CachedAt:      cachedAt,
	})
}

// Run renders + drives a finalized playbook in-process from opts and returns an
// exit code (the caller owns os.Exit). It is the real viewer entrypoint the
// launcher configures directly through Options; Main is a thin argv wrapper over
// it. On no TTY (tests / pipes) it drains the stream, renders once, and exits.
func Run(opts Options) int {
	// Force narrow (1-cell) accounting for East-Asian-ambiguous characters
	// (em-dash, ellipsis, smart quotes, nerd-font icons).  The terminal renders
	// them as 1 cell; without this setting go-runewidth counts them as 2,
	// causing admonition/code background fills to come up short.
	// Must run before any lipgloss/bubbletea call: charmbracelet/x/ansi reads
	// RUNEWIDTH_EASTASIAN in its package init, so the env var must be set first.
	os.Setenv("RUNEWIDTH_EASTASIAN", "0")
	runewidth.DefaultCondition.EastAsianWidth = false

	harness := opts.Harness
	if harness == "" {
		harness = "agent"
	}
	thinkingLabel := opts.ThinkingLabel
	if thinkingLabel == "" {
		thinkingLabel = "Working…"
	}

	file := opts.File
	isCached := opts.Cached
	cachedAt := opts.CachedAt

	// Input source: opts.File (a saved playbook / prose file), or stdin. Content
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
	// playbookEnv is the front-matter env map for the run-from-file path; nil for
	// stdin streams and files without a front-matter env block. Threaded into the
	// model as confirmEnv so the B2b confirmation gate can inspect declared vars.
	var playbookEnv map[string]frontmatter.EnvValue
	if file != "" {
		// A finalized playbook artifact from a file (also the cached-serve path). Read
		// it fully, strip any preamble above the H1 title, and use the playbook title
		// as the pager header. The stripped body is the document stream (saved
		// playbooks are plain markdown, no control records). Stripping here also cleans
		// EXISTING saved files that still carry preamble. A file with no H1 is left
		// unchanged (title stays empty).
		r, title, subtitle, env, err := loadPlaybookSource(file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ai-playbook run: %v\n", err)
			return 1
		}
		playbookTitle = title
		playbookSubtitle = subtitle
		playbookEnv = env
		src = r
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
		m.isCached = isCached
		m.cachedAt = cachedAt
		m.title = effectiveTitle(opts.Title, playbookTitle)
		m.subtitle = playbookSubtitle
		fmt.Print(m.staticRender())
		return 0
	}
	defer tty.Close()

	// In-process mode: when we have a playbook file to run, drive the real shell
	// directly via the orchestrator. The driver's working dir is opts.Cwd, else the
	// dir of <file.md>, else $PWD. A failed driver.Open falls back to the (no-orch)
	// render-only behavior with a logged note rather than crashing. Done only on the
	// interactive path (after a real TTY) so render-only invocations never spawn a
	// shell.
	var orch *orchestrator.Orchestrator
	var reeng *reengage.Engine
	// Async-orchestrator path: when Options.Ready is set, do NOT open a driver or
	// build an orch synchronously. Render the playbook IMMEDIATELY with the
	// shell-action buttons disabled (driverPending), and let a startup tea.Cmd read
	// the background-opened orchestrator off readyCh → orchReadyMsg, which enables the
	// buttons. Keeps blank-pane startup off the critical path entirely.
	readyCh := opts.Ready
	driverPending := false
	// projectRoot is hoisted so it's available to stash on the model after build. On
	// the sync new-driver path it is set inside the if-d==nil block; on all other
	// paths (async, reused-driver, answer-regen) it stays "".
	projectRoot := ""
	// Skip the shell driver entirely for a cached ANSWER (opts.AnswerRegen set): an
	// answer has no run blocks and its reload is a cheap-model call (ClassifyRequest),
	// not a shell command. Opening a driver here spawns a shell that sources the user's
	// full profile — seconds of blank-pane startup — for nothing. (The cached-PLAYBOOK
	// path reuses the session's already-open driver, so it never pays this.)
	if readyCh != nil {
		// ASYNC: the orchestrator is delivered later on readyCh. Leave orch nil and mark
		// the driver pending; the OTHER options (servedBase/asker/answerRegen/…) are
		// still honored below — they don't need the driver.
		driverPending = true
	} else if opts.AnswerRegen == nil {
		if file != "" {
			// Reuse the session's shared driver when supplied (the troubleshoot
			// cached-replay path), so run blocks execute in the shell the tools
			// backend exposes; else open our own. A session-supplied driver is owned
			// by the session — we don't close it here.
			d := opts.Driver
			if d == nil {
				runCwd := opts.Cwd
				if runCwd == "" {
					if abs, aerr := filepath.Abs(file); aerr == nil {
						runCwd = filepath.Dir(abs)
					}
				}
				if runCwd == "" {
					runCwd, _ = os.Getwd()
				}
				// PROJECT_ROOT: a project_bound playbook run supplies the heuristic
				// project root so the driver exports it and the body's portable
				// $PROJECT_ROOT references resolve. "" → not injected.
				projectRoot = opts.ProjectRoot
				env := os.Environ()
				if projectRoot != "" {
					env = append(env, "PROJECT_ROOT="+projectRoot)
				}
				var derr error
				d, derr = driver.Open(driver.Options{Cwd: runCwd, Shell: opts.Shell, Env: env})
				if derr != nil {
					fmt.Fprintf(os.Stderr, "ai-playbook run: driver.Open failed (%v); falling back to render-only\n", derr)
					d = nil
				} else {
					defer d.Close()
				}
			}
			if d != nil {
				orch, reeng = BuildOrch(d, opts.Reengage, opts.OriginPane)
			}
		}
	}

	// Force TrueColor: zellij's alt-screen pane underreports the color profile
	// during bubbletea's auto-detection, causing colors to be downsampled.
	// The UI targets a truecolor Catppuccin terminal, so we pin it explicitly.
	m := newModel(harness, "")
	m.title = effectiveTitle(opts.Title, playbookTitle)
	m.subtitle = playbookSubtitle
	// Run journal: opened only on this interactive path (the no-TTY branch
	// above renders without running). nil when the launcher supplied no
	// JournalPath (journaling off). The journal is LAZY — nothing is written
	// until a block result actually records — so a view-then-quit session (or
	// a driver.Open-failure render-only fallback) can never overwrite the
	// previous run's journal with an empty "ok".
	journal := runlog.Open(opts.JournalPath, opts.JournalPlaybookPath, opts.JournalContentHash)
	m.journal = journal
	// `run --retry`: pre-seed the prior run's ok blocks (Status ok +
	// PreviousRun) and install their records into the journal skeleton.
	m.applyRetrySeed(opts.RetrySeed)
	m.confirmEnv = playbookEnv     // front-matter env for the B2b confirmation gate
	m.projectRoot = projectRoot    // heuristic project root (also in driver.Options.Env)
	m.sourcePath = opts.SourcePath // on-disk .md path; non-empty → file-backed, [edit] enabled
	m.autoRollback = opts.AutoRollback
	m.assisted = opts.Assisted
	// NOTE (Phase-2 / in-session assisted-run): the async-startup and reused-driver
	// paths leave m.projectRoot empty (projectRoot is only set on the sync new-driver
	// path). When the gate is reused for an in-session assisted run, projectRoot must
	// also be threaded on those paths — otherwise PROJECT_ROOT exports empty string.
	m.orch = orch
	m.reeng = reeng
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
	m.servedBase = opts.ServedBase
	m.asker = opts.Asker
	m.answerRegen = opts.AnswerRegen
	m.askBridge = opts.AskBridge
	m.finalDraft = opts.FinalDraft
	// NOTE: assisted-mode entry (startAssisted) is deliberately NOT called here.
	// The model is built with EMPTY markdown (newModel(harness, "") above) — the
	// playbook content only streams into m.md via the parser after prog.Run()
	// starts, so m.blocks is still empty at this point (assistedNextID would see
	// zero runnable blocks and jump straight to the "done" footer). Instead,
	// maybeStartAssisted() is called from the stream-EOF handler in Update once
	// m.md/m.blocks are final for the run.
	prog := tea.NewProgram(
		m,
		tea.WithInput(tty),
		tea.WithOutput(tty),
		tea.WithColorProfile(colorprofile.TrueColor),
	)
	fm, err := prog.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook run: %v\n", err)
		return 1
	}
	// Drain and cancel any agent ask that arrives after the viewer exits so the
	// tools goroutine is never left blocked on an orphaned ask. A nil stop
	// channel keeps the goroutine running until process exit (bounded).
	if opts.AskBridge != nil {
		go drainAskCancel(opts.AskBridge, nil)
	}
	// Settle the exit: finalize the run journal (Outcome + Finished stamped
	// from the accumulated block records) and surface the final model's
	// exitCode (e.g. a GUIDED/assisted run that ends on a failed/aborted step)
	// instead of always returning 0.
	return finishRun(fm, journal)
}
