package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Townk/ai-playbook/internal/runlog"
)

// journalModel builds a viewer model over md with a live run journal at a
// temp path, mirroring what Run does when the launcher supplies the journal
// options. Returns the model and the journal path for Load-based asserts.
func journalModel(t *testing.T, md string) (model, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "j.json")
	m := newModel("T", md)
	m.width, m.height = 80, 24
	m.journal = runlog.Open(path, "/proj/pb.md", runlog.ContentHash(md))
	if m.journal == nil {
		t.Fatal("runlog.Open returned nil")
	}
	m.reflow()
	return m, path
}

func loadJournal(t *testing.T, path string) runlog.Run {
	t.Helper()
	r, err := runlog.Load(path)
	if err != nil {
		t.Fatalf("Load(%s): %v", path, err)
	}
	return r
}

// TestJournal_ResultWritesEveryOutcome: ok, failed (with timeout marker), and
// stopped results must each land in the journal as they settle, with exit,
// duration (from the running mark), and timed_out_after.
func TestJournal_ResultWritesEveryOutcome(t *testing.T) {
	md := "```bash {id=a}\ntrue\n```\n\n```bash {id=b needs=a}\nfalse\n```\n\n```bash {id=c}\nsleep 99\n```\n"
	m, path := journalModel(t, md)

	// a runs ok. markRunning stamps the start; back-date it so the recorded
	// duration is provably wall-clock (>= 30ms), not zero.
	m = m.markRunning("a")
	st := m.blockStates["a"]
	st.runStartedAt = time.Now().Add(-30 * time.Millisecond)
	m.blockStates["a"] = st
	m = mustModel(m.handleResult(resultMsg{ID: "a", Exit: 0}))
	r := loadJournal(t, path)
	if got := r.Blocks["a"]; got.Outcome != runlog.OutcomeOK || got.Exit != 0 || got.Duration < 30*time.Millisecond {
		t.Errorf("a = %+v, want ok/0 with duration >= 30ms", got)
	}

	// b fails at its timeout ceiling.
	m = m.markRunning("b")
	m = mustModel(m.handleResult(resultMsg{ID: "b", Exit: 124, TimedOut: true, TimedOutAfter: 10 * time.Second}))
	r = loadJournal(t, path)
	if got := r.Blocks["b"]; got.Outcome != runlog.OutcomeFailed || got.Exit != 124 || got.TimedOutAfter != "10s" {
		t.Errorf("b = %+v, want failed/124 timed_out_after 10s", got)
	}
	if r.FirstFailure != "b" {
		t.Errorf("FirstFailure = %q, want b", r.FirstFailure)
	}

	// c is deliberately stopped by the user.
	m = m.markRunning("c")
	m.markStopped("c")
	_ = mustModel(m.handleResult(resultMsg{ID: "c", Exit: 143}))
	r = loadJournal(t, path)
	if got := r.Blocks["c"]; got.Outcome != runlog.OutcomeStopped || got.Exit != 143 {
		t.Errorf("c = %+v, want stopped/143", got)
	}
	// b failed first; c's stop must not displace it.
	if r.FirstFailure != "b" {
		t.Errorf("FirstFailure = %q after stop, want b", r.FirstFailure)
	}
}

// TestJournal_RerunOverwritesRecord: a failed block manually re-run to ok is
// OVERWRITTEN in place (one record per id) and FirstFailure clears.
func TestJournal_RerunOverwritesRecord(t *testing.T) {
	m, path := journalModel(t, "```bash {id=a}\ntrue\n```\n")
	m = m.markRunning("a")
	m = mustModel(m.handleResult(resultMsg{ID: "a", Exit: 1}))
	if r := loadJournal(t, path); r.FirstFailure != "a" {
		t.Fatalf("FirstFailure = %q, want a", r.FirstFailure)
	}
	m = m.markRunning("a")
	_ = mustModel(m.handleResult(resultMsg{ID: "a", Exit: 0}))
	r := loadJournal(t, path)
	if got := r.Blocks["a"]; got.Outcome != runlog.OutcomeOK {
		t.Errorf("a = %+v, want the ok re-run to overwrite the failure", got)
	}
	if len(r.Blocks) != 1 {
		t.Errorf("blocks = %d records, want 1 (overwrite, not append)", len(r.Blocks))
	}
	if r.FirstFailure != "" {
		t.Errorf("FirstFailure = %q, want cleared once the failed block re-ran ok", r.FirstFailure)
	}
}

