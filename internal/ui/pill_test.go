package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Truecolor SGR fragments for the pill palette, used to pin the pill forms.
const (
	peachBg     = "48;2;250;179;135" // colPeach   #fab387 as background
	pillPeachFg = "38;2;92;50;18"    // colPillPeachFg #5c3212 as foreground
	blueBg      = "48;2;137;180;250" // colBlue    #89b4fa as background
	pillBlueFg  = "38;2;22;50;92"    // colPillBlueFg  #16325c as foreground
	flashBg     = "48;2;255;255;255" // colFlashOn #ffffff as background
)

// pillLine returns the raw text of the first rendered line containing sub.
func pillLine(t *testing.T, lines []Line, sub string) string {
	t.Helper()
	for _, l := range lines {
		if strings.Contains(strip(l.Text), sub) {
			return l.Text
		}
	}
	t.Fatalf("no rendered line contains %q", sub)
	return ""
}

// TestFollowupButtonRendersAsPeachPill pins the followup ("try another fix")
// button's pill form: powerline caps, peach body background, and the darker
// same-hue text — not the old bare peach glyph + grey subtext.
func TestFollowupButtonRendersAsPeachPill(t *testing.T) {
	md := "```bash {id=fix}\nmake build\n```\n"
	st := map[string]blockRunState{"fix": {Status: "failed", Exit: 1}}
	lines, btns, _ := Render(md, 80, RenderOpts{States: st})

	row := pillLine(t, lines, "try another fix")
	for _, want := range []string{"\U0000E0B6", "\U0000E0B4", peachBg, pillPeachFg, glyphRetry} {
		if !strings.Contains(row, want) {
			t.Errorf("followup pill row missing %q:\n%q", want, row)
		}
	}
	// The whole pill is the click target: Width covers caps + body, not a 2-col glyph.
	b := buttonForBlock(btns, "fix", "followup")
	if b == nil {
		t.Fatal("failed run block must register a followup button")
	}
	if b.Width <= 2 {
		t.Errorf("followup hit box must cover the whole pill, got Width=%d", b.Width)
	}
}

// TestFollowupPillFlashHighlightsWholePill verifies the flash highlight still
// reads on the pill: with flashKey set, the pill body flips to the bright
// flash background and the peach resting background is gone.
func TestFollowupPillFlashHighlightsWholePill(t *testing.T) {
	md := "```bash {id=fix}\nmake build\n```\n"
	st := map[string]blockRunState{"fix": {Status: "failed", Exit: 1}}
	lines, _, _ := Render(md, 80, RenderOpts{States: st, FlashKey: "fix:followup"})
	row := pillLine(t, lines, "try another fix")
	if !strings.Contains(row, flashBg) {
		t.Errorf("flashed followup pill must use the flash bg:\n%q", row)
	}
	if strings.Contains(row, peachBg) {
		t.Errorf("flashed followup pill must drop the peach bg:\n%q", row)
	}
}

// TestRollbackButtonRendersAsPeachPill pins the rollback ("Rollback playbook")
// button's pill form (peach, like followup).
func TestRollbackButtonRendersAsPeachPill(t *testing.T) {
	md := "```bash {id=boom}\nfalse\n```\n"
	st := map[string]blockRunState{"boom": {Status: "failed", Exit: 1}}
	lines, btns, _ := Render(md, 80, RenderOpts{States: st, NoReengage: true, RollbackAvail: true})

	row := pillLine(t, lines, "Rollback playbook")
	for _, want := range []string{"\U0000E0B6", "\U0000E0B4", peachBg, pillPeachFg, glyphUndo} {
		if !strings.Contains(row, want) {
			t.Errorf("rollback pill row missing %q:\n%q", want, row)
		}
	}
	b := buttonForBlock(btns, "boom", "rollback")
	if b == nil {
		t.Fatal("failed block must register a rollback button")
	}
	if b.Width <= 2 {
		t.Errorf("rollback hit box must cover the whole pill, got Width=%d", b.Width)
	}
}

