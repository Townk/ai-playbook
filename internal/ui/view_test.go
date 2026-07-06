package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// TestCachedBadgePillRow verifies that when isCached=true the shared badges row
// (the row BELOW the title/subtitle) contains "cached ·", an age token, and the
// pill glyphs — and that the title lines themselves carry no pill.
func TestCachedBadgePillRow(t *testing.T) {
	m := newModel("agent", "hello world")
	m.width = 120
	m.isCached = true
	m.cachedAt = time.Now().Add(-3 * time.Minute) // 3 minutes ago
	m.answerRegen = fakeAnswerRegen()             // wire a regenerate path so the reload renders

	row := m.badgesRowString()
	plain := strip(row)
	if !strings.Contains(plain, "cached ·") {
		t.Fatalf("badges row missing 'cached ·' badge: %q", plain)
	}
	if !strings.Contains(plain, "m ago") {
		t.Fatalf("badges row missing age token (expected '...m ago'): %q", plain)
	}
	if !strings.Contains(row, "\U0000E0B6") {
		t.Fatalf("badges row missing powerline left-cap (U+E0B6): %q", row)
	}
	if !strings.Contains(row, "\U0010F1DA") {
		t.Fatalf("badges row missing reload icon (U+10F1DA): %q", row)
	}
	if strings.Contains(strings.Join(m.titleLines(), "\n"), "\U0000E0B6") {
		t.Fatalf("title lines should not carry the pill")
	}
}

// TestCachedBadgeInNormalLines verifies that normalLines() places the badges row
// directly below the title, followed by the top-pad blank.
// Layout: index 0=leading blank, 1=title, 2=badges row, 3=top-pad blank, 4+=body.
func TestCachedBadgeInNormalLines(t *testing.T) {
	m := newModel("agent", "hello")
	m.width = 120
	m.height = 24
	m.isCached = true
	m.cachedAt = time.Now().Add(-7 * time.Minute)
	m.answerRegen = fakeAnswerRegen() // wire a regenerate path so the reload renders
	m.reflow()

	lines := m.normalLines()
	if len(lines) < 5 {
		t.Fatal("normalLines returned fewer than 5 lines")
	}

	// Row 2: the badges row, directly below the (1-line) title.
	raw := lines[2]
	plain := strip(raw)
	if !strings.Contains(plain, "cached ·") {
		t.Fatalf("badges row (index 2) missing cached badge: %q", plain)
	}
	if !strings.Contains(plain, "m ago") {
		t.Fatalf("badges row (index 2) missing age in cached badge: %q", plain)
	}
	if !strings.Contains(raw, "\U0000E0B6") {
		t.Fatalf("badges row (index 2) missing powerline left-cap (U+E0B6): %q", raw)
	}
	if !strings.Contains(raw, "\U0010F1DA") {
		t.Fatalf("badges row (index 2) missing reload icon (U+10F1DA): %q", raw)
	}

	// Row 3: the top-pad blank below the badges row.
	if got := strings.TrimSpace(strip(lines[3])); got != "" {
		t.Fatalf("row 3 (top-pad below badges) must be empty, got: %q", got)
	}
}

// TestBodyTopAndBodyHeightCached verifies the bodyTop/body height arithmetic
// for both cached and non-cached models.
func TestBodyTopAndBodyHeightCached(t *testing.T) {
	const h = 24

	// Non-cached: bodyTop == 3, body == h-5.
	mn := newModel("agent", "")
	mn.height = h
	mn.isCached = false
	if got := mn.bodyTop(); got != 3 {
		t.Errorf("non-cached bodyTop = %d, want 3", got)
	}
	if got := mn.body(); got != h-5 {
		t.Errorf("non-cached body = %d, want %d", got, h-5)
	}

	// Cached: one badges row → bodyTop == 4, body == h-6.
	mc := newModel("agent", "")
	mc.height = h
	mc.isCached = true
	if got := mc.bodyTop(); got != 4 {
		t.Errorf("cached bodyTop = %d, want 4", got)
	}
	if got := mc.body(); got != h-6 {
		t.Errorf("cached body = %d, want %d", got, h-6)
	}
}

