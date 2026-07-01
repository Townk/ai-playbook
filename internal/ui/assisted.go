package ui

import "github.com/Townk/ai-playbook/internal/autorun"

// assisted.go holds the GUIDED-fullscreen (--assisted) run-mode engine: the
// readyID cursor, advance-on-completion, skip, and the scroll-to-⅓ that keeps
// the ready step framed in the viewport. It has no footer UI of its own (that's
// wired by a later Plan 2 task) — this is pure model-state logic.

// assistedNextID returns the next runnable block's id per autorun's shared
// Sequence/NextRunnable logic (the same dependency + rollback-target filtering
// the headless --auto path uses), or "" when nothing is left to run.
func (m model) assistedNextID() string {
	b, ok := autorun.NextRunnable(m.toAutorunBlocks())
	if ok {
		return b.ID
	}
	return ""
}

// startAssisted sets the initial readyID cursor (the first runnable block) and
// raises the "step" footer, scrolling that block ~⅓ down the viewport. An empty
// playbook (no runnable blocks) goes straight to the "done" footer.
func (m model) startAssisted() model {
	m.readyID = m.assistedNextID()
	if m.readyID != "" {
		m.assistedFooter = "step"
		m.footerFocus = 0
		m = m.scrollToFraction(m.readyID, 1, 3)
	} else {
		m.assistedFooter = "done"
	}
	return m
}

// assistedAdvance recomputes the readyID cursor after the current step settles
// (ok or skipped) and either re-frames the next step ("step" footer) or, once
// nothing remains runnable, raises the "done" footer and clears the cursor.
func (m model) assistedAdvance() model {
	m.readyID = m.assistedNextID()
	if m.readyID != "" {
		m.assistedFooter = "step"
		m.footerFocus = 0
		m = m.scrollToFraction(m.readyID, 1, 3)
	} else {
		m.assistedFooter = "done"
		m.readyID = ""
	}
	return m
}

// assistedSkip marks the current ready block as deliberately skipped (its
// dependents naturally never become runnable — NextRunnable treats any non-ok
// need as blocking, so no transitive marking is needed) and advances the cursor.
func (m model) assistedSkip() model {
	st := m.blockStates[m.readyID]
	st.Status = autorun.StatusSkipped
	m.blockStates[m.readyID] = st
	return m.assistedAdvance()
}

// scrollToFraction positions the viewport so block id's line sits num/den of the
// way down the body — e.g. (1, 3) frames it about a third of the way down,
// mirroring pinAnnouncement's scroll math. clampScroll then caps it in range.
func (m model) scrollToFraction(id string, num, den int) model {
	line := m.lineForBlock(id)
	m.yOff = line - m.body()*num/den
	m.clampScroll()
	return m
}

// lineForBlock returns the document line of block id's copy button (every block
// carries one, so this doubles as a block→line lookup with no separate index to
// maintain) or 0 if the id isn't found (e.g. before the first reflow).
func (m model) lineForBlock(id string) int {
	for _, b := range m.buttons {
		if b.BlockID == id {
			return b.Line
		}
	}
	return 0
}