// TestDriftButtonsRenderAsBluePills pins the drift-resolve/drift-regen pill
// forms (blue body, darker blue text) and the glyphSep still separating them.
func TestDriftButtonsRenderAsBluePills(t *testing.T) {
	src := "```diff {id=fix}\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n```\n"
	states := map[string]blockRunState{"fix": {Drifted: true}}
	lines, _, _ := Render(src, 100, RenderOpts{States: states})

	row := pillLine(t, lines, "resolve manually")
	if !strings.Contains(strip(row), "regenerate") {
		t.Fatalf("resolve and regenerate pills must share one row:\n%q", strip(row))
	}
	for _, want := range []string{"\U0000E0B6", "\U0000E0B4", blueBg, pillBlueFg, glyphViewDiff, glyphRetry, glyphSep} {
		if !strings.Contains(row, want) {
			t.Errorf("drift pills row missing %q:\n%q", want, row)
		}
	}
}

// TestDriftPillHitBoxes verifies both drift pills' hit boxes cover their whole
// pill, don't overlap, and resolve a click at either end of each pill.
func TestDriftPillHitBoxes(t *testing.T) {
	m := newTestModelWithDiffBlock(t, "fix")
	m.blockStates["fix"] = blockRunState{Drifted: true}
	m.reflow()

	resolve := buttonForBlock(m.buttons, "fix", "drift-resolve")
	regen := buttonForBlock(m.buttons, "fix", "drift-regen")
	if resolve == nil || regen == nil {
		t.Fatal("drifted block must register both drift buttons")
	}
	if resolve.Width <= 2 || regen.Width <= 2 {
		t.Fatalf("pill hit boxes must cover the whole pill: resolve=%d regen=%d", resolve.Width, regen.Width)
	}
	if resolve.Col+resolve.Width > regen.Col {
		t.Fatalf("pill hit boxes must not overlap: resolve ends at %d, regen starts at %d",
			resolve.Col+resolve.Width, regen.Col)
	}
	y := m.bodyTop() + (resolve.Line - m.yOff)
	for _, tc := range []struct {
		name string
		x    int
		want string
	}{
		{"resolve left cap", 2 + resolve.Col, "drift-resolve"},
		{"resolve right cap", 2 + resolve.Col + resolve.Width - 1, "drift-resolve"},
		{"regen left cap", 2 + regen.Col, "drift-regen"},
		{"regen right cap", 2 + regen.Col + regen.Width - 1, "drift-regen"},
	} {
		got, ok := buttonAt(m.buttons, tc.x, y, m.yOff, m.bodyTop())
		if !ok || got.Kind != tc.want {
			t.Errorf("%s: buttonAt(%d,%d) = (%+v, %v), want Kind=%s", tc.name, tc.x, y, got, ok, tc.want)
		}
	}
}

// TestFollowupPillHitBox verifies a click resolves across the whole followup
// pill on the failed-result summary row.
func TestFollowupPillHitBox(t *testing.T) {
	m, _ := newReengageModel(t, "") // reengage wired → the followup affordance renders
	m.md = "```bash {id=fix}\nmake build\n```\n"
	m.width, m.height = 100, 24
	m.blockStates["fix"] = blockRunState{Status: "failed", Exit: 1}
	m.reflow()

	b := buttonForBlock(m.buttons, "fix", "followup")
	if b == nil {
		t.Fatal("failed run block must register a followup button")
	}
	y := m.bodyTop() + (b.Line - m.yOff)
	for _, x := range []int{2 + b.Col, 2 + b.Col + b.Width - 1} {
		got, ok := buttonAt(m.buttons, x, y, m.yOff, m.bodyTop())
		if !ok || got.Kind != "followup" {
			t.Errorf("buttonAt(%d,%d) = (%+v, %v), want the followup pill", x, y, got, ok)
		}
	}
}

