package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Townk/ai-playbook/internal/runlog"
)

// retrySeededModel builds a journaled viewer model over md with ids pre-seeded
// the way ui.Run does for a `run --retry` (applyRetrySeed), each carrying the
// given previous record.
func retrySeededModel(t *testing.T, md string, seed map[string]runlog.BlockRecord) (model, string) {
	t.Helper()
	m, path := journalModel(t, md)
	m.applyRetrySeed(seed)
	m.reflow()
	return m, path
}

// hasBlockButton reports whether m registers a button of the given kind for
// the given block (linesContain/hasButton exist with other shapes elsewhere
// in the package).
func hasBlockButton(m model, kind, blockID string) bool {
	for _, b := range m.buttons {
		if b.Kind == kind && b.BlockID == blockID {
			return true
		}
	}
	return false
}

// TestRetry_PreseedRendersDistinctAndSatisfiesNeeds: a pre-seeded block
// renders the distinct "done — previous run" form, satisfies its dependents'
// needs= (the unchanged needsSatisfied gating — Status is "ok"), and STAYS
// manually re-runnable (a live run button, unlike an ordinary ok block).
func TestRetry_PreseedRendersDistinctAndSatisfiesNeeds(t *testing.T) {
	md := "```bash {id=one}\ntrue\n```\n\n```bash {id=two needs=one}\ntrue\n```\n"
	m, _ := retrySeededModel(t, md, map[string]runlog.BlockRecord{
		"one": {Outcome: runlog.OutcomeOK, Duration: time.Second, PreviousRun: true},
	})

	if got := m.blockStates["one"]; got.Status != "ok" || !got.PreviousRun {
		t.Fatalf("one state = %+v, want ok + PreviousRun", got)
	}
	if !linesContain(m.lines, "previous run") {
		t.Error("render missing the \"done — previous run\" form for the pre-seeded block")
	}
	// two's needs= are met by the pre-seed: no ⊘ blocker, live run button.
	if linesContain(m.lines, "⊘ needs") {
		t.Error("two must not be blocked — the pre-seeded one satisfies needs=")
	}
	if !hasBlockButton(m, "run", "two") {
		t.Error("two must have a live run button (needs met by the pre-seed)")
	}
	// The pre-seeded block itself stays manually re-runnable.
	if !hasBlockButton(m, "run", "one") {
		t.Error("the pre-seeded block must keep a live run button (manually re-runnable)")
	}

	// Contrast: an ordinary ok block (no PreviousRun) has NO run button.
	m2, _ := journalModel(t, md)
	m2 = m2.markRunning("one")
	m2 = mustModel(m2.handleResult(resultMsg{ID: "one", Exit: 0}))
	if hasBlockButton(m2, "run", "one") {
		t.Error("an ordinary ok block must not regain a run button (pre-existing contract)")
	}
	if linesContain(m2.lines, "previous run") {
		t.Error("an ordinary ok block must not render the previous-run form")
	}
}

// TestRetry_PreseedIsLazyOnDisk: seeding alone (then quitting) writes nothing —
// the previous run's failed journal survives a view-then-quit retry session.
func TestRetry_PreseedIsLazyOnDisk(t *testing.T) {
	md := "```bash {id=one}\ntrue\n```\n"
	m, path := retrySeededModel(t, md, map[string]runlog.BlockRecord{
		"one": {Outcome: runlog.OutcomeOK, PreviousRun: true},
	})
	if code := finishRun(m, m.journal); code != 0 {
		t.Fatalf("finishRun = %d", code)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("a retry session with no real result must write nothing; stat err = %v", err)
	}
}

// TestRetry_FirstResultPersistsSeededRecords: the first real block result
// persists the journal COMPLETE — the seeded record lands with previous_run
// and its PREVIOUS duration alongside the fresh record.
func TestRetry_FirstResultPersistsSeededRecords(t *testing.T) {
	md := "```bash {id=one}\ntrue\n```\n\n```bash {id=two needs=one}\ntrue\n```\n"
	m, path := retrySeededModel(t, md, map[string]runlog.BlockRecord{
		"one": {Outcome: runlog.OutcomeOK, Duration: 2500 * time.Millisecond, PreviousRun: true},
	})
	m = m.markRunning("two")
	m = mustModel(m.handleResult(resultMsg{ID: "two", Exit: 0}))

	r := loadJournal(t, path)
	one := r.Blocks["one"]
	if one.Outcome != runlog.OutcomeOK || !one.PreviousRun || one.Duration != 2500*time.Millisecond {
		t.Errorf("one = %+v, want ok/previous_run/2.5s (seed re-recorded with the previous duration)", one)
	}
	if two := r.Blocks["two"]; two.Outcome != runlog.OutcomeOK || two.PreviousRun {
		t.Errorf("two = %+v, want a fresh ok record without previous_run", two)
	}

	// Finalize: seeded ok + fresh ok derive an ok run.
	_ = finishRun(m, m.journal)
	if r := loadJournal(t, path); r.Outcome != runlog.OutcomeOK {
		t.Errorf("run outcome = %q, want ok", r.Outcome)
	}
}

