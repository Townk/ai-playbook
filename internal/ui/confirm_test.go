package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Townk/ai-playbook/internal/reengage"
)

// Stage 2 (spec §A): a verify exit-0 result sets the NATIVE confirm state ONCE
// (rendering an inline row, no auto agent-ask wrap-up); a second verify-0 must not
// re-prompt. No re-engagement cmd is fired on the result itself — the confirm is
// answered by y/n/click.
func TestVerifySuccessSetsConfirmOnce(t *testing.T) {
	m, fe := newReengageEventsModel(t, "# resolved?\n", "## Solution\ndone\n")
	m.md = "# Playbook\n\n```bash {id=verify}\nmake build\n```\n"
	m.reflow()
	if !m.canReengageInProc() {
		t.Fatal("setup: expected in-process re-engagement")
	}

	// First verify exit 0 → confirm state set; NO agent re-engagement fired yet.
	nm, cmd := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)
	// The only cmd here must be the hide-cursor re-assert (the confirm row repaints),
	// never an agent re-engagement / generation. fe.calls / thinking below confirm
	// no generation fired.
	if !hasHide(collectRawSeqs(t, cmd)) {
		t.Errorf("verify exit 0 must re-assert the hide-cursor and NOT fire a re-engagement, got %T", cmd)
	}
	if !m.confirmResolved {
		t.Fatal("verify exit 0 must set the native confirmResolved state")
	}
	if !m.wrappedUp {
		t.Fatal("verify exit 0 must set wrappedUp (once-guard)")
	}
	if m.thinking {
		t.Error("setting the confirm must NOT set thinking=true (no generation yet)")
	}
	if fe.calls != 0 {
		t.Errorf("no agent call must happen on the confirm prompt itself, got %d", fe.calls)
	}

	// Second verify exit 0 → must NOT re-set the confirm (once per resolution). Clear
	// the first confirm to detect a spurious re-set.
	m.confirmResolved = false
	nm2, cmd2 := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm2.(model)
	if cmd2 != nil {
		t.Errorf("a second verify exit 0 must NOT re-trigger, got %T", cmd2)
	}
	if m.confirmResolved {
		t.Error("a second verify exit 0 must NOT re-set the confirm state")
	}
}

// Stage 2 (spec §A): the confirm row + its [ Yes ] [ No ] buttons render in the
// pager pane on a verify-success.
func TestVerifySuccessRendersConfirmRow(t *testing.T) {
	m, _ := newReengageEventsModel(t, "# resolved?\n", "# Playbook\n")
	m.md = "# Playbook\n\n```bash {id=verify}\nmake build\n```\n"
	m.width = 100 // wide enough that the prompt fits on a single line (no wrap split)
	m.reflow()

	nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)
	if !m.confirmResolved {
		t.Fatal("verify exit 0 must set confirmResolved")
	}
	view := strip(m.viewString())
	if !strings.Contains(view, "Generate a playbook for this solution?") {
		t.Errorf("confirm prompt prose missing from view:\n%s", view)
	}
	if !strings.Contains(view, confirmYesLabel) || !strings.Contains(view, confirmNoLabel) {
		t.Errorf("confirm Yes/No buttons missing from view:\n%s", view)
	}
	// The two Screen-fixed confirm buttons must be registered for click hit-testing.
	var yes, no bool
	for _, b := range m.buttons {
		if b.Kind == "confirm-yes" {
			yes = true
		}
		if b.Kind == "confirm-no" {
			no = true
		}
	}
	if !yes || !no {
		t.Errorf("confirm buttons not registered: yes=%v no=%v", yes, no)
	}
}

