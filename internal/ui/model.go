package ui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Townk/ai-playbook/internal/askbridge"
	"github.com/Townk/ai-playbook/internal/frontmatter"
	"github.com/Townk/ai-playbook/internal/input"
	"github.com/Townk/ai-playbook/internal/orchestrator"
)

// spinTickMsg drives the spinner animation/timer while thinking. gen identifies
// which tick loop issued it: only the loop whose gen == m.tickGen continues, so a
// restartTick (which bumps tickGen) makes any older overlapping loop self-cancel.
type spinTickMsg struct{ gen int }

// tickCmd issues a tick for the CURRENT generation (the streaming hot-path's
// single loop). restartTick uses tickCmdGen to stamp a fresh generation.
func (m model) tickCmd() tea.Cmd { return m.tickCmdGen(m.tickGen) }

func (m model) tickCmdGen(gen int) tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return spinTickMsg{gen: gen} })
}

// hideCursorSeq is DECTCEM "hide cursor" (ESC[?25l). Our pager keeps
// View.Cursor nil forever (see View), and bubbletea's cursed_renderer only
// (re-)emits a cursor-visibility sequence when View.Cursor flips between
// nil/non-nil, or on the first frame / an alt-screen toggle (see
// shouldUpdateCursorVis in cursed_renderer.go). So after the very first frame
// bubbletea NEVER re-hides the cursor. Some multiplexers (zellij) re-SHOW the
// hardware cursor whenever they observe the renderer moving the cursor around
// to paint a diff — which happens in the re-render-heavy states: hint mode, the
// spinner/wave animation, and the verify-success confirm. We counter that by
// re-asserting the hide on those states/transitions and on focus regain.
const hideCursorSeq = "\x1b[?25l" // == ansi.ResetModeTextCursorEnable / ansi.HideCursor

// reassertHideCursor re-emits ESC[?25l via tea.Raw — the supported way to send a
// raw escape sequence. The program serializes RawMsg output through p.flush(),
// which the render-loop goroutine runs BEFORE p.renderer.flush() on the same
// tick, so the sequence can never land inside the renderer's synchronized-output
// (?2026) frame and corrupt it. Re-hiding an already-hidden cursor is a no-op at
// the terminal, so re-asserting every tick does not flicker.
func reassertHideCursor() tea.Cmd { return tea.Raw(hideCursorSeq) }

// startTick returns the single spinner tick loop, or nil if a loop is already
// live. External entry points (Init, thinkEvent, click handlers, regenerate,
// wrap-up, follow-up) call this instead of tickCmd directly so that at most one
// 100ms loop ever exists — overlapping loops would advance spinTicks multiple
// times per tick and race the seconds counter. The loop's own continuation (the
// spinTickMsg CONTINUE path) re-issues tickCmd directly; the STOP path clears
// tickRunning.
func (m *model) startTick() tea.Cmd {
	if m.tickRunning {
		return nil
	}
	m.tickRunning = true
	return m.tickCmd()
}

// restartTick force-(re)starts the spinner tick loop for a NEW thinking state,
// even when tickRunning is already set. The re-engagement paths (follow-up,
// regenerate, wrap-up) enter a fresh thinking state whose first stream chunk may
// be minutes away (claude --print is silent until its tool-use phase ends); the
// spinner must animate the whole time. startTick's single-loop guard is correct
// for the streaming hot-path but leaves the follow-up spinner STATIC whenever
// tickRunning is stale-true (e.g. the prior verify-run loop's flag had not yet
// been cleared) — startTick no-ops, so no loop drives the new thinking state.
//
// restartTick bumps tickGen and issues a fresh tickCmd unconditionally. Every
// tickCmd is stamped with the generation it belongs to (spinTickMsg.gen); the
// spinTickMsg handler advances the spinner once per tick but only CONTINUES the
// loop whose gen is current, so any older in-flight loop self-cancels on its next
// fire — exactly one loop survives, no double-counted seconds, and the spinner is
// guaranteed to animate. Use this on the re-engagement entry points; startTick on
// the streaming continuation path.
func (m *model) restartTick() tea.Cmd {
	m.tickGen++
	m.tickRunning = true
	return m.tickCmdGen(m.tickGen)
}

// renderInterval bounds how often streamed text is re-rendered. A stream can
// deliver many small chunks per second; rather than reflow (parse + highlight
// the whole accumulated buffer) and repaint on every chunk — which saturates
// the event loop and stutters — chunks are appended cheaply and a single
// reflow is coalesced per interval (~30fps).
const renderInterval = 33 * time.Millisecond

// renderTickMsg flushes any pending streamed text into a reflow.
type renderTickMsg struct{}

func (m model) renderTickCmd() tea.Cmd {
	return tea.Tick(renderInterval, func(time.Time) tea.Msg { return renderTickMsg{} })
}

// flashCmd returns a command that fires flashTickMsg after ~140ms, clearing
// the active flash highlight.
func (m model) flashCmd() tea.Cmd {
	return tea.Tick(140*time.Millisecond, func(time.Time) tea.Msg { return flashTickMsg{} })
}

// flashTickMsg clears the active flash highlight after ~140ms.
type flashTickMsg struct{}

type model struct {
	harness string
	// title is the finalized-playbook title shown in the pager header (▓▓▓ <title>)
	// in place of the default "ai-playbook — <harness>". Set from the playbook's first
	// H1 (playbookHeading) when rendering a FINALIZED playbook (run-from-file,
	// cached-serve, or an accepted final draft). Empty for a troubleshoot/authoring
	// transcript, which keeps the default header.
	title string
	// subtitle is the playbook description (front-matter `description`) shown as a
	// dim line directly under the ▓▓▓ <title> header. Set only when a finalized /
	// served playbook carries front matter with a description; empty for drafts,
	// transcripts, and old saved files without front matter (no subtitle row).
	subtitle    string
	md          string
	lines       []Line
	buttons     []Button
	blocks      []Block
	blockStates map[string]blockRunState
	width       int
	height      int
	xOff        int
	yOff        int
	hintMode    bool
	hintLabels  map[string]Button
	helpMode    bool
	helpLines   []Line
	helpYOff    int
	helpXOff    int

	// no-mux ask overlay: when askBridge is set, a tea.Cmd drains pending agent
	// asks (recvAskCmd); askMode raises the embedded ask dialog over the document
	// (the help-modal compositing mechanism) and routes keys to it; askReq is the
	// pending request answered on submit/cancel. nil bridge (mux path / tests) →
	// the overlay is never raised.
	askBridge *askbridge.Bridge
	askMode   bool
	ask       *input.Ask
	askReq    askbridge.Request

	// streaming + thinking
	thinking      bool
	thinkLabel    string
	defaultLabel  string
	spinFrame     int
	spinTicks     int // 100ms ticks within the current thinking session (seconds = /10)
	streaming     bool
	follow        bool      // auto-scroll to bottom while streaming
	justAnnounced bool      // set by announceFollowup so beginFollowupInProc skips its own `---` (the announcement already framed the attempt with a separator ABOVE the phrase)
	pinTop        int       // body line pinned to the viewport top (>=0): relaxes the scroll clamp so a freshly-announced follow-up sits at top with blank space below until new content fills it. -1 = none (no effect once content grows past the body).
	reader        io.Reader // input stream source (set by main); nil in tests/static
	parser        *streamParser

	dirty           bool // streamed text appended since the last reflow
	renderScheduled bool // a coalesced render tick is already pending
	tickRunning     bool // a single 100ms spinner tick loop is live
	tickGen         int  // current spinner-loop generation; older loops self-cancel

	// flash: non-empty while a button is briefly highlighted after activation.
	// Identity key is "<blockID>:<kind>"; cleared by flashTickMsg after ~140ms.
	flashKey string

	// cached replay: set when --cached <ISO-8601> is passed; the badge pill is
	// shown in the header line to tell the user this result is a cache replay.
	isCached bool
	cachedAt time.Time

	// orch is the in-process orchestrator. When non-nil the model talks to the
	// shell driver directly (in-process mode); nil means there is no orchestrator
	// (render-only / degraded), so orch-driven button actions are a no-op. Set by
	// Main when a playbook file is run, or delivered later via orchReadyMsg.
	orch *orchestrator.Orchestrator

	// driverPending marks the ASYNC-startup window: the playbook renders IMMEDIATELY
	// while the shell driver + orchestrator open in the BACKGROUND. While true the
	// shell-action buttons (run ▶ / run-in-assistant-shell / view-diff / apply / undo /
	// stop and the cached regenerate pill) render DIMMED and are INERT (their click /
	// hint / key path is a no-op); the copy-to-clipboard button stays fully enabled and
	// normally colored throughout. Cleared by orchReadyMsg once the orchestrator lands
	// (or its background open failed). False on the sync path (orch already built). See
	// shellActionsReady.
	driverPending bool

	// readyCh delivers the background-opened orchestrator on the async-startup path
	// (set by Main from the consume-once SetPendingReady stash). Init subscribes to it
	// via a tea.Cmd that reads the single OrchReady → orchReadyMsg. nil on the sync
	// path (the orchestrator was built before the program started).
	readyCh <-chan OrchReady

	// answerRegen is the cached-ANSWER regenerate seam (set by Main from
	// SetAnswerRegen). When non-nil, the cached pill's reload re-runs the cheap
	// classify on the original request, streams the fresh prose back (the returned
	// reader), and re-caches it — INSTEAD of the orchestrator's playbook-shaped
	// Regenerate. nil → the orchestrator path (playbooks) or a flash-only no-op.
	answerRegen func() (io.ReadCloser, error)

	// status is a transient one-line message shown in the status bar (e.g. when
	// an in-process action is not yet implemented). Cleared on the next key/click.
	status string

	// reengageStream is the live re-engagement stream (regenerate/followup/wrapup)
	// swapped in via the in-process re-arm. It is closed on EOF so the agent
	// process is reaped and the orchestrator's tee-on-close side effects fire
	// (regenerate's cache re-store, wrap-up's solution-artifact close). nil when no
	// in-process re-engagement stream is active.
	reengageStream io.Closer

	// activity is the buffered channel the session writes the agent's live tool
	// calls to (via the tools backend's OnActivity hook). The model subscribes via
	// a tea.Cmd (activityWaitCmd) that reads one summary → activityMsg. nil when no
	// tools backend is wired (the no-tools fallback) — then no activity is shown and
	// the spinner still animates. Set by RunStream from StreamOptions.Activity.
	activity <-chan string

	// activityLine is the latest agent tool-call summary, shown under the "Working…"
	// line while thinking/streaming. Cleared when real playbook content starts
	// arriving (the first textEvent) so it never lingers over rendered content.
	activityLine string

	// followups counts how many auto-follow-ups have fired this session. The
	// verify-fail auto-fire repeats on EACH failure while followups < maxFollowups;
	// past the cap it falls back to the manual "try another fix" button.
	followups    int
	maxFollowups int

	// wrappedUp gates the verify-SUCCESS auto wrap-up (issue #3) to fire ONCE per
	// resolution. A verify RUN with exit 0 auto-triggers the wrap-up re-engagement
	// (the agent asks the user, via the ask tool, whether the fix solved their
	// problem, then finalizes the `## Solution` + remember only on confirmation).
	// Set the first time it fires so a re-rendered/re-run verify-0 does not re-trigger
	// (no wrap-up loop). The manual `w`-key wrap-up is unaffected by this flag.
	wrappedUp bool

	// confirmResolved is the native verify-success confirm state (stage 2, spec §A).
	// When true the pager renders an inline confirm block — a green prompt and the
	// [ Yes ] [ No ] buttons — answerable by mouse-click on the buttons or the `y`/`n`
	// keys. It replaces the old agent-ask wrap-up: Yes generates the final playbook
	// (REPLACE draft); No simply dismisses (the command already succeeded — the user can
	// quit or press `c` to bring the confirm back). Set once on a verify-success (gated like the
	// old wrap-up); cleared when answered.
	confirmResolved bool

	// confirmFocus is the keyboard-focused confirm button while confirmResolved is
	// true: 0 = Yes (the default), 1 = No. The user moves focus with ←/→ (also h/l
	// and Tab) and selects with Enter/Space; the focused button is highlighted and
	// the other dimmed (appendConfirmButtons / confirmButtonLabel). The direct y/n
	// keys and a mouse click on either button still resolve regardless of focus.
	confirmFocus int

	// servedBase is the playbook body served on a cache HIT (spec §C amend-on-rerun).
	// When non-empty the session is SERVING an existing playbook for this context: a
	// failing step → the follow-up loop troubleshoots it → the verify-success confirm /
	// `w`-generate AMENDS the served playbook (base=servedBase) instead of starting
	// fresh, so the served playbook is improved in place and re-cached under the same
	// keys. Empty for a FRESH troubleshoot (authorPlaybook / cache MISS). Set by Main
	// from the consume-once SetServedBase stash, threaded from serveCachedPlaybook.
	servedBase string

	// finalDraft marks that the rendered playbook is a GENERATED final-playbook draft
	// (the confirm "Yes" / `f` / `w`-on-transcript produced it). committed flips true
	// once it is persisted (save + cache-replace via orchestrator.CommitPlaybook) —
	// either by the auto-finish baseline (spec §D) or a `w` re-persist.
	finalDraft bool
	committed  bool

	// persistOnFinish marks that the in-flight final-playbook generation is a FINALIZE
	// (confirm-yes / `w`-on-transcript) rather than an `f` AMEND (spec §D). It is set by
	// beginFinalPlaybookInProc and cleared by the `f`-amend path (fChangeMsg). At
	// stream-EOF the finalDraft branch reads it: persistOnFinish → auto-persist a
	// baseline (commitPlaybookCmd; committed flips true on success) and reset the flag;
	// an `f` amend leaves committed=false (an unsaved tweak the `w`/quit-guard handle).
	persistOnFinish bool

	// preFinalMd backs up the resolved troubleshoot displayed at the moment a FINAL
	// playbook generation REPLACES it (beginFinalPlaybookGenerate sets m.md = ""). If
	// the generation finishes with junk (a narration: no H1 / no runnable blocks) the
	// stream-EOF guard restores this so the good troubleshoot is never lost or persisted.
	preFinalMd string

	// quitGuard is set when the user pressed quit (q/esc/ctrl+c) while an uncommitted
	// draft was displayed (finalDraft && !committed): instead of quitting we show a
	// one-line warning and require a SECOND quit to actually exit. A `w` commit in
	// between clears it (the draft is now saved). Reset on any non-quit key so the
	// "press quit again" intent stays immediate, not sticky across other interactions.
	quitGuard bool

	// asker spawns the request-input float (the same floatinput.Asker the agent's
	// `ask` tool uses) and returns the user's typed answer, OFF the bubbletea event
	// loop. It backs the `f` keybind (spec §D): `f` → ask "What should I change?" →
	// the user types a free-form adjustment → re-author the displayed playbook in
	// AMEND mode (base=m.md, change=the typed value) → REPLACE draft. nil when the
	// float can't be spawned (off-zellij / tests / no selfExe) → `f` is a no-op. Set
	// by Main/RunStream from the consume-once SetAsker stash / StreamOptions.Asker.
	asker AskFunc
}

