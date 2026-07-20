package diff

// viewer_test.go — behavioral tests for the standalone diff viewer's tea model
// (newViewerModel/rerender/Update/View/clamp). Main itself (the tea.Program
// run) needs a TTY and stays on the live path.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// writeViewerPatch writes a small parseable patch and returns its path.
func writeViewerPatch(t *testing.T, lines int) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("--- a/f.txt\n+++ b/f.txt\n")
	for i := 0; i < lines; i++ {
		b.WriteString("@@ -1 +1 @@\n-old\n+new\n")
	}
	p := filepath.Join(t.TempDir(), "p.patch")
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestViewerModel_ScrollAndView(t *testing.T) {
	m := newViewerModel(writeViewerPatch(t, 40))
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 11})
	v := nm.(viewerModel)
	if v.height != 10 {
		t.Fatalf("height = %d, want terminal-1", v.height)
	}
	if len(v.lines) == 0 {
		t.Fatal("rerender must populate lines")
	}
	maxOff := len(v.lines) - v.height
	// j scrolls down, k back up, G to bottom, g to top, paging clamps.
	step := func(m viewerModel, key string) viewerModel {
		nm, _ := m.Update(tea.KeyPressMsg{Text: key})
		return nm.(viewerModel)
	}
	v = step(v, "j")
	if v.offset != 1 {
		t.Errorf("j: offset = %d, want 1", v.offset)
	}
	v = step(v, "k")
	if v.offset != 0 {
		t.Errorf("k: offset = %d, want 0", v.offset)
	}
	v = step(v, "G")
	if v.offset != maxOff {
		t.Errorf("G: offset = %d, want %d", v.offset, maxOff)
	}
	v = step(v, "pgdown")
	if v.offset != maxOff {
		t.Errorf("pgdown at bottom must clamp: %d", v.offset)
	}
	v = step(v, "g")
	if v.offset != 0 {
		t.Errorf("g: offset = %d, want 0", v.offset)
	}
	v = step(v, "pgup")
	if v.offset != 0 {
		t.Errorf("pgup at top must clamp: %d", v.offset)
	}
	// View pads to height and always shows the hint bar last.
	out := v.View().Content
	rows := strings.Split(out, "\n")
	if len(rows) != v.height+1 {
		t.Fatalf("view rows = %d, want height+hint = %d", len(rows), v.height+1)
	}
	if !strings.Contains(rows[len(rows)-1], "q quit") {
		t.Errorf("last row must be the hint bar: %q", rows[len(rows)-1])
	}
	// q quits.
	if _, cmd := v.Update(tea.KeyPressMsg{Text: "q"}); cmd == nil {
		t.Error("q must quit the viewer")
	}
}

func TestViewerModel_MissingFileStillRenders(t *testing.T) {
	m := newViewerModel(filepath.Join(t.TempDir(), "missing.patch"))
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	v := nm.(viewerModel)
	if !strings.Contains(strings.Join(v.lines, "\n"), "cannot read") {
		t.Error("a missing patch must render the read error, not crash")
	}
}

func TestClamp(t *testing.T) {
	if clamp(5, 0, 3) != 3 || clamp(-2, 0, 3) != 0 || clamp(2, 0, 3) != 2 {
		t.Error("clamp bounds wrong")
	}
}
