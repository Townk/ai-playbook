package ui

import (
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Townk/ai-playbook/internal/askbridge"
	idiff "github.com/Townk/ai-playbook/internal/diff"
	"github.com/Townk/ai-playbook/internal/frontmatter"
	"github.com/Townk/ai-playbook/internal/input"
	"github.com/Townk/ai-playbook/internal/orchestrator"
	"github.com/Townk/ai-playbook/internal/reengage"
)

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
	// maxWide caches MaxWideWidth(m.lines) — the widest Wide line's display width —
	// computed once in reflow() where m.lines is assigned. clampScroll and the
	// horizontal home/end ($) handler read it instead of re-walking every rendered
	// line (an ANSI-aware width scan) on every keypress/wheel/reflow.
	maxWide    int
	hintMode   bool
	hintLabels map[string]Button
	helpMode   bool
	helpLines  []Line
	helpYOff   int
	helpXOff   int

	// no-mux in-viewer diff overlay: when diffMode is true the pager overlays a
	// bordered scrollable side-by-side diff box (rendered by internal/diff) over the
	// live document. Only raised on the no-mux path (m.asker == nil); mux-on keeps
	// the existing emitAction→float path. Closed by q/esc.
	diffMode  bool
	diffFiles []idiff.FileDiff // parsed patch; kept so the narrow overlay can render unified
	diffRows  []idiff.Row      // structured side-by-side rows, rendered (windowed) per frame
	diffYOff  int
	diffXOff  int
	// diff-overlay geometry cache: derived quantities that depend only on diffRows /
	// diffFiles and the terminal width, so they change at exactly two events — the
	// overlay opening (activateDiffButton) and a resize (WindowSizeMsg). Both call
	// recomputeDiffGeometry, which repopulates these and sets diffGeomValid. Without
	// it, narrow mode re-rendered the whole patch through chroma up to three times
	// per keypress and wide mode re-walked every row per frame. When diffGeomValid is
	// false (e.g. a test that assigns diffRows directly, never opening the overlay)
	// the accessors fall back to a live computation, so behavior is identical.
	diffGeomValid   bool
	diffUnifiedC    []string // cached unified lines (narrow mode)
	diffUnifiedMaxW int      // widest unified line (narrow horizontal max)
	diffGutterC     int      // cached gutter width (wide mode)
	diffTextColC    int      // cached per-pane text column (wide mode)
	diffPaneLeftC   int      // cached left-pane max horizontal offset (wide mode)
	diffPaneRightC  int      // cached right-pane max horizontal offset (wide mode)
	diffLangsC      []string // cached per-row highlight language (wide mode)

	// no-mux ask overlay: when askBridge is set, a tea.Cmd drains pending agent
	// asks (recvAskCmd); askMode raises the embedded ask dialog over the document
	// (the help-modal compositing mechanism) and routes keys to it; askReq is the
	// pending request answered on submit/cancel. nil bridge (mux path / tests) →
	// the overlay is never raised.
	askBridge *askbridge.Bridge
	askMode   bool
	ask       *input.Ask
	askReq    askbridge.Request
	// askCompletion, when set, fires when a VIEWER-initiated overlay (not a bridge
	// agent ask) completes: handleAskKey calls it instead of askReq.Respond, and the
	// returned msg becomes the update result. The no-mux `refine` (f) path sets it to
	// route the typed refinement into an fChangeMsg amend. nil → the bridge path.
	askCompletion func(value string, submitted bool) tea.Msg

	// refusals is the session-lifetime list of user-rejected approaches (spec
	// refuse-solution §1). Every submitted refine note is appended verbatim (trimmed,
	// non-empty only) and injected as a Constraints section into EVERY subsequent
	// re-engagement prompt (regenerate/followup/final-playbook/drift-regen) so a rejected
	// approach cannot resurface. In-memory only — constraints die with the session.
	refusals []string

	// streaming + thinking
	thinking      bool
	thinkLabel    string
	defaultLabel  string
	progress      ProgressWidget // spinner frame, elapsed ticks, and activity summary
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

	// reeng is the AI-layer re-engagement engine (ADR-0009 step 2): the second handle
	// the model holds beside the executor. It owns regenerate / followup /
	// finalplaybook / drift-regenerate / commit. nil means no re-engagement context is
	// wired (a standalone/sample viewer), so those affordances degrade to inert no-ops.
	// Set alongside orch by Main / RunStream, or delivered later via orchReadyMsg.
	reeng *reengage.Engine

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
	// (set by Run from Options.Ready). Init subscribes to it
	// via a tea.Cmd that reads the single OrchReady → orchReadyMsg. nil on the sync
	// path (the orchestrator was built before the program started).
	readyCh <-chan OrchReady

	// answerRegen is the cached-ANSWER regenerate seam (set by Run from
	// Options.AnswerRegen). When non-nil, the cached pill's reload re-runs the cheap
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

	// followups counts how many auto-follow-ups have fired this session. The
	// verify-fail auto-fire repeats on EACH failure while followups < maxFollowups;
	// past the cap it falls back to the manual "try another fix" button.
	followups    int
	maxFollowups int

	// hadFollowup is true after a follow-up (auto or manual) launches — the run
	// diverged from the proposed playbook. Reset when the playbook is re-authored.
	hadFollowup bool

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
	// keys. Empty for a FRESH troubleshoot (authorPlaybook / cache MISS). Set by Run
	// from Options.ServedBase, threaded from serveCachedPlaybook.
	servedBase string

	// finalDraft marks that the rendered playbook is a GENERATED final-playbook draft
	// (the confirm "Yes" / `f` / `w`-on-transcript produced it). committed flips true
	// once it is persisted (save + cache-replace via reengage.Engine.CommitPlaybook) —
	// either by the auto-finish baseline (spec §D) or a `w` re-persist.
	finalDraft bool
	committed  bool
	// reauthored is true when the finalDraft was produced by a deliberate re-author
	// (beginFinalPlaybookGenerate) rather than arriving as an unrun proposal. Used by
	// wFinalize to skip the "save unverified" confirm gate: a re-authored draft already
	// incorporates the run's outcome; an unrun proposal (reauthored=false) needs the gate.
	reauthored bool

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
	// by Run/RunStream from Options.Asker / StreamOptions.Asker.
	asker AskFunc

	// structured marks that this stream carries the agent's narration, NOT the
	// playbook (the playbook arrives via submit_playbook → OnPlaybook → bodyProvider).
	// When true, textEvents are drained (not accumulated as m.md) and on stream EOF
	// m.md is set from bodyProvider() so the existing finalDraft processing runs on
	// the captured rendered playbook. Set by RunStream from StreamOptions.Structured.
	structured bool
	// bodyProvider, when non-nil, returns the captured rendered playbook at stream EOF
	// in structured mode. Set by RunStream from StreamOptions.Body.
	bodyProvider func() string

	// B2b pre-run variable confirmation
	confirmEnv  map[string]frontmatter.EnvValue // declared env (front matter); nil/empty → no gate
	projectRoot string                          // heuristic root (the PROJECT_ROOT value)
	sourcePath  string                          // on-disk .md path (non-empty → file-backed; enables [edit])
	sourceMtime time.Time                       // mtime of sourcePath at last read; used by the mux poll to detect saves
	polling     bool                            // a mtime-poll loop is already live; guards against N concurrent [edit] clicks
	// driftEditPath/driftEditMtime back the MUX "resolve manually" (F21) poll: while
	// driftEditPath is set the shared source-poll loop ALSO watches that target file
	// and re-checks drift when it is saved (mirrors sourcePath/sourceMtime for [edit]).
	driftEditPath  string
	driftEditMtime time.Time
	// driftTempPath/driftTempTarget back the conflict-marked "resolve manually" flow:
	// instead of editing the raw target, we write a conflict-marked COPY to a temp file
	// (driftTempPath) and, on save, read it back, reconcile it into the real target
	// (driftTempTarget), and re-check drift. Empty driftTempPath ⇒ the raw-file fallback
	// is in effect (ConflictMarkup couldn't locate the hunk) and driftEditPath is used.
	driftTempPath    string
	driftTempTarget  string
	driftTempBlockID string // the diff block being resolved (so driftResolveFinish can flag it)
	// driftResolveBackup maps a manually-resolved diff block's ID → the target file's
	// content from just BEFORE the resolve, so the "resolved manually" block's Undo can
	// restore it (reverting to the drifted state — there is no git patch to reverse).
	driftResolveBackup map[string]string
	// autoRollback (set from the --auto-rollback run flag) makes a step failure auto-fire
	// the rollback chain instead of only showing the manual "Rollback playbook" button.
	autoRollback bool
	// assisted (set from the --assisted run flag / Options.Assisted) opts into the
	// GUIDED-fullscreen run mode; it rides the same viewer path as the default
	// run — the assisted behavior itself is wired by later Plan 2 tasks.
	assisted bool
	// exitCode is the process exit code Run() surfaces after prog.Run() returns
	// the final model (in place of the default 0). Zero (the default) means "no
	// override" — a GUIDED/assisted run that ends on a failed/aborted step can
	// set this to signal failure to the caller.
	exitCode int
	// readyID is the assisted-mode cursor: the block id the GUIDED run wants the
	// user to act on next ("" when no step is ready — either not started, or the
	// playbook is done). Advanced by startAssisted/assistedAdvance/assistedSkip.
	readyID string
	// assistedStarted guards maybeStartAssisted so the guided walk is entered
	// exactly once, at the stream-EOF where m.md/m.blocks first become final —
	// not at model-build time (Run()), when the playbook hasn't streamed in yet.
	assistedStarted bool
	// assistedFooter selects which GUIDED bottom bar (wired in a later Plan 2
	// task) to render: "" (hidden — not in assisted mode, or not yet started),
	// "step" (readyID has a next action), "failure" (readyID's run just failed),
	// or "done" (no runnable blocks remain).
	assistedFooter string
	// footerFocus is the assisted footer's own button-focus index (independent of
	// the pager's hint-mode focus); reset to 0 whenever the footer's button set
	// changes (start/advance/failure).
	footerFocus int
	// assistedFailedID is the block id whose failure raised assistedFooter="failure";
	// "" when the footer isn't in the failure state.
	assistedFailedID string
	// rollbackFailedID is the failed block currently driving a rollback chain (it shows
	// the "rolling back…" spinner, then the "all steps rolled back" suffix); "" = none.
	// rollbackPending counts the rollback targets still running, so we know when the chain
	// finishes (→ mark rollbackFailedID's suffix).
	rollbackFailedID string
	rollbackPending  int
	gateSatisfied    bool // the gate ran (or wasn't needed) this session
	// gate holds the in-progress pre-run confirmation state machine while the user
	// steps through the confirm/customize overlays; nil when no gate is active.
	gate *confirmGate
}