// TestEditBadgeHasIconAndBadgesRow pins the edit pill's new form (pencil icon
// in the body) and its home: the shared badges row below the title/subtitle,
// left-grouped after the cached pill.
func TestEditBadgeHasIconAndBadgesRow(t *testing.T) {
	m := newModel("T", "# File-backed\n")
	m.width, m.height = 100, 24
	m.sourcePath = "/store/x.md"
	m.subtitle = "a short description"
	m.reflow()

	badge := m.editBadge()
	if !strings.Contains(badge, editIcon) {
		t.Fatalf("edit pill must carry the pencil icon: %q", badge)
	}
	// Badges row sits below the subtitle; with a 1-line title + 1-line subtitle
	// that is screen row 3 (leading blank, title, subtitle, badges).
	if got := m.badgeRowIdx(); got != 3 {
		t.Fatalf("badgeRowIdx = %d, want 3 (1-line title + 1-line subtitle)", got)
	}
	lines := m.normalLines()
	row := strip(lines[m.badgeRowIdx()])
	if !strings.Contains(row, "edit") {
		t.Fatalf("badges row must carry the edit pill, got %q", row)
	}
	b := buttonForBlock(m.buttons, "edit", "edit")
	if b == nil {
		t.Fatal("file-backed playbook must register an edit button")
	}
	if b.Line != m.badgeRowIdx() {
		t.Errorf("edit button Line = %d, want the badges row %d", b.Line, m.badgeRowIdx())
	}
	if b.Col != titleTextCol-2 {
		t.Errorf("edit button Col = %d, want %d (subtitle-aligned, no cached pill)", b.Col, titleTextCol-2)
	}
}

// TestBadgesRowSharedCachedAndEdit verifies a cached AND file-backed playbook
// puts both pills on the ONE badges row (cached first, then edit) and both hit
// boxes resolve on that row without overlapping.
func TestBadgesRowSharedCachedAndEdit(t *testing.T) {
	m := newModel("T", "hello")
	m.width, m.height = 120, 24
	m.isCached = true
	m.cachedAt = time.Now().Add(-2 * time.Minute)
	m.answerRegen = fakeAnswerRegen()
	m.sourcePath = "/store/x.md"
	m.reflow()

	row := strip(m.normalLines()[m.badgeRowIdx()])
	if !strings.Contains(row, "cached ·") || !strings.Contains(row, "edit") {
		t.Fatalf("badges row must carry BOTH pills, got %q", row)
	}
	if strings.Index(row, "cached") > strings.Index(row, "edit") {
		t.Fatalf("cached pill must precede the edit pill, got %q", row)
	}

	regen := buttonForBlock(m.buttons, "cached", "regenerate")
	edit := buttonForBlock(m.buttons, "edit", "edit")
	if regen == nil || edit == nil {
		t.Fatal("both the regenerate and edit buttons must be registered")
	}
	if regen.Line != m.badgeRowIdx() || edit.Line != m.badgeRowIdx() {
		t.Fatalf("both buttons must sit on the badges row %d: regen=%d edit=%d",
			m.badgeRowIdx(), regen.Line, edit.Line)
	}
	if wantCol := titleTextCol - 2 + lipgloss.Width(m.cachedBadge()); edit.Col != wantCol {
		t.Errorf("edit button Col = %d, want %d (subtitle-aligned, after the cached pill)", edit.Col, wantCol)
	}
	if regen.Col+regen.Width > edit.Col {
		t.Errorf("pills must not overlap: regen ends at %d, edit starts at %d",
			regen.Col+regen.Width, edit.Col)
	}
	// Clicks at each pill resolve to the right button (Screen buttons: absolute Y).
	if got, ok := buttonAt(m.buttons, 2+regen.Col, regen.Line, m.yOff, m.bodyTop()); !ok || got.Kind != "regenerate" {
		t.Errorf("click on the cached pill resolved to (%+v, %v)", got, ok)
	}
	if got, ok := buttonAt(m.buttons, 2+edit.Col+edit.Width-1, edit.Line, m.yOff, m.bodyTop()); !ok || got.Kind != "edit" {
		t.Errorf("click on the edit pill resolved to (%+v, %v)", got, ok)
	}
}