// fix(ui): the confirm renders the QUESTION prose (wrapped to the pane's content width)
// on its OWN row(s) and the Yes / No buttons on a SEPARATE row directly below it. The
// buttons must NOT share the question line(s), and they stay pinned at m.height-3 no
// matter how many lines the question wraps to. Covered at a WIDE width (single question
// line) and a NARROW width (the question wraps to 2+ lines, none overflowing the pane).
func TestConfirmRendersButtonsOnSeparateRow(t *testing.T) {
	newConfirm := func(width int) model {
		m, _ := newReengageEventsModel(t, "# Playbook\n", "# Playbook\n")
		m.md = "# Playbook\n\n```bash {id=verify}\nmake build\n```\n"
		m.width = width
		m.reflow()
		nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
		m = nm.(model)
		if !m.confirmResolved {
			t.Fatal("setup: confirm not set")
		}
		return m
	}
	blank := func(s string) bool { return strings.TrimSpace(s) == "" }
	buttonsRow := func(lines []string) int {
		for i, ln := range lines {
			if strings.Contains(ln, confirmYesLabel) && strings.Contains(ln, confirmNoLabel) {
				return i
			}
		}
		return -1
	}

	// The question row(s) never carry the button labels; the buttons row carries both.
	assertSplit := func(m model) {
		t.Helper()
		for _, q := range m.confirmQuestionRows() {
			if strings.Contains(strip(q), confirmYesLabel) || strings.Contains(strip(q), confirmNoLabel) {
				t.Errorf("buttons must NOT be on a question row, got %q", strip(q))
			}
		}
		btns := strip(m.confirmButtonsRowString())
		if !strings.Contains(btns, confirmYesLabel) || !strings.Contains(btns, confirmNoLabel) {
			t.Errorf("buttons row must carry both labels, got %q", btns)
		}
	}

	// WIDE: the prompt fits on ONE line. Layout: blank, question, blank, buttons, blank.
	t.Run("wide single line", func(t *testing.T) {
		m := newConfirm(100)
		if got := m.confirmQuestionLines(); got != 1 {
			t.Fatalf("wide width must keep the question on a single line, got %d", got)
		}
		assertSplit(m)
		lines := strings.Split(strip(m.viewString()), "\n")
		promptLine := -1
		for i, ln := range lines {
			if strings.Contains(ln, "Generate a playbook for this solution?") {
				promptLine = i
			}
		}
		buttonLine := buttonsRow(lines)
		if promptLine < 0 || buttonLine < 0 {
			t.Fatalf("could not find both rows: prompt=%d buttons=%d", promptLine, buttonLine)
		}
		if buttonLine != m.height-3 {
			t.Errorf("buttons must render on m.height-3 (%d), got %d", m.height-3, buttonLine)
		}
		// Layout: blank(prompt-1), prompt, blank(prompt+1), buttons(prompt+2), blank, status.
		if buttonLine != promptLine+2 {
			t.Errorf("buttons must sit two rows below the question (a blank between): prompt=%d buttons=%d", promptLine, buttonLine)
		}
		if promptLine-1 < 0 || !blank(lines[promptLine-1]) {
			t.Errorf("a blank row must sit ABOVE the question (row %d)", promptLine-1)
		}
		if !blank(lines[promptLine+1]) {
			t.Errorf("a blank row must sit BETWEEN the question and the buttons (row %d)", promptLine+1)
		}
		if buttonLine+1 >= len(lines) || !blank(lines[buttonLine+1]) {
			t.Errorf("a blank row must sit BELOW the buttons (row %d)", buttonLine+1)
		}
	})

	// NARROW: the prompt WRAPS to 2+ lines, each fitting inside the content width (no
	// overflow past the right margin), and the buttons stay pinned at m.height-3.
	t.Run("narrow wraps", func(t *testing.T) {
		m := newConfirm(60)
		n := m.confirmQuestionLines()
		if n < 2 {
			t.Fatalf("narrow width must wrap the question to 2+ lines, got %d", n)
		}
		assertSplit(m)
		// Each wrapped question row fits within the content inner width — nothing runs to
		// the pane's right edge (a trailing margin is left).
		for i, q := range m.confirmQuestionRows() {
			if w := lipgloss.Width(q); w > m.contentWidth() {
				t.Errorf("wrapped question row %d width %d overflows content width %d", i, w, m.contentWidth())
			}
		}
		lines := strings.Split(strip(m.viewString()), "\n")
		buttonLine := buttonsRow(lines)
		if buttonLine != m.height-3 {
			t.Errorf("buttons must stay pinned on m.height-3 (%d) even when the question wraps, got %d", m.height-3, buttonLine)
		}
		// The N question lines occupy m.height-4-N .. m.height-5, with a blank above the
		// first and the blank/buttons/blank fixed below.
		firstQ := m.height - 4 - n
		if firstQ-1 < 0 || !blank(lines[firstQ-1]) {
			t.Errorf("a blank row must sit ABOVE the first question line (row %d)", firstQ-1)
		}
		if !blank(lines[m.height-4]) {
			t.Errorf("a blank row must sit BETWEEN the question and the buttons (row %d)", m.height-4)
		}
		if !blank(lines[m.height-2]) {
			t.Errorf("a blank row must sit BELOW the buttons (row %d)", m.height-2)
		}
		for r := firstQ; r <= m.height-5; r++ {
			if blank(lines[r]) {
				t.Errorf("question row %d must carry prose, got blank", r)
			}
		}
	})
}

// fix(ui): the confirm buttons register on the BUTTONS row (m.height-2), left-aligned
// at the content edge — Yes at the left, No after it by the shared gap. The drawn
// label positions (content col 0 → screen col 2 under the 2-col margin) must match
// the registered click cells.
func TestAppendConfirmButtonsLeftAligned(t *testing.T) {
	m, _ := newReengageEventsModel(t, "# Playbook\n", "# Playbook\n")
	m.md = "# Playbook\n\n```bash {id=verify}\nmake build\n```\n"
	m.reflow()
	nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)

	var yes, no Button
	var fy, fn bool
	for _, b := range m.buttons {
		switch b.Kind {
		case "confirm-yes":
			yes, fy = b, true
		case "confirm-no":
			no, fn = b, true
		}
	}
	if !fy || !fn {
		t.Fatal("confirm buttons not registered")
	}
	wantRow := m.height - 3
	if yes.Line != wantRow || no.Line != wantRow {
		t.Errorf("buttons must be on the buttons row %d: yes.Line=%d no.Line=%d", wantRow, yes.Line, no.Line)
	}
	if yes.Col != confirmButtonIndent {
		t.Errorf("Yes must be left-aligned at content edge %d, got %d", confirmButtonIndent, yes.Col)
	}
	// Each button cell is the label width plus the Padding(0, confirmButtonPad) on both
	// sides; No follows the Yes cell plus the shared gap.
	wantYesW := len(confirmYesLabel) + 2*confirmButtonPad
	if yes.Width != wantYesW {
		t.Errorf("Yes cell width must include the padding: got %d want %d", yes.Width, wantYesW)
	}
	if no.Width != len(confirmNoLabel)+2*confirmButtonPad {
		t.Errorf("No cell width must include the padding: got %d want %d", no.Width, len(confirmNoLabel)+2*confirmButtonPad)
	}
	wantNoCol := confirmButtonIndent + wantYesW + confirmButtonGap
	if no.Col != wantNoCol {
		t.Errorf("No col must follow the Yes cell by the shared gap: got %d want %d", no.Col, wantNoCol)
	}
	// A click at each button's drawn cell (content col + 2-col margin) round-trips
	// through buttonAt back to the same button.
	if got, ok := buttonAt(m.buttons, yes.Col+2, yes.Line, m.yOff, m.bodyTop()); !ok || got.Kind != "confirm-yes" {
		t.Fatalf("buttonAt at the Yes cell must hit Yes: ok=%v kind=%q", ok, got.Kind)
	}
	if got, ok := buttonAt(m.buttons, no.Col+2, no.Line, m.yOff, m.bodyTop()); !ok || got.Kind != "confirm-no" {
		t.Fatalf("buttonAt at the No cell must hit No: ok=%v kind=%q", ok, got.Kind)
	}
}

