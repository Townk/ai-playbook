package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// saveConfirmMsg is the resolution of the "save unverified run?" confirm overlay
// raised by the `w` handler when the verify block has not passed. ok=true means
// the user chose to save anyway; ok=false means they cancelled.
type saveConfirmMsg struct{ ok bool }

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
		return m.saveDecision()
	}
	return nil
}
