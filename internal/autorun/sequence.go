package autorun

import "time"

type StepKind int

const (
	KindRun StepKind = iota
	KindApplyDiff
	KindCreateFile
)

// Block is autorun's own DTO — decoupled from internal/ui.Block. The launcher and
// internal/ui convert their ui.Block into this.
type Block struct {
	ID       string
	Command  string // ui.Block.Payload
	Lang     string // fence language; drives the canonical payload assembly (script blocks self-invoke their interpreter)
	Needs    []string
	From     string // id of the block whose retained stdout feeds this one's stdin (from=<id>); "" if none. Folds into effectiveNeeds so --auto orders the producer first.
	Rollback string // id of the block that undoes this one; "" if none
	Static   bool
	Timeout  time.Duration // declared timeout= run ceiling; zero when undeclared (the orchestrator's default applies)
	Kind     StepKind
}

// effectiveNeeds returns b's combined data+order dependency set: Needs plus From
// (when non-empty and not already listed). from= implies needs= (ADR-0010), so
// NextRunnable gates and orders a consumer behind its producer without the From id
// having to be duplicated into Needs textually.
func (b Block) effectiveNeeds() []string {
	if b.From == "" {
		return b.Needs
	}
	for _, n := range b.Needs {
		if n == b.From {
			return b.Needs
		}
	}
	return append(append(make([]string, 0, len(b.Needs)+1), b.Needs...), b.From)
}

// Status string literals shared by value with internal/ui.blockRunState.Status.
const (
	StatusOK         = "ok"
	StatusFailed     = "failed"
	StatusSkipped    = "skipped"
	StatusRolledBack = "rolledback"
	StatusCancelled  = "cancelled"
)

// Sequence returns the forward-runnable blocks in document order: not Static and
// not a rollback TARGET (never referenced by another block's Rollback).
func Sequence(blocks []Block) []Block {
	// Build rollback-target set.
	targets := make(map[string]bool)
	for _, b := range blocks {
		if b.Rollback != "" {
			targets[b.Rollback] = true
		}
	}

	// Filter: keep only blocks that are not Static and not targets.
	var result []Block
	for _, b := range blocks {
		if !b.Static && !targets[b.ID] {
			result = append(result, b)
		}
	}
	return result
}

// NextRunnable returns the first Sequence block whose status is "" (never run) and
// whose every Need has status StatusOK. ok=false when none remain. A block whose Need
// is skipped/failed/unrun is itself not runnable (→ effectively auto-skipped).
func NextRunnable(blocks []Block, status map[string]string) (Block, bool) {
	seq := Sequence(blocks)
	for _, b := range seq {
		// Check if this block has been run.
		if status[b.ID] != "" {
			continue // Already run, skip.
		}

		// Check if all effective needs (needs= ∪ from=) have StatusOK, so a from=
		// producer is materialized before its consumer in the headless topological
		// order exactly like a needs= edge.
		allNeedsOK := true
		for _, need := range b.effectiveNeeds() {
			if status[need] != StatusOK {
				allNeedsOK = false
				break
			}
		}

		if allNeedsOK {
			return b, true
		}
	}
	return Block{}, false
}

// RollbackPairs returns reverse-order (originID, targetID) for every block whose
// status is StatusOK and whose Rollback is non-empty. Order: last-applied first.
func RollbackPairs(blocks []Block, status map[string]string) [][2]string {
	var pairs [][2]string
	// Iterate in reverse to get last-applied first.
	for i := len(blocks) - 1; i >= 0; i-- {
		b := blocks[i]
		if status[b.ID] == StatusOK && b.Rollback != "" {
			pairs = append(pairs, [2]string{b.ID, b.Rollback})
		}
	}
	return pairs
}