// fix(ui): the button columns are INDEPENDENT of the prompt width. The old layout put
// Yes at width(prompt)+2 (and No further right), so a long prompt pushed the buttons
// toward / past the pane edge — unreachable. The new left-aligned layout keeps both
// buttons at the content edge, well to the LEFT of the old width(prompt)+2 position,
// regardless of the prompt mode (fresh / amend).
func TestConfirmButtonColsIndependentOfPromptWidth(t *testing.T) {
	colsFor := func(servedBase string) (yesCol, noCol, promptW int) {
		m, _ := newReengageEventsModel(t, "# Playbook\n", "# Playbook\n")
		m.md = "# Playbook\n\n```bash {id=verify}\nmake build\n```\n"
		m.servedBase = servedBase
		m.reflow()
		nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
		m = nm.(model)
		promptW = len(strip(m.confirmRowString()))
		for _, b := range m.buttons {
			if b.Kind == "confirm-yes" {
				yesCol = b.Col
			}
			if b.Kind == "confirm-no" {
				noCol = b.Col
			}
		}
		return
	}
	fy, fn, fw := colsFor("")                    // fresh prompt
	ay, an, aw := colsFor("# served playbook\n") // amend prompt
	wantNoCol := confirmButtonIndent + len(confirmYesLabel) + 2*confirmButtonPad + confirmButtonGap
	for _, c := range []struct {
		name             string
		yes, no, promptW int
	}{{"fresh", fy, fn, fw}, {"amend", ay, an, aw}} {
		if c.yes != confirmButtonIndent {
			t.Errorf("%s: Yes must stay at the left edge %d, got %d", c.name, confirmButtonIndent, c.yes)
		}
		if c.no != wantNoCol {
			t.Errorf("%s: No col must be the shared-constant value %d, got %d", c.name, wantNoCol, c.no)
		}
		// The OLD layout would have put Yes at promptW+2 / No further right; the new
		// columns sit well to the left of that, so the prompt width can't push them off.
		if c.no >= c.promptW {
			t.Errorf("%s: No col %d must be left of the old width(prompt)+2 position (promptW=%d)", c.name, c.no, c.promptW)
		}
	}
	// Independence: the columns are identical across the two prompt modes.
	if fy != ay || fn != an {
		t.Errorf("button cols must not change with the prompt: fresh=(%d,%d) amend=(%d,%d)", fy, fn, ay, an)
	}
}

// fix(ui): a mouse click at the drawn No button cell DISMISSES the confirm (the command
// already succeeded — nothing to re-fix) and does NOT start a generation. Yes-by-click is
// covered by TestConfirmResolvesByClick; this covers No and the drawn-cell round-trip.
func TestConfirmClickNoResolves(t *testing.T) {
	m, _ := newReengageEventsModel(t, "# Playbook\n", "# Playbook\nclean\n")
	m.md = "# Troubleshoot\n\n```bash {id=verify}\nmake build\n```\n"
	m.reflow()
	nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)
	if !m.confirmResolved {
		t.Fatal("setup: confirm not set")
	}
	var no Button
	var found bool
	for _, b := range m.buttons {
		if b.Kind == "confirm-no" {
			no, found = b, true
		}
	}
	if !found {
		t.Fatal("confirm-no button not registered")
	}
	nm2, _ := m.Update(tea.MouseClickMsg{X: no.Col + 2, Y: no.Line, Button: tea.MouseLeft})
	m = nm2.(model)
	if m.confirmResolved {
		t.Error("clicking No must dismiss (clear) the confirm state")
	}
	if m.finalDraft {
		t.Error("clicking No must NOT start a playbook generation")
	}
}

