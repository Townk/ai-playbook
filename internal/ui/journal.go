// journal.go — the viewer's run-journal hooks (internal/runlog): every block
// result is persisted incrementally, rollback re-records undone blocks, and
// the run finalizes when the viewer exits. The ui package never resolves
// journal paths or hashes itself — the launcher supplies them via Options
// (JournalPath/JournalPlaybookPath/JournalContentHash); an empty JournalPath
// means journaling is off (a nil journal, and every hook is a no-op).
// Journal writes are advisory: they can never fail or alter the run.
package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Townk/ai-playbook/internal/orchestrator"
	"github.com/Townk/ai-playbook/internal/runlog"
)

// journalRecordResult writes one block's SETTLED result state into the run
// journal, mapping the viewer's status vocabulary onto the journal's:
//
//	ok/failed/stopped  → recorded with exit, duration, timed_out_after
//	"" (undone)        → the record is removed — the block is unrun again
//
// Duration is wall-clock from the moment the block was marked running
// (blockRunState.runStartedAt); zero when no start was captured (defensive —
// every dispatch path stamps it).
func (m *model) journalRecordResult(id string, st blockRunState, msg resultMsg) {
	if m.journal == nil {
		return
	}
	var outcome string
	switch st.Status {
	case "ok":
		outcome = runlog.OutcomeOK
	case "failed":
		outcome = runlog.OutcomeFailed
	case "stopped":
		outcome = runlog.OutcomeStopped
	case "":
		// A successful undo returned the block to the unrun state.
		m.journal.Remove(id)
		return
	default:
		return // transient states ("running", "regenerating", …) never journal
	}
	var dur time.Duration
	if !st.runStartedAt.IsZero() {
		dur = time.Since(st.runStartedAt)
	}
	timedOut := ""
	if msg.TimedOut {
		timedOut = orchestrator.FormatTimeout(msg.TimedOutAfter)
	}
	m.journal.Record(id, runlog.BlockRecord{
		Outcome:       outcome,
		Exit:          msg.Exit,
		Duration:      dur,
		TimedOutAfter: timedOut,
	})
}

// applyRetrySeed installs a `run --retry` pre-seed (Options.RetrySeed) onto
// the model: every seeded block starts Status "ok" with the PreviousRun
// marker — rendered "✓ done — previous run", satisfying needs= exactly like
// any ok block (needsSatisfied reads Status), still manually re-runnable —
// and the seed's records land in the LAZY journal's skeleton (Preseed), so
// the first real block result persists them alongside it: the journal file is
// complete from its first write, with previous_run: true and the previous
// durations. A nil/empty seed (every fresh run) is a no-op.
func (m *model) applyRetrySeed(seed map[string]runlog.BlockRecord) {
	if len(seed) == 0 {
		return
	}
	m.journal.Preseed(seed)
	for id, rec := range seed {
		m.blockStates[id] = blockRunState{Status: "ok", Exit: rec.Exit, PreviousRun: true}
	}
}

// journalRemoveAll drops the journal records of blocks whose run state was
// reset (resetDependents): they re-locked, so the journal must not keep them
// as done.
func (m *model) journalRemoveAll(ids []string) {
	if m.journal == nil {
		return
	}
	for _, id := range ids {
		m.journal.Remove(id)
	}
}

// finishRun settles the viewer's exit: it finalizes the run journal (Outcome +
// Finished + FirstFailure are stamped from the accumulated records; nil-safe
// when journaling is off) and surfaces the final model's exitCode. Factored
// out of Run so the quit-time journal contract is unit-testable without a TTY.
func finishRun(fm tea.Model, j *runlog.Journal) int {
	j.Finalize()
	if mm, ok := fm.(model); ok && mm.exitCode != 0 {
		return mm.exitCode
	}
	return 0
}
