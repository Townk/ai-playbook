package autorun

import (
	"io"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	calls     []string
	exits     map[string]int    // id → exit (default 0)
	cancelled map[string]bool   // id → cancelled (default false)
	timedOut  map[string]string // id → formatted effective ceiling ("" = not timed out)
}

func (f *fakeRunner) RunStep(s Step) (int, string, string, bool) {
	f.calls = append(f.calls, s.ID)
	return f.exits[s.ID], "", f.timedOut[s.ID], f.cancelled[s.ID] // 0/""/false unless scripted
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

// timeoutRunner records each step's declared Timeout by id (extends the
// fakeRunner pattern; kept separate so the existing exits/cancelled scripting
// stays untouched).
type timeoutRunner struct {
	fakeRunner
	timeouts map[string]time.Duration
}

func (f *timeoutRunner) RunStep(s Step) (int, string, string, bool) {
	if f.timeouts == nil {
		f.timeouts = map[string]time.Duration{}
	}
	f.timeouts[s.ID] = s.Timeout
	return f.fakeRunner.RunStep(s)
}

// Execute must carry Block.Timeout onto every Step it builds — the forward
// steps AND a rollback target (which gets its OWN block's declared ceiling).
func TestExecute_StepCarriesTimeout(t *testing.T) {
	blocks := []Block{
		{ID: "a", Kind: KindRun, Command: "a", Timeout: 2 * time.Minute, Rollback: "undo-a"},
		{ID: "undo-a", Kind: KindRun, Command: "undo-a", Timeout: 7 * time.Minute},
		{ID: "b", Kind: KindRun, Command: "b", Needs: []string{"a"}},
	}
	r := &timeoutRunner{fakeRunner: fakeRunner{exits: map[string]int{"b": 1}}}
	Execute(Config{Blocks: blocks, AutoRollback: true, Out: io.Discard}, r)
	if got := r.timeouts["a"]; got != 2*time.Minute {
		t.Errorf("forward step a Timeout = %v, want 2m", got)
	}
	if got := r.timeouts["b"]; got != 0 {
		t.Errorf("undeclared step b Timeout = %v, want 0 (the runner's default applies)", got)
	}
	if got := r.timeouts["undo-a"]; got != 7*time.Minute {
		t.Errorf("rollback target undo-a Timeout = %v, want 7m", got)
	}
}

// A step the runner reports as timed out surfaces the timed-out form in the
// end-of-run summary (Execute → StepResult.TimedOutAfter → Summarize), while
// a plain failure keeps the existing row.
func TestExecute_SummaryShowsTimedOutStep(t *testing.T) {
	blocks := []Block{{ID: "slow", Kind: KindRun, Command: "sleep 30", Timeout: time.Second}}
	var out strings.Builder
	r := &fakeRunner{exits: map[string]int{"slow": 143}, timedOut: map[string]string{"slow": "1s"}}
	Execute(Config{Blocks: blocks, Out: &out}, r)
	if !strings.Contains(out.String(), "(timed out after 1s, exit 143)") {
		t.Errorf("summary must render the timed-out row:\n%s", out.String())
	}

	out.Reset()
	Execute(Config{Blocks: []Block{{ID: "boom", Kind: KindRun, Command: "false"}}, Out: &out},
		&fakeRunner{exits: map[string]int{"boom": 1}})
	if !strings.Contains(out.String(), "(exit 1)") || strings.Contains(out.String(), "timed out") {
		t.Errorf("plain failure summary changed:\n%s", out.String())
	}
}