// TestJournal_RollbackReRecordsUndoneBlocks: a rollback chain re-records the
// undone origin as rolled-back (over its ok record) and the rollback target's
// own execution lands as a normal ok record.
func TestJournal_RollbackReRecordsUndoneBlocks(t *testing.T) {
	md := "```bash {id=stage rollback=undo-stage}\ntrue\n```\n\n" +
		"```bash {id=undo-stage}\ntrue\n```\n\n" +
		"```bash {id=deploy needs=stage}\nfalse\n```\n"
	m, path := journalModel(t, md)

	// stage ok, then deploy fails.
	m = m.markRunning("stage")
	m = mustModel(m.handleResult(resultMsg{ID: "stage", Exit: 0}))
	m = m.markRunning("deploy")
	m = mustModel(m.handleResult(resultMsg{ID: "deploy", Exit: 1}))
	if got := loadJournal(t, path).Blocks["stage"].Outcome; got != runlog.OutcomeOK {
		t.Fatalf("pre-rollback stage = %q, want ok", got)
	}

	// Manual rollback: the origin re-records rolled-back immediately…
	m, _ = m.beginRollback("deploy")
	r := loadJournal(t, path)
	if got := r.Blocks["stage"].Outcome; got != runlog.OutcomeRolledBack {
		t.Errorf("stage = %q after rollback, want rolled-back (NOT ok)", got)
	}
	// …and the failed block's record stays failed.
	if got := r.Blocks["deploy"].Outcome; got != runlog.OutcomeFailed {
		t.Errorf("deploy = %q, want failed", got)
	}

	// The rollback target's own result lands as an ordinary ok record.
	_ = mustModel(m.handleResult(resultMsg{ID: "undo-stage", Exit: 0}))
	r = loadJournal(t, path)
	if got := r.Blocks["undo-stage"].Outcome; got != runlog.OutcomeOK {
		t.Errorf("undo-stage = %q, want ok (the undo ran clean)", got)
	}
	if r.FirstFailure != "deploy" {
		t.Errorf("FirstFailure = %q, want deploy", r.FirstFailure)
	}
}

// TestJournal_UndoRemovesRecordAndDependents: successfully undoing an applied
// block removes its record AND its re-locked dependents' records — they are
// unrun again, not "done".
func TestJournal_UndoRemovesRecordAndDependents(t *testing.T) {
	md := "```bash {id=a}\ntrue\n```\n\n```bash {id=b needs=a}\ntrue\n```\n"
	m, path := journalModel(t, md)
	m = m.markRunning("a")
	m = mustModel(m.handleResult(resultMsg{ID: "a", Exit: 0}))
	m = m.markRunning("b")
	m = mustModel(m.handleResult(resultMsg{ID: "b", Exit: 0}))
	if r := loadJournal(t, path); len(r.Blocks) != 2 {
		t.Fatalf("blocks = %d, want 2 before the undo", len(r.Blocks))
	}

	// Undo a (the apply/undo result path): a's record and b's must both go.
	st := m.blockStates["a"]
	st.Action = "undo"
	m.blockStates["a"] = st
	_ = mustModel(m.handleResult(resultMsg{ID: "a", Exit: 0}))
	r := loadJournal(t, path)
	if len(r.Blocks) != 0 {
		t.Errorf("blocks = %+v after undo, want empty (a undone, b re-locked)", r.Blocks)
	}
}

// TestJournal_FinishRunFinalizes: finishRun (the Run exit hook) stamps
// Finished + the run-level Outcome from the accumulated records.
func TestJournal_FinishRunFinalizes(t *testing.T) {
	m, path := journalModel(t, "```bash {id=a}\ntrue\n```\n")
	m = m.markRunning("a")
	m = mustModel(m.handleResult(resultMsg{ID: "a", Exit: 1}))

	if code := finishRun(m, m.journal); code != 0 {
		t.Errorf("finishRun exit = %d, want 0 (exitCode unset)", code)
	}
	r := loadJournal(t, path)
	if r.Outcome != runlog.OutcomeFailed || r.Finished.IsZero() {
		t.Errorf("finalized run = outcome %q finished %v, want failed + stamped", r.Outcome, r.Finished)
	}
	if r.FirstFailure != "a" {
		t.Errorf("FirstFailure = %q, want a", r.FirstFailure)
	}
}

// TestJournal_NilJournalOff: without a journal every hook is a no-op — the
// pre-journal viewer behavior, and the exitCode surfacing still works.
func TestJournal_NilJournalOff(t *testing.T) {
	m := newModel("T", "```bash {id=a}\ntrue\n```\n")
	m.width, m.height = 80, 24
	m.reflow()
	m = m.markRunning("a")
	m = mustModel(m.handleResult(resultMsg{ID: "a", Exit: 0}))
	m.exitCode = 3
	if code := finishRun(m, nil); code != 3 {
		t.Errorf("finishRun exit = %d, want 3", code)
	}
}

