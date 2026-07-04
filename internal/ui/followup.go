package ui

import (
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/Townk/ai-playbook/pkg/dialog"
)

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

// wFinalize is the shared finalisation step invoked by the `w` handler on both a
// dirty finalDraft branch and the raw-transcript branch. It decides whether to show
// the "save unverified" confirm gate or proceed directly to saveDecision:
//   - Not verified AND not reauthored → the user is saving an unrun proposal; show
//     the confirm overlay (askMode) so they acknowledge the playbook is untested.
//   - Verified OR reauthored → the gate is satisfied; set wrappedUp and delegate to
//     saveDecision (re-author if diverged, persist otherwise).
//
// wrappedUp is only set here (after the gate) and in the saveConfirmMsg{ok:true} arm,
// never before the gate in the `w` handler itself.
func (m model) wFinalize() (model, tea.Cmd) {
	m.confirmResolved = false
	verified := m.blockStates[m.verifyBlockID()].Status == "ok"
	if !verified && !m.reauthored {
		// First save of an unrun proposal — warn before committing.
		m.askMode = true
		m.ask = dialog.NewAsk("ai-playbook",
			"This playbook wasn't fully run, so we couldn't verify it works. Save this state as a new playbook anyway?",
			"", "confirm", nil, "Save", "Cancel")
		m.askCompletion = func(value string, submitted bool) tea.Msg {
			return saveConfirmMsg{ok: submitted && value == "yes"}
		}
		return m, m.ask.Init()
	}
	m.wrappedUp = true
	m.status = "finalizing…" // the commit path's transient indicator (re-author overwrites it)
	cmd := m.saveDecision()
	return m, cmd
}

// saveDecision finalizes the troubleshoot result: if the run diverged from the
// proposed playbook (a follow-up occurred), re-author a fresh structured playbook
// folding in the resolution; otherwise the rendered playbook IS the result, so
// persist it as-is.
func (m *model) saveDecision() tea.Cmd {
	if m.hadFollowup {
		return m.beginFinalPlaybookInProc() // re-author (resets hadFollowup via beginFinalPlaybookGenerate)
	}
	return m.commitPlaybookCmd(m.md)
}

// canReengageInProc reports whether FOLLOWUP-grade in-process re-engagement is wired
// (an orchestrator with a full authoring/troubleshoot Reengage context). When true,
// beginFollowupStream re-arms the parser with the agent's revised-fix stream directly.
// A DriftRegenOnly context (a `run --file` viewer wired to the harness for drift
// regenerate alone) does NOT count — the followup affordances stay off there.
func (m *model) canReengageInProc() bool {
	return m.reeng != nil && !m.reeng.DriftRegenOnly()
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
	return m.reeng != nil ||
		m.answerRegen != nil
}

func (m *model) beginFollowupStream(blockID, command string) tea.Cmd {
	dbg("emit %s id=%s", "followup", blockID)
	// Record the divergence: a follow-up launched, so the run diverged from the
	// proposed playbook. Set this BEFORE early-return so the intent is recorded even
	// if the in-proc actuator no-ops (no Reengage).
	m.hadFollowup = true
	// In-process: re-engage the agent via the orchestrator and re-arm the parser
	// with the revised-fix stream (APPEND). The failed command's output is read
	// from the block's run logfile (capped, like the shell's tail -c 4000).
	if m.reeng != nil {
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
