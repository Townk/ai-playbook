package ui

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"

	tea "charm.land/bubbletea/v2"

	"ai-playbook/orchestrator"
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

// kindOf maps a UI button kind string to the orchestrator's typed Kind. The
// second result is false for kinds that have no orchestrator action (e.g.
// "toggle", which is pager-local and never reaches emitAction in in-process use).
func kindOf(s string) (orchestrator.Kind, bool) {
	switch s {
	case "run":
		return orchestrator.KindRun, true
	case "stop":
		return orchestrator.KindStop, true
	case "copy":
		return orchestrator.KindCopy, true
	case "play":
		return orchestrator.KindPlay, true
	case "diff", "view-diff":
		return orchestrator.KindViewDiff, true
	case "apply-diff":
		return orchestrator.KindApplyDiff, true
	case "undo-diff":
		return orchestrator.KindUndoDiff, true
	case "regenerate":
		return orchestrator.KindRegenerate, true
	case "followup":
		return orchestrator.KindFollowup, true
	default:
		return 0, false
	}
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
	return func() tea.Msg {
		res, err := orch.Do(orchestrator.Action{Kind: k, ID: b.BlockID, Payload: b.Payload})
		if errors.Is(err, orchestrator.ErrNotImplemented) {
			return statusMsg{text: b.Kind + ": not available in in-process mode yet"}
		}
		if err != nil {
			return statusMsg{text: b.Kind + ": " + err.Error()}
		}
		switch k {
		case orchestrator.KindRun, orchestrator.KindApplyDiff, orchestrator.KindUndoDiff:
			// These return a real driver.Result. Bridge it to the model's resultMsg
			// via a temp logfile (the same {id, exit, logpath} shape parseResults
			// produces from the FIFO: stdout then stderr). The model's resultMsg
			// handler then flips the apply⇄undo toggle / re-gates dependents off
			// st.Action + res.Exit (set on the click), exactly as in fifo mode.
			logpath := writeRunLog(b.BlockID, res.Out, res.Err)
			return resultMsg{ID: b.BlockID, Exit: res.Exit, Logpath: logpath}
		default:
			// stop/copy/play/view-diff have no result to surface: stop/copy/play
			// performed their effect and the model already updated its own state on
			// the trigger; view-diff is fire-and-forget (the float opened).
			return nil
		}
	}
}

// reArmStreamMsg carries a fresh in-process re-engagement stream into the model
// once the orchestrator has produced it (off the event loop). It mirrors the
// FIFO-era reArmedMsg, but the reader is the agent's stdout STREAM (not a re-opened
// FIFO) and the closer lets the model reap the process + fire the orchestrator's
// on-close side effects when the stream EOFs.
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
		m.spinFrame = 0
		m.spinTicks = 0
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
	orch := m.orch
	if orch == nil || orch.Reengage == nil {
		return nil
	}
	// REPLACE: reset the rendered content + thinking state, exactly like the
	// FIFO-era regenerate did before re-opening the input FIFO.
	m.md = ""
	m.isCached = false
	m.thinking = true
	m.spinFrame = 0
	m.spinTicks = 0
	m.streaming = true
	m.follow = false
	// Issue #3: a re-generated document is a NEW document — scroll to the TOP and
	// drop any follow-up pin so the user reads it from the start. follow stays false
	// so streaming content stays anchored at the top rather than chasing the bottom.
	m.yOff = 0
	m.pinTop = -1
	m.reflow()
	return tea.Batch(m.restartTick(), func() tea.Msg {
		stream, activity, _, err := orch.Regenerate()
		return reArmStreamMsg{reader: stream, activity: activity, err: err}
	})
}

