package dialog

import (
	"path/filepath"
	"testing"
)

// TestApplyHistory_LoadsIntoTextField asserts the load seam wires the JSONL file
// into the model's text field (so UP recall sees the entries) and is inert when
// the path is empty.
func TestApplyHistory_LoadsIntoTextField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "request-history.jsonl")
	if err := AppendHistory(path, "first request", historyCap); err != nil {
		t.Fatal(err)
	}
	if err := AppendHistory(path, "second request", historyCap); err != nil {
		t.Fatal(err)
	}

	m := newInputModel(defaultTheme(), "default", "t", "", "", "", 3, 1, 1, false, "")
	applyHistory(&m, path)

	tf, ok := m.fld.(*textField)
	if !ok {
		t.Fatal("text model field should be a *textField")
	}
	if len(tf.history) != 2 || tf.history[0] != "first request" || tf.history[1] != "second request" {
		t.Fatalf("loaded history = %v, want [first request second request]", tf.history)
	}

	// Empty path → no load (history stays empty, recall inert).
	m2 := newInputModel(defaultTheme(), "default", "t", "", "", "", 3, 1, 1, false, "")
	applyHistory(&m2, "")
	if tf2 := m2.fld.(*textField); len(tf2.history) != 0 {
		t.Fatalf("empty path should load nothing, got %v", tf2.history)
	}
}

// TestRecordHistory_AppendsOnSubmit asserts the append seam records the submitted
// value (honoring dedup) and is inert / non-fatal in the no-history and bad-path
// cases — the submit path must never block on history.
func TestRecordHistory_AppendsOnSubmit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "request-history.jsonl")

	recordHistory(path, "alpha")
	recordHistory(path, "beta")
	recordHistory(path, "beta") // consecutive dup → skipped

	got := LoadHistory(path)
	if len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("history after record = %v, want [alpha beta]", got)
	}

	// Empty path → no-op (no panic, nothing written).
	recordHistory("", "ignored")

	// A bad path (parent is a file, not a dir) must be non-fatal — recordHistory
	// reports to stderr and returns without panicking.
	bad := filepath.Join(path, "cannot", "nest", "under", "a", "file.jsonl")
	recordHistory(bad, "value") // must not panic
}
