package ui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Townk/ai-playbook/internal/mux"
	"github.com/Townk/ai-playbook/internal/orchestrator"
	"github.com/Townk/ai-playbook/pkg/playbook"
)

// statusMsg carries a transient one-line status update into the model (e.g. when
// an in-process action is deferred/not-yet-implemented). It is rendered in the
// status bar until the next key/click and never crashes the UI.
type statusMsg struct{ text string }

// playbookCommittedMsg carries the outcome of a commitPlaybookCmd persist (auto-finish
// baseline or a `w` re-persist, spec §D) back into the model. On success (err==nil)
// the handler flips committed=true and shows "✓ saved playbook → <path>"; on failure
// it shows the error and leaves committed=false (so `w`/the quit-guard still apply).
// Carrying the outcome (vs an optimistic flip on the trigger) keeps committed tied to
// the actual persist result.
type playbookCommittedMsg struct {
	path string
	err  error
}

// fChangeMsg carries the outcome of the `f` request-input float back into the model
// (spec §D, stage 5): the user's typed adjustment (value) and whether they submitted.
// base is the pager content snapshotted when `f` was pressed — the AMEND base, so a
// stream arriving between the press and the answer can't race the amend input. On a
// submitted non-empty value the model amends base+value (REPLACE draft); a cancel or
// an empty value is a no-op.
type fChangeMsg struct {
	base, value string
	submitted   bool
}

// activityMsg carries one agent tool-call summary read off the activity channel
// (the session bridged the tools backend's OnActivity hook to it). ok is false
// when the channel closed (the session torn down) — the model then stops
// re-subscribing. The summary is shown under the "Working…" line while thinking.
type activityMsg struct {
	summary string
	ok      bool
	// ch is the channel this summary was read from. The handler uses it to ignore a
	// close (!ok) from a STALE feed: when re-engagement swaps m.activity to a fresh
	// channel, the old initial-authoring channel's close must not clobber the new
	// subscription. Only a close matching the current m.activity clears it.
	ch <-chan string
}

// activityWaitCmd blocks (inside the tea.Cmd goroutine, off the event loop) on
// the next activity summary and reports it as an activityMsg. It returns nil when
// no activity channel is wired (no tools backend) so the subscription simply never
// starts. The handler re-issues this cmd to keep the subscription live.
func (m model) activityWaitCmd() tea.Cmd {
	ch := m.activity
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		s, ok := <-ch
		return activityMsg{summary: s, ok: ok, ch: ch}
	}
}

// orchReadyMsg delivers the background-opened orchestrator into the model on the
// async-startup path (the OrchReady read off readyCh). The handler installs the
// orchestrator (and asker), clears driverPending — re-enabling the shell-action
// buttons — and reflows. A nil Orch (the background open failed) still clears
// driverPending so the UI degrades to "no shell" (buttons stay disabled) rather
// than hanging.
type orchReadyMsg struct{ OrchReady }

// orchReadyWaitCmd blocks (inside the tea.Cmd goroutine, off the event loop) on the
// single OrchReady delivered by the async-startup path and reports it as an
// orchReadyMsg. It returns nil when no ready-channel is wired (the sync path), so the
// subscription simply never starts. A closed channel yields a zero OrchReady (nil
// Orch), which the handler treats as a failed/abandoned open — driverPending clears,
// buttons stay disabled, no hang.
func (m model) orchReadyWaitCmd() tea.Cmd {
	ch := m.readyCh
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		r := <-ch // zero value (nil Orch) on a closed channel
		return orchReadyMsg{OrchReady: r}
	}
}