// TestButtonHitTestCachedLayout verifies that with isCached=true a button's
// known screen-Y maps back through buttonAt to the same button, proving that
// the 2-row layout delta is threaded consistently through bodyTop and the
// buttonAt call sites.
func TestButtonHitTestCachedLayout(t *testing.T) {
	const h = 24
	// Render a shell block so we have at least one button.
	m := newModel("agent", "```bash {id=blk}\nls\n```\n")
	m.width = 80
	m.height = h
	m.isCached = true
	m.reflow()

	if len(m.buttons) == 0 {
		t.Fatal("expected at least one button from the shell block")
	}
	b := m.buttons[0]

	// Screen Y for this button: button.Line is the content-line index (0-based
	// in m.lines). With yOff=0, screen Y = b.Line + m.bodyTop().
	screenY := b.Line + m.bodyTop()
	// Screen X: 2-col left margin + b.Col (the button glyph column).
	screenX := 2 + b.Col

	got, ok := buttonAt(m.buttons, screenX, screenY, m.yOff, m.bodyTop())
	if !ok {
		t.Fatalf("buttonAt(%d,%d) returned no button; bodyTop=%d, button.Line=%d, button.Col=%d",
			screenX, screenY, m.bodyTop(), b.Line, b.Col)
	}
	if got.Kind != b.Kind || got.BlockID != b.BlockID {
		t.Fatalf("buttonAt returned wrong button: got %+v, want %+v", got, b)
	}
}

// TestCachedBadgeAbsentWhenNotCachedNoPill verifies that with isCached=false the
// pill row is empty and the title line carries no pill glyphs.
func TestCachedBadgeAbsentWhenNotCachedNoPill(t *testing.T) {
	m := newModel("agent", "hello world")
	m.width = 120
	m.isCached = false

	if m.badgesRowString() != "" {
		t.Fatalf("badges row must be empty when isCached=false and not file-backed: %q", m.badgesRowString())
	}
	if strings.Contains(strings.Join(m.titleLines(), "\n"), "\U0000E0B6") {
		t.Fatalf("title lines must not contain the pill when isCached=false")
	}
}

// TestCachedReloadButtonRegistered verifies that when isCached=true and reflow
// is called, a Screen-fixed "regenerate" button is present at the badges row
// (badgeRowIdx) with a sane column, whole-pill width, BlockID="cached".
func TestCachedReloadButtonRegistered(t *testing.T) {
	m := newModel("agent", "hello")
	m.width = 120
	m.height = 24
	m.isCached = true
	m.cachedAt = time.Now().Add(-5 * time.Minute)
	m.answerRegen = fakeAnswerRegen() // wire a regenerate path so the reload renders
	m.reflow()

	var regenBtn *Button
	for i := range m.buttons {
		if m.buttons[i].Kind == "regenerate" {
			regenBtn = &m.buttons[i]
			break
		}
	}
	if regenBtn == nil {
		t.Fatal("no regenerate button found after reflow with isCached=true")
	}
	if !regenBtn.Screen {
		t.Error("regenerate button must have Screen=true")
	}
	wantLine := m.badgeRowIdx()
	if regenBtn.Line != wantLine {
		t.Errorf("regenerate button Line = %d, want %d (badgeRowIdx)", regenBtn.Line, wantLine)
	}
	if regenBtn.BlockID != "cached" {
		t.Errorf("regenerate button BlockID = %q, want %q", regenBtn.BlockID, "cached")
	}
	// The ENTIRE pill is the click target: Col titleTextCol-2 (the left cap of the
	// subtitle-aligned row, after buttonAt strips the 2-col margin) and Width =
	// the pill's visible width sans trailing space.
	if regenBtn.Col != titleTextCol-2 {
		t.Errorf("regenerate button Col = %d, want %d (whole-pill target starts at the left cap)", regenBtn.Col, titleTextCol-2)
	}
	wantW := lipgloss.Width(m.cachedBadge()) - 1
	if regenBtn.Width != wantW {
		t.Errorf("regenerate button Width = %d, want %d (whole pill, sans trailing space)", regenBtn.Width, wantW)
	}
}