// AskFunc opens the request-input float with the given prompt (the floatinput Type
// is text) and blocks until the user submits or cancels. It returns the typed value
// and whether the user submitted (false → cancel/Esc or the float vanished). It is a
// closure so the ui package needn't import floatinput; the session builds it from its
// floatinput.Asker (a fixed text-type Request with the given prompt).
type AskFunc func(prompt string) (value string, submitted bool)

// emitAction performs a button's action. When an in-process orchestrator is wired
// (m.orch != nil) it returns a tea.Cmd that drives the orchestrator directly (off
// the event loop) and feeds a resultMsg back. When there is no orchestrator
// (m.orch == nil — render-only / degraded startup) an orch-driven action is a clean
// NO-OP returning nil; the shell-action buttons are rendered disabled in that state
// (driverPending / canRegenerate gating), so this is the safety floor. The returned
// Cmd is nil when there is nothing to feed back, so callers can unconditionally batch it.
func (m model) emitAction(b Button) tea.Cmd {
	if m.orch != nil {
		return m.orchCmd(b)
	}
	return nil
}

// shellActionsReady reports whether the shell-backed buttons may render enabled and
// dispatch. It is false ONLY during the async-startup window (driverPending), while
// the background orchestrator is still opening; in that window the shell-action
// buttons render dimmed and are inert. Once the orchestrator lands (orchReadyMsg)
// this is true and the buttons render/behave exactly as before. The copy button
// never consults this — it needs no shell.
func (m model) shellActionsReady() bool { return !m.driverPending }

// isShellActionKind reports whether a button kind needs the shell driver /
// orchestrator to act: run (▶), play (run-in-assistant-shell), stop, (view-)diff,
// apply-diff, undo-diff, and the cached regenerate pill. These are the buttons gated
// off shellActionsReady on the async-startup path. Copy (clipboard) and pager-local
// kinds (toggle / confirm / followup) are NOT gated.
func isShellActionKind(kind string) bool {
	switch kind {
	case "run", "play", "stop", "diff", "view-diff", "apply-diff", "undo-diff", "regenerate":
		return true
	}
	return false
}

func newModel(harness, md string) model {
	return model{
		harness:      harness,
		md:           md,
		width:        80,
		height:       24,
		helpLines:    buildHelpLines(),
		defaultLabel: "Working…",
		follow:       false, // start at the top on load; only append (wrap-up) re-enables follow
		pinTop:       -1,    // no pin until a follow-up announcement frames itself at the top
		blockStates:  map[string]blockRunState{},
		maxFollowups: resolveMaxFollowups(),
	}
}

// defaultMaxFollowups is how many times the verify-fail auto-follow-up may fire
// before falling back to the manual "try another fix" button.
const defaultMaxFollowups = 3

// resolveMaxFollowups reads the auto-follow-up cap from $AI_PLAYBOOK_MAX_FOLLOWUPS
// (a positive integer), else defaultMaxFollowups. A non-positive / unparseable
// value falls back to the default rather than disabling the feature.
func resolveMaxFollowups() int {
	if v := os.Getenv("AI_PLAYBOOK_MAX_FOLLOWUPS"); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			return n
		}
	}
	return defaultMaxFollowups
}

func (m model) Init() tea.Cmd {
	var cmds []tea.Cmd
	// Subscribe to the agent's live activity feed (the tools backend's OnActivity
	// bridged through m.activity). nil-channel returns nil, so this is a no-op
	// without a tools backend. Started even when there's no stream reader so the
	// feed is live for the whole session.
	if c := m.activityWaitCmd(); c != nil {
		cmds = append(cmds, c)
	}
	// Async-startup: subscribe to the background-opened orchestrator. The cmd reads the
	// single OrchReady off readyCh (off the event loop) → orchReadyMsg, which installs
	// the orchestrator and re-enables the shell buttons. nil readyCh (the sync path)
	// returns nil, so this is a no-op there.
	if c := m.orchReadyWaitCmd(); c != nil {
		cmds = append(cmds, c)
	}
	// Subscribe to the no-mux agent-ask bridge: each pending ask arrives as an
	// askOpenMsg that raises the overlay. nil bridge returns nil (no-op).
	if c := recvAskCmd(m.askBridge); c != nil {
		cmds = append(cmds, c)
	}
	if m.reader == nil {
		return tea.Batch(cmds...)
	}
	cmds = append(cmds, readStream(m.reader, m.parser))
	var tickIssued bool
	if m.thinking {
		if c := m.startTick(); c != nil {
			cmds = append(cmds, c)
			tickIssued = true
		}
	}
	dbg("Init thinking=%v streaming=%v reader=%v activity=%v tickIssued=%v",
		m.thinking, m.streaming, m.reader != nil, m.activity != nil, tickIssued)
	return tea.Batch(cmds...)
}

// headerRows is the height the header takes (title only; top padding provides
// the gap between header and body).
const headerRows = 1

// hintRows is the height the bottom key-hint takes.
const hintRows = 1

// contentWidth returns the render/scroll width: full width minus 2-col left
// and 2-col right margins (floored at 1).
func (m *model) contentWidth() int {
	w := m.width - 4
	if w < 1 {
		w = 1
	}
	return w
}

// body returns the number of visible body rows.
// Non-cached layout: leading(1) + header(1) + top-pad(1) + body + bot-pad(1) + hint(1) = H → body = H-5.
// Cached layout:     leading(1) + header(1) + blank(1) + pill(1) + blank(1) + body + bot-pad(1) + hint(1) = H → body = H-7.
func (m *model) body() int {
	// subtract leading blank + top/bottom pads + cached extra rows + subtitle row
	h := m.height - headerRows - hintRows - 3 - m.cachedRows() - m.subtitleRows()
	if m.confirmResolved {
		// The confirm block is questionLines+4 bottom rows (blank, the wrapped question's
		// N lines, blank, buttons, blank). It REPLACES the single bottom-pad already
		// counted in the base formula above, so body() reduces by questionLines+3 (= the
		// block minus that one pad) — for a single-line question this is the prior fixed
		// 4. This keeps the buttons pinned on m.height-3 and the question always fully fits
		// without overlapping content.
		h -= m.confirmQuestionLines() + 3
	}
	if h < 1 {
		h = 1
	}
	return h
}

func (m *model) reflow() {
	m.lines, m.buttons, m.blocks = Render(m.renderBody(), m.contentWidth(), m.blockStates, m.flashKey, m.driverPending)
	m.appendCachedButton()
	m.appendConfirmButtons()
	m.clampScroll()
}

// flushRender re-renders the accumulated stream buffer if any text is pending,
// pinning the view to the bottom while following. No-op when nothing is dirty,
// so it's cheap to call from the render tick and on EOF.
func (m *model) flushRender() {
	if !m.dirty {
		return
	}
	m.reflow()
	if m.follow {
		m.yOff = len(m.lines) // clampScroll caps to the bottom
		m.clampScroll()
	}
	m.dirty = false
}

func (m *model) clampScroll() {
	maxY := len(m.lines) - m.body()
	if maxY < 0 {
		maxY = 0
	}
	// A pinned follow-up announcement may sit near the end of the doc; allow the
	// over-scroll (blank space below) so it can stay at the viewport top until the
	// new attempt's content fills in. No effect once content grows past the body
	// (then pinTop <= maxY and this is a no-op).
	if m.pinTop >= 0 && m.pinTop > maxY {
		maxY = m.pinTop
	}
	if m.yOff > maxY {
		m.yOff = maxY
	}
	if m.yOff < 0 {
		m.yOff = 0
	}
	maxX := MaxWideWidth(m.lines) - m.contentWidth()
	if maxX < 0 {
		maxX = 0
	}
	if m.xOff > maxX {
		m.xOff = maxX
	}
	if m.xOff < 0 {
		m.xOff = 0
	}
}

// cachedRows returns the number of extra header rows inserted when showing a
// cached-replay badge: 2 (blank above the pill + blank below the pill) when
// isCached, 0 otherwise. This is the single source of truth for the layout
// delta between cached and non-cached views.
func (m *model) cachedRows() int {
	if m.isCached {
		return 2
	}
	return 0
}

