package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// hasButton returns true if any button in btns has the given Kind.
func hasButton(btns []Button, kind string) bool {
	for _, b := range btns {
		if b.Kind == kind {
			return true
		}
	}
	return false
}

// newTestModelFileBacked builds a model with sourcePath set (file-backed) and
// a standard test layout, reflowed so m.buttons is populated.
func newTestModelFileBacked(t *testing.T, path string) model {
	t.Helper()
	m := newModel("T", "# File-backed\n")
	m.width, m.height = 80, 24
	m.sourcePath = path
	return m
}

// TestEditButton_OnlyWhenFileBacked verifies that:
//   - a file-backed playbook (sourcePath non-empty) gets an [edit] button after reflow
//   - an ephemeral playbook (sourcePath == "") does NOT get an [edit] button
func TestEditButton_OnlyWhenFileBacked(t *testing.T) {
	fb := newTestModelFileBacked(t, "/store/x.md")
	fb.reflow()
	if !hasButton(fb.buttons, "edit") {
		t.Fatal("file-backed playbook must have an [edit] button")
	}

	eph := newModel("T", "# Ephemeral\n")
	eph.width, eph.height = 80, 24
	eph.reflow()
	if hasButton(eph.buttons, "edit") {
		t.Fatal("ephemeral playbook must NOT have an [edit] button")
	}
}

// TestEditButton_IsScreen verifies the [edit] button is Screen=true (absolute
// row hit-test, not body-line relative) and on the title row (screen row 1).
func TestEditButton_IsScreen(t *testing.T) {
	m := newTestModelFileBacked(t, "/store/x.md")
	m.reflow()
	for _, b := range m.buttons {
		if b.Kind == "edit" {
			if !b.Screen {
				t.Error("[edit] button must have Screen=true")
			}
			if b.Line != 1 {
				t.Errorf("[edit] button Line = %d, want 1 (title row)", b.Line)
			}
			if b.BlockID != "edit" {
				t.Errorf("[edit] button BlockID = %q, want \"edit\"", b.BlockID)
			}
			return
		}
	}
	t.Fatal("[edit] button not found (TestEditButton_IsScreen precondition failed)")
}

// TestResolveEditor_Order verifies the resolution precedence:
// $VISUAL wins over $EDITOR, $EDITOR wins over the "vi" fallback.
func TestResolveEditor_Order(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	if got := resolveEditor(); got != "vi" {
		t.Fatalf("fallback must be vi, got %q", got)
	}

	t.Setenv("EDITOR", "nano")
	if got := resolveEditor(); got != "nano" {
		t.Fatalf("$EDITOR wins over fallback, got %q", got)
	}

	t.Setenv("VISUAL", "code -w")
	if got := resolveEditor(); got != "code -w" {
		t.Fatalf("$VISUAL wins over $EDITOR, got %q", got)
	}
}

// TestSourcePoll_ReloadsOnMtimeChange verifies that a sourcePollMsg fires a reload
// when the source file's mtime has advanced past m.sourceMtime.
func TestSourcePoll_ReloadsOnMtimeChange(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "p.md")
	if err := os.WriteFile(src, []byte("# A\n\n```bash {id=one}\necho a\n```\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newTestModelFileBacked(t, src)
	st, _ := os.Stat(src)
	m.sourceMtime = st.ModTime()
	// bump mtime and update content
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(src, future, future); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("# A\n\n```bash {id=one}\necho b\n```\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m2i, _ := m.Update(sourcePollMsg{})
	if !strings.Contains(m2i.(model).md, "echo b") {
		t.Fatal("a newer source mtime must reload")
	}
}

// TestReloadSource_UpdatesMdAndPreservesState verifies that reloadSource:
//   - re-reads sourcePath and updates m.md to match the new content
//   - does NOT clear blockStates, so transient per-block state survives the reload
func TestReloadSource_UpdatesMdAndPreservesState(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "p.md")
	if err := os.WriteFile(src, []byte("# T\n\n```bash {id=one}\necho hi\n```\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newTestModelFileBacked(t, src)
	m.blockStates["one"] = blockRunState{Status: "ok"} // transient state to preserve
	// edit the source on disk
	if err := os.WriteFile(src, []byte("# T2\n\n```bash {id=one}\necho bye\n```\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.reloadSource(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.md, "echo bye") {
		t.Fatal("m.md must reflect the edited source")
	}
	if m.blockStates["one"].Status != "ok" {
		t.Fatal("transient block state must survive reload")
	}
}