// Stage 2 (spec §A): answering "Yes" (the `y` key) when a follow-up diverged the run
// (hadFollowup=true) re-authors the FINAL-PLAYBOOK in REPLACE mode as a DRAFT — the
// producer is called with KindReengageFinalPlaybook, the rendered content is reset,
// thinking starts, and finalDraft is set / committed stays false.
func TestConfirmYesGeneratesFinalPlaybookReplaceDraft(t *testing.T) {
	m, fe := newReengageEventsModel(t, "# Playbook — fix\n\n```bash {id=verify}\nclean playbook\n```\n", "# Playbook — fix\n\n```bash {id=verify}\nclean playbook\n```\n")
	troubleshoot := "# Troubleshoot\n\n```bash {id=verify}\nmake build\n```\n"
	m.md = troubleshoot
	m.hadFollowup = true // diverged run → re-author path
	m.reflow()

	nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)
	if !m.confirmResolved {
		t.Fatal("setup: confirm not set")
	}

	nm2, cmd := m.Update(key("y"))
	m = nm2.(model)
	if cmd == nil {
		t.Fatal("confirm Yes (y) must trigger the final-playbook generation")
	}
	if m.confirmResolved {
		t.Error("answering Yes must clear the confirm state")
	}
	// REPLACE: the troubleshoot content was reset on the trigger.
	if m.md != "" {
		t.Errorf("Yes must REPLACE (reset m.md), got %q", m.md)
	}
	if !m.thinking {
		t.Error("Yes must set thinking=true (generation in flight)")
	}
	if !m.finalDraft {
		t.Error("Yes must mark the result a finalDraft")
	}
	if m.committed {
		t.Error("stage 2 must NOT commit the draft (committed stays false)")
	}

	// Drain the generation so the producer is invoked and we can assert the kind/change.
	m = pumpReArm(t, m, cmd)
	if fe.calls != 1 {
		t.Fatalf("producer calls = %d, want 1", fe.calls)
	}
	if fe.gotKind != reengage.KindReengageFinalPlaybook {
		t.Errorf("producer kind = %v, want KindReengageFinalPlaybook", fe.gotKind)
	}
	if fe.gotBase != "" {
		t.Errorf("stage 2 is fresh-only: base must be empty, got %q", fe.gotBase)
	}
	if fe.gotChange != troubleshoot {
		t.Errorf("Yes must thread the troubleshoot content as the change, got %q", fe.gotChange)
	}
	if !strings.Contains(m.md, "clean playbook") {
		t.Errorf("the clean playbook must stream into m.md, got %q", m.md)
	}
}

// Answering "No" (the `n` key) DISMISSES the confirm and does nothing else: the command
// already succeeded, so there is no follow-up and no generation. The user can still
// press `c` to bring the confirm back (covered by TestCKeyReshowsConfirmAfterNoDismiss).
func TestConfirmNoDismisses(t *testing.T) {
	m, fe := newReengageEventsModel(t, "# Revised\n", "# Revised fix\n")
	m.md = "# Troubleshoot\n\n```bash {id=verify}\nmake build\n```\n"
	m.reflow()

	nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)
	if !m.confirmResolved {
		t.Fatal("setup: confirm not set")
	}
	originalMd := m.md

	nm2, cmd := m.Update(key("n"))
	m = nm2.(model)
	if cmd != nil {
		t.Fatal("confirm No (n) must NOT trigger a follow-up or generation (dismiss only)")
	}
	if m.confirmResolved {
		t.Error("answering No must clear the confirm state")
	}
	if m.finalDraft {
		t.Error("No must NOT mark a finalDraft")
	}
	if m.thinking || m.streaming {
		t.Error("No must not start any stream")
	}
	// Content is untouched (no APPEND, no REPLACE).
	if m.md != originalMd {
		t.Errorf("No must leave the content untouched, got %q want %q", m.md, originalMd)
	}
	if fe.calls != 0 {
		t.Errorf("No must not invoke the producer, got %d calls", fe.calls)
	}
}

// Stage 4 (spec §C amend-on-rerun): when the session is SERVING an existing playbook
// (m.servedBase set, a cache HIT), a verify-success confirm → Yes AMENDS the served
// playbook — the producer is called with base==servedBase and change==the troubleshoot
// content (which carries the resolved fix). The served playbook is the base; the
// output is base+fix, re-cached under the same keys on `w` (never lost).
func TestConfirmYesAmendsServedPlaybook(t *testing.T) {
	m, fe := newReengageEventsModel(t, "# Playbook — amended\nbase + fix\n", "# Playbook — amended\nbase + fix\n")
	served := "# Playbook — My Setup\n\nstep 1\nstep 2\n"
	troubleshoot := "# Troubleshoot\n\n```bash {id=verify}\nmake build\n```\n"
	m.servedBase = served // serving an existing playbook for this context
	m.md = troubleshoot
	m.hadFollowup = true // diverged run → re-author (amend) path
	m.reflow()

	nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)
	if !m.confirmResolved {
		t.Fatal("setup: confirm not set")
	}

	nm2, cmd := m.Update(key("y"))
	m = nm2.(model)
	if cmd == nil {
		t.Fatal("confirm Yes must trigger the amend generation")
	}
	if !m.finalDraft || m.committed {
		t.Errorf("amend must mark a draft (finalDraft=%v committed=%v)", m.finalDraft, m.committed)
	}

	m = pumpReArm(t, m, cmd)
	if fe.calls != 1 {
		t.Fatalf("producer calls = %d, want 1", fe.calls)
	}
	if fe.gotKind != reengage.KindReengageFinalPlaybook {
		t.Errorf("producer kind = %v, want KindReengageFinalPlaybook", fe.gotKind)
	}
	if fe.gotBase != served {
		t.Errorf("AMEND must thread the served playbook as base, got %q want %q", fe.gotBase, served)
	}
	if fe.gotChange != troubleshoot {
		t.Errorf("AMEND must thread the troubleshoot content as change, got %q", fe.gotChange)
	}
}