// TestEditButtonMouseClickDispatches verifies a mouse click on the edit pill's
// badges-row position reaches editDispatch: no-mux, so it returns the editor
// ExecProcess cmd and sets the edit flash.
func TestEditButtonMouseClickDispatches(t *testing.T) {
	m := newModel("T", "# File-backed\n")
	m.width, m.height = 100, 24
	m.sourcePath = "/store/x.md"
	m.asker = nil // no-mux → ExecProcess editor path
	m.reflow()

	b := buttonForBlock(m.buttons, "edit", "edit")
	if b == nil {
		t.Fatal("file-backed playbook must register an edit button")
	}
	m2i, cmd := m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: 2 + b.Col, Y: b.Line})
	m2 := m2i.(model)
	if cmd == nil {
		t.Fatal("clicking the edit pill must return the editor ExecProcess cmd")
	}
	if m2.flashKey != "edit:edit" {
		t.Errorf("flashKey = %q, want edit:edit", m2.flashKey)
	}
}

// TestEditPillHitBoxTracksCachedAgeWidth verifies the click hit-test follows
// the LIVE badges row: the cached pill's age string can change width between
// reflows ("just now" → "5m ago"), shifting the drawn edit pill, and the mouse
// path re-derives the header buttons' geometry before hit-testing so the click
// lands on what is on screen — not on the reflow-frozen columns.
func TestEditPillHitBoxTracksCachedAgeWidth(t *testing.T) {
	m := newModel("T", "# File-backed\n")
	m.width, m.height = 100, 24
	m.sourcePath = "/store/x.md"
	m.asker = nil // no-mux → ExecProcess editor path
	m.isCached = true
	m.cachedAt = time.Now() // renders as "just now" (8 cells)
	m.reflow()

	stale := buttonForBlock(m.buttons, "edit", "edit")
	if stale == nil {
		t.Fatal("file-backed playbook must register an edit button")
	}
	// The age rolls over WITHOUT a reflow: "5m ago" is 2 cells narrower than
	// "just now", so the drawn edit pill shifts left of the frozen Col.
	m.cachedAt = time.Now().Add(-5 * time.Minute)
	liveCol := titleTextCol - 2 + lipgloss.Width(m.cachedBadge())
	if liveCol == stale.Col {
		t.Fatalf("setup: the age change must shift the edit pill (stale=%d live=%d)", stale.Col, liveCol)
	}
	m2i, cmd := m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: 2 + liveCol, Y: stale.Line})
	m2 := m2i.(model)
	if cmd == nil {
		t.Fatal("a click at the live edit position must dispatch the edit action")
	}
	if m2.flashKey != "edit:edit" {
		t.Errorf("flashKey = %q, want edit:edit", m2.flashKey)
	}
}

// TestEditHintChipRendered verifies hint mode paints a chip over the edit pill
// on the badges row (the gap this round closes: the label was assigned but
// never rendered for the Screen-fixed edit button).
func TestEditHintChipRendered(t *testing.T) {
	m := newModel("T", "# File-backed\n")
	m.width, m.height = 100, 24
	m.sourcePath = "/store/x.md"
	m.reflow()

	m2 := mustModel(m.Update(tea.KeyPressMsg{Code: tea.KeySpace}))
	if !m2.hintMode {
		t.Fatal("space must enter hint mode")
	}
	var lbl string
	for l, b := range m2.hintLabels {
		if b.Kind == "edit" {
			lbl = l
		}
	}
	if lbl == "" {
		t.Fatal("the edit button must receive a hint label")
	}
	lines := strings.Split(strip(m2.viewString()), "\n")
	row := lines[m2.badgeRowIdx()]
	// The chip is spliced over the pencil-icon cell, so the row reads "<lbl> edit".
	if !strings.Contains(row, lbl+" edit") {
		t.Fatalf("hint chip %q must overlay the edit pill's icon on the badges row, got %q", lbl, row)
	}
}