// beginFollowupInProc (in-process) re-engages the agent with the "fix didn't work"
// prompt and re-arms the parser with the revised-fix stream in APPEND mode: a
// separator + spinner are appended below the existing playbook and the new section
// streams in. failedOutput is the captured output of the failed command (read from
// the block's run log, capped). Returns nil when re-engagement isn't wired.
func (m *model) beginFollowupInProc(failedOutput string) tea.Cmd {
	orch := m.orch
	if orch == nil || orch.Reengage == nil {
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
	m.spinFrame = 0
	m.spinTicks = 0
	m.streaming = true
	// Issue #1: a follow-up must NOT yank the viewport to the bottom as the revised
	// fix streams in — the user is reading the failed attempt. Keep follow=false so
	// flushRender leaves m.yOff where the user left it (the spinner/activity line
	// still clamp into the visible body, so the "thinking" feedback stays on screen).
	m.follow = false
	m.reflow()
	return tea.Batch(m.restartTick(), func() tea.Msg {
		stream, activity, _, err := orch.Followup(failedOutput)
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
	cmd := m.beginFinalPlaybookGenerate(m.servedBase, m.md)
	// This is a FINALIZE (confirm-yes / `w`-on-transcript), not an `f` amend: the
	// stream-EOF handler must auto-persist a baseline so quitting before `w` still
	// leaves a complete saved playbook with front matter (spec §D). The `f` amend path
	// (fChangeMsg → beginFinalPlaybookGenerate directly) leaves this cleared.
	m.persistOnFinish = true
	return cmd
}

// beginFinalPlaybookGenerate is the shared REPLACE re-arm that both the confirm /
// `w` finalize path (beginFinalPlaybookInProc, base=servedBase) and the user-initiated
// `f` amend (base=m.md — amend what's shown) drive. It resets the rendered content,
// marks the upcoming render a DRAFT (finalDraft=true, committed=false — persistence is
// the `w` commit), and re-arms the parser with orch.FinalPlaybook(base, change) in
// REPLACE mode. base!="" → AMEND (fold change into base, preserve existing steps);
// base=="" → FRESH. Returns nil when re-engagement isn't wired (off-zellij/tests).
func (m *model) beginFinalPlaybookGenerate(base, change string) tea.Cmd {
	orch := m.orch
	if orch == nil || orch.Reengage == nil {
		return nil
	}
	// Back up the resolved troubleshoot BEFORE the REPLACE clears it: if the generation
	// turns out to be junk (a narration, not a real playbook) the stream-EOF guard
	// restores this so the good troubleshoot is never wiped or persisted over.
	m.preFinalMd = m.md
	// REPLACE: reset the rendered content + thinking state (like regenerate).
	m.md = ""
	m.isCached = false
	m.thinking = true
	m.spinFrame = 0
	m.spinTicks = 0
	m.streaming = true
	m.follow = false
	// Issue #3: the (re)generated final playbook is a NEW document — scroll to the
	// TOP and drop any follow-up pin so the user reads it from the start; follow
	// stays false so streaming content stays anchored at the top.
	m.yOff = 0
	m.pinTop = -1
	// Mark the upcoming render a draft (not yet committed/persisted). Default the
	// auto-persist intent OFF: this shared re-arm is also the `f` AMEND path, which must
	// NOT auto-persist (it leaves an unsaved tweak). The FINALIZE caller
	// (beginFinalPlaybookInProc) re-sets persistOnFinish=true after this returns.
	m.finalDraft = true
	m.committed = false
	m.persistOnFinish = false
	m.reflow()
	return tea.Batch(m.restartTick(), func() tea.Msg {
		stream, activity, _, err := orch.FinalPlaybook(base, change)
		return reArmStreamMsg{reader: stream, activity: activity, err: err}
	})
}

// commitPlaybookCmd (in-process, spec §D/§E) persists the displayed final playbook
// draft via orchestrator.CommitPlaybook (save the .md + cache-replace this request's
// entry, assembling+prepending front matter), OFF the event loop, and surfaces the
// outcome as a playbookCommittedMsg. The handler flips committed=true on success and
// shows "✓ saved playbook → <path>" / the error. body is the draft to commit
// (snapshotted on the trigger so a later stream can't race it). Returns a no-op status
// when re-engagement is unwired.
func (m *model) commitPlaybookCmd(body string) tea.Cmd {
	orch := m.orch
	if orch == nil || orch.Reengage == nil {
		return func() tea.Msg { return statusMsg{text: "commit: not available in this mode"} }
	}
	// Backstop: never save/cache a non-playbook (no H1 / no runnable block). The
	// stream-EOF guard already prevents an invalid draft from being displayed, so this
	// is defense-in-depth for any `w`-commit path. Count runnable blocks the same way
	// the renderer does so the predicate matches what the pager would show.
	_, _, blocks := Render(body, m.contentWidth(), m.blockStates, m.flashKey)
	if !isValidPlaybook(body, len(blocks)) {
		return func() tea.Msg {
			return statusMsg{text: "Not a playbook — nothing saved (no title or no runnable steps)."}
		}
	}
	return func() tea.Msg {
		path, err := orch.CommitPlaybook(body)
		return playbookCommittedMsg{path: path, err: err}
	}
}

// writeRunLog writes a run's captured stdout then stderr to a temp file and
// returns its path. On any error it returns "" — the model treats an empty
// logpath as "no log", which is harmless. The file is not cleaned up here; it
// lives for the session so the user can inspect a failed run's output (mirroring
// the broker, which left per-run logs on disk).
func writeRunLog(id, out, errOut string) string {
	f, err := os.CreateTemp("", "aapb-run-"+sanitizeLogID(id)+"-*.log")
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
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			b[i] = '_'
		}
	}
	return string(b)
}

// cliMux is the in-process Mux: clipboard via pbcopy (darwin) and a recorded
// no-op Play. Full type-into-origin-pane is the later mux-adapter stage; for now
// Play just records the command so the wiring is exercised without a real pane.
type cliMux struct {
	played []string // commands handed to Play (recorded; see note above)
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

// Play records the command. Typing it into the user's origin pane is deferred to
// the mux-adapter stage; recording keeps the orchestrator's play path live and
// inspectable without a real pane.
func (c *cliMux) Play(cmd string) error {
	c.played = append(c.played, cmd)
	return nil
}