// bodyTop returns the screen row (0-based) of the first body line.
// Non-cached layout: leading blank(1) + header(1) + [subtitle?] + top-pad(1) = row 3 (+1 with a subtitle).
// Cached layout:     leading blank(1) + header(1) + [subtitle?] + blank(1) + pill(1) + blank(1) = row 5 (+1 with a subtitle).
func (m *model) bodyTop() int {
	return 1 + headerRows + m.subtitleRows() + 1 + m.cachedRows()
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// no-mux ask overlay: a pending agent ask raises the dialog; while it's open
	// every message except a window resize (which still reaches the document so the
	// layout behind the overlay stays correct) drives the embedded ask.
	if om, ok := msg.(askOpenMsg); ok {
		return m, m.openAsk(om.req)
	}
	// Ask overlay: only user-input messages are diverted to the embedded ask
	// widget; stream/tick/ready messages fall through to the main switch so the
	// stream keeps draining and re-arming behind the overlay (mirrors helpMode,
	// which only intercepts inside case tea.KeyPressMsg).
	if m.askMode {
		switch msg.(type) {
		case tea.KeyPressMsg, tea.PasteMsg, tea.MouseClickMsg, tea.MouseWheelMsg:
			return m, m.handleAskKey(msg)
		}
		// WindowSizeMsg and all non-input messages (streamEventsMsg, renderTickMsg,
		// orchReadyMsg, spinTickMsg, flashTickMsg, …) fall through to the main switch.
	}
	switch msg := msg.(type) {
	case streamEventsMsg:
		startedThinking := false
		quit := false
		for _, ev := range msg.events {
			switch e := ev.(type) {
			case textEvent:
				m.md += e.text // cheap append; reflow is coalesced (renderTickMsg)
				m.dirty = true
				// Real playbook content arriving ends the thinking phase: the spinner +
				// activity line stop, and the activity line is cleared so it doesn't
				// linger over the rendered content. Guard against an EMPTY/whitespace-only
				// text event flipping thinking off — claude's stream can interleave empty
				// text chunks during the work phase, and an empty chunk is not real
				// playbook content. Only non-whitespace text ends thinking.
				if strings.TrimSpace(e.text) != "" {
					if m.thinking {
						dbg("textEvent ends thinking: textlen=%d %q", len(e.text), collapseLine(e.text))
					}
					m.thinking = false
					m.activityLine = ""
				}
			case thinkEvent:
				label := e.label
				if label == "" {
					label = m.defaultLabel
				}
				if !m.thinking { // new thinking session: reset the timer
					m.thinking = true
					m.spinFrame = 0
					m.spinTicks = 0
					startedThinking = true
				}
				m.thinkLabel = label
			case quitEvent:
				quit = true
			}
		}
		if quit {
			dbg("quitEvent received -> tea.Quit")
			return m, tea.Quit
		}
		if msg.eof {
			m.flushRender() // render whatever's pending immediately
			m.streaming = false
			m.thinking = false
			// Confirm what the agent actually produced: 0 runnable blocks at EOF means
			// it narrated/applied instead of WRITING {id=fix}/{id=verify} blocks (a
			// prompt-compliance gap), vs blocks>0 not visible (a render gap).
			dbg("stream EOF: md=%dB blocks=%d head=%q", len(m.md), len(m.blocks), collapseLine(m.md))
			// Finalized-playbook draft: strip any preamble above the H1 title and set
			// the pager header to the playbook title. Gated on finalDraft so a
			// troubleshoot transcript (non-finalDraft EOF) is left untouched (default
			// "ai-playbook — <harness>" header, no stripping).
			if m.finalDraft {
				title, body := playbookHeading(m.md)
				// Safety guard: occasionally the model NARRATES instead of producing a
				// playbook (e.g. a short "The playbook is above…" with 0 runnable blocks).
				// A real playbook has an H1 title AND at least one runnable block. If this
				// draft is NOT a real playbook, restore the troubleshoot we replaced and
				// do NOT keep or persist the junk.
				if !isValidPlaybook(m.md, len(m.blocks)) {
					dbg("invalid final playbook (title=%q blocks=%d) — restoring troubleshoot, skipping persist", title, len(m.blocks))
					m.md = m.preFinalMd
					m.title = ""
					m.finalDraft = false
					m.persistOnFinish = false
					m.status = "Couldn't generate a clean playbook — kept the troubleshoot. Press c to retry."
					m.reflow()
					return m, nil
				}
				// VALID: strip any preamble above the H1 and set the pager header title.
				m.md = body
				m.title = title
				m.reflow()
			}
			// Close a live in-process re-engagement stream so the agent process is
			// reaped and the orchestrator's on-close side effects fire (regenerate's
			// cache re-store, wrap-up's artifact close). No-op when no stream is active (nil).
			if m.reengageStream != nil {
				_ = m.reengageStream.Close()
				m.reengageStream = nil
			}
			// Auto-finish baseline (spec §D): a FINALIZE generation (confirm-yes /
			// `w`-on-transcript, marked persistOnFinish) auto-persists at EOF so quitting
			// before `w` still leaves a complete saved playbook with front matter. An `f`
			// AMEND leaves persistOnFinish cleared → no auto-persist, committed stays
			// false (an unsaved tweak the `w`/quit-guard handle). Show a transient
			// "finalizing…" while the (slow) metadata round-trip runs; the commit result
			// (statusMsg) replaces it with the saved/err line and committed flips true on
			// success. Reset the flag so a subsequent stream-EOF doesn't re-persist.
			if m.finalDraft && m.persistOnFinish {
				m.persistOnFinish = false
				m.status = "finalizing…"
				return m, m.commitPlaybookCmd(m.md)
			}
			return m, nil
		}
		cmds := []tea.Cmd{readStream(m.reader, m.parser)}
		if startedThinking {
			cmds = append(cmds, m.startTick())
		}
		// Coalesce the (expensive) whole-buffer reflow to renderInterval instead
		// of reflowing on every chunk. Schedule at most one tick at a time.
		if m.dirty && !m.renderScheduled {
			m.renderScheduled = true
			cmds = append(cmds, m.renderTickCmd())
		}
		return m, tea.Batch(cmds...)
	case renderTickMsg:
		m.renderScheduled = false
		m.flushRender()
		return m, nil
	case orchReadyMsg:
		// Async-startup: the background-opened orchestrator landed. Install it (and the
		// asker, when supplied), clear the pending state, and reflow so the now-enabled
		// shell buttons re-render normally colored + live. A nil Orch (background open
		// failed) leaves m.orch nil but still clears driverPending — the buttons stay
		// disabled (degraded, no shell) rather than hanging.
		m.orch = msg.Orch
		if msg.Asker != nil {
			m.asker = msg.Asker
		}
		m.driverPending = false
		m.reflow()
		return m, nil
	case flashTickMsg:
		m.flashKey = ""
		m.reflow()
		return m, nil
	case spinTickMsg:
		// Stale loop: a restartTick bumped the generation, so a newer loop now drives
		// the spinner. Drop this tick WITHOUT advancing the frame or seconds (the
		// live loop already does both) and do NOT continue — it self-cancels here,
		// leaving exactly one live loop and no double-counted seconds.
		if msg.gen != m.tickGen {
			return m, nil
		}
		running := false
		for id, st := range m.blockStates {
			if st.Status == "running" {
				st.SpinFrame++
				m.blockStates[id] = st
				running = true
			}
		}
		if m.thinking {
			m.spinFrame++
			m.spinTicks++
		}
		if !m.thinking && !running {
			m.tickRunning = false
			return m, nil
		}
		if running {
			m.reflow()
		}
		// Re-assert the hide-cursor on every live tick: the spinner/wave diff this
		// tick paints is exactly the renderer activity that makes zellij re-show the
		// hardware cursor. Idempotent, so it never flickers (see reassertHideCursor).
		return m, tea.Batch(m.tickCmd(), reassertHideCursor())
	case tea.FocusMsg:
		// The pager regains focus after the thinking float closes; some terminals
		// re-show the cursor on focus. Re-assert the hide (ReportFocus is enabled in
		// View so this msg is actually delivered).
		return m, reassertHideCursor()
	case tea.WindowSizeMsg:
		m.flashKey = ""
		m.width = msg.Width
		m.height = msg.Height
		m.reflow()
		m.clampHelpScroll()
		return m, nil
	case tea.MouseClickMsg:
		m.flashKey = ""
		m.status = ""
		if msg.Button == tea.MouseLeft {
			if b, ok := buttonAt(m.buttons, msg.X, msg.Y, m.yOff, m.bodyTop()); ok {
				// Async startup: the shell isn't open yet — the shell-action buttons are
				// dimmed and INERT (no flash, no dispatch). Copy stays live (not gated).
				if isShellActionKind(b.Kind) && !m.shellActionsReady() {
					return m, nil
				}
				m.flashKey = b.BlockID + ":" + b.Kind
				if b.Kind == "toggle" {
					m = m.handleToggle(b.BlockID) // handleToggle already calls reflow
					return m, m.flashCmd()
				}
				if b.Kind == "run" {
					m = m.markRunning(b.BlockID)
					ac := m.emitAction(b)
					m.reflow()
					return m, tea.Batch(m.startTick(), m.flashCmd(), ac)
				}
				if b.Kind == "stop" {
					m.flashKey = b.BlockID + ":" + b.Kind
					m.markStopped(b.BlockID)
					ac := m.emitAction(b)
					m.reflow()
					return m, tea.Batch(m.flashCmd(), ac)
				}
				if b.Kind == "apply-diff" {
					st := m.blockStates[b.BlockID]
					st.Status = "running"
					st.Action = "apply"
					st.SpinFrame = 0
					m.blockStates[b.BlockID] = st
					ac := m.emitAction(b)
					m.reflow()
					return m, tea.Batch(m.startTick(), m.flashCmd(), ac)
				}
				if b.Kind == "undo-diff" {
					st := m.blockStates[b.BlockID]
					st.Status = "running"
					st.Action = "undo"
					st.SpinFrame = 0
					m.blockStates[b.BlockID] = st
					ac := m.emitAction(b)
					m.reflow()
					return m, tea.Batch(m.startTick(), m.flashCmd(), ac)
				}
				if b.Kind == "regenerate" {
					m.flashKey = "cached:regenerate"
					// In-process: re-author via the orchestrator and re-arm the parser
					// (REPLACE). Else flash-only (no regenerate path wired).
					if cmd := m.beginRegenerate(); cmd != nil {
						return m, tea.Batch(m.flashCmd(), cmd)
					}
					m.reflow()
					return m, m.flashCmd()
				}
				if b.Kind == "followup" {
					if cmd := m.beginFollowupStream(b.BlockID, b.Payload); cmd != nil {
						return m, tea.Batch(m.flashCmd(), cmd)
					}
					m.reflow()
					return m, m.flashCmd()
				}
				if b.Kind == "confirm-yes" || b.Kind == "confirm-no" {
					if cmd := m.resolveConfirm(b.Kind == "confirm-yes"); cmd != nil {
						return m, tea.Batch(m.flashCmd(), cmd)
					}
					m.reflow()
					return m, m.flashCmd()
				}
				ac := m.emitAction(b)
				m.reflow()
				return m, tea.Batch(m.flashCmd(), ac)
			}
		}
		return m, nil
	case tea.MouseWheelMsg:
		// The wheel scrolls the help modal when it's open, otherwise the document
		// (a few lines per notch). Ignored in hint mode (a transient selection).
		// Vertical only — terminals don't reliably deliver horizontal-wheel events.
		const wheelStep = 3
		var delta int
		switch msg.Button {
		case tea.MouseWheelUp:
			delta = -wheelStep
		case tea.MouseWheelDown:
			delta = wheelStep
		default:
			return m, nil
		}
		if m.helpMode {
			m.helpYOff += delta
			m.clampHelpScroll()
		} else if !m.hintMode {
			m.yOff += delta
			m.clampScroll()
		}
		return m, nil
	case tea.KeyPressMsg:
		m.flashKey = ""
		m.status = ""
		// The uncommitted-draft quit guard is a two-press intent: it only persists across
		// a consecutive quit (to discard) or a `w` (to save, which clears it). Any OTHER
		// key (navigation, help, …) cancels the pending discard so a later quit warns
		// afresh rather than silently exiting.
		if s := msg.String(); s != "q" && s != "esc" && s != "ctrl+c" && s != "w" {
			m.quitGuard = false
		}
		// Help overlay: resolve before hint/normal handling.
		if m.helpMode {
			switch msg.String() {
			case "esc", "q", "?":
				m.helpMode = false
			case "down", "j":
				m.helpYOff++
			case "up", "k":
				m.helpYOff--
			case "ctrl+d":
				m.helpYOff += helpHalf(m)
			case "ctrl+u":
				m.helpYOff -= helpHalf(m)
			case "ctrl+f", "pgdown":
				m.helpYOff += helpPage(m)
			case "ctrl+b", "pgup":
				m.helpYOff -= helpPage(m)
			case "g", "home":
				m.helpYOff = 0
			case "G", "end":
				m.helpYOff = len(m.helpLines)
			case "right", "l":
				m.helpXOff++
			case "left", "h":
				m.helpXOff--
			case "L":
				m.helpXOff += helpHalfW(m)
			case "H":
				m.helpXOff -= helpHalfW(m)
			case "0", "^":
				m.helpXOff = 0
			case "$":
				m.helpXOff = MaxWideWidth(m.helpLines)
			}
			m.clampHelpScroll()
			return m, nil
		}
		// Hint mode: resolve the pending label before any normal nav.
		if m.hintMode {
			switch msg.String() {
			case "esc":
				m.hintMode = false
				m.hintLabels = nil
			default:
				if b, ok := m.hintLabels[msg.String()]; ok {
					// Async startup: shell-action buttons are inert until the orchestrator
					// lands — close the hint overlay without dispatching. Copy is not gated.
					if isShellActionKind(b.Kind) && !m.shellActionsReady() {
						m.hintMode = false
						m.hintLabels = nil
						return m, nil
					}
					m.flashKey = b.BlockID + ":" + b.Kind
					m.hintMode = false
					m.hintLabels = nil
					if b.Kind == "toggle" {
						m = m.handleToggle(b.BlockID) // handleToggle already calls reflow
						return m, m.flashCmd()
					}
					if b.Kind == "run" {
						m = m.markRunning(b.BlockID)
						ac := m.emitAction(b)
						m.reflow()
						return m, tea.Batch(m.startTick(), m.flashCmd(), ac)
					}
					if b.Kind == "stop" {
						m.flashKey = b.BlockID + ":" + b.Kind
						m.markStopped(b.BlockID)
						ac := m.emitAction(b)
						m.reflow()
						return m, tea.Batch(m.flashCmd(), ac)
					}
					if b.Kind == "apply-diff" {
						st := m.blockStates[b.BlockID]
						st.Status = "running"
						st.Action = "apply"
						st.SpinFrame = 0
						m.blockStates[b.BlockID] = st
						ac := m.emitAction(b)
						m.reflow()
						return m, tea.Batch(m.startTick(), m.flashCmd(), ac)
					}
					if b.Kind == "undo-diff" {
						st := m.blockStates[b.BlockID]
						st.Status = "running"
						st.Action = "undo"
						st.SpinFrame = 0
						m.blockStates[b.BlockID] = st
						ac := m.emitAction(b)
						m.reflow()
						return m, tea.Batch(m.startTick(), m.flashCmd(), ac)
					}
					if b.Kind == "regenerate" {
						m.flashKey = "cached:regenerate"
						if cmd := m.beginRegenerate(); cmd != nil {
							return m, tea.Batch(m.flashCmd(), cmd)
						}
						m.reflow()
						return m, m.flashCmd()
					}
					if b.Kind == "followup" {
						if cmd := m.beginFollowupStream(b.BlockID, b.Payload); cmd != nil {
							return m, tea.Batch(m.flashCmd(), cmd)
						}
						m.reflow()
						return m, m.flashCmd()
					}
					if b.Kind == "confirm-yes" || b.Kind == "confirm-no" {
						if cmd := m.resolveConfirm(b.Kind == "confirm-yes"); cmd != nil {
							return m, tea.Batch(m.flashCmd(), cmd)
						}
						m.reflow()
						return m, m.flashCmd()
					}
					ac := m.emitAction(b)
					m.reflow()
					return m, tea.Batch(m.flashCmd(), ac)
				}
				m.hintMode = false
				m.hintLabels = nil
			}
			return m, nil
		}
		// Issue #4: while the verify-success confirm row is active it is keyboard-
		// FOCUSABLE — ←/→ (also h/l, Tab) move focus between [ Yes ] and [ No ], and
		// Enter/Space SELECT the focused button. These keys are captured ONLY while the
		// confirm is shown so normal nav (h/l scroll, space=hint leader) is unaffected
		// otherwise. The direct y/n keys and a mouse click still resolve regardless of
		// focus (handled below / in the click path).
		if m.confirmResolved {
			switch msg.String() {
			case "left", "h":
				m.confirmFocus = 0
				return m, nil
			case "right", "l":
				m.confirmFocus = 1
				return m, nil
			case "tab":
				m.confirmFocus = 1 - m.confirmFocus
				return m, nil
			case "enter", "space", " ":
				if cmd := m.resolveConfirm(m.confirmFocus == 0); cmd != nil {
					return m, cmd
				}
				m.reflow()
				return m, nil
			}
		}
		// Leader: Space enters hint mode over the visible buttons. bubbletea v2
		// (ultraviolet) reports the space key as "space", not " ".
		if s := msg.String(); s == "space" || s == " " {
			var visible []Button
			for _, b := range m.buttons {
				if b.Screen {
					// Screen-fixed buttons are always "visible" (they're in the
					// fixed header, not the scrollable body).
					visible = append(visible, b)
					continue
				}
				if b.Line >= m.yOff && b.Line < m.yOff+m.body() {
					visible = append(visible, b)
				}
			}
			if len(visible) > 0 {
				m.hintLabels = assignHintLabels(visible)
				m.hintMode = true
				// Entering hint mode repaints the hint overlay; re-assert the hide so
				// zellij can't re-show the cursor on that activity.
				return m, reassertHideCursor()
			}
			return m, nil
		}
		switch msg.String() {
		case "?":
			m.helpMode = true
			m.helpYOff = 0
			m.helpXOff = 0
			return m, nil
		case "q", "esc", "ctrl+c":
			// Uncommitted-draft guard (spec §E): a generated/served playbook draft that
			// has not been `w`-committed (save + cache-replace) would be LOST on quit. The
			// first quit press warns instead of exiting; a SECOND quit press confirms the
			// discard. A `w` commit in between clears the guard (the draft is persisted).
			if m.finalDraft && !m.committed && !m.quitGuard {
				dbg("quit with uncommitted draft — warning, requiring a second quit")
				m.quitGuard = true
				m.status = "uncommitted playbook — w to save, quit again to discard"
				return m, nil
			}
			return m, tea.Quit
		case "w":
			// `w` is the single finalize/commit action (spec §D/§E). Only when settled
			// (not streaming). Three branches:
			//   - a DIRTY draft (finalDraft && !committed): `w` re-persists the current
			//     doc (orchestrator.CommitPlaybook → save + cache-replace), and the result
			//     handler flips committed + clears the quit guard + shows the saved path.
			//     "finalizing…" covers the (slow) metadata round-trip. This fires after an
			//     `f` tweak; the auto-finish baseline already persisted the first cut.
			//   - an ALREADY-SAVED draft (finalDraft && committed): no-op — the doc is
			//     unchanged since the baseline/last `w`, so re-running the metadata call
			//     would be wasted work (spec §D efficiency). Just confirm "✓ already saved".
			//   - no draft (the pager holds a raw troubleshoot TRANSCRIPT): `w` generates
			//     the final-playbook draft (which then auto-persists a baseline at EOF).
			if !m.streaming {
				if m.finalDraft && m.committed {
					dbg("w: draft already saved (unchanged) — no-op")
					m.status = "✓ already saved"
					return m, nil
				}
				if m.finalDraft && !m.committed {
					dbg("w: re-persist dirty final-playbook draft")
					m.confirmResolved = false
					m.status = "finalizing…"
					return m, m.commitPlaybookCmd(m.md)
				}
				dbg("w: manual finalize → generate final-playbook draft")
				m.wrappedUp = true
				m.confirmResolved = false
				if cmd := m.beginFinalPlaybookInProc(); cmd != nil {
					return m, cmd
				}
			}
			return m, nil
		case "f":
			// Stage 5 (spec §D): `f` is the user-initiated proactive amend. It opens the
			// request-input float ("What should I change?"), the user types a free-form
			// adjustment, and the agent re-authors the DISPLAYED document in AMEND mode
			// (base=m.md — amend what's shown) → REPLACE draft. Repeatable (each `f`
			// amends the new content); `w` then commits. Only meaningful while settled
			// (not mid-stream) and only when an asker is wired (off-zellij/tests → no-op).
			if m.streaming {
				return m, nil
			}
			if m.asker == nil {
				m.status = "follow-up unavailable in this mode"
				return m, nil
			}
			// Spawn the float + poll OFF the event loop, then feed the answer back as an
			// fChangeMsg. The base is snapshotted now so a later stream can't race it.
			ask := m.asker
			base := m.md
			return m, func() tea.Msg {
				value, submitted := ask("What should I change?")
				return fChangeMsg{base: base, value: value, submitted: submitted}
			}
		case "y":
			// Confirm "Yes" (spec §A): the verify-success resolved — generate the final
			// playbook draft (REPLACE). Only meaningful while the confirm row is shown.
			if m.confirmResolved {
				if cmd := m.resolveConfirm(true); cmd != nil {
					return m, cmd
				}
				m.reflow()
			}
			return m, nil
		case "n":
			// Confirm "No": the command already succeeded, so No simply DISMISSES the
			// confirm — nothing to re-fix. The user can still quit or press `c` to bring the
			// confirm back. Only meaningful while the confirm row is shown.
			if m.confirmResolved {
				if cmd := m.resolveConfirm(false); cmd != nil {
					return m, cmd
				}
				m.reflow()
			}
			return m, nil
		case "c":
			// `c` RE-SHOWS the solution confirm (it does NOT generate blindly) so an
			// accidental keypress can't trigger generation — the user still confirms via
			// the buttons. Works whether the confirm was dismissed with No or never shown,
			// so a user who declined can bring it back. Guarded: only after a solution
			// (m.wrappedUp) and never while a stream is in flight.
			if m.wrappedUp && !m.streaming {
				m.confirmResolved = true
				m.confirmFocus = 0
				m.reflow()
				// Re-showing the confirm repaints; re-assert the hide-cursor.
				return m, reassertHideCursor()
			}
			return m, nil
		// Vertical: line
		case "down", "j":
			m.yOff++
		case "up", "k":
			m.yOff--
		// Vertical: half-page
		case "ctrl+d":
			half := m.body() / 2
			if half < 1 {
				half = 1
			}
			m.yOff += half
		case "ctrl+u":
			half := m.body() / 2
			if half < 1 {
				half = 1
			}
			m.yOff -= half
		// Vertical: full-page
		case "ctrl+f", "pgdown":
			m.yOff += m.body()
		case "ctrl+b", "pgup":
			m.yOff -= m.body()
		// Vertical: top/bottom
		case "g", "home":
			m.yOff = 0
		case "G", "end":
			m.yOff = len(m.lines)
		// Horizontal: 1-col
		case "right", "l":
			m.xOff++
		case "left", "h":
			m.xOff--
		// Horizontal: half-width jump
		case "L":
			hstep := m.contentWidth() / 2
			if hstep < 1 {
				hstep = 1
			}
			m.xOff += hstep
		case "H":
			hstep := m.contentWidth() / 2
			if hstep < 1 {
				hstep = 1
			}
			m.xOff -= hstep
		// Horizontal: home/end
		case "0", "^":
			m.xOff = 0
		case "$":
			m.xOff = MaxWideWidth(m.lines) // clampScroll will cap it
		}
		m.clampScroll()
		return m, nil
	case resultMsg:
		st := m.blockStates[msg.ID]
		prevAction := st.Action
		if dbgFile != nil {
			ids := make([]string, 0, len(m.blockStates))
			for id := range m.blockStates {
				ids = append(ids, id)
			}
			dbg("result id=%s exit=%d priorStatus=%q priorAction=%q knownBlockStates=%v", msg.ID, msg.Exit, st.Status, prevAction, ids)
		}
		st.Logpath = msg.Logpath
		st.Exit = msg.Exit
		// A result for a block the user deliberately stopped is NOT a failed fix.
		// Resolve to the neutral "stopped" state, clear the flag, and never auto-fire
		// the follow-up — regardless of the (typically 143/SIGTERM) exit code.
		if st.Stopped {
			st.Status = "stopped"
			st.Action = ""
			st.Stopped = false
			m.blockStates[msg.ID] = st
			dbg("result id=%s exit=%d STOPPED — no auto-followup", msg.ID, msg.Exit)
			m.reflow()
			return m, nil
		}
		switch {
		case msg.Exit == 0 && st.Action == "undo":
			// Successful undo: patch is no longer applied; clear status so dependents re-lock.
			st.Status = ""
			st.Action = ""
		case msg.Exit != 0 && st.Action == "undo":
			// Failed undo: patch is still applied (graceful — surface error, keep button as undo).
			st.Status = "ok"
			// keep st.Action="" so the error region shows normally
			st.Action = ""
		case msg.Exit == 0:
			// Successful apply or run.
			st.Status = "ok"
			st.Action = ""
		default:
			// Failed apply or run.
			st.Status = "failed"
			st.Action = ""
		}
		m.blockStates[msg.ID] = st
		dbg("result id=%s exit=%d action=%s status->%s", msg.ID, msg.Exit, prevAction, st.Status)
		m.reflow()
		// Auto-fire a follow-up when the VERIFY re-run fails: a non-zero exit on a
		// RUN result (not an apply/undo) for block id "verify" is the unambiguous
		// "the fix didn't work" signal. It fires on EACH verify failure — including
		// the re-armed follow-up playbook's own verify block, which reuses id=verify
		// and so flows through this same path — until the attempt cap (m.maxFollowups,
		// default 3, $AI_PLAYBOOK_MAX_FOLLOWUPS) is reached. Past the cap it stops
		// auto-firing and the manual "try another fix" button is shown on the verify
		// block instead (render.go gates that button on m.followups >= m.maxFollowups).
		//
		// NOTE: the previous once-only guard (prevStatus == "failed") meant the SECOND
		// verify failure — the re-armed playbook's verify, which leaves the block in
		// "failed" — was suppressed as "already fired", so the loop never auto-repeated.
		// The attempt counter replaces that guard.
		//
		// verifyID is the agent's {id=verify} tag; if the agent drifted and left its
		// blocks untagged (the parser then auto-names them), fall back to the LAST
		// runnable block as the verify so success/follow-up detection still works.
		verifyID := m.verifyBlockID()
		if msg.ID == verifyID && msg.Exit != 0 &&
			prevAction != "apply" && prevAction != "undo" {
			switch {
			case m.followups >= m.maxFollowups:
				// Cap reached: stop auto-firing. Mark the verify block so render.go shows
				// the manual "try another fix" button, letting the user keep going by hand.
				dbg("auto-followup SUPPRESSED: cap reached (followups=%d max=%d id=%s)", m.followups, m.maxFollowups, msg.ID)
				vst := m.blockStates[msg.ID]
				vst.FollowupExhausted = true
				m.blockStates[msg.ID] = vst
				m.reflow()
			case msg.Exit > 128:
				// Signal-killed (e.g. 143=SIGTERM, 130=SIGINT): a deliberate kill is
				// not a fix failure — do NOT auto-fire. Ordinary non-zero exits
				// (1/2/…) still escalate to a follow-up below.
				dbg("auto-followup SUPPRESSED: signal-killed exit>128 (id=%s exit=%d)", msg.ID, msg.Exit)
			case msg.Exit == 127:
				// "command not found": the verify command itself couldn't run (e.g. the
				// original command is a shell alias/function absent from the agent's
				// non-interactive shell), NOT that the fix failed — do NOT auto-fire.
				// The manual "try another fix" button still appears (unchanged).
				dbg("auto-followup SUPPRESSED: exit 127 (command not found) id=%s", msg.ID)
			case !m.canReengageInProc():
				// No way to deliver the follow-up: in-process re-engagement is not wired
				// (no orch + Reengage). The live session path (file/stdin input, Reengage
				// set) does have it and so still auto-fires below.
				dbg("auto-followup SUPPRESSED: no in-process reengage (id=%s exit=%d)", msg.ID, msg.Exit)
			default:
				m.followups++
				dbg("auto-followup fire: id=%s exit=%d attempt=%d/%d", msg.ID, msg.Exit, m.followups, m.maxFollowups)
				// Issue #1+#2: announce this AUTO follow-up in the agent's voice ABOVE the
				// new attempt and scroll once so it becomes the top visible row. Only the
				// AUTO path narrates; the manual "try another fix" button does not.
				m.announceFollowup(m.followups)
				if cmd := m.beginFollowupStream(verifyID, m.blockCommand(verifyID)); cmd != nil {
					return m, cmd
				}
			}
		}
		// Stage 2 (spec §A): a SUCCESSFUL verify (exit 0 on a RUN, not an apply/undo)
		// means the fix verified — render the NATIVE in-pager confirm row INSTEAD of the
		// old agent-ask wrap-up. The ui owns the branch: Yes generates the final-playbook
		// draft (REPLACE); No dismisses the confirm (the command already succeeded, so
		// there is nothing to re-fix — the user can quit or press `c` to bring the
		// confirm back). Gated on m.wrappedUp so it shows
		// ONCE per resolution — a re-rendered or re-run verify-0 must not re-prompt. A
		// deliberately stopped verify already returned above; exit 0 is by definition
		// neither signal-killed (>128) nor 127. Requires in-process re-engagement (the
		// live session path), so the confirm's Yes can actually generate.
		if msg.ID == verifyID && msg.Exit == 0 && !m.wrappedUp &&
			prevAction != "apply" && prevAction != "undo" &&
			m.canReengageInProc() {
			m.wrappedUp = true
			m.confirmResolved = true
			m.confirmFocus = 0 // default keyboard focus = Yes
			dbg("verify exit 0 — rendering native resolve-confirm row")
			m.reflow()
			// The confirm row appearing is a one-shot repaint; re-assert the hide so
			// zellij can't re-show the cursor on it.
			return m, reassertHideCursor()
		}
		return m, nil
	case statusMsg:
		// Transient one-line note (e.g. a deferred in-process action). Shown in the
		// status bar until the next key/click clears it. Never crashes the UI.
		dbg("status: %s", msg.text)
		m.status = msg.text
		return m, nil
	case playbookCommittedMsg:
		// The auto-finish baseline (spec §D) or a `w` re-persist completed. On success
		// flip committed=true (clears the uncommitted-draft quit guard — the baseline is
		// now the guaranteed artifact; the guard then only fires on later `f` edits) and
		// show the saved path; on failure leave committed=false so `w`/the quit-guard
		// still apply. Replaces the transient "finalizing…" status either way.
		if msg.err != nil {
			dbg("commit failed: %v", msg.err)
			m.status = "commit: " + msg.err.Error()
			return m, nil
		}
		dbg("commit ok → %s", msg.path)
		m.committed = true
		m.quitGuard = false
		m.status = "✓ saved playbook → " + msg.path
		return m, nil
	case fChangeMsg:
		// Stage 5 (spec §D): the `f` request-input float returned. On a submitted,
		// non-empty value → AMEND the displayed playbook: base = the snapshotted pager
		// content (amend what was shown), change = the typed adjustment. This drives the
		// SAME REPLACE re-arm the confirm/`w` finalize uses, marking a fresh draft
		// (finalDraft=true, committed=false) so the existing uncommitted-draft quit guard
		// and the `w` commit apply unchanged. A cancel or an empty value is a no-op.
		if !msg.submitted {
			return m, nil
		}
		if strings.TrimSpace(msg.value) == "" {
			return m, nil
		}
		dbg("f: proactive amend (base len=%d, change=%q)", len(msg.base), msg.value)
		if cmd := m.beginFinalPlaybookGenerate(msg.base, msg.value); cmd != nil {
			return m, cmd
		}
		return m, nil
	case activityMsg:
		// One agent tool-call summary off the activity feed. A summary from a STALE
		// feed (m.activity was swapped to a fresh re-engagement channel since this
		// wait was issued) is ignored — don't paint it and don't re-subscribe to the
		// dead channel. msg.ch == nil is the legacy/no-source case (always current).
		if msg.ch != nil && msg.ch != m.activity {
			return m, nil
		}
		// Channel closed (!ok): the current feed is torn down — stop re-subscribing.
		// Otherwise record the latest summary (shown under the "Working…" line ONLY
		// while thinking, so a late summary never paints over settled content) and
		// wait for the next one.
		if !msg.ok {
			m.activity = nil
			return m, nil
		}
		if m.thinking {
			// The feed now carries the model's live REASONING as well as tool
			// summaries (agentstream Reasoning/ToolActivity). Reasoning can be long or
			// multi-line, so collapse to ONE trimmed line; the render then truncates it
			// to the column width.
			m.activityLine = collapseLine(msg.summary)
		}
		return m, m.activityWaitCmd()
	case reArmStreamMsg:
		// In-process re-arm: swap the parser to the fresh re-engagement stream
		// (regenerate/followup/wrapup). The orchestrator already produced the stream
		// off the event loop; here we point the reader at it and resume streaming.
		// The stream's Closer is held so EOF reaps the agent + fires the
		// orchestrator's on-close side effects.
		dbg("re-arm (in-process): reader ready err=%v", msg.err)
		if msg.err != nil {
			m.thinking = false
			m.md += fmt.Sprintf("\n\n_re-engage error: %v_\n", msg.err)
			m.reflow()
			return m, nil
		}
		if m.reengageStream != nil {
			_ = m.reengageStream.Close()
		}
		// Reset block run-states for the fresh round: the re-authored playbook reuses
		// ids (id=fix, id=verify, …), so stale states from the prior round would
		// otherwise paint "failed"/"succeeded" onto the new, not-yet-run blocks.
		dbg("re-arm: clearing %d stale block states for the fresh round", len(m.blockStates))
		clear(m.blockStates)
		m.reengageStream = msg.reader
		m.reader = bufio.NewReader(msg.reader)
		m.parser = &streamParser{}
		cmds := []tea.Cmd{readStream(m.reader, m.parser)}
		// Swap the activity feed to the re-engagement's live reasoning + tool feed and
		// re-subscribe, so EVERY round (followup/regenerate/wrapup) shows live reasoning
		// on the activity line, exactly like the initial authoring.
		//
		// Issue #2 (live activity on repeat rounds): each re-engagement round's
		// orchestrator fan-out (orchestrator.Followup/Regenerate/Wrapup → agentstream.
		// FanOut) yields a FRESH activity channel; the ui MUST swap m.activity to it
		// and issue a fresh activityWaitCmd unconditionally. Critically this must NOT
		// be gated on the PRIOR feed's liveness: by the 2nd follow-up the 1st round's
		// channel has already drained+closed, so m.activity is nil and there is NO live
		// wait. A swap that re-subscribes only "when the previous one is alive" would
		// leave the 2nd round with a dead activity line (the reported symptom — a long
		// silent wait, then text). The fresh wait captures the just-swapped channel, so
		// a stale in-flight wait from the prior round resolves against its own (now
		// different) channel and is dropped by the activityMsg stale-guard — it can
		// never clobber this fresh subscription.
		//
		// A nil activity (text-fallback round only) leaves the previous subscription
		// untouched — there is no live feed to swap in.
		if msg.activity != nil {
			m.activity = msg.activity
			m.activityLine = ""
			cmds = append(cmds, m.activityWaitCmd())
		}
		return m, tea.Batch(cmds...)
	}
	return m, nil
}

