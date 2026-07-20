package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Townk/ai-playbook/internal/autorun"
)

// assisted.go holds the GUIDED-fullscreen (--assisted) run-mode engine: the
// readyID cursor, advance-on-completion, skip, and the scroll-to-⅓ that keeps
// the ready step framed in the viewport, PLUS (this task) the focusable footer
// that renders the Run/Skip/Quit (and failure Roll-back/Leave-as-is/Quit, and
// done Quit) button rows and reserves the viewport space for them. Input
// wiring (keys/clicks resolving these buttons) is a later Plan 2 task — this
// file only renders and registers Screen buttons for the click hit-test.

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

// assistedStartMsg is emitted once an assisted-start env gate's export
// completes (enterAssistedReady); its Update arm raises the ready
// cursor/footer (startAssisted) — so the footer appears only AFTER the
// declared vars are confirmed and exported, never before.
type assistedStartMsg struct{}

// maybeStartAssisted enters the guided walk once the playbook's blocks are
// available (stream EOF). Guarded so it fires exactly once. Per the spec, the
// playbook's declared env vars (if any) must be confirmed BEFORE the ready
// cursor/footer appear, so this fires the assisted-start env gate
// (beginAssistedGate) rather than jumping straight to startAssisted — a
// playbook with no declared vars (or an already-satisfied gate) falls
// straight through to the ready footer, same as before.
func (m model) maybeStartAssisted() (model, tea.Cmd) {
	if m.assisted && !m.assistedStarted {
		m.assistedStarted = true
		return m.beginAssistedGate()
	}
	return m, nil
}

