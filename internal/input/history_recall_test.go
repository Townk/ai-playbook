package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// keyUp / keyDown build KeyPressMsg values the same way the other input tests do
// (e.g. tea.KeyPressMsg{Code: tea.KeyEnter}).
var (
	keyUp   = tea.KeyPressMsg{Code: tea.KeyUp}
	keyDown = tea.KeyPressMsg{Code: tea.KeyDown}
)

// recall drives one handle call and returns the field for chaining.
func recall(t *testing.T, f field, msg tea.Msg) field {
	t.Helper()
	nf, act, _ := f.handle(msg)
	if act != fieldNone {
		t.Fatalf("recall key must yield fieldNone, got %d", act)
	}
	return nf
}

func TestHistoryRecallWalkBackAndForward(t *testing.T) {
	tf := newTextField(defaultTheme(), "", "", 1, true)
	tf.SetHistory([]string{"a", "b", "c"})
	f := field(tf)

	f = recall(t, f, keyUp)
	if f.value() != "c" || tf.histIdx != 2 {
		t.Fatalf("UP from live: want value c histIdx 2, got %q %d", f.value(), tf.histIdx)
	}
	if tf.draft != "" {
		t.Fatalf("draft must be the saved live text %q, got %q", "", tf.draft)
	}
	f = recall(t, f, keyUp)
	if f.value() != "b" || tf.histIdx != 1 {
		t.Fatalf("UP: want b/1, got %q/%d", f.value(), tf.histIdx)
	}
	f = recall(t, f, keyUp)
	if f.value() != "a" || tf.histIdx != 0 {
		t.Fatalf("UP: want a/0, got %q/%d", f.value(), tf.histIdx)
	}
	// UP at the oldest entry stays put.
	f = recall(t, f, keyUp)
	if f.value() != "a" || tf.histIdx != 0 {
		t.Fatalf("UP at oldest must stay a/0, got %q/%d", f.value(), tf.histIdx)
	}

	f = recall(t, f, keyDown)
	if f.value() != "b" || tf.histIdx != 1 {
		t.Fatalf("DOWN: want b/1, got %q/%d", f.value(), tf.histIdx)
	}
	f = recall(t, f, keyDown)
	if f.value() != "c" || tf.histIdx != 2 {
		t.Fatalf("DOWN: want c/2, got %q/%d", f.value(), tf.histIdx)
	}
	// DOWN past the newest restores the (empty) live draft.
	f = recall(t, f, keyDown)
	if f.value() != "" || tf.histIdx != -1 {
		t.Fatalf("DOWN past newest must restore draft/-1, got %q/%d", f.value(), tf.histIdx)
	}
}

func TestHistoryRecallPreservesDraft(t *testing.T) {
	tf := newTextField(defaultTheme(), "wip", "", 1, true)
	tf.SetHistory([]string{"a", "b", "c"})
	f := field(tf)

	f = recall(t, f, keyUp)
	if tf.draft != "wip" {
		t.Fatalf("UP must save live draft %q, got %q", "wip", tf.draft)
	}
	if f.value() != "c" {
		t.Fatalf("UP must recall newest c, got %q", f.value())
	}
	// Page DOWN past the newest to restore the draft.
	f = recall(t, f, keyDown) // -> oldest direction? no: histIdx 2 is newest, DOWN -> restore draft
	if f.value() != "wip" || tf.histIdx != -1 {
		t.Fatalf("DOWN past newest must restore wip/-1, got %q/%d", f.value(), tf.histIdx)
	}
}

func TestHistoryRecallEmptyHistoryNoRecall(t *testing.T) {
	tf := newTextField(defaultTheme(), "", "", 1, true)
	f := field(tf)

	// No SetHistory call: empty history. UP/DOWN must not recall — the key
	// falls through to the textarea (pure cursor movement); value and histIdx
	// are unchanged.
	_, _, _ = f.handle(keyUp)
	if f.value() != "" || tf.histIdx != -1 {
		t.Fatalf("empty-history UP must not recall, got %q/%d", f.value(), tf.histIdx)
	}
	_, _, _ = f.handle(keyDown)
	if f.value() != "" || tf.histIdx != -1 {
		t.Fatalf("empty-history DOWN must not recall, got %q/%d", f.value(), tf.histIdx)
	}
}

func TestHistoryRecallGuardNotOnFirstLine(t *testing.T) {
	// A multi-line value with the cursor at the end sits on the LAST logical
	// line, so ta.Line() > 0. UP must NOT recall (the guard requires line 0);
	// it falls through as cursor movement.
	tf := newTextField(defaultTheme(), "line0\nline1", "", 3, false)
	tf.SetHistory([]string{"a", "b", "c"})
	f := field(tf)

	if tf.ta.Line() == 0 {
		t.Fatalf("test precondition: cursor must be off line 0, got Line()=%d", tf.ta.Line())
	}
	_, _, _ = f.handle(keyUp)
	if tf.histIdx != -1 {
		t.Fatalf("UP off the first line must not recall, histIdx=%d", tf.histIdx)
	}
	if f.value() != "line0\nline1" {
		t.Fatalf("UP off the first line must not change the value, got %q", f.value())
	}
}