// TestCachedReloadButtonAbsentWhenNotCached verifies that with isCached=false
// no regenerate button is added after reflow.
func TestCachedReloadButtonAbsentWhenNotCached(t *testing.T) {
	m := newModel("agent", "hello")
	m.width = 120
	m.height = 24
	m.isCached = false
	m.reflow()

	for _, b := range m.buttons {
		if b.Kind == "regenerate" {
			t.Fatalf("regenerate button must NOT be present when isCached=false, got %+v", b)
		}
	}
}

// TestCachedReloadButtonHitTest verifies that buttonAt resolves a click at the
// reload icon's screen position to the regenerate button.
func TestCachedReloadButtonHitTest(t *testing.T) {
	m := newModel("agent", "hello")
	m.width = 120
	m.height = 24
	m.isCached = true
	m.cachedAt = time.Now().Add(-2 * time.Minute)
	m.answerRegen = fakeAnswerRegen() // wire a regenerate path so the reload renders
	m.reflow()

	var regenBtn *Button
	for i := range m.buttons {
		if m.buttons[i].Kind == "regenerate" {
			regenBtn = &m.buttons[i]
			break
		}
	}
	if regenBtn == nil {
		t.Fatal("no regenerate button found")
	}

	// Screen-fixed button: click at screen Y = regenBtn.Line, screen X = 2 + regenBtn.Col.
	screenY := regenBtn.Line
	screenX := 2 + regenBtn.Col // 2-col left margin applied by buttonAt

	got, ok := buttonAt(m.buttons, screenX, screenY, m.yOff, m.bodyTop())
	if !ok {
		t.Fatalf("buttonAt(%d,%d) returned no button; regenBtn=%+v", screenX, screenY, *regenBtn)
	}
	if got.Kind != "regenerate" || got.BlockID != "cached" {
		t.Fatalf("buttonAt returned wrong button: got %+v, want Kind=regenerate BlockID=cached", got)
	}
	// The WHOLE pill is clickable: a click at the right end resolves too.
	rightX := 2 + regenBtn.Col + regenBtn.Width - 1
	got2, ok2 := buttonAt(m.buttons, rightX, screenY, m.yOff, m.bodyTop())
	if !ok2 || got2.Kind != "regenerate" {
		t.Fatalf("buttonAt at the pill's right end (%d,%d) did not resolve to regenerate: ok=%v got=%+v", rightX, screenY, ok2, got2)
	}
}

// TestCachedRegenHintLabelRendered verifies that in hint mode the regenerate
// button's hint label is painted directly OVER the badges row (anchored at the
// reload glyph's column) — the row above is the title/subtitle, never blank,
// so the own-row placement applies (mirroring the drift buttons under their
// banner) — and that it lands at the reload-icon column, not at the row start.
func TestCachedRegenHintLabelRendered(t *testing.T) {
	m := newModel("agent", "hello")
	m.width = 120
	m.height = 24
	m.isCached = true
	m.cachedAt = time.Now()
	m.answerRegen = fakeAnswerRegen() // wire a regenerate path so the reload renders
	m.reflow()

	var regen Button
	found := false
	for _, b := range m.buttons {
		if b.Kind == "regenerate" {
			regen, found = b, true
		}
	}
	if !found {
		t.Fatal("no regenerate button registered after reflow")
	}
	m.hintMode = true
	m.hintLabels = map[string]Button{"Z": regen}

	lines := strings.Split(strip(m.viewString()), "\n")
	pillIdx := -1
	for i, l := range lines {
		if strings.Contains(l, "cached ·") {
			pillIdx = i
			break
		}
	}
	if pillIdx < 1 {
		t.Fatalf("badges row not found at index >=1: %#v", lines)
	}
	row := lines[pillIdx]
	labelAt := strings.Index(row, "Z")
	if labelAt < 0 {
		t.Fatalf("regenerate hint label 'Z' not rendered on the badges row; row=%q", row)
	}
	// The label is anchored at the reload glyph's column inside the pill (the
	// stripped row's byte layout differs from display cells, so just require it
	// to sit past the pill's "cached ·" prefix rather than at the row start).
	if labelAt < strings.Index(row, "cached") {
		t.Errorf("hint label must anchor at the reload glyph, not before the pill; row=%q", row)
	}
}

