package autorun

import (
	"io"
	"testing"
)

type fakeRunner struct {
	calls []string
	exits map[string]int // id → exit (default 0)
}

func (f *fakeRunner) RunStep(s Step) (int, string) {
	f.calls = append(f.calls, s.ID)
	return f.exits[s.ID], "" // 0 unless scripted
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
