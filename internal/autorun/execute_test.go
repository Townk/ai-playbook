package autorun

import (
	"io"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls     []string
	exits     map[string]int  // id → exit (default 0)
	cancelled map[string]bool // id → cancelled (default false)
}

func (f *fakeRunner) RunStep(s Step) (int, string, bool) {
	f.calls = append(f.calls, s.ID)
	return f.exits[s.ID], "", f.cancelled[s.ID] // 0/false unless scripted
}

func TestExecute_StopsAtFirstFailure_NoLaterSteps(t *testing.T) {
	blocks := []Block{
		{ID: "a", Kind: KindRun, Command: "a"},
		{ID: "b", Kind: KindRun, Command: "b", Needs: []string{"a"}},
		{ID: "c", Kind: KindRun, Command: "c", Needs: []string{"b"}},
	}
	r := &fakeRunner{exits: map[string]int{"b": 3}}
	code := Execute(Config{Blocks: blocks, Out: io.Discard}, r)
	if code != 3 {
		t.Errorf("exit code = %d, want 3 (failed step's exit)", code)
	}
	// c must never run (never continue past a failure).
	for _, id := range r.calls {
		if id == "c" {
			t.Error("c ran after b failed")
		}
	}
}

func TestExecute_AutoRollback_ReverseOfCompleted(t *testing.T) {
	blocks := []Block{
		{ID: "a", Kind: KindRun, Command: "a", Rollback: "undo-a"},
		{ID: "undo-a", Kind: KindRun, Command: "undo-a"},
		{ID: "b", Kind: KindRun, Command: "b", Needs: []string{"a"}},
	}
	r := &fakeRunner{exits: map[string]int{"b": 1}}
	code := Execute(Config{Blocks: blocks, AutoRollback: true, Out: io.Discard}, r)
	if code == 0 {
		t.Error("failure must exit non-zero")
	}
	// after a ok then b fails, undo-a must have run.
	ran := false
	for _, id := range r.calls {
		if id == "undo-a" {
			ran = true
		}
	}
	if !ran {
		t.Error("auto-rollback must run undo-a for the completed step a")
	}
}

func TestExecute_NoAutoRollback_LeavesState(t *testing.T) {
	blocks := []Block{
		{ID: "a", Kind: KindRun, Command: "a", Rollback: "undo-a"},
		{ID: "undo-a", Kind: KindRun, Command: "undo-a"},
		{ID: "b", Kind: KindRun, Command: "b", Needs: []string{"a"}},
	}
	r := &fakeRunner{exits: map[string]int{"b": 1}}
	Execute(Config{Blocks: blocks, AutoRollback: false, Out: io.Discard}, r)
	for _, id := range r.calls {
		if id == "undo-a" {
			t.Error("no-auto-rollback must NOT run undo-a")
		}
	}
}

func TestExecute_AllGreen_ExitZero(t *testing.T) {
	blocks := []Block{{ID: "a", Kind: KindRun, Command: "a"}, {ID: "b", Kind: KindRun, Command: "b", Needs: []string{"a"}}}
	if code := Execute(Config{Blocks: blocks, Out: io.Discard}, &fakeRunner{}); code != 0 {
		t.Errorf("all-green exit = %d, want 0", code)
	}
}

// statusOfID scans a Summarize-rendered summary for the row whose ID column
// (the 3rd whitespace-separated field: symbol, status, id) matches id, and
// returns that row's status column.
func statusOfID(summary, id string) string {
	for _, line := range strings.Split(summary, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[2] == id {
			return fields[1]
		}
	}
	return ""
}

func TestExecute_Rollback_LabelsOriginRolledBackTargetOk(t *testing.T) {
	blocks := []Block{
		{ID: "a", Kind: KindRun, Command: "a", Rollback: "undo-a"},
		{ID: "undo-a", Kind: KindRun, Command: "undo-a"},
		{ID: "boom", Kind: KindRun, Command: "boom", Needs: []string{"a"}},
	}
	r := &fakeRunner{exits: map[string]int{"boom": 1}}
	var out strings.Builder
	code := Execute(Config{Blocks: blocks, AutoRollback: true, Out: &out}, r)
	if code == 0 {
		t.Fatal("failure must exit non-zero")
	}

	if got := statusOfID(out.String(), "a"); got != StatusRolledBack {
		t.Errorf("origin a summary status = %q, want %q\n%s", got, StatusRolledBack, out.String())
	}
	if got := statusOfID(out.String(), "undo-a"); got != StatusOK {
		t.Errorf("target undo-a summary status = %q, want %q\n%s", got, StatusOK, out.String())
	}
}

func TestExecute_Cancelled_SkipsRollbackAndLabels(t *testing.T) {
	blocks := []Block{
		{ID: "a", Kind: KindRun, Command: "a", Rollback: "undo-a"},
		{ID: "undo-a", Kind: KindRun, Command: "undo-a"},
		{ID: "boom", Kind: KindRun, Command: "boom", Needs: []string{"a"}},
	}
	r := &fakeRunner{
		exits:     map[string]int{"boom": 130},
		cancelled: map[string]bool{"boom": true},
	}
	var out strings.Builder
	code := Execute(Config{Blocks: blocks, AutoRollback: true, Out: &out}, r)
	if code == 0 {
		t.Fatal("cancelled run must exit non-zero")
	}

	for _, id := range r.calls {
		if id == "undo-a" {
			t.Error("undo-a must NOT run after a cancellation")
		}
	}
	if got := statusOfID(out.String(), "boom"); got != StatusCancelled {
		t.Errorf("cancelled step summary status = %q, want %q\n%s", got, StatusCancelled, out.String())
	}
}