// TestHeaderPillsGreyedInHintMode verifies hint mode renders the header pills
// (cached + edit) with the inverted greyed fill — muted text on the solid
// colSurface0 fill, caps in the fill color — instead of their normal
// peach/green bodies, joining the greyed-out screen like the body pills.
func TestHeaderPillsGreyedInHintMode(t *testing.T) {
	m := newModel("T", "# File-backed\n")
	m.width, m.height = 100, 24
	m.sourcePath = "/store/x.md"
	m.isCached = true
	m.cachedAt = time.Now()
	m.reflow()

	const surfaceBg = "48;2;49;50;68"  // colSurface0 #313244 as background
	const greenBg = "48;2;166;227;161" // colGreen #a6e3a1 as background
	// Normal mode keeps the accent bodies.
	if !strings.Contains(m.editBadge(), greenBg) || !strings.Contains(m.cachedBadge(), peachBg) {
		t.Fatal("pills must keep their accent colors outside hint mode")
	}
	m.hintMode = true
	edit := m.editBadge()
	if !strings.Contains(edit, surfaceBg) || strings.Contains(edit, greenBg) {
		t.Fatalf("edit pill must grey out with the inverted fill in hint mode: %q", edit)
	}
	cached := m.cachedBadge()
	if !strings.Contains(cached, surfaceBg) || strings.Contains(cached, peachBg) {
		t.Fatalf("cached pill must grey out with the inverted fill in hint mode: %q", cached)
	}
	// Same geometry dimmed and not: hit boxes and label anchors stay put.
	m2 := m
	m2.hintMode = false
	if lipgloss.Width(m.editBadge()) != lipgloss.Width(m2.editBadge()) ||
		lipgloss.Width(m.cachedBadge()) != lipgloss.Width(m2.cachedBadge()) {
		t.Fatal("greyed pills must keep the exact geometry of the colored ones")
	}
}

// TestTitleGreyedInHintMode verifies hint mode dims the header title (and the
// "a step failed" cue) to the muted overlay tone — with the pills also greyed,
// nothing in the header keeps color besides the hint letters.
func TestTitleGreyedInHintMode(t *testing.T) {
	m := newModel("My Playbook", "# My Playbook\n\nbody\n")
	m.width, m.height = 100, 24
	m.blockStates["b"] = blockRunState{Status: "failed"} // arms the ⚠ cue
	m.reflow()

	const mauveFg = "38;2;203;166;247"   // colMauve — the normal title color
	const redFg = "38;2;243;139;168"     // colRed — the normal cue color
	const overlayFg = "38;2;108;112;134" // colOverlay0 — the dimmed tone
	normal := strings.Join(m.titleLines(), "\n")
	if !strings.Contains(normal, mauveFg) || !strings.Contains(normal, redFg) {
		t.Fatalf("normal mode must keep the mauve title and red cue: %q", normal)
	}
	m.hintMode = true
	dimmed := strings.Join(m.titleLines(), "\n")
	if strings.Contains(dimmed, mauveFg) || strings.Contains(dimmed, redFg) {
		t.Fatalf("hint mode must grey the title and cue: %q", dimmed)
	}
	if !strings.Contains(dimmed, overlayFg) {
		t.Fatalf("hint-mode title must use the muted overlay tone: %q", dimmed)
	}
}

// TestTitleWrapsAt80Cells verifies a long title wraps at 80 display cells with
// continuation lines aligned under the title's first text character (col 6,
// after the "  ▓▓▓ " prefix), even in a wider pane.
func TestTitleWrapsAt80Cells(t *testing.T) {
	m := newModel("agent", "")
	m.width, m.height = 120, 24
	m.title = "Playbook — a very long generated title that keeps going well past the eighty cell wrap limit"

	rows := m.titleLines()
	if len(rows) < 2 {
		t.Fatalf("long title must wrap to multiple rows, got %d", len(rows))
	}
	for i, r := range rows {
		if w := lipgloss.Width(r); w > 80 {
			t.Errorf("title row %d is %d cells wide, must wrap at 80: %q", i, w, strip(r))
		}
	}
	if !strings.HasPrefix(strip(rows[0]), "  ▓▓▓ Playbook") {
		t.Errorf("first title row must carry the ▓▓▓ prefix, got %q", strip(rows[0]))
	}
	for i, r := range rows[1:] {
		plain := strip(r)
		if !strings.HasPrefix(plain, strings.Repeat(" ", 6)) || plain[6] == ' ' {
			t.Errorf("continuation row %d must start at col 6 (title text column), got %q", i+1, plain)
		}
	}
	// The whole title survives the wrap (no truncation).
	joined := strip(strings.Join(rows, " "))
	if !strings.Contains(strings.Join(strings.Fields(joined), " "), "eighty cell wrap limit") {
		t.Errorf("wrapped title must keep the full text, got %q", joined)
	}
	// Layout math follows the wrapped count.
	if want := 1 + len(rows) + 1; m.bodyTop() != want {
		t.Errorf("bodyTop = %d, want %d (leading + %d title rows + top-pad)", m.bodyTop(), want, len(rows))
	}
}