// driftCheckCmds builds one async tea.Cmd per diff block in m.blocks, each
// calling orch.CheckDrift off the event loop (never blocking render) and
// returning a driftMsg. Returns nil when the orchestrator is not installed or
// there are no diff blocks. Callers tea.Batch the result with existing cmds.
func (m model) driftCheckCmds() tea.Cmd {
	if m.orch == nil {
		return nil
	}
	var cmds []tea.Cmd
	for _, blk := range m.blocks {
		if blk.Type != "diff" {
			continue
		}
		id, patch, orch := blk.ID, blk.Payload, m.orch
		cmds = append(cmds, func() tea.Msg {
			v, _ := orch.CheckDrift(patch)
			return driftMsg{ID: id, Verdict: v}
		})
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// driftRegenCmd builds the async tea.Cmd that calls orch.DriftRegen for one
// drifted diff block, OFF the event loop, and returns a driftRegenMsg. It is
// SELF-CONTAINED: it captures orch and the patch at call time and never touches
// m.reader/m.structured/m.bodyProvider/m.streaming/m.thinking or resets the
// pane — that is beginRegenerate's role. Mirrors the shape of driftCheckCmds.
// Returns nil when the orchestrator is not installed.
func (m model) driftRegenCmd(id, patch string) tea.Cmd {
	reeng := m.reeng
	if reeng == nil {
		// No re-engagement wired: yield the same "unavailable" verdict the engine would,
		// so the drift block surfaces RegenFailed rather than spinning forever.
		return func() tea.Msg {
			return driftRegenMsg{ID: id, Err: errors.New("regenerate unavailable")}
		}
	}
	constraints := m.refusals // snapshot: inject session-rejected approaches (refuse-solution §1)
	return func() tea.Msg {
		np, err := reeng.DriftRegen(patch, constraints)
		return driftRegenMsg{ID: id, NewPatch: np, Err: err}
	}
}

// kindOf maps a UI button kind string to the orchestrator's typed Kind. It
// delegates to orchestrator.ParseKind — the single inverse of Kind.String — so the
// ui does not hand-maintain a duplicate switch. The second result is false for kinds
// that have no orchestrator action (e.g. "toggle", which is pager-local and never
// reaches emitAction in in-process use).
func kindOf(s string) (orchestrator.Kind, bool) {
	return orchestrator.ParseKind(s)
}

// orchCmd builds the tea.Cmd that performs button b's action against the live
// orchestrator, OFF the event loop (inside the returned Cmd's goroutine so the
// UI never blocks on the shell). The result is fed back as the SAME resultMsg
// the model already handles — for a run, by writing the captured stdout/stderr to
// a temp logfile and reporting {id, exit, logpath}, exactly mirroring the FIFO
// broker's record. Deferred kinds resolve to a brief statusMsg instead of
// crashing; stop/copy/play perform their effect and report nothing.
func (m model) orchCmd(b Button) tea.Cmd {
	orch := m.orch
	if orch == nil {
		return nil
	}
	k, ok := kindOf(b.Kind)
	if !ok {
		return nil
	}
	// Run blocks are assembled HERE (at dispatch), not at render: the run button
	// carries the raw block payload, and the canonical schema rule
	// (playbook.ExecCommand) turns a script block into a `<interp> <script>`
	// invocation whose stdin stays free for from= piping. Assembly is deferred to
	// dispatch because the run button is built during render, possibly before the
	// driver (and its session dir) exists on the async-open path. A shell block —
	// or an unknown block id (a synthetic rollback/assisted button) — stays
	// verbatim. cleanup removes the script file once the run completes.
	payload := b.Payload
	var cleanup func()
	var stdinPath string
	var timeout time.Duration
	if k == orchestrator.KindRun {
		payload, cleanup = m.assembleRun(b.BlockID, b.Payload)
		// Wire from= piping: feed the block's stdin from its producer's retained
		// stdout capture. Resolved via the driver (which owns the retention layout)
		// and STAT-verified — a missing capture ⇒ producer never ran ⇒ </dev/null.
		stdinPath = m.resolveStdin(b.BlockID)
		// Carry the block's declared timeout= ceiling; zero (unknown id / none
		// declared) lets the orchestrator apply its default.
		if blk, ok := m.blockByID(b.BlockID); ok {
			timeout = blk.Timeout
		}
	}
	return func() tea.Msg {
		if cleanup != nil {
			defer cleanup()
		}
		res, err := orch.Do(orchestrator.Action{Kind: k, ID: b.BlockID, Payload: payload, StdinPath: stdinPath, Timeout: timeout})
		if errors.Is(err, orchestrator.ErrMisrouted) {
			// A re-engagement kind reached the executor — a wiring bug (the ui should
			// have driven it through the reengage engine). Surface it distinctly so it
			// doesn't masquerade as an unimplemented action.
			return statusMsg{text: b.Kind + ": internal routing error (should use the reengage engine)"}
		}
		if errors.Is(err, orchestrator.ErrNotImplemented) {
			return statusMsg{text: b.Kind + ": not available in in-process mode yet"}
		}
		if err != nil {
			return statusMsg{text: b.Kind + ": " + err.Error()}
		}
		switch k {
		case orchestrator.KindRun, orchestrator.KindApplyDiff, orchestrator.KindUndoDiff, orchestrator.KindCreateFile, orchestrator.KindUndoCreate:
			// These return a real driver.Result. Bridge it to the model's resultMsg
			// via a temp logfile holding the {id, exit, logpath} shape (stdout then
			// stderr). The model's resultMsg handler then flips the apply⇄undo toggle
			// / re-gates dependents off st.Action + res.Exit (set on the click).
			logpath := writeRunLog(b.BlockID, res.Out, res.Err)
			msg := resultMsg{ID: b.BlockID, Exit: res.Exit, Logpath: logpath}
			// Surface a run killed at its ceiling as such (run kind ONLY: an
			// apply/undo has its own fixed applyTimeout, whose duration this
			// effective-run computation would misname).
			if k == orchestrator.KindRun && res.TimedOut {
				msg.TimedOut = true
				msg.TimedOutAfter = orchestrator.EffectiveTimeout(timeout)
			}
			return msg
		default:
			// stop/copy/play/view-diff have no result to surface: stop/copy/play
			// performed their effect and the model already updated its own state on
			// the trigger; view-diff is fire-and-forget (the float opened).
			return nil
		}
	}
}

// assembleRun turns a run block's raw payload into the command the shell eval's,
// via the canonical schema payload assembly (playbook.ExecCommand): a shell block
// runs verbatim, a script (run) block is written to a session temp script invoked
// by its interpreter so its stdin stays free for from= data. The block is looked
// up by id (m.blocks) to recover its type/lang; an unknown id — e.g. a synthetic
// rollback/assisted run button whose block isn't currently rendered — falls back
// to the raw payload with a nil cleanup. Scripts are written under the driver's
// session dir (survives until Close) and the returned cleanup removes the file
// after the run.
func (m model) assembleRun(id, raw string) (string, func()) {
	blk, ok := m.blockByID(id)
	if !ok {
		return raw, nil
	}
	scriptDir := ""
	if m.orch != nil && m.orch.Drv != nil {
		scriptDir = m.orch.Drv.SessionDir()
	}
	cmd, cleanup, err := playbook.ExecCommand(blk, scriptDir)
	if err != nil {
		return raw, nil
	}
	return cmd, cleanup
}

// resolveStdin returns the filesystem path to feed as block id's stdin — the
// retained stdout capture of its from= producer — or "" when the block has no
// from= edge, no driver, or the producer's capture file does not yet exist. The
// path comes from the driver (which owns the retention layout) and is
// STAT-verified: a set capture path is NOT a guarantee the file exists (a
// producer killed before its redirect opened leaves none), so a missing file
// means "producer never ran" and the block runs with </dev/null — the from-chain
// materialization is what will have produced it by the time the consumer runs.
func (m model) resolveStdin(id string) string {
	blk, ok := m.blockByID(id)
	if !ok || blk.From == "" || m.orch == nil || m.orch.Drv == nil {
		return ""
	}
	return statCapture(m.orch.Drv.CapturePath(blk.From))
}

// statCapture returns path when it names an existing regular file, else "". A
// retained capture is byte-exact on disk; a missing file means the producer never
// actually ran, so callers fall back to </dev/null rather than trust the path.
func statCapture(path string) string {
	if path == "" {
		return ""
	}
	if fi, err := os.Stat(path); err != nil || fi.IsDir() {
		return ""
	}
	return path
}

// blockByID returns the currently-rendered block with the given id (m.blocks is
// the canonical parsed list). ok is false when no such block is rendered.
func (m model) blockByID(id string) (Block, bool) {
	for _, b := range m.blocks {
		if b.ID == id {
			return b, true
		}
	}
	return Block{}, false
}

// reArmStreamMsg carries a fresh in-process re-engagement stream into the model
// once the orchestrator has produced it (off the event loop). The reader is the
// agent's stdout STREAM and the closer lets the model reap the process + fire the
// orchestrator's on-close side effects when the stream EOFs.
type reArmStreamMsg struct {
	reader io.ReadCloser
	// activity is the re-engagement's live reasoning + tool-activity feed (from the
	// orchestrator's fan-out), or nil when the re-engagement used the text fallback
	// path. When non-nil the model swaps m.activity to it and re-subscribes so the
	// followup/regenerate wait shows live reasoning on the activity line,
	// exactly like the initial authoring.
	activity <-chan string
	err      error
}

// beginRegenerate (in-process) re-authors the original request cache-bypassed and
// re-arms the parser with the fresh stream in REPLACE mode: the rendered playbook
// is reset and the new one streams in. Mirrors the FIFO-era regenerate's pane
// reset (m.md=""), but the new stream comes from the orchestrator, not a re-opened
// input FIFO. Returns nil when the orchestrator can't re-engage (no Reengage
// wired) so the caller falls back to a flash-only no-op.
func (m *model) beginRegenerate() tea.Cmd {
	// Two regenerate paths share the one reload button (cachedBadge / appendCachedButton
	// gate it on canRegenerate). For a cached ANSWER the answerRegen seam re-runs the
	// cheap classify in place and re-caches the prose; this is preferred over the
	// orchestrator's playbook-shaped Regenerate (front-matter authoring is wrong for
	// prose). For a cached PLAYBOOK answerRegen is nil and we take the orchestrator path.
	if m.answerRegen != nil {
		regen := m.answerRegen
		// REPLACE: same pane/spinner reset as the orchestrator path below.
		m.md = ""
		m.isCached = false
		m.thinking = true
		m.progress.Reset()
		m.streaming = true
		m.follow = false
		m.yOff = 0
		m.pinTop = -1
		m.reflow()
		return tea.Batch(m.restartTick(), func() tea.Msg {
			r, err := regen()
			// No live activity feed for the cheap re-classify (it's a bare model call);
			// the spinner alone covers it.
			return reArmStreamMsg{reader: r, activity: nil, err: err}
		})
	}
	reeng := m.reeng
	if reeng == nil {
		return nil
	}
	// REPLACE: reset the rendered content + thinking state, exactly like the
	// FIFO-era regenerate did before re-opening the input FIFO.
	m.md = ""
	m.isCached = false
	m.thinking = true
	m.progress.Reset()
	m.streaming = true
	m.follow = false
	// Issue #3: a re-generated document is a NEW document — scroll to the TOP and
	// drop any follow-up pin so the user reads it from the start. follow stays false
	// so streaming content stays anchored at the top rather than chasing the bottom.
	m.yOff = 0
	m.pinTop = -1
	m.reflow()
	// Per-stream structured render: only enter structured mode when the Body closure
	// is set (the event path with Task-1's live capture). The text-fallback path
	// (Body==nil) streams the playbook directly into m.md and must NOT drain the stream.
	if body := reeng.Body(); body != nil {
		m.structured = true
		m.bodyProvider = body
	}
	constraints := m.refusals // snapshot: inject session-rejected approaches (refuse-solution §1)
	return tea.Batch(m.restartTick(), func() tea.Msg {
		stream, activity, _, err := reeng.Regenerate(constraints)
		return reArmStreamMsg{reader: stream, activity: activity, err: err}
	})
}

// beginFollowupInProc (in-process) re-engages the agent with the "fix didn't work"
// prompt and re-arms the parser with the revised-fix stream in APPEND mode: a
// separator + spinner are appended below the existing playbook and the new section
// streams in. failedOutput is the captured output of the failed command (read from
// the block's run log, capped). Returns nil when re-engagement isn't wired.
func (m *model) beginFollowupInProc(failedOutput string) tea.Cmd {
	reeng := m.reeng
	if reeng == nil {
		return nil
	}
	// APPEND: keep the existing playbook, add a separator + spinner below it — UNLESS
	// an AUTO follow-up already framed the attempt with a separator ABOVE its
	// announcement phrase (justAnnounced); a second `---` would double the rule.
	if !m.justAnnounced {
		m.md += "\n\n---\n\n"
	}
	m.justAnnounced = false
	m.thinking = true
	m.progress.Reset()
	m.streaming = true
	// Issue #1: a follow-up must NOT yank the viewport to the bottom as the revised
	// fix streams in — the user is reading the failed attempt. Keep follow=false so
	// flushRender leaves m.yOff where the user left it (the spinner/activity line
	// still clamp into the visible body, so the "thinking" feedback stays on screen).
	m.follow = false
	m.reflow()
	// Per-stream structured render: followup is a markdown APPEND — clear structured
	// so the EOF render does NOT clobber the appended markdown with a stale bodyProvider.
	m.structured = false
	m.bodyProvider = nil
	constraints := m.refusals // snapshot: inject session-rejected approaches (refuse-solution §1)
	return tea.Batch(m.restartTick(), func() tea.Msg {
		stream, activity, _, err := reeng.Followup(failedOutput, constraints)
		return reArmStreamMsg{reader: stream, activity: activity, err: err}
	})
}

// beginFinalPlaybookInProc (in-process, stage 2/4 / spec §A+§B+§C) generates the
// clean final-playbook and re-arms the parser with it in REPLACE mode: the rendered
// troubleshoot is cleared and the playbook streams in, like `run <file>.md`. The
// current troubleshoot content (m.md) is passed as the change to fold in. The result
// is marked a DRAFT (finalDraft=true, committed=false): generation does NOT save or
// cache — persistence is the `w` commit (stage 3). Returns nil when re-engagement
// isn't wired.
//
// AMEND vs FRESH (stage 4, spec §C) is selected by m.servedBase:
//   - servedBase != "" → AMEND: the session is serving an existing playbook for this
//     context (a cache HIT). base=servedBase, change=the troubleshoot content (which
//     carries the resolved fix). The AMEND prompt integrates the new fix and PRESERVES
//     the existing steps, so the served playbook is improved IN PLACE; the `w` commit
//     re-caches it under the same keys (overwriting the served entry — never lost).
//   - servedBase == "" → FRESH: a cache MISS / direct troubleshoot. base="" → a new
//     playbook distilled from the troubleshoot content (unchanged stage-2 behavior).
//
// Amend-vs-fresh is naturally scoped by the cache key: a same-context failure serves
// (servedBase set) → amends; a different context is a different cache entry → a miss
// → authorPlaybook leaves servedBase "" → fresh. Unrelated playbooks never cross.
func (m *model) beginFinalPlaybookInProc() tea.Cmd {
	// The troubleshoot content is the input the FINAL-PLAYBOOK prompt distills; grab
	// it BEFORE the REPLACE reset clears m.md. The served base is independent of m.md
	// (stashed on the cache-HIT serve), so it survives the reset.
	return m.beginFinalPlaybookGenerate(m.servedBase, m.md)
}

// beginFinalPlaybookGenerate is the shared REPLACE re-arm that both the confirm /
// `w` finalize path (beginFinalPlaybookInProc, base=servedBase) and the user-initiated
// `f` amend (base=m.md — amend what's shown) drive. It resets the rendered content,
// marks the upcoming render a DRAFT (finalDraft=true, committed=false — persistence is
// the `w` commit), and re-arms the parser with orch.FinalPlaybook(base, change) in
// REPLACE mode. base!="" → AMEND (fold change into base, preserve existing steps);
// base=="" → FRESH. Returns nil when re-engagement isn't wired (off-zellij/tests).
func (m *model) beginFinalPlaybookGenerate(base, change string) tea.Cmd {
	reeng := m.reeng
	if reeng == nil {
		return nil
	}
	// Reset hadFollowup: the playbook is being re-authored, so the doc now reflects
	// the resolution. A subsequent follow-up would set it again if needed.
	m.hadFollowup = false
	// Mark this finalDraft as deliberately re-authored so wFinalize skips the
	// "save unverified" confirm gate — the re-authoring already incorporates the run.
	m.reauthored = true
	// Back up the resolved troubleshoot BEFORE the REPLACE clears it: if the generation
	// turns out to be junk (a narration, not a real playbook) the stream-EOF guard
	// restores this so the good troubleshoot is never wiped or persisted over.
	m.preFinalMd = m.md
	// REPLACE: reset the rendered content + thinking state (like regenerate).
	m.md = ""
	m.isCached = false
	m.thinking = true
	m.progress.Reset()
	m.streaming = true
	m.follow = false
	// Issue #3: the (re)generated final playbook is a NEW document — scroll to the
	// TOP and drop any follow-up pin so the user reads it from the start; follow
	// stays false so streaming content stays anchored at the top.
	m.yOff = 0
	m.pinTop = -1
	// Mark the upcoming render a draft (not yet committed/persisted). The `f` AMEND
	// path leaves an unsaved tweak that the `w`/quit-guard handles; the FINALIZE path
	// (beginFinalPlaybookInProc → saveDecision) commits explicitly.
	m.finalDraft = true
	m.committed = false
	m.reflow()
	// Per-stream structured render: only enter structured mode when the Body closure
	// is set (the event path with Task-1's live capture). The text-fallback path
	// (Body==nil) streams the playbook directly into m.md and must NOT drain the stream.
	if body := reeng.Body(); body != nil {
		m.structured = true
		m.bodyProvider = body
	}
	constraints := m.refusals // snapshot: inject session-rejected approaches (refuse-solution §1)
	return tea.Batch(m.restartTick(), func() tea.Msg {
		stream, activity, _, err := reeng.FinalPlaybook(base, change, constraints)
		return reArmStreamMsg{reader: stream, activity: activity, err: err}
	})
}

// commitPlaybookCmd (in-process, spec §D/§E) persists the displayed final playbook
// draft via the reengage engine's CommitPlaybook (save the .md + cache-replace this
// request's entry, assembling+prepending front matter), OFF the event loop, and
// surfaces the outcome as a playbookCommittedMsg. The handler flips committed=true on
// success and shows "✓ saved playbook → <path>" / the error. body is the draft to
// commit (snapshotted on the trigger so a later stream can't race it). Returns a no-op
// status when re-engagement is unwired.
func (m *model) commitPlaybookCmd(body string) tea.Cmd {
	reeng := m.reeng
	if reeng == nil {
		return func() tea.Msg { return statusMsg{text: "commit: not available in this mode"} }
	}
	// Backstop: never save/cache a non-playbook (no H1 / no runnable block). The
	// stream-EOF guard already prevents an invalid draft from being displayed, so this
	// is defense-in-depth for any `w`-commit path. countBlocks applies the same
	// block-count rule the renderer does (without the full styled Render) so the
	// predicate matches what the pager would show.
	if !isValidPlaybook(body, countBlocks(body)) {
		return func() tea.Msg {
			return statusMsg{text: "Not a playbook — nothing saved (no title or no runnable steps)."}
		}
	}
	return func() tea.Msg {
		path, err := reeng.CommitPlaybook(body)
		return playbookCommittedMsg{path: path, err: err}
	}
}

// writeRunLog writes a run's captured stdout then stderr to a temp file and
// returns its path. On any error it returns "" — the model treats an empty
// logpath as "no log", which is harmless. The file is not cleaned up here; it
// lives for the session so the user can inspect a failed run's output (mirroring
// the broker, which left per-run logs on disk).
func writeRunLog(id, out, errOut string) string {
	f, err := os.CreateTemp("", "apb-run-"+sanitizeLogID(id)+"-*.log")
	if err != nil {
		return ""
	}
	defer f.Close()
	if out != "" {
		_, _ = f.WriteString(out)
		if errOut != "" {
			_, _ = f.WriteString("\n")
		}
	}
	if errOut != "" {
		_, _ = f.WriteString(errOut)
	}
	return f.Name()
}

// sanitizeLogID keeps a block id safe for a filename: non-[A-Za-z0-9_-] → _.
func sanitizeLogID(id string) string {
	b := []byte(id)
	for i, c := range b {
		if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' && c != '-' {
			b[i] = '_'
		}
	}
	return string(b)
}

// cliMux is the in-process Mux: clipboard via pbcopy (darwin) and the play
// action. Play stages the command into the request's ORIGIN shell pane via the
// mux type-into seam (`zellij action write-chars --pane-id <origin>` —
// focus-independent, no trailing CR, so it sits at the prompt awaiting the
// user's ENTER). Without an origin pane (off-zellij, `run`/`show` in the user's
// own pane) or on a failed write (stale pane) it degrades to the clipboard so
// the command never vanishes, and returns a note the ui surfaces as status.
type cliMux struct {
	mu     sync.Mutex // guards played: Play runs on concurrent tea.Cmd goroutines
	played []string   // commands handed to Play (recorded for tests/inspection)
	// origin is the mux pane id of the request's origin shell (Options.
	// OriginPane; e.g. "terminal_3"). "" → no origin pane to type into.
	origin string
	// typeInto is the mux write seam (mux.Mux.TypeInto). nil → no mux.
	typeInto func(pane, text string) error
	// copyFn is the clipboard degrade seam (c.Copy in production; a test may
	// substitute or leave nil to observe the no-clipboard message).
	copyFn func(text string) error
}

// newCLIMux builds the production cliMux: origin is Options.OriginPane and fl
// the active float mux (mux.Null off-zellij — its TypeInto fails, which routes
// Play onto the clipboard degrade).
func newCLIMux(origin string, fl mux.Mux) *cliMux {
	c := &cliMux{origin: origin}
	if fl != nil {
		c.typeInto = fl.TypeInto
	}
	c.copyFn = c.Copy
	return c
}

// Copy places text on the system clipboard. On darwin it shells out to pbcopy;
// elsewhere it is a no-op success (OSC 52 emission is a later refinement).
func (c *cliMux) Copy(text string) error {
	if runtime.GOOS == "darwin" {
		cmd := exec.Command("pbcopy")
		cmd.Stdin = nil
		in, err := cmd.StdinPipe()
		if err != nil {
			return err
		}
		if err := cmd.Start(); err != nil {
			return err
		}
		_, _ = in.Write([]byte(text))
		_ = in.Close()
		return cmd.Wait()
	}
	return nil
}

// Play stages cmd at the origin shell's prompt (no trailing CR — the user
// reviews and presses ENTER). Degrades to the clipboard when there is no
// origin pane or the pane write fails; the returned error is the user-facing
// status note for the degrade (nil ONLY when the command reached the prompt).
func (c *cliMux) Play(cmd string) error {
	c.mu.Lock()
	c.played = append(c.played, cmd)
	c.mu.Unlock()
	var terr error
	if c.origin != "" && c.typeInto != nil {
		if terr = c.typeInto(c.origin, cmd); terr == nil {
			return nil
		}
	}
	note := "no origin shell pane"
	if terr != nil {
		note = "typing into the origin pane failed"
	}
	if c.copyFn == nil {
		return errors.New(note)
	}
	if cerr := c.copyFn(cmd); cerr != nil {
		return fmt.Errorf("%s, and the clipboard fallback failed: %v", note, cerr)
	}
	return errors.New(note + " — command copied to the clipboard instead")
}

// Played returns a snapshot of the recorded Play commands, taken under the lock —
// the safe way for tests to read the slice that Play appends to from concurrent
// tea.Cmd goroutines.
func (c *cliMux) Played() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.played...)
}