// Stage 4 (spec §C): without a served base (a FRESH troubleshoot / cache MISS), the
// verify-success confirm → Yes generates a FRESH playbook (base==""), unchanged from
// stage 2. This is the amend-vs-fresh branch scoped by whether a playbook was served.
func TestConfirmYesFreshWhenNoServedBase(t *testing.T) {
	m, fe := newReengageEventsModel(t, "# Playbook — fresh\nnew\n", "# Playbook — fresh\nnew\n")
	troubleshoot := "# Troubleshoot\n\n```bash {id=verify}\nmake build\n```\n"
	m.servedBase = "" // FRESH: no playbook served
	m.md = troubleshoot
	m.hadFollowup = true // diverged run → re-author (fresh) path
	m.reflow()

	nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)
	if !m.confirmResolved {
		t.Fatal("setup: confirm not set")
	}

	nm2, cmd := m.Update(key("y"))
	m = nm2.(model)
	if cmd == nil {
		t.Fatal("confirm Yes must trigger the fresh generation")
	}
	m = pumpReArm(t, m, cmd)
	if fe.gotKind != reengage.KindReengageFinalPlaybook {
		t.Errorf("producer kind = %v, want KindReengageFinalPlaybook", fe.gotKind)
	}
	if fe.gotBase != "" {
		t.Errorf("FRESH must thread an empty base, got %q", fe.gotBase)
	}
	if fe.gotChange != troubleshoot {
		t.Errorf("FRESH must thread the troubleshoot content as change, got %q", fe.gotChange)
	}
}

// Stage 4 (spec §C): the confirm wording differs by mode — amend prose when serving
// an existing playbook (servedBase set), fresh prose otherwise.
func TestConfirmWordingByMode(t *testing.T) {
	// Fresh: no served base → "Generate a playbook for this solution?".
	mf, _ := newReengageEventsModel(t, "# x\n", "# x\n")
	mf.md = "# Troubleshoot\n\n```bash {id=verify}\nmake build\n```\n"
	mf.servedBase = ""
	mf.width = 100 // wide enough to keep the prose on a single line (assert the full string)
	mf.reflow()
	nmf, _ := mf.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	mf = nmf.(model)
	freshView := strip(mf.viewString())
	if !strings.Contains(freshView, "Generate a playbook for this solution?") {
		t.Errorf("fresh confirm must read the fresh prose:\n%s", freshView)
	}
	if strings.Contains(freshView, "Update the playbook with this solution?") {
		t.Errorf("fresh confirm must NOT show the amend prose:\n%s", freshView)
	}

	// Amend: served base → "Update the playbook with this solution?".
	ma, _ := newReengageEventsModel(t, "# x\n", "# x\n")
	ma.md = "# Troubleshoot\n\n```bash {id=verify}\nmake build\n```\n"
	ma.servedBase = "# Playbook — served\n\nstep\n"
	ma.width = 100 // wide enough to keep the prose on a single line (assert the full string)
	ma.reflow()
	nma, _ := ma.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	ma = nma.(model)
	amendView := strip(ma.viewString())
	if !strings.Contains(amendView, "Update the playbook with this solution?") {
		t.Errorf("amend confirm must read the amend prose:\n%s", amendView)
	}
	if strings.Contains(amendView, "Generate a playbook for this solution?") {
		t.Errorf("amend confirm must NOT show the fresh prose:\n%s", amendView)
	}
	// The amend confirm's Yes/No buttons must still render + register for clicks.
	if !strings.Contains(amendView, confirmYesLabel) || !strings.Contains(amendView, confirmNoLabel) {
		t.Errorf("amend confirm Yes/No buttons missing:\n%s", amendView)
	}
}

// Stage 2 (spec §A): a mouse click on the [ Yes ] / [ No ] buttons resolves the
// confirm exactly like the y/n keys.
func TestConfirmResolvesByClick(t *testing.T) {
	m, fe := newReengageEventsModel(t, "# Playbook\n", "# Playbook\nclean\n")
	m.md = "# Troubleshoot\n\n```bash {id=verify}\nmake build\n```\n"
	m.hadFollowup = true // diverged run → re-author path (generation expected)
	m.reflow()

	nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)
	if !m.confirmResolved {
		t.Fatal("setup: confirm not set")
	}
	// Locate the registered Yes button and click its center cell (+2 for the left margin).
	var yes Button
	var found bool
	for _, b := range m.buttons {
		if b.Kind == "confirm-yes" {
			yes = b
			found = true
		}
	}
	if !found {
		t.Fatal("confirm-yes button not registered")
	}
	clickX := yes.Col + 2 // buttonAt strips the 2-col left margin
	clickY := yes.Line    // Screen button: absolute screen row
	nm2, cmd := m.Update(tea.MouseClickMsg{X: clickX, Y: clickY, Button: tea.MouseLeft})
	m = nm2.(model)
	if cmd == nil {
		t.Fatal("clicking Yes must trigger the final-playbook generation")
	}
	if m.confirmResolved {
		t.Error("clicking Yes must clear the confirm state")
	}
	if !m.finalDraft {
		t.Error("clicking Yes must mark a finalDraft")
	}
	m = pumpReArm(t, m, cmd)
	if fe.gotKind != reengage.KindReengageFinalPlaybook {
		t.Errorf("click Yes kind = %v, want KindReengageFinalPlaybook", fe.gotKind)
	}
}