// h1Heading matches the first markdown H1 line `# <title>` (one or more spaces or
// tabs after the hash), capturing the trimmed title text.
var h1Heading = regexp.MustCompile(`(?m)^#[ \t]+(.+?)[ \t]*$`)

// playbookHeading splits a finalized-playbook markdown body at its first H1 title.
// It returns the heading text (e.g. "Playbook — Compiling an Android Application")
// and the body from that H1 line onward, with any preamble prose ABOVE the title
// removed. When md has no H1, title is "" and body is md unchanged (a transcript,
// not a playbook — do NOT strip).
//
// Limitation: the scan is a simple first-`^# ` match and does NOT skip `#` lines
// inside fenced code blocks. A finalized playbook leads with its H1 title before
// any fence, so in practice the title is matched first; a leading fenced `# foo`
// would be a false positive, but that doesn't occur for our generated playbooks.
// loadPlaybookDocument parses a finalized/served playbook document for display:
// it strips any leading YAML front matter (frontmatter.Parse), then strips any
// preamble above the H1 (playbookHeading) from the remaining body. It returns
// the pager title (the front-matter `name` when present, else the H1 heading),
// the front-matter `description` as the subtitle (empty when there is no front
// matter / no description), and the front-matter-stripped body to render/stash.
//
// A document WITHOUT front matter degrades to the prior behavior: subtitle is
// empty and the title comes from the H1 (a transcript with no H1 keeps an empty
// title and an unchanged body).
func loadPlaybookDocument(content string) (title, subtitle, body string) {
	fm, rest, ok := frontmatter.Parse(content)
	h1, stripped := playbookHeading(rest)
	body = stripped
	if ok {
		subtitle = fm.Description
		if fm.Name != "" {
			title = fm.Name
			return title, subtitle, body
		}
	}
	title = h1
	return title, subtitle, body
}

