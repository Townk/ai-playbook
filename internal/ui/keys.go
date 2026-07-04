package ui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/Townk/ai-playbook/pkg/dialog"
)

func (m model) handleKeyPress(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.flashKey = ""
	m.status = ""
	// The uncommitted-draft quit guard is a two-press intent: it only persists across
	// a consecutive quit (to discard) or a `w` (to save, which clears it). Any OTHER
	// key (navigation, help, …) cancels the pending discard so a later quit warns
	// afresh rather than silently exiting.
	if s := msg.String(); s != "q" && s != "esc" && s != "ctrl+c" && s != "w" {
		m.quitGuard = false
	}
	// Diff overlay: resolve before help/hint/normal handling.
	if m.diffMode {
		switch msg.String() {
		case "esc", "q":
			m.diffMode = false
		case "down", "j":
			m.diffYOff++
		case "up", "k":
			m.diffYOff--
		case "ctrl+d":
			m.diffYOff += diffHalf(m)
		case "ctrl+u":
			m.diffYOff -= diffHalf(m)
		case "ctrl+f", "pgdown":
			m.diffYOff += diffPage(m)
		case "ctrl+b", "pgup":
			m.diffYOff -= diffPage(m)
		case "g", "home":
			m.diffYOff = 0
		case "G", "end":
			m.diffYOff = m.diffRowCount()
		case "right", "l":
			m.diffXOff++
		case "left", "h":
			m.diffXOff--
		case "L":
			m.diffXOff += diffHalfW(m)
		case "H":
			m.diffXOff -= diffHalfW(m)
		case "0", "^":
			m.diffXOff = 0
		}
		m.clampDiffScroll()
		return m, nil
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
				m.hintMode = false
				m.hintLabels = nil
				return m.activateButton(b)
			}
			m.hintMode = false
			m.hintLabels = nil
		}
		return m, nil
	}
	// Assisted (GUIDED) footer: while a footer row is up (Run/Skip/Quit,
	// Roll-back/Leave-as-is/Quit, or the done Quit) it is keyboard-FOCUSABLE —
	// ←/→ (also h/l, Tab) move focus between its buttons, Enter activates the
	// focused one. Captured BEFORE the confirm/leader/global switch so footer
	// nav keys are never mistaken for normal nav while a footer is active; a
	// mouse click still resolves regardless of focus (click-dispatch path).
	//
	// Only the nav/activate keys below are captured (each returns explicitly).
	// Any OTHER key (ctrl+c, q, esc, scroll keys, space, ?, w, ...) falls
	// through to the confirm/leader/global handling further down — in
	// particular ctrl+c must always be able to quit, the doc must stay
	// scrollable while a footer is on screen, and Space must reach the
	// Space-leader → hint mode so the ready block's copy/expand buttons stay
	// hintable instead of the footer swallowing Space as "activate".
	if m.assistedFooterActive() {
		btns := m.assistedFooterButtons()
		// Clamp a stale focus (e.g. carried over from a footer with more buttons)
		// before using it as an index below.
		if m.footerFocus > len(btns)-1 {
			m.footerFocus = len(btns) - 1
		}
		if m.footerFocus < 0 {
			m.footerFocus = 0
		}
		switch msg.String() {
		case "left", "h":
			if m.footerFocus > 0 {
				m.footerFocus--
			}
			return m, nil
		case "right", "l":
			if m.footerFocus < len(btns)-1 {
				m.footerFocus++
			}
			return m, nil
		case "tab":
			if len(btns) > 0 {
				m.footerFocus = (m.footerFocus + 1) % len(btns)
			}
			return m, nil
		case "enter":
			if m.footerFocus >= 0 && m.footerFocus < len(btns) {
				return m.assistedActivate(btns[m.footerFocus].Kind)
			}
			return m, nil
		}
		// space/" " deliberately falls through (no case above, no catch-all
		// return) to the Space-leader → hint mode below, so the user can
		// hint-select the ready block's copy/expand buttons while the footer
		// is shown, instead of the footer swallowing Space as "activate".
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
			// F19: a currently-inert button (its dispatch is swallowed) gets no hint
			// label — otherwise the user picks a letter that does nothing. Covers a
			// drifted block's apply-diff and the async-startup shell buttons.
			if m.buttonInert(b) {
				continue
			}
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
		if m.assisted && msg.String() == "ctrl+c" {
			// Abort is non-zero; q/esc stay a clean exit (0 unless an unresolved
			// failure already set it).
			m.exitCode = 1
		}
		return m, tea.Quit
	case "w":
		// `w` is the single finalize/commit action (spec §D/§E). Only when settled
		// (not streaming). Three branches:
		//   - an ALREADY-SAVED draft (finalDraft && committed): no-op — the doc is
		//     unchanged since the baseline/last `w`, so re-running the metadata call
		//     would be wasted work (spec §D efficiency). Just confirm "✓ already saved".
		//   - a DIRTY draft (finalDraft && !committed): wFinalize handles the gate —
		//     skips the confirm for re-authored drafts, warns for unrun proposals.
		//   - no draft (the pager holds a raw troubleshoot TRANSCRIPT): wFinalize
		//     applies the same gate, then delegates to saveDecision (persist or re-author).
		if !m.streaming {
			if m.finalDraft && m.committed {
				dbg("w: draft already saved (unchanged) — no-op")
				m.status = "✓ already saved"
				return m, nil
			}
			if m.finalDraft && !m.committed {
				dbg("w: re-persist dirty final-playbook draft")
				var cmd tea.Cmd
				m, cmd = m.wFinalize()
				return m, cmd
			}
			dbg("w: manual finalize → save decision (persist clean run / re-author diverged)")
			var cmd tea.Cmd
			m, cmd = m.wFinalize()
			return m, cmd
		}
		return m, nil
	case "r":
		// `r` is REFINE: the playbook is final (nothing pending from the model), but
		// the user can refine it. It captures a free-form adjustment and the agent
		// re-authors the DISPLAYED document in AMEND mode (base=m.md) → REPLACE draft.
		// Repeatable (each refine amends the new content); `w` then commits. Only while
		// settled (not mid-stream). MUX → the request-input float (m.asker); NO-MUX →
		// the same in-viewer ask overlay the agent's `ask` tool uses (m.askBridge).
		if m.streaming {
			return m, nil
		}
		base := m.md // snapshot now so a later stream can't race the amend base
		if m.asker != nil {
			// MUX: spawn the float + poll OFF the event loop, feed back an fChangeMsg.
			ask := m.asker
			return m, func() tea.Msg {
				value, submitted := ask("What should I change?")
				return fChangeMsg{base: base, value: value, submitted: submitted}
			}
		}
		if m.askBridge != nil {
			// NO-MUX: open the in-viewer ask overlay (reused from the agent-ask path);
			// its completion routes the refinement into the amend via fChangeMsg.
			m.askMode = true
			m.askCompletion = func(value string, submitted bool) tea.Msg {
				return fChangeMsg{base: base, value: value, submitted: submitted}
			}
			m.ask = dialog.NewAsk("ai-playbook", "What should I change?", "", "text", nil, "", "")
			return m, m.ask.Init()
		}
		m.status = "refine unavailable in this mode"
		return m, nil
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
	case "ctrl+x":
		// HIDDEN debug affordance (not in the help): dump the current raw document
		// as-is so a malformed render can be captured + reproduced.
		if path := dumpDocument(m.md); path != "" {
			m.status = "dumped document → " + path
		} else {
			m.status = "document dump failed"
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
		m.xOff = m.maxWide // clampScroll will cap it
	}
	m.clampScroll()
	return m, nil
}