// Stage 4b (spec §D): confirm-Yes with no follow-up → saveDecision → immediate
// commit (the rendered playbook IS the result; no re-author needed). The commit cmd
// fires right out of the key handler; driving the playbookCommittedMsg flips committed.
func TestConfirmYesNoFollowupPersistsImmediately(t *testing.T) {
	m, _ := newReengageEventsModel(t, "# Playbook — fix\n\n```bash {id=verify}\nclean playbook\n```\n", "# Playbook — fix\n\n```bash {id=verify}\nclean playbook\n```\n")
	m.md = "# Troubleshoot\n\n```bash {id=verify}\nmake build\n```\n"
	m.reflow()

	nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)
	if !m.confirmResolved {
		t.Fatal("setup: confirm not set")
	}
	// hadFollowup defaults false → saveDecision takes the commit path.
	nm2, cmd := m.Update(key("y"))
	m = nm2.(model)
	if cmd == nil {
		t.Fatal("confirm Yes (no followup) must return the commit cmd")
	}
	// No generation started — streaming stays false.
	if m.streaming {
		t.Error("confirm Yes (no followup) must NOT start a re-author generation")
	}
	// Drive the commit cmd and verify committed flips.
	pc, ok := cmd().(playbookCommittedMsg)
	if !ok {
		t.Fatalf("confirm Yes (no followup) cmd must yield a playbookCommittedMsg, got %T", cmd())
	}
	if pc.err != nil {
		t.Fatalf("CommitPlaybook must succeed, got %v", pc.err)
	}
	nm3, _ := m.Update(pc)
	m = nm3.(model)
	if !m.committed {
		t.Error("commit result must flip committed=true")
	}
	if !strings.Contains(m.status, "saved playbook") {
		t.Errorf("commit result must show the saved path, got %q", m.status)
	}
}

// Issue #4: the verify-success confirm row is keyboard-focusable. Default focus is
// Yes (confirmFocus==0); ←/→ (and h/l, Tab) move focus; Enter/Space select the
// focused button; y/n still resolve directly regardless of focus.
func TestConfirmFocusDefaultsToYes(t *testing.T) {
	m, _ := newReengageEventsModel(t, "# Playbook\n", "# Playbook\nclean\n")
	m.md = "# Troubleshoot\n\n```bash {id=verify}\nmake build\n```\n"
	m.reflow()

	nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)
	if !m.confirmResolved {
		t.Fatal("setup: confirm not set")
	}
	if m.confirmFocus != 0 {
		t.Errorf("default confirm focus must be Yes (0), got %d", m.confirmFocus)
	}
}
func TestConfirmFocusArrowsMove(t *testing.T) {
	m, _ := newReengageEventsModel(t, "# Playbook\n", "# Playbook\nclean\n")
	m.md = "# Troubleshoot\n\n```bash {id=verify}\nmake build\n```\n"
	m.reflow()
	nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)

	// → moves focus to No.
	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m = nm.(model)
	if m.confirmFocus != 1 {
		t.Errorf("right arrow must focus No (1), got %d", m.confirmFocus)
	}
	// ← moves focus back to Yes.
	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	m = nm.(model)
	if m.confirmFocus != 0 {
		t.Errorf("left arrow must focus Yes (0), got %d", m.confirmFocus)
	}
	// l (vim) focuses No; h focuses Yes.
	nm, _ = m.Update(key("l"))
	m = nm.(model)
	if m.confirmFocus != 1 {
		t.Errorf("l must focus No (1), got %d", m.confirmFocus)
	}
	nm, _ = m.Update(key("h"))
	m = nm.(model)
	if m.confirmFocus != 0 {
		t.Errorf("h must focus Yes (0), got %d", m.confirmFocus)
	}
	// Tab toggles.
	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = nm.(model)
	if m.confirmFocus != 1 {
		t.Errorf("tab must toggle focus to No (1), got %d", m.confirmFocus)
	}
}

// Enter selects the FOCUSED button: with focus on No, Enter must DISMISS the confirm
// (No = dismiss now), not generate or follow up.
func TestConfirmEnterSelectsFocused(t *testing.T) {
	m, fe := newReengageEventsModel(t, "# delta\n", "# Playbook\nclean\n")
	m.md = "# Troubleshoot\n\n```bash {id=verify}\nmake build\n```\n"
	m.reflow()
	nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)

	// Move focus to No, then Enter → dismiss (no follow-up, no generation).
	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m = nm.(model)
	nm2, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = nm2.(model)
	if cmd != nil {
		t.Fatal("Enter on focused No must dismiss (no follow-up, no generation)")
	}
	if m.confirmResolved {
		t.Error("Enter must clear the confirm state")
	}
	if m.finalDraft {
		t.Error("Enter on No must NOT mark a finalDraft (that's the Yes path)")
	}
	if fe.calls != 0 {
		t.Errorf("Enter on No must not invoke the producer, got %d calls", fe.calls)
	}
}

// Enter on the DEFAULT focus (Yes) re-authors when the run diverged; Space also
// selects the focused button.
func TestConfirmSpaceSelectsFocusedYes(t *testing.T) {
	m, fe := newReengageEventsModel(t, "# Playbook\nclean\n", "# Playbook\nclean\n")
	m.md = "# Troubleshoot\n\n```bash {id=verify}\nmake build\n```\n"
	m.hadFollowup = true // diverged run → re-author path (generation expected)
	m.reflow()
	nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)
	nm2, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	m = nm2.(model)
	if cmd == nil {
		t.Fatal("Space on focused Yes must trigger the final-playbook generation")
	}
	if !m.finalDraft {
		t.Error("Space on Yes must mark a finalDraft")
	}
	m = pumpReArm(t, m, cmd)
	if fe.gotKind != reengage.KindReengageFinalPlaybook {
		t.Errorf("Space-on-Yes kind = %v, want KindReengageFinalPlaybook", fe.gotKind)
	}
}