// isValidPlaybook reports whether md is a REAL final playbook rather than a narration:
// it must carry an H1 title (playbookHeading finds one) AND at least one runnable block
// (blocks > 0, the count parsed by Render). Used to guard the final-playbook draft at
// stream-EOF and as a backstop before any commit, so a narrated non-playbook is never
// displayed in place of the troubleshoot nor saved/cached.
func isValidPlaybook(md string, blocks int) bool {
	title, _ := playbookHeading(md)
	return title != "" && blocks > 0
}

func playbookHeading(md string) (title, body string) {
	loc := h1Heading.FindStringSubmatchIndex(md)
	if loc == nil {
		return "", md
	}
	title = strings.TrimSpace(md[loc[2]:loc[3]])
	body = md[loc[0]:]
	return title, body
}

// renderBody is the document to RENDER in the scroll area. For a finalized playbook
// the H1 title is shown in the pager header (m.title), so drop that leading H1 line
// (and the blank lines after it) from the body to avoid a double title. m.md itself
// keeps the H1 — it is what gets committed/saved. No title → render m.md as-is.
func (m model) renderBody() string {
	if m.title == "" {
		return m.md
	}
	i := strings.IndexByte(m.md, '\n')
	if i < 0 {
		if h1Heading.MatchString(m.md) { // only the title, nothing below
			return ""
		}
		return m.md
	}
	if h1Heading.MatchString(m.md[:i]) {
		return strings.TrimLeft(m.md[i+1:], "\n")
	}
	return m.md // first line isn't the H1 (shouldn't happen for a finalized playbook)
}

func (m model) header() string {
	label := "ai-playbook — " + m.harness
	if m.title != "" {
		label = m.title
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(colMauve)).Bold(true).
		Render(strings.Repeat("▓", 3) + " " + label)
}

// subtitleRowString returns the styled subtitle row (the front-matter
// description) shown directly under the ▓▓▓ title for a finalized/served playbook
// that carries one, with the standard 2-col left margin and indented to align
// under the title text. It returns "" when there is no subtitle (so no extra
// header row is emitted). The text is dim (subtext) so it reads as a caption.
func (m model) subtitleRowString() string {
	if m.subtitle == "" {
		return ""
	}
	// 2-col pane margin + 4 cols (3 ▓ + 1 space) to align under the title text.
	return "  " + lipgloss.NewStyle().Foreground(lipgloss.Color(colOverlay0)).
		Render("    "+m.subtitle)
}

// subtitleRows returns the number of extra header rows the subtitle occupies: 1
// when a subtitle is present, 0 otherwise. Single source of truth for the layout
// delta the subtitle introduces (mirrors cachedRows()).
func (m *model) subtitleRows() int {
	if m.subtitle != "" {
		return 1
	}
	return 0
}

// relativeAge formats the age of cachedAt relative to now as a short string:
// "just now" (<60s), "<N>m ago" (<60m), "<N>h ago" (<24h), else "<N>d ago".
func relativeAge(cachedAt time.Time) string {
	d := time.Since(cachedAt)
	if d < 0 {
		d = 0
	}
	switch {
	case d < 60*time.Second:
		return "just now"
	case d < 60*time.Minute:
		return itoa(int(d/time.Minute)) + "m ago"
	case d < 24*time.Hour:
		return itoa(int(d/time.Hour)) + "h ago"
	default:
		return itoa(int(d/(24*time.Hour))) + "d ago"
	}
}

