package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

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

// TestExpandedEmptyLog_SuccessMessages verifies F17: a successful diff-apply /
// file-create with an empty log shows an affirmative message rather than the
// "(log unavailable)" fallback (which is kept for a genuinely empty run log).
func TestExpandedEmptyLog_SuccessMessages(t *testing.T) {
	cases := []struct {
		name  string
		md    string
		want  string
		avoid string
	}{
		{
			name:  "diff-apply",
			md:    "```diff {id=fix}\n--- a\n+++ b\n@@ -1 +1 @@\n-a\n+b\n```\n",
			want:  "Diff applied successfully",
			avoid: "(log unavailable)",
		},
		{
			name:  "file-create",
			md:    "```text {id=fix file=x.txt}\nhello\n```\n",
			want:  "File created",
			avoid: "(log unavailable)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel("T", tc.md)
			m.width, m.height = 100, 30
			// Expanded, successful, empty log (nonexistent path → tailFile returns nil).
			m.blockStates = map[string]blockRunState{"fix": {Status: "ok", Expanded: true, Logpath: "/nonexistent/ai-playbook-log"}}
			m.reflow()
			out := strip(m.viewString())
			if !strings.Contains(out, tc.want) {
				t.Errorf("expected %q in output:\n%s", tc.want, out)
			}
			if strings.Contains(out, tc.avoid) {
				t.Errorf("did not expect %q in output:\n%s", tc.avoid, out)
			}
		})
	}
}

// TestApplyDiffClickSetsRunningAndEmits verifies that clicking apply-diff sets
// Status=running, Action=apply, and returns a non-nil cmd.
func TestApplyDiffClickSetsRunningAndEmits(t *testing.T) {
	md := "```diff {id=fix}\n--- a\n+++ b\n```\n"
	m := newModel("T", md)
	m.width, m.height = 80, 24
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
}

// TestUndoDiffClickSetsRunningAndEmits verifies that clicking undo-diff sets
// Status=running, Action=undo, and returns a non-nil cmd.
func TestUndoDiffClickSetsRunningAndEmits(t *testing.T) {
	md := "```diff {id=fix}\n--- a\n+++ b\n```\n"
	m := newModel("T", md)
	m.width, m.height = 80, 24
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
}