// TestRetry_ManualRerunReRecordsWithoutPreviousRun: manually re-running a
// pre-seeded block clears the marker and journals the NEW result honestly
// (this session's duration, no previous_run).
func TestRetry_ManualRerunReRecordsWithoutPreviousRun(t *testing.T) {
	md := "```bash {id=one}\ntrue\n```\n"
	m, path := retrySeededModel(t, md, map[string]runlog.BlockRecord{
		"one": {Outcome: runlog.OutcomeOK, Duration: time.Hour, PreviousRun: true},
	})

	m = m.markRunning("one")
	if m.blockStates["one"].PreviousRun {
		t.Error("markRunning must clear PreviousRun (the re-run is this session's)")
	}
	st := m.blockStates["one"]
	st.runStartedAt = time.Now().Add(-30 * time.Millisecond)
	m.blockStates["one"] = st
	m = mustModel(m.handleResult(resultMsg{ID: "one", Exit: 0}))

	rec := loadJournal(t, path).Blocks["one"]
	if rec.PreviousRun {
		t.Errorf("re-recorded block = %+v, want previous_run cleared", rec)
	}
	if rec.Duration >= time.Hour || rec.Duration < 30*time.Millisecond {
		t.Errorf("duration = %v, want THIS run's wall clock, not the seeded hour", rec.Duration)
	}
	m.reflow()
	if linesContain(m.lines, "previous run") {
		t.Error("after the re-run the block must render as an ordinary ok, not previous-run")
	}
}

// TestRetry_AssistedCursorSkipsPreseeded: in assisted (GUIDED) mode the ready
// cursor lands on the first NON-seeded runnable block — the shared
// NextRunnable gating treats the pre-seed as done.
func TestRetry_AssistedCursorSkipsPreseeded(t *testing.T) {
	md := "```bash {id=one}\ntrue\n```\n\n```bash {id=two needs=one}\ntrue\n```\n"
	m, _ := retrySeededModel(t, md, map[string]runlog.BlockRecord{
		"one": {Outcome: runlog.OutcomeOK, PreviousRun: true},
	})
	if got := m.assistedNextID(); got != "two" {
		t.Errorf("assistedNextID = %q, want two (one is pre-seeded done)", got)
	}
}

// TestRetry_ApplyRetrySeedNilIsNoop: every fresh run passes a nil seed — the
// model and journal must be untouched.
func TestRetry_ApplyRetrySeedNilIsNoop(t *testing.T) {
	m, path := journalModel(t, "```bash {id=a}\ntrue\n```\n")
	m.applyRetrySeed(nil)
	if len(m.blockStates) != 0 {
		t.Errorf("blockStates = %v, want empty", m.blockStates)
	}
	if _, err := os.Stat(filepath.Dir(path) + "/j.json"); !os.IsNotExist(err) {
		t.Errorf("nil seed must write nothing; stat err = %v", err)
	}
}