// cachedBadge returns the styled powerline pill string for a cached-replay
// result, followed by exactly 1 trailing space. The pill is composed of:
//
//	capL (U+E0B6, fg=colPeach, no bg) +
//	body (bg=colPeach, fg=colBase: db-icon U+F1C0, " cached · <age> ", reload-icon U+10F1DA) +
//	capR (U+E0B4, fg=colPeach, no bg) +
//	" " (trailing space)
//
// The caps use only a foreground colour (colPeach) so their background is the
// terminal's pane background, creating the classic powerline blended-end look.
// The entire body (including both icons) uses one continuous colPeach background
// to avoid the PUA-glyph background-mismatch shift-down bug.
//
// When m.flashKey == "cached:regenerate" (the pill was clicked) the WHOLE pill
// highlights: caps + body switch to the bright flash colour (colFlashOn) as the
// background with dark bold text, so the entire button lights up. The background
// stays continuous across the whole body (both glyphs included), so there's no
// per-glyph background-mismatch row-shift.
func (m model) cachedBadge() string {
	if !m.isCached {
		return ""
	}
	capFg, bodyBg, bodyFg, bold := colPeach, colPeach, colBase, false
	if m.driverPending {
		// Async startup: the reload is inert until the orchestrator lands — render the
		// whole pill muted (grey caps + grey body with overlay text) so it reads as
		// disabled. Same geometry, so it doesn't jump when it enables.
		capFg, bodyBg, bodyFg = colSurface1, colSurface1, colOverlay0
	}
	if m.flashKey == "cached:regenerate" {
		capFg, bodyBg, bodyFg, bold = colFlashOn, colFlashOn, colBase, true
	}
	capStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(capFg))
	bodyStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(bodyFg)).
		Background(lipgloss.Color(bodyBg)).
		Bold(bold)

	const reloadIcon = "\U0010F1DA"
	// The reload glyph renders only when regeneration is actually POSSIBLE (an
	// orchestrator re-engagement OR the cached-answer seam). With neither wired the
	// pill stays informational (db-icon + "cached · <age>") and shows no dead reload.
	prefix := "\U0000F1C0 cached · " + relativeAge(m.cachedAt) + " "
	bodyText := prefix
	if m.canRegenerate() {
		bodyText += reloadIcon
	} else {
		// Drop the single trailing pad space that separated the age from the glyph so
		// the pill isn't left with a dangling space when the reload is omitted.
		bodyText = strings.TrimRight(bodyText, " ")
	}
	capL := capStyle.Render("\U0000E0B6")
	body := bodyStyle.Render(bodyText)
	capR := capStyle.Render("\U0000E0B4")
	return capL + body + capR + " "
}

// appendCachedButton adds the screen-fixed regenerate button to m.buttons when
// isCached is true. The ENTIRE pill is the click target; the flash highlight
// anchors only to the reload glyph (handled in cachedBadge). Line is the pill's
// absolute screen row (bodyTop()-2 in the cached header layout). Col is 0 — the
// left cap, once buttonAt strips the 2-col left margin (the pill row's "  "
// indent IS that margin). Width is the pill's visible width minus the trailing
// space. Screen=true so buttonAt resolves it by absolute Y, not content line.
func (m *model) appendCachedButton() {
	// Only add the clickable regenerate button when regeneration is actually possible
	// (an orchestrator re-engagement OR the cached-answer seam). With neither wired the
	// reload glyph isn't rendered (see cachedBadge), so a click target would be dead.
	if !m.canRegenerate() {
		return
	}
	pillRow := m.bodyTop() - 2
	pillW := lipgloss.Width(m.cachedBadge()) - 1 // drop the trailing space
	if pillW < 1 {
		pillW = 1
	}
	m.buttons = append(m.buttons, Button{
		Line:    pillRow,
		Col:     0,
		Width:   pillW,
		Kind:    "regenerate",
		BlockID: "cached",
		Screen:  true,
	})
}

// reloadIconScreenCol returns the absolute screen column of the reload glyph in
// the cached pill: 2-col indent + left cap (1) + the pill prefix width (db icon
// + " cached · <age> "). Used to anchor the regenerate hint label above the glyph.
func (m model) reloadIconScreenCol() int {
	prefix := "\U0000F1C0 cached · " + relativeAge(m.cachedAt) + " "
	return 2 + 1 + lipgloss.Width(prefix)
}

// regenLabel returns the hint label assigned to the regenerate (cached pill)
// button in the current hint session, or "" if none is assigned.
func (m model) regenLabel() string {
	for lbl, b := range m.hintLabels {
		if b.Kind == "regenerate" {
			return lbl
		}
	}
	return ""
}

// titleLine builds the full header line string for the given available width.
// When isCached, the powerline pill is right-aligned with exactly 1 trailing
// space (last cell sits one column from the right edge). The pill is never
// dropped — the title is truncated if necessary to make room. If the budget
// for the title falls below 2 columns the pane is too narrow and the pill is
// omitted rather than overflowing.
func (m model) titleLine(_ int) string {
	return "  " + m.header()
}

// cachedBadgeRow returns the header row shown directly BELOW the title (reusing
// the top-pad row): the left-aligned powerline pill on a cached replay, else ""
// (the normal blank top-pad).
func (m model) cachedBadgeRow() string {
	if !m.isCached {
		return ""
	}
	return "  " + m.cachedBadge()
}

func bi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// helpTextDims returns the modal's visible help-text area (cols x rows) and
// whether each scrollbar is shown. The title now scrolls with the content
// (m.helpLines includes it), so the modal area (m.height-4) holds, top to
// bottom: border(1) + padTop(1) + text rows + padBottom(1) + border(1) = text+4.
// Horizontally the box is capped to width-8 — the modal is centered in the full
// pane width with a 4-col margin on each side (mirroring the vertical) — and laid
// out as border(1) + leftPad(2) + text + gap(2) + vbar(needV?1:0) + border(1):
// the bar sits flush against the right border with a 2-col gap from the text, so
// the text budget is width-14, minus one more column when the vbar is shown. The
// horizontal bar (when needH) takes one text row. All dims floored at 1.
func (m model) helpTextDims() (textW, textH int, needV, needH bool) {
	contentMaxW := MaxWideWidth(m.helpLines)
	maxRows := m.height - 8
	if maxRows < 1 {
		maxRows = 1
	}
	// Two passes resolve the interaction between the bars: reserving the hbar row
	// can tip vertical overflow, and showing the vbar narrows the text budget.
	for pass := 0; pass < 2; pass++ {
		available := maxRows - bi(needH) // rows left for text after the hbar
		if available < 1 {
			available = 1
		}
		needV = len(m.helpLines) > available
		maxTextW := m.width - 14 - bi(needV)
		if maxTextW < 1 {
			maxTextW = 1
		}
		needH = contentMaxW > maxTextW
	}
	// At a tiny pane there may be no room for the hbar row; drop it so the box
	// still fits the area (one text row beats a scrollbar that overflows it).
	if maxRows-bi(needH) < 1 {
		needH = false
	}
	// Visible dims: content-sized, capped to the available area.
	textH = maxRows - bi(needH)
	if textH > len(m.helpLines) {
		textH = len(m.helpLines)
	}
	if textH < 1 {
		textH = 1
	}
	textW = m.width - 14 - bi(needV)
	if textW > contentMaxW {
		textW = contentMaxW
	}
	if textW < 1 {
		textW = 1
	}
	return textW, textH, needV, needH
}

func (m *model) clampHelpScroll() {
	textW, textH, _, _ := m.helpTextDims()
	maxY := len(m.helpLines) - textH
	if maxY < 0 {
		maxY = 0
	}
	if m.helpYOff > maxY {
		m.helpYOff = maxY
	}
	if m.helpYOff < 0 {
		m.helpYOff = 0
	}
	maxX := MaxWideWidth(m.helpLines) - textW
	if maxX < 0 {
		maxX = 0
	}
	if m.helpXOff > maxX {
		m.helpXOff = maxX
	}
	if m.helpXOff < 0 {
		m.helpXOff = 0
	}
}

// statusBar is the slim, mode-aware bottom hint.
func (m model) statusBar() string {
	st := lipgloss.NewStyle().Foreground(lipgloss.Color(colOverlay0))
	if m.status != "" && !m.hintMode && !m.helpMode {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(colPeach)).Render(m.status)
	}
	if m.hintMode || m.helpMode {
		return st.Render("\U000F12B7: cancel")
	}
	return st.Render("\U000F1050: action • \U000F12B7: close • ?: keys")
}

// confirmPromptFresh / confirmPromptAmend are the leading prose of the native
// verify-success confirm row. The mode is selected by m.servedBase: amend wording
// ("Update the playbook with this solution?") when serving an existing playbook for
// this context (spec §C), the fresh wording otherwise (spec §A).
const (
	confirmPromptFresh = "✓ The original command now runs successfully. Generate a playbook for this solution?"
	confirmPromptAmend = "✓ The original command now runs successfully. Update the playbook with this solution?"
)

// confirmPrompt returns the active confirm prose for this model's mode: the amend
// wording when serving an existing playbook (servedBase set), else the fresh wording.
func (m model) confirmPrompt() string {
	if m.servedBase != "" {
		return confirmPromptAmend
	}
	return confirmPromptFresh
}

// confirmYesLabel / confirmNoLabel are the two button labels. No bracket chars —
// the filled background + Padding(0,2) (confirmButtonLabel) is what reads them as
// clickable controls, like the ask-tool buttons.
const (
	confirmYesLabel = "Yes"
	confirmNoLabel  = "No"
)

// confirmButtonIndent is the content column of the leftmost (Yes) confirm button on
// the buttons row — the same left edge as block content. confirmButtonGap is the
// number of spaces drawn between the Yes and No labels. Both are shared by the
// renderer (confirmButtonsRowString) and the hit-test (appendConfirmButtons) so the
// registered click columns land exactly on the drawn button cells, independent of the
// prompt width.
const (
	confirmButtonIndent = 0
	confirmButtonGap    = 4
)

// confirmButtonPad is the horizontal Padding(0, confirmButtonPad) applied to each
// confirm button (matching the ask-tool buttons in input/field_confirm.go). A button's
// drawn cell width is therefore width(label)+2*confirmButtonPad; the hit-test
// (appendConfirmButtons) registers that same padded width so clicks land on the cell.
const confirmButtonPad = 2

// confirmRowString builds the styled QUESTION block: the green confirm prompt prose
// SOFT-WRAPPED (lipgloss .Width) to the pager's content inner width (m.contentWidth() —
// the same usable width the body content uses, pane width minus the left+right margins).
// A long prompt becomes 1+ visual lines instead of overflowing the right edge; the
// returned string carries one "\n" per wrap. normalLines applies the SAME 2-col left
// indent the body uses, and the .Width fit leaves the matching trailing margin, so the
// wrapped lines line up with the body content and never run to the pane edge. The Yes/No
// buttons render on a SEPARATE row below it (confirmButtonsRowString). Rendered inside
// the pager pane (NOT a mux float). Returns "" when the confirm state is not active.
func (m model) confirmRowString() string {
	if !m.confirmResolved {
		return ""
	}
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(colGreen)).
		Width(m.contentWidth()).
		Render(m.confirmPrompt())
}

// confirmQuestionRows returns the wrapped confirm question as one styled row string per
// visual line (split on the "\n" boundaries lipgloss inserted in confirmRowString).
// normalLines emits each with the body's 2-col left indent. Returns nil when inactive.
func (m model) confirmQuestionRows() []string {
	if !m.confirmResolved {
		return nil
	}
	return strings.Split(m.confirmRowString(), "\n")
}

// confirmQuestionLines is the number of visual rows the wrapped confirm question
// occupies (the "\n" count + 1; >=1 while active, 0 otherwise). body() reserves
// confirmQuestionLines()+4 bottom rows for the whole block and normalLines emits this
// many question rows above the buttons.
func (m model) confirmQuestionLines() int {
	return len(m.confirmQuestionRows())
}

// confirmButtonsRowString builds the styled BUTTONS row: the [ Yes ] [ No ] labels,
// left-aligned at the content's left edge (confirmButtonIndent) with confirmButtonGap
// spaces between them. The focused button carries the highlight and the other is
// dimmed; a mouse-click flash wins. The fixed left-aligned positions mirror the
// click hit-test (appendConfirmButtons). Returns "" when the confirm is not active.
func (m model) confirmButtonsRowString() string {
	if !m.confirmResolved {
		return ""
	}
	yes := m.confirmButtonLabel(confirmYesLabel, "confirm-yes", colGreen, m.confirmFocus == 0)
	no := m.confirmButtonLabel(confirmNoLabel, "confirm-no", colPeach, m.confirmFocus == 1)
	return strings.Repeat(" ", confirmButtonIndent) + yes + strings.Repeat(" ", confirmButtonGap) + no
}

// confirmButtonLabel renders one confirm button as a FILLED control, matching the
// ask-tool buttons (input/field_confirm.go `button()`): lipgloss.Padding(0, 2) with a
// background. A mouse-click flash always wins (bright/bold on colFlashOn). Otherwise the
// FOCUSED button (focused=true, issue #4) carries a GREEN background (colGreen) with a
// dark foreground (colBase) + bold so it reads as the selected control; the unfocused
// button is a muted filled button (colSurface1 bg / colSubtext fg) so both read as
// buttons with the focused one highlighted green. The accent arg is retained for the
// call-site/test signature; the focused highlight is always green per the design.
func (m model) confirmButtonLabel(label, kind, accent string, focused bool) string {
	st := lipgloss.NewStyle().Padding(0, confirmButtonPad)
	if m.flashKey == "confirm:"+kind {
		return st.Foreground(lipgloss.Color(colFlashOn)).Bold(true).Render(label)
	}
	if focused {
		return st.
			Foreground(lipgloss.Color(colBase)).
			Background(lipgloss.Color(colGreen)).
			Bold(true).Render(label)
	}
	return st.
		Foreground(lipgloss.Color(colSubtext)).
		Background(lipgloss.Color(colSurface1)).
		Render(label)
}

