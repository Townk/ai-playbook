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
	case "wrapup":
		return orchestrator.KindWrapup, true
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
	// followup/regenerate/wrapup wait shows live reasoning on the activity line,
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
	// APPEND: keep the existing playbook, add a separator + spinner below it.
	m.md += "\n\n---\n\n"
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

// beginWrapupInProc (in-process) runs the wrap-up pass and re-arms the parser with
// the `## Solution` summary stream in APPEND mode (the summary streams below the
// playbook). The orchestrator performs the side effects (solution artifact + KB
// append). runlog is the run log to feed the wrap-up prompt (empty here — the
// in-process run log is the model's block states; a richer run log is a later
// refinement). Returns nil when re-engagement isn't wired.
func (m *model) beginWrapupInProc(runlog string) tea.Cmd {
	orch := m.orch
	if orch == nil || orch.Reengage == nil {
		return nil
	}
	// APPEND: keep the playbook, add a separator + spinner below it.
	m.md += "\n\n---\n\n"
	m.thinking = true
	m.spinFrame = 0
	m.spinTicks = 0
	m.streaming = true
	m.follow = true
	m.reflow()
	return tea.Batch(m.restartTick(), func() tea.Msg {
		stream, activity, _, err := orch.Wrapup(runlog)
		return reArmStreamMsg{reader: stream, activity: activity, err: err}
	})
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