// TestCachedReloadButtonMouseClick verifies that clicking the reload icon sets
// m.flashKey="cached:regenerate", returns a non-nil cmd, and triggers the
// in-process regenerate (REPLACE: md cleared, thinking on, no longer cached). A
// cached-answer regenerate seam stands in for a real orchestrator.
func TestCachedReloadButtonMouseClick(t *testing.T) {
	m := newModel("agent", "hello")
	m.width = 120
	m.height = 24
	m.isCached = true
	m.cachedAt = time.Now().Add(-1 * time.Minute)
	m.answerRegen = fakeAnswerRegen()
	m.reflow()

	var regenBtn *Button
	for i := range m.buttons {
		if m.buttons[i].Kind == "regenerate" {
			regenBtn = &m.buttons[i]
			break
		}
	}
	if regenBtn == nil {
		t.Fatal("no regenerate button found")
	}

	// Simulate a left-click at the reload icon's screen position.
	clickX := 2 + regenBtn.Col
	clickY := regenBtn.Line

	msg2, cmd := m.Update(tea.MouseClickMsg{
		Button: tea.MouseLeft,
		X:      clickX,
		Y:      clickY,
	})
	m2 := msg2.(model)

	if m2.flashKey != "cached:regenerate" {
		t.Errorf("flashKey = %q, want %q", m2.flashKey, "cached:regenerate")
	}
	if cmd == nil {
		t.Error("clicking reload icon must return a non-nil cmd")
	}
	if m2.md != "" {
		t.Errorf("regenerate must clear md (REPLACE); got %q", m2.md)
	}
	if m2.isCached {
		t.Error("regenerate must drop the cached state")
	}
	if !m2.thinking {
		t.Error("regenerate must start a thinking session")
	}
}

// TestCachedBadgeFlashHighlightsWholePill verifies that on click (flashKey ==
// "cached:regenerate") the WHOLE pill highlights: it flips to the flash colour
// (colFlashOn = #ffffff) as the background — so the peach bg is gone — while the
// reload glyph is still present. Without flash, the pill keeps its colPeach bg.
func TestCachedBadgeFlashHighlightsWholePill(t *testing.T) {
	m := newModel("agent", "hello")
	m.isCached = true
	m.cachedAt = time.Now().Add(-3 * time.Minute)
	m.answerRegen = fakeAnswerRegen() // wire a regenerate path so the reload renders

	const whiteBg = "48;2;255;255;255" // colFlashOn (#ffffff) as background
	const peachBg = "48;2;250;179;135" // colPeach  (#fab387) as background

	// Without flash: peach pill, no white flash bg.
	m.flashKey = ""
	normal := m.cachedBadge()
	if strings.Contains(normal, whiteBg) {
		t.Errorf("without flash, pill must NOT contain the white flash bg %q\ngot: %q", whiteBg, normal)
	}
	if !strings.Contains(normal, peachBg) {
		t.Errorf("without flash, pill must have the colPeach bg %q\ngot: %q", peachBg, normal)
	}
	if !strings.Contains(normal, "\U0010F1DA") {
		t.Error("cachedBadge() must contain the reload icon U+10F1DA")
	}

	// With flash: the WHOLE pill highlights — white flash bg present, peach bg gone.
	m.flashKey = "cached:regenerate"
	flash := m.cachedBadge()
	if !strings.Contains(flash, whiteBg) {
		t.Errorf("on click the whole pill must highlight with the white flash bg %q\ngot: %q", whiteBg, flash)
	}
	if strings.Contains(flash, peachBg) {
		t.Errorf("on flash the whole pill flips to the flash colour; colPeach bg must be gone\ngot: %q", flash)
	}
	if !strings.Contains(flash, "\U0010F1DA") {
		t.Error("flashed pill must still contain the reload icon")
	}
}

// TestRelativeAge verifies the relative-age helper boundaries.
func TestRelativeAge(t *testing.T) {
	now := time.Now()
	cases := []struct {
		age  time.Duration
		want string
	}{
		{30 * time.Second, "just now"},
		{3 * time.Minute, "3m ago"},
		{90 * time.Minute, "1h ago"},
		{48 * time.Hour, "2d ago"},
	}
	for _, tc := range cases {
		got := relativeAge(now.Add(-tc.age))
		if got != tc.want {
			t.Errorf("relativeAge(-%v) = %q, want %q", tc.age, got, tc.want)
		}
	}
}
