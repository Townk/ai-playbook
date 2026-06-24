package ui

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestParseResultsEmitsPerRecord(t *testing.T) {
	in := strings.NewReader("fix\x1f0\x1f/tmp/log1\x1ebad\x1f2\x1f/tmp/log2\x1e")
	var got []resultMsg
	parseResults(in, func(m tea.Msg) { got = append(got, m.(resultMsg)) })
	if len(got) != 2 || got[0].ID != "fix" || got[0].Exit != 0 || got[1].Exit != 2 || got[1].Logpath != "/tmp/log2" {
		t.Fatalf("parse = %+v", got)
	}
}

func TestReviewingStateShownThenClearedByResult(t *testing.T) {
	m := newModel("T", "```diff {id=d}\n--- a\n+++ b\n```\n")
	m.width, m.height = 80, 24
	m = m.markReviewing("d")
	m.reflow()
	if !linesContain(m.lines, "Reviewing") {
		t.Fatal("reviewing state must show Reviewing…")
	}
	m2, _ := m.Update(resultMsg{ID: "d", Exit: 0, Logpath: ""})
	if m2.(model).blockStates["d"].Status == "reviewing" {
		t.Fatal("resultMsg must clear reviewing")
	}
}

func TestUpdateResultMsgSetsBlockState(t *testing.T) {
	m := newModel("T", "")
	m.width, m.height = 80, 24
	m.blockStates = map[string]blockRunState{"fix": {Status: "running"}}
	m2, _ := m.Update(resultMsg{ID: "fix", Exit: 0, Logpath: "/tmp/log"})
	st := m2.(model).blockStates["fix"]
	if st.Status != "ok" || st.Logpath != "/tmp/log" {
		t.Fatalf("state = %+v", st)
	}
	m3, _ := m2.(model).Update(resultMsg{ID: "z", Exit: 1, Logpath: "/l"})
	if m3.(model).blockStates["z"].Status != "failed" {
		t.Fatalf("nonzero exit must be failed")
	}
}

// TestParseResultsSurvivesWriterReopen verifies that a reader opened O_RDWR
// (rather than O_RDONLY) survives the broker's per-write open→write→close
// pattern, receiving ALL records across separate writer open/close cycles.
//
// With O_RDONLY the reader would see EOF after the first writer closes, so
// only the first record would arrive; with O_RDWR the pager itself is always
// a writer on the pipe so no EOF is generated between broker cycles.
func TestParseResultsSurvivesWriterReopen(t *testing.T) {
	dir := t.TempDir()
	fifo := dir + "/results"
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}

	// Open the fifo O_RDWR — this is the fix we're testing.
	r, err := os.OpenFile(fifo, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open rdwr: %v", err)
	}

	// got receives results from parseResults via send; capacity 4 so send
	// never blocks (parseResults calls send synchronously inside the scan loop).
	got := make(chan resultMsg, 4)

	// After exactly 2 records are delivered, the send callback writes a sentinel
	// record separator to the write side of the fifo — using r itself (which is
	// opened O_RDWR, so it can write). This wakes up the blocking scanner with
	// real data, and parseResults processes the empty-looking token and loops
	// back. The send callback then closes r on the second call, which causes
	// the next Read in the scanner to return an error and parseResults to return.
	// We rely on the fact that closing r from within the send callback (called
	// synchronously by parseResults in the same goroutine) is safe — parseResults
	// won't call Read again until send returns, so there's no concurrent read.
	count := 0
	send := func(m tea.Msg) {
		got <- m.(resultMsg)
		count++
		if count >= 2 {
			r.Close() // safe: called from within parseResults, before next Scan
		}
	}

	// Simulate the broker: two separate open→write→close cycles.
	go func() {
		// First record.
		w1, err := os.OpenFile(fifo, os.O_WRONLY, 0)
		if err != nil {
			return
		}
		w1.WriteString("a\x1f0\x1f/l1\x1e")
		w1.Close()

		// Second record — new open/close cycle: this is what kills an O_RDONLY reader.
		w2, err := os.OpenFile(fifo, os.O_WRONLY, 0)
		if err != nil {
			return
		}
		w2.WriteString("b\x1f1\x1f/l2\x1e")
		w2.Close()
	}()

	parseResults(r, send)
	close(got)

	var results []resultMsg
	for rm := range got {
		results = append(results, rm)
	}

	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d: %+v", len(results), results)
	}
	if results[0].ID != "a" || results[0].Exit != 0 {
		t.Errorf("record 0: want {a 0}, got {%s %d}", results[0].ID, results[0].Exit)
	}
	if results[1].ID != "b" || results[1].Exit != 1 {
		t.Errorf("record 1: want {b 1}, got {%s %d}", results[1].ID, results[1].Exit)
	}
}