// startAssisted sets the initial readyID cursor (the first runnable block) and
// raises the "step" footer, scrolling that block ~⅓ down the viewport. An empty
// playbook (no runnable blocks) goes straight to the "done" footer.
func (m model) startAssisted() model {
	// Recompute m.blocks from the current m.md regardless of caller state — the
	// caller (maybeStartAssisted at stream EOF, or a test constructing a model
	// directly) may not have reflowed yet, and assistedNextID below depends on
	// m.blocks being current.
	m.reflow()
	m.readyID = m.assistedNextID()
	if m.readyID != "" {
		m.assistedFooter = "step"
		m.footerFocus = 0
		m = m.scrollToFraction(m.readyID, 1, 3)
	} else {
		m.assistedFooter = "done"
	}
	// reflow so the newly-raised footer's Screen buttons are registered right
	// away — callers (including tests) that construct/advance the model
	// directly, without going through Update()'s own trailing reflow(), still
	// see a consistent m.buttons/m.lines.
	m.reflow()
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
	// See startAssisted's comment: reflow here too so direct callers (assistedSkip,
	// tests) observe the new footer's Screen buttons without relying on a caller's
	// own trailing reflow() (Update()'s resultMsg handler already calls reflow()
	// again right after — reflow is idempotent, so the extra call is harmless).
	m.reflow()
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

// footerBtn is one button in the assisted (GUIDED) footer's button row: the
// label shown, the Kind that identifies it to the click hit-test/flash (e.g.
// "assist-run") and assistedFooterButtonLabel's flash lookup, and the Accent —
// the FOCUSED-highlight color for this footer mode (all buttons in a mode
// share one accent: a lighter "selected tab" grey for "step", the warn peach
// for "failure", green only for the terminal "done" success state).
type footerBtn struct{ Label, Kind, Accent string }

// assistedFooterButtons returns the button set for the current assistedFooter
// mode: "step" → Run/Skip/Quit; "failure" → Roll back (only when at least one
// already-run block declares a rollback target) / Leave as-is / Quit; "done" →
// a single Quit; "" (footer not shown) → nil.
//
// Accent is mode-wide, not per-button: "step" hasn't succeeded at anything yet,
// so its FOCUSED button reads as a neutral "selected tab" — a lighter grey
// (colOverlay1) than the unfocused buttons' colSurface1, with bright-white text
// (see assistedFooterButtonLabel) — rather than success-green or the dialog
// accent. "failure" uses the warn tone (colPeach) already used elsewhere for
// rollback/danger actions. "done" is the one case a completed run legitimately
// reads as success (colGreen). Both "failure" and "done" pair their colored
// accent with dark (colBase) focused text, readable on a light background.
func (m model) assistedFooterButtons() []footerBtn {
	switch m.assistedFooter {
	case "step":
		return []footerBtn{
			{"Run", "assist-run", colOverlay1},
			{"Skip", "assist-skip", colOverlay1},
			{"Quit", "assist-quit", colOverlay1},
		}
	case "failure":
		var btns []footerBtn
		if m.anyRollbackable() {
			btns = append(btns, footerBtn{"Roll back", "assist-rollback", colPeach})
		}
		btns = append(btns,
			footerBtn{"Leave as-is", "assist-leave", colPeach},
			footerBtn{"Quit", "assist-quit", colPeach},
		)
		return btns
	case "done":
		return []footerBtn{{"Quit", "assist-quit", colGreen}}
	default:
		return nil
	}
}

// assistedFooterActive reports whether the GUIDED footer should render/reserve
// space this frame. It hides while the ready step is actually running (the
// block shows its own spinner instead) and defers to any overlay (ask dialog)
// or the verify-success confirm row (assisted runs don't reach that wrap-up,
// but the guard is kept so the two bottom-reserved rows never both fire).
func (m model) assistedFooterActive() bool {
	return m.assisted && m.assistedFooter != "" && !m.askMode && !m.confirmResolved &&
		m.blockStates[m.readyID].Status != "running"
}

// assistedStepPosition returns the 1-based position of readyID among the
// playbook's runnable steps (autorun.Sequence — the same dependency +
// rollback-target filtering NextRunnable uses) and the total runnable count,
// for the "step" footer's "Step <n>/<total>" context line.
func (m model) assistedStepPosition() (n, total int) {
	ab, _ := m.toAutorunBlocks()
	seq := autorun.Sequence(ab)
	total = len(seq)
	for i, b := range seq {
		if b.ID == m.readyID {
			n = i + 1
			break
		}
	}
	return n, total
}

// assistedDoneCounts tallies how many blocks ended up "ok" (ran) vs "skipped"
// once the assisted run has nothing left runnable, for the "done" footer's
// summary line.
func (m model) assistedDoneCounts() (ran, skipped int) {
	for _, b := range m.blocks {
		switch m.blockStates[b.ID].Status {
		case autorun.StatusOK:
			ran++
		case autorun.StatusSkipped:
			skipped++
		}
	}
	return ran, skipped
}

// readyCommand returns the ready block's payload, collapsed to its first
// line (commands are often single-line shell, but this keeps the context
// line to one row even for multi-line payloads), or "" if readyID isn't a
// known block.
func (m model) readyCommand() string {
	for _, b := range m.blocks {
		if b.ID == m.readyID {
			cmd, _, _ := strings.Cut(strings.TrimSpace(b.Payload), "\n")
			return cmd
		}
	}
	return ""
}

// assistedFooterContextRowString builds the single-line context row shown
// above the footer buttons: the ready step's position + id + command for
// "step", the failed id for "failure", or the ran/skipped tally for "done".
// Returns "" when the footer isn't active.
func (m model) assistedFooterContextRowString() string {
	switch m.assistedFooter {
	case "step":
		n, total := m.assistedStepPosition()
		return fmt.Sprintf("Step %d/%d · %s · %s", n, total, m.readyID, m.readyCommand())
	case "failure":
		return fmt.Sprintf("Step failed · %s", m.assistedFailedID)
	case "done":
		ran, skipped := m.assistedDoneCounts()
		return fmt.Sprintf("Assisted run complete — %d ran, %d skipped", ran, skipped)
	default:
		return ""
	}
}

// assistedFooterButtonLabel renders one footer button, mirroring
// confirmButtonLabel's filled-control structure (same padding, same flash
// treatment) but WITHOUT confirmButtonLabel's always-green focused highlight:
// the focused color comes from the button's own Accent (mode-wide — see
// assistedFooterButtons), so the "step" footer's selected button reads as a
// lighter-grey "selected tab" (colOverlay1 bg) with bright-white text, rather
// than "success" before anything has run. The colored "failure"/"done" accents
// pair with dark (colBase) text instead, since colBase would be unreadable on
// their light backgrounds. confirmButtonLabel itself is left untouched — the
// verify-success confirm row still shares it.
func (m model) assistedFooterButtonLabel(label, kind, accent string, focused bool) string {
	st := lipgloss.NewStyle().Padding(0, confirmButtonPad)
	if m.hintMode {
		// Hint mode greys the screen: footer buttons take the muted fill (like
		// the pills' inverted treatment) so the overlapping letter chips are the
		// only color. The focus highlight is suppressed while dimmed — hint
		// letters, not focus, select a button in this mode. Same geometry.
		return st.
			Foreground(lipgloss.Color(colSubtext)).
			Background(lipgloss.Color(colSurface0)).
			Render(label)
	}
	if m.flashKey == "assist:"+kind {
		return st.Foreground(lipgloss.Color(colFlashOn)).Bold(true).Render(label)
	}
	if focused {
		fg := colBase
		if accent == colOverlay1 {
			fg = colWhite
		}
		return st.
			Foreground(lipgloss.Color(fg)).
			Background(lipgloss.Color(accent)).
			Bold(true).Render(label)
	}
	return st.
		Foreground(lipgloss.Color(colSubtext0)).
		Background(lipgloss.Color(colSurface1)).
		Render(label)
}

// assistedFooterButtonsRowString builds the styled BUTTONS row via
// assistedFooterButtonLabel (NOT confirmButtonLabel — that primitive always
// highlights the focused button green, which would read as "success" on the
// normal "step" footer) — only the footer's OWN focus index (footerFocus)
// drives the highlight, independent of confirmFocus. Left-aligned at the
// content edge, same indent/gap constants as the confirm buttons row so the
// layout reads consistently.
func (m model) assistedFooterButtonsRowString() string {
	btns := m.assistedFooterButtons()
	labels := make([]string, len(btns))
	for i, b := range btns {
		labels[i] = m.assistedFooterButtonLabel(b.Label, b.Kind, b.Accent, i == m.footerFocus)
	}
	return strings.Repeat(" ", confirmButtonIndent) + strings.Join(labels, strings.Repeat(" ", confirmButtonGap))
}

// assistedFooterRowString combines the context line and the buttons row,
// "\n"-joined (mirroring how confirmRowString's wrapped lines get split by
// confirmQuestionRows) — View()/normalLines splits this back into its two rows
// so the buttons stay pinned on their own screen row. Returns "" when inactive.
func (m model) assistedFooterRowString() string {
	if !m.assistedFooterActive() {
		return ""
	}
	return m.assistedFooterContextRowString() + "\n" + m.assistedFooterButtonsRowString()
}

// assistedFooterRows splits assistedFooterRowString into its two visual rows
// (context, buttons), or nil when the footer isn't active.
func (m model) assistedFooterRows() []string {
	if !m.assistedFooterActive() {
		return nil
	}
	return strings.Split(m.assistedFooterRowString(), "\n")
}

// assistedFooterLines is the number of EXTRA bottom rows body() must reserve
// beyond the single bottom-pad row already counted in its base formula —
// mirroring confirmQuestionLines()+3 (blank, N question lines, blank, buttons,
// blank replacing the one already-reserved pad). The footer's context line is
// always exactly 1 row (no wrapping), so the block is always blank+context(1)+
// blank+buttons+blank = 5 rows, net +4 over the base pad. 0 when inactive.
func (m model) assistedFooterLines() int {
	if !m.assistedFooterActive() {
		return 0
	}
	return 4
}

// assistedFooterScreenRow returns the absolute screen row the footer BUTTONS
// row occupies — pinned at m.height-3, exactly like confirmButtonsScreenRow,
// so a mouse click hit-test and the painted row always agree regardless of
// how the context line's content changes. -1 when the footer isn't shown.
func (m model) assistedFooterScreenRow() int {
	if !m.assistedFooterActive() {
		return -1
	}
	return m.height - 3
}

// assistedActivate dispatches one footer button by its Kind (e.g. "assist-run")
// to the underlying action — the keyboard Enter/Space handler and the mouse
// click handler both funnel through here so the two input paths can never
// drift apart.
func (m model) assistedActivate(kind string) (model, tea.Cmd) {
	switch kind {
	case "assist-run":
		b := Button{Kind: "run", Payload: m.blockCommand(m.readyID), BlockID: m.readyID}
		m.assistedFooter = "" // hide the footer while the step runs — the block's own spinner takes over
		return m.runOrGate(b)
	case "assist-skip":
		m = m.assistedSkip()
		m.reflow()
		return m, nil
	case "assist-rollback":
		m.assistedFooter = ""
		m.exitCode = 0 // the failure is being resolved by rolling back
		mm, cmd := m.beginRollback(m.assistedFailedID)
		mm.assistedFooter = "done"
		return mm, cmd
	case "assist-leave":
		// exitCode stays whatever it already is (1, from the failure) — leaving the
		// failure as-is does not resolve it.
		return m, tea.Quit
	case "assist-quit":
		// exit uses whatever m.exitCode already holds (0 unless an unresolved
		// failure set it to 1).
		return m, tea.Quit
	}
	return m, nil
}

// appendAssistedFooter registers one Screen-fixed Button per footer button on
// the BUTTONS row (mirroring appendConfirmButtons) so a mouse click can resolve
// them (input wiring lands in a later Plan 2 task). Left-aligned at the content
// edge, same indent/gap the renderer (assistedFooterButtonsRowString) draws
// with, so the click columns land exactly on the drawn cells.
func (m *model) appendAssistedFooter() {
	if !m.assistedFooterActive() {
		return
	}
	row := m.assistedFooterScreenRow()
	if row < 0 {
		return
	}
	col := confirmButtonIndent
	for _, b := range m.assistedFooterButtons() {
		cellW := lipgloss.Width(b.Label) + 2*confirmButtonPad
		m.buttons = append(m.buttons, Button{
			Line: row, Col: col, Width: cellW, Kind: b.Kind, BlockID: "assist", Screen: true,
		})
		col += cellW + confirmButtonGap
	}
}