// The y/n direct shortcuts still resolve regardless of the focus position.
func TestConfirmYNStillWorkWithFocus(t *testing.T) {
	m, fe := newReengageEventsModel(t, "# Playbook\nclean\n", "# Playbook\nclean\n")
	m.md = "# Troubleshoot\n\n```bash {id=verify}\nmake build\n```\n"
	m.hadFollowup = true // diverged run → re-author path (generation expected)
	m.reflow()
	nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)
	// Focus is on No, but `y` must still resolve Yes (final playbook).
	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m = nm.(model)
	nm2, cmd := m.Update(key("y"))
	m = nm2.(model)
	if cmd == nil {
		t.Fatal("y must resolve Yes regardless of focus")
	}
	if !m.finalDraft {
		t.Error("y must mark a finalDraft (Yes path)")
	}
	m = pumpReArm(t, m, cmd)
	if fe.gotKind != reengage.KindReengageFinalPlaybook {
		t.Errorf("y kind = %v, want KindReengageFinalPlaybook", fe.gotKind)
	}
}

// The focus-navigation keys must be captured ONLY while the confirm is active: with
// no confirm, ←/→/h/l still scroll and space enters hint mode (not swallowed).
func TestConfirmKeysInertWhenNoConfirm(t *testing.T) {
	// Tall content so vertical scroll has headroom; a runnable block so space → hint.
	// Blank-line-separated paragraphs render as distinct body lines (a markdown
	// paragraph collapses consecutive non-blank lines, which wouldn't overflow).
	m := newModel("T", "```bash\nmake build\n```\n"+strings.Repeat("body paragraph\n\n", 60))
	m.width, m.height = 80, 24
	m.reflow()
	if m.confirmResolved {
		t.Fatal("setup: confirm must not be active")
	}
	// space must enter hint mode over the visible run button — NOT be swallowed as a
	// confirm selection (which only happens while the confirm row is active). Tested at
	// the top so the run button is in the visible window.
	nmSpace, _ := m.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	mSpace := nmSpace.(model)
	if !mSpace.hintMode {
		t.Error("space with no confirm must enter hint mode, not act as a confirm select")
	}
	// `down` must still scroll vertically — confirm-only keys never intercept nav, and
	// the confirm branch returns early only WHILE confirmResolved.
	nm, _ := m.Update(key("j"))
	m = nm.(model)
	if m.yOff == 0 {
		t.Error("j with no confirm must still scroll down (yOff should advance)")
	}
}

// Issue #4: the focused button is highlighted and the unfocused one dimmed; moving
// focus swaps which label carries the highlight in the rendered confirm row.
func TestConfirmFocusHighlightInRender(t *testing.T) {
	m, _ := newReengageEventsModel(t, "# Playbook\n", "# Playbook\nclean\n")
	m.md = "# Troubleshoot\n\n```bash {id=verify}\nmake build\n```\n"
	m.reflow()
	nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)

	// Default focus Yes: the Yes label must carry a background highlight (the focus
	// ring), distinct from the No label. We compare the raw (un-stripped) ANSI of the
	// BUTTONS row (the buttons live on their own row now, not the prompt row).
	rowYes := m.confirmButtonsRowString()
	yesFocused := m.confirmButtonLabel(confirmYesLabel, "confirm-yes", colGreen, true)
	if !strings.Contains(rowYes, yesFocused) {
		t.Errorf("with Yes focused, the buttons row must contain the highlighted Yes label")
	}
	// Move focus to No; now No carries the highlight and Yes is dimmed.
	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m = nm.(model)
	rowNo := m.confirmButtonsRowString()
	noFocused := m.confirmButtonLabel(confirmNoLabel, "confirm-no", colPeach, true)
	if !strings.Contains(rowNo, noFocused) {
		t.Errorf("with No focused, the buttons row must contain the highlighted No label")
	}
	if rowYes == rowNo {
		t.Error("moving focus must change the rendered buttons row (highlight moved)")
	}
}

// The confirm buttons render as ask-style FILLED controls: the focused one carries a
// GREEN background, the unfocused one a muted (surface) background, and a click-flash
// (bright) wins over both. Each button is a padded cell (label width + 2*pad).
func TestConfirmButtonStyling(t *testing.T) {
	m, _ := newReengageEventsModel(t, "# Playbook\n", "# Playbook\nclean\n")
	m.md = "# Troubleshoot\n\n```bash {id=verify}\nmake build\n```\n"
	m.reflow()
	nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)

	focused := m.confirmButtonLabel(confirmYesLabel, "confirm-yes", colGreen, true)
	if !strings.Contains(focused, bgANSI(colGreen)) {
		t.Errorf("focused confirm button must carry the green background")
	}
	unfocused := m.confirmButtonLabel(confirmNoLabel, "confirm-no", colPeach, false)
	if !strings.Contains(unfocused, bgANSI(colSurface1)) {
		t.Errorf("unfocused confirm button must carry the muted surface background")
	}
	if strings.Contains(unfocused, bgANSI(colGreen)) {
		t.Errorf("unfocused confirm button must NOT be green")
	}
	// Both render as PADDED cells: label width + 2*confirmButtonPad.
	if w := len([]rune(strip(focused))); w != len(confirmYesLabel)+2*confirmButtonPad {
		t.Errorf("focused cell width = %d, want %d", w, len(confirmYesLabel)+2*confirmButtonPad)
	}
	// A click-flash wins over both the focused and unfocused styling (no fill bg).
	m.flashKey = "confirm:confirm-yes"
	flashed := m.confirmButtonLabel(confirmYesLabel, "confirm-yes", colGreen, true)
	if strings.Contains(flashed, bgANSI(colGreen)) || strings.Contains(flashed, bgANSI(colSurface1)) {
		t.Errorf("the click-flash must win over the fill backgrounds")
	}
}