// TestResultHandlerApplyUndoInterpretation verifies the state-machine table for
// the apply⇄undo result handler:
//
//	(apply, exit=0)  → Status "ok"
//	(undo,  exit=0)  → Status "" (cleared; dependents re-lock)
//	(undo,  exit≠0)  → Status "ok" (graceful; still applied)
//	(apply, exit≠0)  → Status "failed"
func TestResultHandlerApplyUndoInterpretation(t *testing.T) {
	cases := []struct {
		name       string
		action     string
		exit       int
		wantStatus string
	}{
		{"apply-ok", "apply", 0, "ok"},
		{"undo-ok", "undo", 0, ""},
		{"undo-fail", "undo", 1, "ok"},
		{"apply-fail", "apply", 1, "failed"},
		{"run-ok", "run", 0, "ok"},
		{"run-fail", "", 1, "failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel("T", "")
			m.width, m.height = 80, 24
			m.blockStates = map[string]blockRunState{
				"blk": {Status: "running", Action: tc.action},
			}
			m2, _ := m.Update(resultMsg{ID: "blk", Exit: tc.exit, Logpath: "/tmp/log"})
			st := m2.(model).blockStates["blk"]
			if st.Status != tc.wantStatus {
				t.Errorf("action=%q exit=%d: Status = %q, want %q", tc.action, tc.exit, st.Status, tc.wantStatus)
			}
			if st.Action != "" {
				t.Errorf("action=%q exit=%d: Action must be cleared after result, got %q", tc.action, tc.exit, st.Action)
			}
		})
	}
}

// TestApplyDiffClickSetsRunningAndEmits verifies that clicking apply-diff sets
// Status=running, Action=apply, and emits an apply-diff action record.
func TestApplyDiffClickSetsRunningAndEmits(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "act")

	md := "```diff {id=fix}\n--- a\n+++ b\n```\n"
	m := newModel("T", md)
	m.width, m.height = 80, 24
	m.fifoPath = fifo
	m.reflow()

	// Find the apply-diff button.
	var applyBtn *Button
	for i := range m.buttons {
		if m.buttons[i].Kind == "apply-diff" && m.buttons[i].BlockID == "fix" {
			applyBtn = &m.buttons[i]
			break
		}
	}
	if applyBtn == nil {
		t.Fatal("apply-diff button not found")
	}

	m2, cmd := m.Update(tea.MouseClickMsg{
		Button: tea.MouseLeft,
		X:      applyBtn.Col + 2, // +2 for the 2-col left margin
		Y:      applyBtn.Line + m.bodyTop(),
	})
	m3 := m2.(model)

	st := m3.blockStates["fix"]
	if st.Status != "running" {
		t.Errorf("after apply-diff click: Status = %q, want running", st.Status)
	}
	if st.Action != "apply" {
		t.Errorf("after apply-diff click: Action = %q, want apply", st.Action)
	}
	if cmd == nil {
		t.Error("apply-diff click must return a non-nil cmd (tick+flash)")
	}

	// Verify the emitted record kind is apply-diff.
	f, err := os.Open(fifo)
	if err != nil {
		t.Fatalf("open fifo: %v", err)
	}
	defer f.Close()
	buf := make([]byte, 256)
	n, _ := f.Read(buf)
	rec := string(buf[:n])
	kind, _, _ := strings.Cut(strings.TrimSuffix(rec, "\x1e"), "\x1f")
	if kind != "apply-diff" {
		t.Errorf("emitted kind = %q, want apply-diff", kind)
	}
}

// TestUndoDiffClickSetsRunningAndEmits verifies that clicking undo-diff sets
// Status=running, Action=undo, and emits an undo-diff action record.
func TestUndoDiffClickSetsRunningAndEmits(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "act")

	md := "```diff {id=fix}\n--- a\n+++ b\n```\n"
	m := newModel("T", md)
	m.width, m.height = 80, 24
	m.fifoPath = fifo
	// Seed the block as already applied.
	m.blockStates["fix"] = blockRunState{Status: "ok"}
	m.reflow()

	// Find the undo-diff button.
	var undoBtn *Button
	for i := range m.buttons {
		if m.buttons[i].Kind == "undo-diff" && m.buttons[i].BlockID == "fix" {
			undoBtn = &m.buttons[i]
			break
		}
	}
	if undoBtn == nil {
		t.Fatal("undo-diff button not found after seeding Status=ok")
	}

	m2, cmd := m.Update(tea.MouseClickMsg{
		Button: tea.MouseLeft,
		X:      undoBtn.Col + 2, // +2 for the 2-col left margin
		Y:      undoBtn.Line + m.bodyTop(),
	})
	m3 := m2.(model)

	st := m3.blockStates["fix"]
	if st.Status != "running" {
		t.Errorf("after undo-diff click: Status = %q, want running", st.Status)
	}
	if st.Action != "undo" {
		t.Errorf("after undo-diff click: Action = %q, want undo", st.Action)
	}
	if cmd == nil {
		t.Error("undo-diff click must return a non-nil cmd (tick+flash)")
	}

	// Verify the emitted record kind is undo-diff.
	f, err := os.Open(fifo)
	if err != nil {
		t.Fatalf("open fifo: %v", err)
	}
	defer f.Close()
	buf := make([]byte, 256)
	n, _ := f.Read(buf)
	rec := string(buf[:n])
	kind, _, _ := strings.Cut(strings.TrimSuffix(rec, "\x1e"), "\x1f")
	if kind != "undo-diff" {
		t.Errorf("emitted kind = %q, want undo-diff", kind)
	}
}