// TestSubtitleWrapsAndAlignsWithTitleText verifies the subtitle wraps at 80
// cells and every subtitle line starts at the title text column (col 6).
func TestSubtitleWrapsAndAlignsWithTitleText(t *testing.T) {
	m := newModel("agent", "")
	m.width, m.height = 120, 30
	m.title = "Playbook — X"
	m.subtitle = "A rather wordy front-matter description that easily exceeds the eighty display cell limit and must wrap onto a second aligned row"

	rows := m.subtitleRowStrings()
	if len(rows) < 2 {
		t.Fatalf("long subtitle must wrap to multiple rows, got %d", len(rows))
	}
	for i, r := range rows {
		if w := lipgloss.Width(r); w > 80 {
			t.Errorf("subtitle row %d is %d cells wide, must wrap at 80: %q", i, w, strip(r))
		}
		plain := strip(r)
		if !strings.HasPrefix(plain, strings.Repeat(" ", 6)) || plain[6] == ' ' {
			t.Errorf("subtitle row %d must start at col 6 (title text column), got %q", i, plain)
		}
	}
	if m.subtitleRows() != len(rows) {
		t.Errorf("subtitleRows = %d, want %d (wrapped count)", m.subtitleRows(), len(rows))
	}
}

// TestBadgesRowBelowMultiLineHeader verifies the badges row and every derived
// screen geometry track the WRAPPED title/subtitle heights: with a multi-row
// title and subtitle, the pills land on row 1+titleRows+subtitleRows and the
// frame still fills exactly m.height rows.
func TestBadgesRowBelowMultiLineHeader(t *testing.T) {
	m := newModel("T", "hello")
	m.width, m.height = 120, 30
	m.title = "Playbook — a very long generated title that keeps going well past the eighty cell wrap limit"
	m.subtitle = "A rather wordy front-matter description that easily exceeds the eighty display cell limit and must wrap onto a second aligned row"
	m.isCached = true
	m.cachedAt = time.Now().Add(-1 * time.Minute)
	m.answerRegen = fakeAnswerRegen()
	m.sourcePath = "/store/x.md"
	m.reflow()

	if m.titleRows() < 2 || m.subtitleRows() < 2 {
		t.Fatalf("precondition: wrapped header wanted, got title=%d subtitle=%d rows",
			m.titleRows(), m.subtitleRows())
	}
	want := 1 + m.titleRows() + m.subtitleRows()
	if got := m.badgeRowIdx(); got != want {
		t.Fatalf("badgeRowIdx = %d, want %d", got, want)
	}
	lines := m.normalLines()
	if len(lines) != m.height {
		t.Fatalf("normalLines = %d rows, want m.height = %d", len(lines), m.height)
	}
	row := strip(lines[m.badgeRowIdx()])
	if !strings.Contains(row, "cached ·") || !strings.Contains(row, "edit") {
		t.Fatalf("badges row (idx %d) must carry both pills, got %q", m.badgeRowIdx(), row)
	}
	for _, kind := range []string{"regenerate", "edit"} {
		var btn *Button
		for i := range m.buttons {
			if m.buttons[i].Kind == kind {
				btn = &m.buttons[i]
			}
		}
		if btn == nil {
			t.Fatalf("missing %s button", kind)
		}
		if btn.Line != m.badgeRowIdx() {
			t.Errorf("%s button Line = %d, want badges row %d", kind, btn.Line, m.badgeRowIdx())
		}
		if got, ok := buttonAt(m.buttons, 2+btn.Col, btn.Line, m.yOff, m.bodyTop()); !ok || got.Kind != kind {
			t.Errorf("click on the %s pill resolved to (%+v, %v)", kind, got, ok)
		}
	}
}