// confirmButtonsScreenRow returns the absolute screen row the confirm BUTTONS row
// occupies. The confirm block is questionLines+4 rows above the status bar: a blank, the
// wrapped question (N lines, m.height-4-N .. m.height-5), a blank (m.height-4), the
// buttons (m.height-3), a blank (m.height-2), then the status bar (m.height-1). The
// buttons stay PINNED on m.height-3 regardless of how many lines the question wraps to,
// so the hit-test below matches the drawn cells. -1 when the confirm is not shown.
func (m model) confirmButtonsScreenRow() int {
	if !m.confirmResolved {
		return -1
	}
	// The block bottom is fixed: blank (m.height-4), buttons (m.height-3), blank
	// (m.height-2), status (m.height-1). The question's N lines wrap ABOVE the blank at
	// m.height-4, so the buttons row is always m.height-3.
	return m.height - 3
}

// appendConfirmButtons registers the two Screen-fixed confirm buttons (Yes/No) on the
// BUTTONS row so a mouse click resolves them. The buttons are left-aligned at the
// content edge (confirmButtonIndent), No after Yes by confirmButtonGap — the SAME
// constants the renderer (confirmButtonsRowString) draws with, so the click columns
// land exactly on the drawn cells regardless of the prompt's length.
func (m *model) appendConfirmButtons() {
	if !m.confirmResolved {
		return
	}
	row := m.confirmButtonsScreenRow()
	if row < 0 {
		return
	}
	// Col is the content column (buttonAt strips the 2-col left margin). Each button is
	// drawn as a FILLED cell whose width includes the Padding(0, confirmButtonPad) on
	// both sides — so the clickable cell width is width(label)+2*confirmButtonPad. No
	// starts after the Yes cell plus the shared gap, exactly as confirmButtonsRowString
	// lays them out, keeping render + hit-test in lockstep regardless of prompt width.
	yesCellW := lipgloss.Width(confirmYesLabel) + 2*confirmButtonPad
	noCellW := lipgloss.Width(confirmNoLabel) + 2*confirmButtonPad
	yesCol := confirmButtonIndent
	noCol := yesCol + yesCellW + confirmButtonGap
	m.buttons = append(m.buttons,
		Button{Line: row, Col: yesCol, Width: yesCellW, Kind: "confirm-yes", BlockID: "confirm", Screen: true},
		Button{Line: row, Col: noCol, Width: noCellW, Kind: "confirm-no", BlockID: "confirm", Screen: true},
	)
}

func helpInnerH(m model) int { _, h, _, _ := m.helpTextDims(); return h }
func helpInnerW(m model) int { w, _, _, _ := m.helpTextDims(); return w }
func helpHalf(m model) int {
	if h := helpInnerH(m) / 2; h > 1 {
		return h
	}
	return 1
}
func helpPage(m model) int {
	if h := helpInnerH(m); h > 1 {
		return h
	}
	return 1
}
func helpHalfW(m model) int {
	if w := helpInnerW(m) / 2; w > 1 {
		return w
	}
	return 1
}

// mantleBg is the ANSI truecolor background sequence for colMantle, used to
// band each interior row so the modal background is uniform throughout.
const mantleBg = "\x1b[48;2;24;24;37m" // #181825 = R24 G24 B37

// helpModal builds the bordered keybinding box (content-sized, capped to width-8
// wide × (m.height-4) tall by helpTextDims). It is NOT placed: the View overlays
// it onto the live document view so the markdown keeps rendering behind it.
func (m model) helpModal() string {
	textW, textH, needV, needH := m.helpTextDims()
	contentW := MaxWideWidth(m.helpLines)

	// All padding is applied manually (the box uses Padding(0,0)) so both
	// scrollbars run flush to their borders. Each row is leftPad(2) + text +
	// gap(2) + vbar(1 when needV). Rows top to bottom: top pad, text rows, bottom
	// pad, then the hbar (when needH) flush against the bottom border with the
	// bottom pad as its gap above. The vbar occupies the rightmost column on every
	// row, so it runs from the top border to the bottom border.
	windowed := Window(m.helpLines, m.helpXOff, m.helpYOff, textW, textH)
	// The vbar track spans top pad + text rows + bottom pad (NOT the hbar row), so
	// when both bars show the vbar ends one cell above the hbar — they don't
	// collide at the corner. With only the vbar, this is every inner row, so it
	// still runs flush from the top border to the bottom border.
	trackH := textH + 2
	vpos, vsize := thumbTrack(len(m.helpLines), textH, trackH, m.helpYOff)
	vbar := func(trackRow int) string {
		if !needV {
			return ""
		}
		glyph, col := "│", colSurface0
		if trackRow >= vpos && trackRow < vpos+vsize {
			glyph, col = "┃", colOverlay1
		}
		return lipgloss.NewStyle().Foreground(lipgloss.Color(col)).Render(glyph)
	}
	// band re-injects the modal bg after every inner color reset so plain gaps and
	// reset segments keep the modal background instead of the terminal's.
	blank := strings.Repeat(" ", textW)
	row := func(text string, trackRow int) string {
		return band("  "+text+"  "+vbar(trackRow), mantleBg, 0)
	}
	var body []string
	tr := 0
	body = append(body, row(blank, tr)) // top pad row
	tr++
	for _, w := range windowed {
		body = append(body, row(padTo(w, textW), tr))
		tr++
	}
	body = append(body, row(blank, tr)) // bottom pad (gap above the hbar; vbar runs through it)
	if needH {
		// Horizontal bar: a row flush to the bottom border, spanning the full inner
		// width. hscrollbarRow always renders 1 leading + 1 trailing space, so the
		// bar floats just inside the left/right borders regardless of the vbar. When
		// the vbar is shown, the trailing space lands in the vbar column (which the
		// vbar vacates on this row), so the two bars never collide at the corner.
		body = append(body, band(hscrollbarRow(contentW, m.helpXOff, textW+4+bi(needV), colMantle), mantleBg, 0))
	}

	content := strings.Join(body, "\n")

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(colSurface1)).
		BorderBackground(lipgloss.Color(colMantle)).
		Background(lipgloss.Color(colMantle)).
		Padding(0, 0).
		Render(content)
}

// viewString assembles the full rendered frame as a plain string. View wraps
// this in tea.NewView so that tests can call viewString() directly without
// needing to extract Content from a tea.View.
func (m model) viewString() string {
	cw := m.contentWidth()
	var sb strings.Builder

	if m.askMode {
		// The ask overlay composites the dialog centered over the live document,
		// exactly like the help modal — the playbook keeps rendering behind it.
		sb.WriteString(m.askOverlay())
		return sb.String()
	}

	if m.hintMode {
		// Labels float on the line above each button (or below when the line
		// above is scrolled off the top). Screen-fixed buttons (e.g. the cached
		// pill reload icon) are skipped here — they live in the header, not the
		// scrollable body; their label is floated on the blank line above the pill
		// in the cached-header block below (anchored to the reload-icon column).
		labelsByRow := map[int]map[int]string{}
		for label, b := range m.hintLabels {
			if b.Screen {
				continue // handled separately in the header region
			}
			row := b.Line - 1
			if row < m.yOff {
				row = b.Line + 1
			}
			if labelsByRow[row] == nil {
				labelsByRow[row] = map[int]string{}
			}
			labelsByRow[row][b.Col] = label
		}
		dim := lipgloss.NewStyle().Foreground(lipgloss.Color(colOverlay0))
		lab := lipgloss.NewStyle().Bold(true).
			Foreground(lipgloss.Color(colHintLabelFg)).
			Background(lipgloss.Color(colHintLabelBg))

		// Button glyph columns per tab line — given the hint-label dark-red bg.
		// Only body buttons (not Screen-fixed) are tracked for code-row highlighting.
		buttonColsByRow := map[int]map[int]bool{}
		for _, b := range m.buttons {
			if b.Screen {
				continue
			}
			if buttonColsByRow[b.Line] == nil {
				buttonColsByRow[b.Line] = map[int]bool{}
			}
			buttonColsByRow[b.Line][b.Col] = true
		}

		rows := Window(m.lines, m.xOff, m.yOff, cw, m.body())
		pos, size := vthumb(len(m.lines), m.body(), m.yOff)
		sb.WriteString("\n")
		sb.WriteString(m.titleLine(m.width) + "\n")
		if m.subtitle != "" {
			sb.WriteString(m.subtitleRowString() + "\n") // description caption under the title
		}
		if m.isCached {
			// Float the regenerate button's hint label on the blank line above the
			// pill, anchored to the reload-icon column (the flash anchor) — mirroring
			// how body buttons float their label on the line above the glyph.
			above := padTo("", m.width)
			if lbl := m.regenLabel(); lbl != "" {
				above = spliceOver(above, lab.Render(lbl), m.reloadIconScreenCol())
			}
			sb.WriteString(above + "\n")              // blank above pill (+ hint label)
			sb.WriteString(m.cachedBadgeRow() + "\n") // cached pill (left-aligned)
			sb.WriteString("\n")                      // blank below pill
		} else {
			sb.WriteString("\n") // top-pad (single blank)
		}
		for i, row := range rows {
			idx := m.yOff + i
			var base string
			if idx >= 0 && idx < len(m.lines) && m.lines[idx].Code {
				base = hintCodeRow(row, cw, buttonColsByRow[idx]) // fill + dark-red button cells
			} else {
				base = dim.Render(padTo(strip(row), cw))
			}
			base = overlayLabels(base, labelsByRow[idx], lab)
			sb.WriteString("  " + base + vscrollCell(i, pos, size) + "\n")
		}
		sb.WriteString("\n")
		sb.WriteString("  " + m.statusBar())
	} else if m.helpMode {
		// The modal is an overlay: render the live document, then composite the
		// keybinding box over it (centered), so the markdown keeps showing and
		// updating behind the modal while help is open.
		base := m.normalLines()
		box := strings.Split(m.helpModal(), "\n")
		boxH := len(box)
		boxW := 0
		if boxH > 0 {
			boxW = lipgloss.Width(box[0])
		}
		left := (m.width - boxW) / 2
		if left < 0 {
			left = 0
		}
		top := 2 + (m.height-4-boxH)/2 // centered in the body region (below the 2 top rows)
		if top < 2 {
			top = 2
		}
		for i, bl := range box {
			if r := top + i; r >= 0 && r < len(base) {
				base[r] = spliceOver(base[r], bl, left)
			}
		}
		sb.WriteString(strings.Join(base, "\n"))
	} else {
		sb.WriteString(strings.Join(m.normalLines(), "\n"))
	}

	return sb.String()
}

// normalLines renders the standard document view as m.height lines, each padded
// to the full pane width. It is the base layer both for normal mode and for the
// help overlay (which composites the modal box over these lines).
func (m model) normalLines() []string {
	cw := m.contentWidth()
	rows := Window(m.lines, m.xOff, m.yOff, cw, m.body())
	pos, size := vthumb(len(m.lines), m.body(), m.yOff)
	pad := func(s string) string { return padTo(s, m.width) }
	out := make([]string, 0, m.height)
	out = append(out, pad(""))                   // leading blank
	out = append(out, pad(m.titleLine(m.width))) // title
	if m.subtitle != "" {
		out = append(out, pad(m.subtitleRowString())) // description caption under the title
	}
	if m.isCached {
		out = append(out, pad(""))                 // blank above pill
		out = append(out, pad(m.cachedBadgeRow())) // cached pill (left-aligned)
		out = append(out, pad(""))                 // blank below pill
	} else {
		out = append(out, pad("")) // top-pad (single blank)
	}
	spinRow := -1
	actRow := -1
	if m.thinking {
		// Spinner sits just below the last real content line visible from the top
		// of the body (or the first body row when empty), within the body region.
		spinRow = len(m.lines) - m.yOff
		if spinRow < 0 {
			spinRow = 0
		}
		// Issue #2: when there's content above the spinner (the natural row > 0, e.g.
		// the follow-up "_That didn't work…_" phrase), leave ONE blank body row between
		// that content and the "Working…" line so the spinner reads as a fresh section,
		// not glued to the prose. At the very top (initial authoring / empty doc) keep
		// the spinner on row 0 with no leading gap.
		if spinRow > 0 {
			spinRow++
		}
		if spinRow > m.body()-1 {
			spinRow = m.body() - 1
		}
		// The live agent-activity line (when any) sits on the row directly below the
		// spinner, as long as there's room in the body. claude --print is silent for
		// minutes during its tool-use phase, so this row shows the agent's latest tool
		// call (e.g. "⟳ run: gg build") next to the animating spinner.
		if m.activityLine != "" && spinRow+1 <= m.body()-1 {
			actRow = spinRow + 1
		}
	}
	for i := 0; i < m.body(); i++ {
		if i == spinRow {
			// Issue #3: use the dynamic working-progression label (workingLabel),
			// computed from the elapsed wait (spinTicks/10 seconds), INSTEAD of the
			// static m.thinkLabel. spinTicks resets per thinking session, so each
			// authoring/follow-up wait restarts at "Working…" and escalates on a 15s
			// cadence, holding the tail. The progression is the desired behavior even
			// when a non-default --thinking-label is configured — for the live wait we
			// intentionally prefer the escalating reassurance over a static custom label.
			elapsed := m.spinTicks / 10
			out = append(out, pad("  "+padTo(spinnerLine(m.spinFrame, workingLabel(elapsed), elapsed), cw)+vscrollCell(spinRow, pos, size)))
			continue
		}
		if i == actRow {
			out = append(out, pad("  "+padTo(activityLineStr(m.activityLine, cw), cw)+vscrollCell(actRow, pos, size)))
			continue
		}
		if i < len(rows) {
			row := rows[i]
			idx := m.yOff + i
			if idx >= 0 && idx < len(m.lines) && m.lines[idx].HBar > 0 {
				row = hscrollbarRow(m.lines[idx].HBar, m.xOff, cw, colCodeBg)
			}
			out = append(out, pad("  "+padTo(row, cw)+vscrollCell(i, pos, size)))
		} else {
			out = append(out, pad(""))
		}
	}
	// The confirm (when shown) occupies the questionLines+4 bottom rows directly above the
	// status bar (spec §A: inline rows in the pane, not a mux float): a blank, the wrapped
	// question (N lines, each with the body's 2-col left indent so it stays inside the
	// pane), a blank, the Yes / No buttons on their own row, then a blank — so the block
	// reads with breathing room and the buttons stay pinned at m.height-3. body() reserves
	// these rows so the confirm never overlaps real content. Otherwise a single bottom pad.
	if m.confirmResolved {
		out = append(out, pad("")) // blank above the question
		for _, q := range m.confirmQuestionRows() {
			out = append(out, pad("  "+q)) // wrapped question line(s)
		}
		out = append(out, pad(""))                               // blank   (m.height-4)
		out = append(out, pad("  "+m.confirmButtonsRowString())) // buttons (m.height-3)
		out = append(out, pad(""))                               // blank   (m.height-2)
	} else {
		out = append(out, pad("")) // bottom pad
	}
	out = append(out, pad("  "+m.statusBar())) // status bar
	return out
}

