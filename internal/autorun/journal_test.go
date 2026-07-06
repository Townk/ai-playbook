package autorun

import (
	"io"
	"path/filepath"
	"testing"

	"github.com/Townk/ai-playbook/internal/runlog"
)

// journalFor opens a journal at a temp path and returns it with its path.
func journalFor(t *testing.T) (*runlog.Journal, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "j.json")
	j := runlog.Open(path, "/proj/pb.md", "hash")
	if j == nil {
		t.Fatal("runlog.Open returned nil")
	}
	return j, path
}

// TestExecute_JournalsEveryStepAndFinalizes drives Execute over a fake runner:
// each forward result must land in the journal incrementally, the first
// failure must be captured, and the run must finalize failed.
func TestExecute_JournalsEveryStepAndFinalizes(t *testing.T) {
	blocks := []Block{
		{ID: "a", Kind: KindRun, Command: "a"},
		{ID: "b", Kind: KindRun, Command: "b", Needs: []string{"a"}},
		{ID: "c", Kind: KindRun, Command: "c", Needs: []string{"b"}},
	}
	j, path := journalFor(t)
	r := &fakeRunner{exits: map[string]int{"b": 3}, timedOut: map[string]string{"b": "10s"}}
	Execute(Config{Blocks: blocks, Out: io.Discard, Journal: j}, r)

	run, err := runlog.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := run.Blocks["a"]; got.Outcome != runlog.OutcomeOK || got.Exit != 0 {
		t.Errorf("a = %+v, want ok/0", got)
	}
	if got := run.Blocks["b"]; got.Outcome != runlog.OutcomeFailed || got.Exit != 3 || got.TimedOutAfter != "10s" {
		t.Errorf("b = %+v, want failed/3 timed_out_after 10s", got)
	}
	if _, ok := run.Blocks["c"]; ok {
		t.Error("c never ran — it must have no record")
	}
	for id, rec := range run.Blocks {
		if rec.Duration < 0 {
			t.Errorf("%s duration = %v, want >= 0", id, rec.Duration)
		}
	}
	if run.FirstFailure != "b" {
		t.Errorf("FirstFailure = %q, want b", run.FirstFailure)
	}
	if run.Outcome != runlog.OutcomeFailed || run.Finished.IsZero() {
		t.Errorf("finalize: outcome=%q finished=%v, want failed + stamped", run.Outcome, run.Finished)
	}
}

// TestExecute_JournalsRollbackReRecords: with auto-rollback, the undone origin
// must be RE-recorded rolled-back (overwriting its ok) and the rollback
// target's own execution recorded as a normal step.
func TestExecute_JournalsRollbackReRecords(t *testing.T) {
	blocks := []Block{
		{ID: "a", Kind: KindRun, Command: "a", Rollback: "undo-a"},
		{ID: "undo-a", Kind: KindRun, Command: "undo-a"},
		{ID: "b", Kind: KindRun, Command: "b", Needs: []string{"a"}},
	}
	j, path := journalFor(t)
	r := &fakeRunner{exits: map[string]int{"b": 1}}
	Execute(Config{Blocks: blocks, AutoRollback: true, Out: io.Discard, Journal: j}, r)

	run, err := runlog.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := run.Blocks["a"].Outcome; got != runlog.OutcomeRolledBack {
		t.Errorf("a outcome = %q, want rolled-back (undone blocks are NOT ok)", got)
	}
	if got := run.Blocks["undo-a"].Outcome; got != runlog.OutcomeOK {
		t.Errorf("undo-a outcome = %q, want ok (the undo itself ran clean)", got)
	}
	if run.Outcome != runlog.OutcomeFailed || run.FirstFailure != "b" {
		t.Errorf("run outcome=%q first_failure=%q, want failed at b", run.Outcome, run.FirstFailure)
	}
}

// TestExecute_JournalsCancelledAsStopped: a user-interrupted step journals
// "stopped", and the run finalizes stopped (not failed).
func TestExecute_JournalsCancelledAsStopped(t *testing.T) {
	blocks := []Block{{ID: "a", Kind: KindRun, Command: "a"}}
	j, path := journalFor(t)
	r := &fakeRunner{exits: map[string]int{"a": cancelExit}, cancelled: map[string]bool{"a": true}}
	Execute(Config{Blocks: blocks, Out: io.Discard, Journal: j}, r)

	run, err := runlog.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := run.Blocks["a"]; got.Outcome != runlog.OutcomeStopped || got.Exit != cancelExit {
		t.Errorf("a = %+v, want stopped/%d", got, cancelExit)
	}
	if run.Outcome != runlog.OutcomeStopped {
		t.Errorf("run outcome = %q, want stopped", run.Outcome)
	}
}