// AskFunc opens the request-input float with the given prompt (the floatinput Type
// is text) and blocks until the user submits or cancels. It returns the typed value
// and whether the user submitted (false → cancel/Esc or the float vanished). It is a
// closure so the ui package needn't import floatinput; the session builds it from its
// floatinput.Asker (a fixed text-type Request with the given prompt).
type AskFunc func(prompt string) (value string, submitted bool)

func newModel(harness, md string) model {
	return model{
		harness:            harness,
		md:                 md,
		width:              80,
		height:             24,
		helpLines:          buildHelpLines(),
		defaultLabel:       "Working…",
		follow:             false, // start at the top on load; only append (wrap-up) re-enables follow
		pinTop:             -1,    // no pin until a follow-up announcement frames itself at the top
		blockStates:        map[string]blockRunState{},
		maxFollowups:       resolveMaxFollowups(),
		driftResolveBackup: map[string]string{},
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
	if m.assistedFooterActive() {
		// Same replace-the-bottom-pad reasoning as the confirmResolved branch
		// above, for the GUIDED footer's own blank+context+blank+buttons+blank
		// block (assistedFooterLines is that block's net addition over the one
		// already-reserved pad).
		h -= m.assistedFooterLines()
	}
	if h < 1 {
		h = 1
	}
	return h
}

func (m *model) reflow() {
	// readyID threads the assisted-mode (--assisted/GUIDED) ready-cursor into the
	// renderer ONLY while a GUIDED run is active, so every non-assisted render is
	// byte-for-byte unchanged (m.readyID is otherwise left as its own zero value).
	readyID := ""
	if m.assisted {
		readyID = m.readyID
	}
	m.lines, m.buttons, m.blocks = Render(m.renderBody(), m.contentWidth(), RenderOpts{
		States:        m.blockStates,
		FlashKey:      m.flashKey,
		ShellDisabled: m.driverPending,
		NoReengage:    !m.canReengageInProc(),
		RollbackAvail: m.anyRollbackable(),
		MuxActive:     m.asker != nil,
		ReadyID:       readyID,
	})
	// Cache the widest Wide-line width now that m.lines is final (the append*Button
	// helpers below only add buttons, never lines) so clampScroll / $ can read it.
	m.maxWide = MaxWideWidth(m.lines)
	m.appendCachedButton()
	m.appendEditButton()
	m.appendConfirmButtons()
	m.appendAssistedFooter()
	m.clampScroll()
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
	maxX := m.maxWide - m.contentWidth()
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

// lineBlank reports whether body line lineIdx is empty or all-whitespace (ANSI
// stripped). Used by the hint-overlay painter (F20): a label normally floats on the
// line above its button, but if that line carries text (e.g. the drift warning banner
// sits directly above the resolve/regenerate buttons) floating there would paint the
// letter INTO that running text — even landing on an inter-word space still corrupts
// it — so the label drops onto the button's own line instead. An out-of-range index
// is treated as blank (nothing to overwrite).
func (m *model) lineBlank(lineIdx int) bool {
	if lineIdx < 0 || lineIdx >= len(m.lines) {
		return true
	}
	return strings.TrimSpace(strip(m.lines[lineIdx].Text)) == ""
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
		return m.handleStreamEvents(msg)
	case renderTickMsg:
		m.renderScheduled = false
		m.flushRender()
		return m, nil
	case gateAnswerMsg:
		// One pre-run confirmation overlay resolved (delivered by handleAskKey via the
		// gate's askCompletion). Drive the state machine: raise the next dialog, or
		// finish (export the values + run the deferred block).
		return m.advanceGate(msg.value, msg.submitted)
	case assistedStartMsg:
		// The assisted-start env gate's export completed — raise the ready
		// cursor/footer now (never before the declared vars were confirmed).
		m = m.startAssisted()
		return m, nil
	case orchReadyMsg:
		// Async-startup: the background-opened orchestrator landed. Install it (and the
		// asker, when supplied), clear the pending state, and reflow so the now-enabled
		// shell buttons re-render normally colored + live. A nil Orch (background open
		// failed) leaves m.orch nil but still clears driverPending — the buttons stay
		// disabled (degraded, no shell) rather than hanging.
		// Site 1: fire async drift checks now that the orchestrator is available —
		// the playbook may already be rendered (diff blocks present) but the orch
		// arrived later via the async-startup path.
		m.orch = msg.Orch
		m.reeng = msg.Reeng
		if msg.Asker != nil {
			m.asker = msg.Asker
		}
		m.driverPending = false
		m.reflow()
		return m, m.driftCheckCmds()
	case flashTickMsg:
		m.flashKey = ""
		m.reflow()
		return m, nil
	case spinTickMsg:
		return m.handleSpinTick(msg)
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
		m.recomputeDiffGeometry() // width changed → diff geometry (gutter/panes/unified) is stale
		m.clampDiffScroll()
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
				return m.activateButton(b)
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
		if m.diffMode {
			m.diffYOff += delta
			m.clampDiffScroll()
		} else if m.helpMode {
			m.helpYOff += delta
			m.clampHelpScroll()
		} else if !m.hintMode {
			m.yOff += delta
			m.clampScroll()
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.handleKeyPress(msg)
	case resultMsg:
		return m.handleResult(msg)
	case driftMsg:
		return m.handleDrift(msg)
	case driftRegenMsg:
		return m.handleDriftRegen(msg)
	case statusMsg:
		// Transient one-line note (e.g. a deferred in-process action). Shown in the
		// status bar until the next key/click clears it. Never crashes the UI.
		dbg("status: %s", msg.text)
		m.status = msg.text
		return m, nil
	case saveConfirmMsg:
		// Resolution of the "save unverified run?" confirm overlay (raised when the user
		// presses `w` before the verify block has passed). ok=true → proceed with the
		// save decision (persist or re-author); ok=false → the user cancelled, no-op.
		if msg.ok {
			m.wrappedUp = true
			return m, m.saveDecision()
		}
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
		// Refuse-solution §2a: record the note verbatim (trimmed) as a session constraint
		// BEFORE re-authoring, so it steers this amend AND every later re-engagement —
		// anything steered away from cannot resurface. The flash confirms it was noted.
		m.refusals = append(m.refusals, strings.TrimSpace(msg.value))
		m.status = "noted — will avoid that from now on"
		dbg("f: proactive amend (base len=%d, change=%q)", len(msg.base), msg.value)
		if cmd := m.beginFinalPlaybookGenerate(msg.base, msg.value); cmd != nil {
			return m, cmd
		}
		return m, nil
	case activityMsg:
		return m.handleActivity(msg)
	case reArmStreamMsg:
		return m.handleReArm(msg)
	case reloadMsg:
		// Editor exited (no-mux ExecProcess callback): re-read the source file and
		// refresh the document. Errors are silently swallowed — the playbook keeps
		// showing its prior content rather than crashing; the user can re-open the
		// editor if needed.
		_ = m.reloadSource()
		return m, nil
	case driftResolveReloadMsg:
		// The $EDITOR opened for "resolve manually" exited. When the conflict-marked
		// temp flow is active (driftTempPath set), reconcile the saved copy into the real
		// target and re-check drift; otherwise (raw-file fallback) just re-check so a
		// successful manual edit clears Drifted. Errors leave the drift state as it was.
		if m.driftTempPath != "" {
			return m.driftResolveFinish()
		}
		return m, m.driftCheckCmds()
	case sourcePollMsg:
		// Mux editor poll: stat the watched file(s) and act when the mtime has advanced
		// (the user saved in the docked editor pane). Re-arm unconditionally so saves at
		// any point while the pane is open are picked up. Watches sourcePath (the [edit]
		// playbook reload) AND, when set, driftEditPath (the "resolve manually" target →
		// re-check drift on save, F21). Stops the loop only when neither is watched.
		var recheck tea.Cmd
		if m.sourcePath != "" {
			if st, err := os.Stat(m.sourcePath); err == nil && st.ModTime().After(m.sourceMtime) {
				m.sourceMtime = st.ModTime()
				_ = m.reloadSource()
			}
		}
		if m.driftTempPath != "" {
			// Conflict-marked temp copy under edit: on save, reconcile it into the real
			// target (or report unresolved markers) and re-check drift. driftResolveFinish
			// clears driftTempPath, so the loop stops on the next tick unless still watched.
			if st, err := os.Stat(m.driftTempPath); err == nil && st.ModTime().After(m.driftEditMtime) {
				m.driftEditMtime = st.ModTime()
				m, recheck = m.driftResolveFinish()
			}
		} else if m.driftEditPath != "" {
			if st, err := os.Stat(m.driftEditPath); err == nil && st.ModTime().After(m.driftEditMtime) {
				m.driftEditMtime = st.ModTime()
				recheck = m.driftCheckCmds()
			}
		}
		if m.sourcePath == "" && m.driftEditPath == "" && m.driftTempPath == "" {
			m.polling = false
			return m, nil
		}
		return m, tea.Batch(m.sourcePollCmd(), recheck)
	}
	return m, nil
}

// markRunning sets the given block's status to "running" and resets its
// SpinFrame to 0. Called by runOrGate (direct path) and runGateBlock (gated path)
// so the spinner appears immediately, without waiting for a resultMsg.
func (m model) markRunning(id string) model {
	if m.blockStates == nil {
		m.blockStates = make(map[string]blockRunState)
	}
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