// TestJournal_ViewThenQuitLeavesPriorJournalIntact is the review finding-1
// regression, straight from the live probe: a playbook whose LAST run failed
// is opened in the viewer and quit without running anything — the failed
// journal must stay byte-identical (the lazy journal writes nothing until a
// block records), so retry/hint/list state survives a mere view.
func TestJournal_ViewThenQuitLeavesPriorJournalIntact(t *testing.T) {
	md := "```bash {id=a}\ntrue\n```\n"
	path := filepath.Join(t.TempDir(), "j.json")
	failed := runlog.Run{
		PlaybookPath: "/proj/pb.md",
		ContentHash:  runlog.ContentHash(md),
		Started:      time.Date(2026, 7, 5, 9, 0, 0, 0, time.UTC),
		Finished:     time.Date(2026, 7, 5, 9, 1, 0, 0, time.UTC),
		Outcome:      runlog.OutcomeFailed,
		FirstFailure: "a",
		Blocks:       map[string]runlog.BlockRecord{"a": {Outcome: runlog.OutcomeFailed, Exit: 7, Duration: time.Second}},
	}
	if err := runlog.Save(path, failed); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// Open the viewer over the same journal (what ui.Run does), stream no
	// results, and quit (finishRun) — the exact probe sequence, sans pty.
	m := newModel("T", md)
	m.width, m.height = 80, 24
	m.journal = runlog.Open(path, "/proj/pb.md", runlog.ContentHash(md))
	m.reflow()
	if code := finishRun(m, m.journal); code != 0 {
		t.Fatalf("finishRun = %d", code)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("previous journal must survive a view-then-quit: %v", err)
	}
	if string(after) != string(before) {
		t.Errorf("view-then-quit rewrote the failed journal:\ngot:\n%s\nwant it byte-identical", after)
	}
}

// TestJournal_FailedUndoKeepsOriginRecord (review finding 2): a FAILED undo
// leaves the block applied — the journal must keep the ORIGINAL apply record
// (outcome ok, the apply's exit/duration), not re-record it with the undo's
// non-zero exit.
func TestJournal_FailedUndoKeepsOriginRecord(t *testing.T) {
	m, path := journalModel(t, "```bash {id=a}\ntrue\n```\n")
	// The original run: ok with a provable duration.
	m = m.markRunning("a")
	st := m.blockStates["a"]
	st.runStartedAt = time.Now().Add(-40 * time.Millisecond)
	m.blockStates["a"] = st
	m = mustModel(m.handleResult(resultMsg{ID: "a", Exit: 0}))
	orig := loadJournal(t, path).Blocks["a"]
	if orig.Outcome != runlog.OutcomeOK || orig.Duration < 40*time.Millisecond {
		t.Fatalf("setup: a = %+v, want ok with duration >= 40ms", orig)
	}

	// A failed undo (exit 13): the record must be untouched.
	st = m.blockStates["a"]
	st.Action = "undo"
	m.blockStates["a"] = st
	_ = mustModel(m.handleResult(resultMsg{ID: "a", Exit: 13}))
	got := loadJournal(t, path).Blocks["a"]
	if got != orig {
		t.Errorf("failed undo re-recorded the origin:\ngot  %+v\nwant %+v (the original apply record)", got, orig)
	}
}

// TestJournal_MultiOriginRollbackPreservesHistory (review finding 3): when
// origin B is also a dependent of origin A, A's dependent-reset must not wipe
// B's journal record before MarkRolledBack re-records it — B keeps its real
// exit/duration under the rolled-back outcome.
func TestJournal_MultiOriginRollbackPreservesHistory(t *testing.T) {
	md := "```bash {id=a rollback=undo-a}\ntrue\n```\n\n" +
		"```bash {id=undo-a}\ntrue\n```\n\n" +
		"```bash {id=b needs=a rollback=undo-b}\ntrue\n```\n\n" +
		"```bash {id=undo-b}\ntrue\n```\n\n" +
		"```bash {id=deploy needs=b}\nfalse\n```\n"
	m, path := journalModel(t, md)

	for _, id := range []string{"a", "b"} {
		m = m.markRunning(id)
		st := m.blockStates[id]
		st.runStartedAt = time.Now().Add(-25 * time.Millisecond)
		m.blockStates[id] = st
		m = mustModel(m.handleResult(resultMsg{ID: id, Exit: 0}))
	}
	m = m.markRunning("deploy")
	m = mustModel(m.handleResult(resultMsg{ID: "deploy", Exit: 1}))

	m, _ = m.beginRollback("deploy")
	r := loadJournal(t, path)
	for _, id := range []string{"a", "b"} {
		got := r.Blocks[id]
		if got.Outcome != runlog.OutcomeRolledBack {
			t.Errorf("%s = %q, want rolled-back", id, got.Outcome)
		}
		if got.Duration < 25*time.Millisecond {
			t.Errorf("%s duration = %v, want the original run's (>= 25ms) preserved through the re-record", id, got.Duration)
		}
	}
	if got := r.Blocks["deploy"].Outcome; got != runlog.OutcomeFailed {
		t.Errorf("deploy = %q, want failed (record survives the rollback)", got)
	}
}