// TestExecute_NilJournalIsOff: no journal configured — Execute must behave
// exactly as before (compile-time nil-receiver no-ops; nothing written).
func TestExecute_NilJournalIsOff(t *testing.T) {
	blocks := []Block{{ID: "a", Kind: KindRun, Command: "a"}}
	if code := Execute(Config{Blocks: blocks, Out: io.Discard}, &fakeRunner{}); code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
}

// TestRun_Journal_EndToEnd is THE one real --auto end-to-end journal test: a
// real driver runs a two-block playbook whose second block fails; the journal
// file must exist with correct per-block outcomes, positive durations, the
// first-failure id, and a failed run outcome.
func TestRun_Journal_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("opens a real driver")
	}
	path := filepath.Join(t.TempDir(), "j.json")
	code := Run(RunConfig{
		Blocks: []Block{
			{ID: "ok-step", Kind: KindRun, Command: "true"},
			{ID: "boom", Kind: KindRun, Command: "false", Needs: []string{"ok-step"}},
		},
		Slug: "t", Out: io.Discard, Now: func() string { return "STAMP" },
		JournalPath:         path,
		JournalPlaybookPath: "/proj/pb.md",
		JournalContentHash:  runlog.ContentHash("body"),
	})
	if code == 0 {
		t.Fatal("the failing run must exit non-zero")
	}
	run, err := runlog.Load(path)
	if err != nil {
		t.Fatalf("journal must exist after the run: %v", err)
	}
	if run.PlaybookPath != "/proj/pb.md" || run.ContentHash != runlog.ContentHash("body") {
		t.Errorf("identity = (%q, %q), want the configured path+hash", run.PlaybookPath, run.ContentHash)
	}
	if got := run.Blocks["ok-step"]; got.Outcome != runlog.OutcomeOK || got.Duration <= 0 {
		t.Errorf("ok-step = %+v, want ok with a positive duration", got)
	}
	if got := run.Blocks["boom"]; got.Outcome != runlog.OutcomeFailed || got.Exit == 0 || got.Duration <= 0 {
		t.Errorf("boom = %+v, want failed with non-zero exit and positive duration", got)
	}
	if run.FirstFailure != "boom" {
		t.Errorf("FirstFailure = %q, want boom", run.FirstFailure)
	}
	if run.Outcome != runlog.OutcomeFailed || run.Finished.IsZero() || run.Started.IsZero() {
		t.Errorf("run outcome=%q started=%v finished=%v, want finalized failed", run.Outcome, run.Started, run.Finished)
	}
}

// TestExecute_JournalsCancelledRollbackTargetAsStopped (review finding 4): a
// user interrupt delivered during a ROLLBACK TARGET's run must journal the
// target "stopped", exactly like the forward loop's cancelled handling.
func TestExecute_JournalsCancelledRollbackTargetAsStopped(t *testing.T) {
	blocks := []Block{
		{ID: "a", Kind: KindRun, Command: "a", Rollback: "undo-a"},
		{ID: "undo-a", Kind: KindRun, Command: "undo-a"},
		{ID: "b", Kind: KindRun, Command: "b", Needs: []string{"a"}},
	}
	j, path := journalFor(t)
	r := &fakeRunner{
		exits:     map[string]int{"b": 1, "undo-a": cancelExit},
		cancelled: map[string]bool{"undo-a": true},
	}
	Execute(Config{Blocks: blocks, AutoRollback: true, Out: io.Discard, Journal: j}, r)

	run, err := runlog.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := run.Blocks["undo-a"]; got.Outcome != runlog.OutcomeStopped || got.Exit != cancelExit {
		t.Errorf("undo-a = %+v, want stopped/%d (cancelled threaded like the forward loop)", got, cancelExit)
	}
	if got := run.Blocks["a"].Outcome; got != runlog.OutcomeRolledBack {
		t.Errorf("a = %q, want rolled-back", got)
	}
}