// TestRetry_ManualRerunOfPreseededConsumerMaterializesProducer (review
// finding 2): BOTH a from= producer and its consumer are pre-seeded (the
// consumer was ok, so demotion rightly kept the producer); a manual re-run of
// the consumer must re-materialize the producer FIRST — its capture is a
// previous session's and is gone — instead of running against /dev/null.
func TestRetry_ManualRerunOfPreseededConsumerMaterializesProducer(t *testing.T) {
	md := "```bash {id=gen}\necho data\n```\n\n```bash {id=use from=gen}\ngrep -q data\n```\n"
	m, path := retrySeededModel(t, md, map[string]runlog.BlockRecord{
		"gen": {Outcome: runlog.OutcomeOK, PreviousRun: true},
		"use": {Outcome: runlog.OutcomeOK, PreviousRun: true},
	})

	// The chain re-materializes the pre-seeded producer before the consumer.
	if got := m.fromChain("use"); len(got) != 2 || got[0] != "gen" || got[1] != "use" {
		t.Fatalf("fromChain(use) = %v, want [gen use] (pre-seeded producer counts as unmaterialized)", got)
	}

	// Clicking the consumer's run button dispatches the producer first…
	m, _ = m.runOrChain(Button{Kind: "run", Payload: m.blockCommand("use"), BlockID: "use"})
	if m.chainStep != "gen" || len(m.chainQueue) != 1 || m.chainQueue[0] != "use" {
		t.Fatalf("chain = step %q queue %v, want gen then [use]", m.chainStep, m.chainQueue)
	}
	if st := m.blockStates["gen"]; st.Status != "running" || st.PreviousRun {
		t.Errorf("gen state = %+v, want running with PreviousRun cleared", st)
	}

	// …and the producer's fresh ok advances the chain onto the consumer.
	m = mustModel(m.handleResult(resultMsg{ID: "gen", Exit: 0}))
	if st := m.blockStates["use"]; st.Status != "running" || st.PreviousRun {
		t.Errorf("use state = %+v, want running with PreviousRun cleared (chain advanced)", st)
	}
	m = mustModel(m.handleResult(resultMsg{ID: "use", Exit: 0}))

	// Both re-record as THIS session's runs.
	r := loadJournal(t, path)
	for _, id := range []string{"gen", "use"} {
		if rec := r.Blocks[id]; rec.Outcome != runlog.OutcomeOK || rec.PreviousRun {
			t.Errorf("%s = %+v, want a fresh ok record (previous_run cleared)", id, rec)
		}
	}

	// Contrast: a producer that ran ok THIS session is still never re-run.
	m2, _ := journalModel(t, md)
	m2 = m2.markRunning("gen")
	m2 = mustModel(m2.handleResult(resultMsg{ID: "gen", Exit: 0}))
	if got := m2.fromChain("use"); len(got) != 1 || got[0] != "use" {
		t.Errorf("fromChain(use) = %v, want [use] (this-session ok producer stays materialized)", got)
	}
}

// TestRetry_FirstActionUndoKeepsPriorJournal (review finding 3): a retry
// session whose FIRST action undoes a pre-seeded block must leave the
// previous run's FAILED journal byte-identical on disk — Remove while the
// journal is still lazy is in-memory only, and quit-time Finalize stays a
// no-op.
func TestRetry_FirstActionUndoKeepsPriorJournal(t *testing.T) {
	md := "```bash {id=one}\ntrue\n```\n\n```bash {id=two}\nfalse\n```\n"
	path := filepath.Join(t.TempDir(), "j.json")
	failed := runlog.Run{
		PlaybookPath: "/proj/pb.md",
		ContentHash:  runlog.ContentHash(md),
		Started:      time.Date(2026, 7, 5, 9, 0, 0, 0, time.UTC),
		Finished:     time.Date(2026, 7, 5, 9, 1, 0, 0, time.UTC),
		Outcome:      runlog.OutcomeFailed,
		FirstFailure: "two",
		Blocks: map[string]runlog.BlockRecord{
			"one": {Outcome: runlog.OutcomeOK, Duration: time.Second},
			"two": {Outcome: runlog.OutcomeFailed, Exit: 7},
		},
	}
	if err := runlog.Save(path, failed); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// The retry session: seed one, then the user's first action is undoing it.
	m := newModel("T", md)
	m.width, m.height = 80, 24
	m.journal = runlog.Open(path, "/proj/pb.md", runlog.ContentHash(md))
	m.reflow()
	m.applyRetrySeed(map[string]runlog.BlockRecord{
		"one": {Outcome: runlog.OutcomeOK, Duration: time.Second, PreviousRun: true},
	})
	st := m.blockStates["one"]
	st.Action = "undo"
	m.blockStates["one"] = st
	m = mustModel(m.handleResult(resultMsg{ID: "one", Exit: 0})) // successful undo → journal Remove
	if code := finishRun(m, m.journal); code != 0 {
		t.Fatalf("finishRun = %d", code)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("previous journal must survive the undo-then-quit: %v", err)
	}
	if string(after) != string(before) {
		t.Errorf("first-action undo rewrote the failed journal:\ngot:\n%s\nwant it byte-identical", after)
	}
}
