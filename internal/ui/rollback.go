package ui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/Townk/ai-playbook/internal/autorun"
)

// anyRollbackable reports whether at least one already-run block (Status=="ok") declares
// a rollback= target — i.e. there is something a "Rollback playbook" click could undo.
// Gates the rollback affordance on a failed step (render's rollbackAvail flag).
func (m model) anyRollbackable() bool {
	for _, b := range m.blocks {
		if b.Rollback != "" && m.blockStates[b.ID].Status == "ok" {
			return true
		}
	}
	return false
}

// toAutorunBlocks maps the pager's blocks and their live run states into the
// autorun package's Block/status shape, so beginRollback can share the same
// autorun.RollbackPairs collection logic used by the headless --auto rollback.
func (m model) toAutorunBlocks() ([]autorun.Block, map[string]string) {
	ab := make([]autorun.Block, 0, len(m.blocks))
	status := make(map[string]string, len(m.blocks))
	for _, b := range m.blocks {
		ab = append(ab, autorun.Block{
			ID:       b.ID,
			Command:  b.Payload,
			Needs:    b.Needs,
			From:     b.From,
			Rollback: b.Rollback,
			Static:   b.Static,
			Kind:     autorun.KindRun,
		})
		status[b.ID] = m.blockStates[b.ID].Status
	}
	return ab, status
}

// beginRollback runs the manual rollback chain (a "Rollback playbook" click): every
// already-run block (Status=="ok") that declares a rollback= target has that target
// executed, in REVERSE registration order, each as a normal run so its result shows
// (visible rollback). The rolled-back blocks' own state is reset — undoing their forward
// effect and re-locking any dependents. tea.Sequence runs the targets one at a time so a
// later undo never races an earlier one.
func (m model) beginRollback(failedID string) (model, tea.Cmd) {
	ab, status := m.toAutorunBlocks()
	pairs := autorun.RollbackPairs(ab, status)
	var origins, targets []string
	for i := 0; i < len(pairs); i++ {
		origins = append(origins, pairs[i][0])
		targets = append(targets, pairs[i][1])
	}
	if len(targets) == 0 {
		return m, nil
	}
	// Capture the failed block's state BEFORE resetting dependents — it usually needs= a
	// rolled-back step, so resetDependents would otherwise wipe its ✗ failed status.
	failedState := m.blockStates[failedID]
	// Clear stale results on everything that (transitively) depended on a rolled-back step
	// so they re-lock, THEN mark each rolled-back step itself "rolledback" (↺ step rolled
	// back) — its forward effect is being undone. (Two passes so a reset of one origin's
	// dependents can't wipe another origin we already marked.) The rollback TARGET blocks
	// that do the undoing land as a normal success (✓ ran) via their own result.
	for _, id := range origins {
		resetDependents(m.blockStates, m.blocks, id)
	}
	for _, id := range origins {
		ost := m.blockStates[id]
		ost.Status = "rolledback"
		ost.Action = ""
		m.blockStates[id] = ost
	}
	// Restore the failed block and mark it rolling back: it shows a "rolling back applied
	// steps…" spinner under its ✗ failure until every target completes (rollbackPending → 0).
	if failedID != "" {
		failedState.RollingBack = true
		failedState.RolledBack = false
		failedState.SpinFrame = 0
		m.blockStates[failedID] = failedState
	}
	m.rollbackFailedID = failedID
	m.rollbackPending = len(targets)
	// Then mark each rollback target running and queue its execution (reverse order).
	var cmds []tea.Cmd
	for _, tgt := range targets {
		st := m.blockStates[tgt]
		st.Status = "running"
		st.Action = "rollback"
		st.SpinFrame = 0
		m.blockStates[tgt] = st
		if c := m.emitAction(Button{Kind: "run", Payload: m.blockCommand(tgt), BlockID: tgt}); c != nil {
			cmds = append(cmds, c)
		}
	}
	m.reflow()
	if len(cmds) == 0 {
		return m, m.flashCmd()
	}
	return m, tea.Batch(m.startTick(), m.flashCmd(), tea.Sequence(cmds...))
}

// finishRollbackIfDone settles the rollback chain when no targets remain pending: it
// clears the failed block's spinner and appends the "all steps rolled back" suffix.
func (m model) finishRollbackIfDone() model {
	if m.rollbackPending > 0 {
		return m
	}
	if id := m.rollbackFailedID; id != "" {
		fst := m.blockStates[id]
		fst.RollingBack = false
		fst.RolledBack = true
		m.blockStates[id] = fst
	}
	m.rollbackFailedID = ""
	return m
}