// markReviewing sets the given block's status to "reviewing". Called by the
// review-diff action trigger so the block body shows a "Reviewing…" indicator
// immediately, without waiting for a resultMsg.
func (m model) markReviewing(id string) model {
	st := m.blockStates[id]
	st.Status = "reviewing"
	m.blockStates[id] = st
	return m
}

// markRunning sets the given block's status to "running" and resets its
// SpinFrame to 0. Called by the action-trigger paths before emitAction so the
// spinner appears immediately, without waiting for a resultMsg.
func (m model) markRunning(id string) model {
	st := m.blockStates[id]
	st.Status = "running"
	st.SpinFrame = 0
	m.blockStates[id] = st
	return m
}

// markStopped records that the user deliberately stopped (killed) a running
// block. The flag is consumed by the resultMsg handler: a result arriving for a
// Stopped block resolves to Status "stopped" (not "failed") and suppresses the
// auto-followup. A deliberate stop is not a failed fix.
func (m model) markStopped(id string) {
	st := m.blockStates[id]
	st.Stopped = true
	m.blockStates[id] = st
}

// blockCommand returns the raw fenced command text (Block.Payload) of the block
// with the given id, or "" if no such block is currently rendered.
func (m model) blockCommand(id string) string {
	for _, b := range m.blocks {
		if b.ID == id {
			return b.Payload
		}
	}
	return ""
}

// followupAnnouncements are the agent-voice narration lines inserted above each
// AUTO follow-up attempt (issue #1). They vary by attempt number so successive
// rounds don't read identically — index = (attempt-1), clamped to the last entry
// for any round at/beyond the list length (e.g. a higher $AI_PLAYBOOK_MAX_FOLLOWUPS).
// Rendered as a dim/italic markdown paragraph so it reads as narration, separate
// from playbook content. Tweak the phrasing here.
var followupAnnouncements = []string{
	"That didn't work — let me try a different approach.",
	"Still not resolved. Let me try another angle.",
	"Hmm, that didn't do it either. One more idea.",
}

// followupAnnouncement returns the agent-voice narration for the given auto
// follow-up attempt (1-based: the value of m.followups after it was incremented
// for this fire). It clamps to the last phrase for attempts beyond the list.
func followupAnnouncement(attempt int) string {
	i := attempt - 1
	if i < 0 {
		i = 0
	}
	if i >= len(followupAnnouncements) {
		i = len(followupAnnouncements) - 1
	}
	return followupAnnouncements[i]
}

// verifyBlockID returns the id the runner treats as the "verify" step: the agent's
// {id=verify} tag when present, else (the agent drifted and left blocks untagged,
// so the parser auto-named them) the LAST runnable block — which by the literate-
// playbook convention IS the verification step. This keeps the verify-success →
// "did this solve it?" confirmation and the verify-fail → follow-up working even
// when the agent doesn't emit the exact {id=verify} tag.
func (m model) verifyBlockID() string {
	last, count, hasVerify := "", 0, false
	for _, b := range m.blocks {
		if b.ID == "verify" {
			hasVerify = true
		}
		if (b.Type == "shell" || b.Type == "run") && !b.Static {
			last = b.ID
			count++
		}
	}
	// The explicit {id=verify} tag always wins. Otherwise only treat the LAST
	// runnable block as the verify when there are ≥2 runnable blocks — that's the
	// fix-then-verify shape, so the last one is the verification step. With 0 or 1
	// runnable blocks there is no implicit verify (a lone fix block's failure must
	// show the manual follow-up button, not auto-fire), so keep the conventional id.
	if hasVerify || count < 2 {
		return "verify"
	}
	return last
}

// announceFollowup inserts the agent-voice narration line for an AUTO follow-up
// (issue #1) into the rendered doc ABOVE the new attempt, then scrolls the
// viewport ONCE so that line becomes the first visible body row (issue #2),
// giving each new attempt a clean "fresh start" frame. attempt is the 1-based
// auto-follow-up count (m.followups after increment). It reflows so the line
// index is accurate, sets m.yOff to the announcement's starting line (clamped),
// and leaves follow=false so subsequent streamed content does not scroll.
func (m *model) announceFollowup(attempt int) {
	// The announcement begins on the line just after the current rendered content.
	// Reflow first so len(m.lines) reflects exactly what's on screen now; that count
	// is the announcement's starting body-line index after the append + reflow.
	m.reflow()
	startLine := len(m.lines)
	// Separator ABOVE the phrase, so the rule frames the TOP of the new attempt:
	// ──────  /  _That didn't work — let me try…_  /  <new instructions>. The
	// following beginFollowupInProc must then NOT add its own `---` (justAnnounced).
	m.md += "\n\n---\n\n_" + followupAnnouncement(attempt) + "_\n\n"
	m.justAnnounced = true
	m.reflow()
	// One-time scroll: make the `---` SEPARATOR the FIRST visible body row. Pin it so
	// clampScroll permits the over-scroll (blank below) — otherwise the announcement,
	// being the last content, gets pulled back to the bottom and the "fresh start"
	// framing is lost. The pin self-neutralizes once the new attempt fills the body.
	//
	// The appended block is "\n\n---\n\n_…_\n\n": the leading "\n\n" closes the prior
	// content's line and adds ONE blank body line at startLine, with the `---` rule on
	// startLine+1. Pin to startLine+1 so the rule (not that leading blank) is the top
	// visible row — the user confirmed the previous startLine pin sat one line too low.
	pin := startLine + 1
	m.pinTop = pin
	m.yOff = pin
	m.follow = false // subsequent streamed content must NOT scroll
	m.clampScroll()
}

// resolveConfirm answers the native verify-success confirm: yes → generate the
// final-playbook draft (REPLACE); no → just DISMISS the confirm and do nothing (the
// command already succeeded, so there is nothing to re-fix). After a No the user can
// still quit or press `c` to bring the confirm back. It clears the confirm state
// and returns the trigger cmd (nil for No, or when re-engagement is unwired).
func (m *model) resolveConfirm(yes bool) tea.Cmd {
	if !m.confirmResolved {
		return nil
	}
	m.confirmResolved = false
	if yes {
		return m.beginFinalPlaybookInProc()
	}
	return nil
}

// canReengageInProc reports whether in-process re-engagement is wired (an
// orchestrator with a Reengage context). When true, beginFollowupStream re-arms
// the parser with the agent's revised-fix stream directly. This is the live
// session path (file/stdin input, Reengage set).
func (m *model) canReengageInProc() bool {
	return m.orch != nil && m.orch.Reengage != nil
}

// canRegenerate reports whether the cached pill's reload can actually do something —
// i.e. a regenerate mechanism is wired:
//   - the orchestrator's in-process re-engagement (playbook regenerate), OR
//   - the cached-answer seam (answerRegen, the prose re-classify).
//
// The badge only renders the clickable button + reload glyph when this is true, so a
// wired reload is always live and a dead reload (e.g. the pre-fix answer pane: cached
// but no regenerate path) is hidden — the defense that kills the no-op reload. isCached
// is the outer gate (a non-cached result has no badge at all).
func (m model) canRegenerate() bool {
	if !m.isCached {
		return false
	}
	// Async startup: the orchestrator is still opening. Show the reload pill NOW (it
	// renders dimmed + inert via driverPending) so it doesn't pop in later — it goes
	// live once orchReadyMsg installs the orchestrator.
	if m.driverPending {
		return true
	}
	return m.orch != nil && m.orch.Reengage != nil ||
		m.answerRegen != nil
}

func (m *model) beginFollowupStream(blockID, command string) tea.Cmd {
	dbg("emit %s id=%s", "followup", blockID)
	// In-process: re-engage the agent via the orchestrator and re-arm the parser
	// with the revised-fix stream (APPEND). The failed command's output is read
	// from the block's run logfile (capped, like the shell's tail -c 4000).
	if m.orch != nil && m.orch.Reengage != nil {
		failedOut := m.failedOutput(blockID)
		if cmd := m.beginFollowupInProc(failedOut); cmd != nil {
			return cmd
		}
	}
	// No in-process re-engagement wired (standalone/sample, or no Reengage): nothing
	// to deliver the follow-up to — no-op.
	return nil
}

// followupCap bounds the failed-command output fed to the follow-up prompt,
// mirroring ai-assist-followup's `tail -c 4000`.
const followupCap = 4000

// failedOutput reads the captured output of the failed block (its run logfile,
// written by writeRunLog) and returns the LAST followupCap bytes — the same cap
// the shell applied. Empty when there is no logfile / it can't be read.
func (m model) failedOutput(blockID string) string {
	st, ok := m.blockStates[blockID]
	if !ok || st.Logpath == "" {
		return ""
	}
	b, err := os.ReadFile(st.Logpath)
	if err != nil {
		return ""
	}
	if len(b) > followupCap {
		b = b[len(b)-followupCap:]
	}
	return string(b)
}

// handleToggle flips the Expanded state of the given block and reflows.
// Toggle is pager-local: it never calls emitAction.
func (m model) handleToggle(id string) model {
	st := m.blockStates[id]
	st.Expanded = !st.Expanded
	m.blockStates[id] = st
	m.reflow()
	return m
}

func (m model) View() tea.View {
	v := tea.NewView(m.viewString())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	// Issue #5 (cont.): receive tea.FocusMsg so we can re-assert the hide-cursor
	// when the pager regains focus (e.g. after the thinking float closes); some
	// terminals re-show the cursor on focus.
	v.ReportFocus = true
	// Issue #5: hide the hardware cursor in the pager. In bubbletea v2 the cursor is
	// shown ONLY when the View carries a non-nil Cursor (the cursed_renderer derives
	// showCursor := view.Cursor != nil and emits the hide-cursor sequence otherwise).
	// We render no editable field, so leaving Cursor nil keeps the blinking terminal
	// cursor hidden while scrolling. Set explicitly to document the intent.
	v.Cursor = nil
	return v
}

// staticRender returns the full rendered content (no scroll chrome) for
// printing to the pane on exit, so the docked pane parks showing the reply.
// Content is wrapped at contentWidth and left-padded with 2 spaces to match
// the interactive View().
func (m model) staticRender() string {
	cw := m.contentWidth()
	lines, _, _ := Render(m.renderBody(), cw, m.blockStates, "")
	var sb strings.Builder
	sb.WriteString(m.titleLine(m.width) + "\n")
	if m.subtitle != "" {
		sb.WriteString(m.subtitleRowString() + "\n") // description caption under the title
	}
	if m.isCached {
		sb.WriteString("\n")                      // blank above pill
		sb.WriteString(m.cachedBadgeRow() + "\n") // cached pill (left-aligned)
		sb.WriteString("\n")                      // blank below pill
	} else {
		sb.WriteString("\n") // top-pad (single blank)
	}
	for _, l := range lines {
		sb.WriteString("  " + l.Text + "\n")
	}
	return sb.String()
}