// resolveConfirm: Yes generates the final-playbook draft; No DISMISSES and returns nil
// (the command already succeeded, so there is nothing to re-fix).
func TestResolveConfirmYesGeneratesNoDismisses(t *testing.T) {
	// No → dismiss, nil, no generation.
	mn, fn := newReengageEventsModel(t, "# Playbook\n", "# Playbook\nclean\n")
	mn.md = "# Troubleshoot\n"
	mn.confirmResolved = true
	if cmd := mn.resolveConfirm(false); cmd != nil {
		t.Errorf("resolveConfirm(false) must return nil (dismiss only)")
	}
	if mn.confirmResolved {
		t.Error("resolveConfirm(false) must clear the confirm state")
	}
	if mn.finalDraft || fn.calls != 0 {
		t.Error("resolveConfirm(false) must not generate")
	}
	// Yes (diverged run) → re-author (generate).
	my, _ := newReengageEventsModel(t, "# Playbook\n", "# Playbook\nclean\n")
	my.md = "# Troubleshoot\n"
	my.confirmResolved = true
	my.hadFollowup = true // diverged run → saveDecision takes the re-author path
	cmd := my.resolveConfirm(true)
	if cmd == nil {
		t.Fatal("resolveConfirm(true) must generate")
	}
	if my.confirmResolved {
		t.Error("resolveConfirm(true) must clear the confirm state")
	}
	if !my.finalDraft {
		t.Error("resolveConfirm(true) must mark a finalDraft")
	}
}

// `c` RE-SHOWS the solution confirm (it does NOT generate blindly) — an accidental
// keypress can't trigger generation; the user still confirms via the buttons. Pressing
// `c` while the confirm is already showing keeps it shown and resets focus to Yes,
// without invoking the generate path.
func TestCKeyReshowsConfirmAfterSolution(t *testing.T) {
	m, fe := newReengageEventsModel(t, "# Playbook\nclean\n", "# Playbook\nclean\n")
	m.md = "# Troubleshoot\n\n```bash {id=verify}\nmake build\n```\n"
	m.reflow()
	nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)
	if !m.confirmResolved || !m.wrappedUp {
		t.Fatal("setup: confirm/wrappedUp not set")
	}
	m.confirmFocus = 1 // move focus to No so we can see `c` reset it
	nm2, cmd := m.Update(key("c"))
	m = nm2.(model)
	if !hasHide(collectRawSeqs(t, cmd)) {
		t.Fatal("c must re-assert the hide-cursor (re-show repaint), NOT trigger generation")
	}
	if !m.confirmResolved {
		t.Error("c must keep the confirm shown")
	}
	if m.confirmFocus != 0 {
		t.Errorf("c must reset focus to Yes: confirmFocus=%d", m.confirmFocus)
	}
	if m.finalDraft {
		t.Error("c must NOT mark a finalDraft (no generation)")
	}
	if fe.calls != 0 {
		t.Errorf("c must NOT invoke the generate path: calls=%d", fe.calls)
	}
}

// `c` after a No dismiss brings the confirm BACK (re-shows it) rather than generating,
// so a user who declined can reconsider and confirm via the buttons.
func TestCKeyReshowsConfirmAfterNoDismiss(t *testing.T) {
	m, fe := newReengageEventsModel(t, "# Playbook\nclean\n", "# Playbook\nclean\n")
	m.md = "# Troubleshoot\n\n```bash {id=verify}\nmake build\n```\n"
	m.reflow()
	nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)
	// Dismiss with No.
	nm, _ = m.Update(key("n"))
	m = nm.(model)
	if m.confirmResolved {
		t.Fatal("setup: No must dismiss the confirm")
	}
	if !m.wrappedUp {
		t.Fatal("setup: wrappedUp must remain set after a No dismiss")
	}
	// `c` re-shows the confirm instead of generating.
	nm2, cmd := m.Update(key("c"))
	m = nm2.(model)
	if !hasHide(collectRawSeqs(t, cmd)) {
		t.Fatal("c after a No dismiss must re-assert the hide-cursor (re-show), NOT trigger generation")
	}
	if !m.confirmResolved {
		t.Error("c after a No dismiss must re-show the confirm")
	}
	if m.confirmFocus != 0 {
		t.Errorf("c must reset focus to Yes: confirmFocus=%d", m.confirmFocus)
	}
	if m.finalDraft || fe.calls != 0 {
		t.Errorf("c must NOT generate: finalDraft=%v calls=%d", m.finalDraft, fe.calls)
	}
}

// `c` is a no-op before a solution is reached and while a stream is in flight.
func TestCKeyNoOpBeforeSolutionOrWhileStreaming(t *testing.T) {
	m, fe := newReengageEventsModel(t, "# Playbook\n", "# Playbook\n")
	m.md = "# Troubleshoot\n"
	m.reflow()
	// Before a solution (wrappedUp false).
	nm, cmd := m.Update(key("c"))
	m = nm.(model)
	if cmd != nil {
		t.Error("c before a solution must be a no-op")
	}
	if m.finalDraft || fe.calls != 0 {
		t.Error("c before a solution must not generate")
	}
	// A solution reached but a stream is in flight.
	m.wrappedUp = true
	m.streaming = true
	nm2, cmd2 := m.Update(key("c"))
	m = nm2.(model)
	if cmd2 != nil {
		t.Error("c while streaming must be a no-op")
	}
	if fe.calls != 0 {
		t.Error("c while streaming must not generate")
	}
}
