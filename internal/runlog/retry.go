// retry.go — the pure `run --retry` computation (spec Decisions 3–4): which
// prior-ok blocks a resumed run pre-seeds, which of them must be DEMOTED back
// to unrun because a remaining block consumes their (gone) outputs, and where
// the retry lands. Pure functions over []playbook.Block + a loaded Run — no
// driver, no filesystem — so both run paths share one seed and the tables are
// unit-testable.
package runlog

import (
	"regexp"
	"strings"

	"github.com/Townk/ai-playbook/pkg/playbook"
)

// Seed is the retry pre-seed both run paths thread from the launcher: the
// prior-ok blocks to start as already-done (with their previous records), the
// prior-ok blocks demoted back to unrun, and the natural landing point.
type Seed struct {
	// PreSeeded maps block id → its previous run's ok record (PreviousRun set,
	// previous duration preserved) for every block the retry starts satisfied.
	// The verify block is NEVER here (on any resume the goal is re-proven).
	PreSeeded map[string]BlockRecord
	// Demoted lists the prior-ok blocks (document order) dropped from
	// PreSeeded because a remaining block consumes their outputs — via from=
	// or an APB_OUT/APB_ERR(_FILE) payload reference — which do not exist in
	// the new session. A demoted block is simply unrun; the EXISTING gating /
	// from=-chain materialization re-runs it before its consumer (re-run
	// ORDER is not computed here).
	Demoted []string
	// StartID is the first non-ok block in DOCUMENT order (failed, stopped,
	// rolled-back, or never recorded) among the forward-runnable blocks — the
	// natural pickup point. Derived from blocks + records, never from
	// Run.FirstFailure (which can be empty on a failed run). "" when every
	// forward block's record is ok.
	StartID string
	// Fresh reports that nothing useful is pre-seeded — the prior run had no
	// ok blocks, or demotion emptied the set — so the caller degrades to a
	// plain fresh run (a message, not an error).
	Fresh bool
}

// verifyBlockID is the schema's success-check block id ({id=verify}); it is
// never pre-seeded — if verify was ok the run succeeded, and on any resume
// the goal must be re-proven.
const verifyBlockID = "verify"

// sanitizeKey mirrors the driver's broker convention (pkg/driver sanitizeKey):
// a block id becomes an APB_* env-var key with every non-[A-Za-z0-9_] byte
// replaced by '_'. Kept in lockstep so ConsumerScan maps payload references
// back to the SAME ids the driver exports them under.
func sanitizeKey(id string) string {
	b := []byte(id)
	for i, c := range b {
		if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
			b[i] = '_'
		}
	}
	return string(b)
}

// apbRefRE finds APB value-passing references in a runnable payload. The
// leading group is the left word boundary (RE2 has no lookbehind): a match
// must start the payload or follow a non-word byte, so $XAPB_OUT_foo (the
// variable XAPB_OUT_foo) never counts. The trailing [A-Za-z0-9_]+ is greedy,
// so the captured key is the FULL variable-name tail — APB_OUT_build_dir
// resolves as the block whose sanitized id is exactly "build_dir", never a
// substring like "build". EXIT is scanned like OUT/ERR: the driver exports
// APB_EXIT_<key> alongside them (every shell adapter), so a consumer like
// `test "$APB_EXIT_check" = 0` demotes its producer too. (EXIT has no FILE_
// variant; the caller's FILE_-prefix fallback over-approximates harmlessly —
// it only resolves against real block ids.)
var apbRefRE = regexp.MustCompile(`(^|[^A-Za-z0-9_])APB_(?:OUT|ERR|EXIT)_([A-Za-z0-9_]+)`)

// ConsumerScan reports, for every block, which OTHER block ids it consumes
// outputs from: its from= producer, plus every block referenced in its
// payload as $APB_OUT_<id> / $APB_ERR_<id> / $APB_EXIT_<id> /
// $APB_OUT_FILE_<id> / $APB_ERR_FILE_<id> (any quoting/bracing — the scan
// keys on the variable name). Only RUNNABLE payloads are scanned (shell/run); static, create, and
// diff payloads are content, not commands, matching the env-declaration
// conventions. References resolve through the driver's sanitize convention: a
// lookup table built from the ACTUAL block ids maps a sanitized key (e.g.
// APB_OUT_my_id) back to every block id that sanitizes to it (my-id, my_id).
// Self-references are ignored. The result lists producers in first-reference
// order, deduped.
func ConsumerScan(blocks []playbook.Block) map[string][]string {
	// sanitized key → the block ids that export under it.
	byKey := make(map[string][]string, len(blocks))
	for _, b := range blocks {
		k := sanitizeKey(b.ID)
		byKey[k] = append(byKey[k], b.ID)
	}

	consumes := make(map[string][]string, len(blocks))
	for _, b := range blocks {
		var producers []string
		seen := map[string]bool{b.ID: true} // never a self-edge
		add := func(id string) {
			if !seen[id] {
				seen[id] = true
				producers = append(producers, id)
			}
		}
		if b.From != "" {
			add(b.From)
		}
		if b.Type == "shell" || b.Type == "run" {
			for _, m := range apbRefRE.FindAllStringSubmatch(b.Payload, -1) {
				rest := m[2]
				// APB_OUT_FILE_x is ambiguous between the OUT ref of a block
				// keyed FILE_x and the OUT_FILE ref of a block keyed x — try
				// both against the real-id table; only actual blocks resolve.
				for _, id := range byKey[rest] {
					add(id)
				}
				if k, ok := strings.CutPrefix(rest, "FILE_"); ok {
					for _, id := range byKey[k] {
						add(id)
					}
				}
			}
		}
		if len(producers) > 0 {
			consumes[b.ID] = producers
		}
	}
	return consumes
}

// RetrySeed computes the retry pre-seed for blocks against the previous run's
// journal (spec Decisions 3–4 + Semantics):
//
//   - forward-runnable blocks (not static, not a rollback target) whose
//     record is ok are pre-seeded — EXCEPT verify, which is never pre-seeded;
//   - failed / stopped / rolled-back / absent records are non-ok: they remain
//     to run, and StartID is the first of them in document order;
//   - any pre-seeded block a remaining block consumes (ConsumerScan) is
//     demoted to unrun, iterated to a fixed point — a demoted block is itself
//     remaining, so ITS producers demote too (their captures are equally
//     gone). Prior-ok blocks feeding only prior-ok blocks stay seeded;
//   - an empty resulting pre-seed (no ok blocks, or all demoted) is Fresh:
//     the caller degrades to a plain fresh run.
func RetrySeed(blocks []playbook.Block, run Run) Seed {
	rollbackTargets := map[string]bool{}
	for _, b := range blocks {
		if b.Rollback != "" {
			rollbackTargets[b.Rollback] = true
		}
	}

	preSeeded := map[string]BlockRecord{}
	remaining := map[string]bool{}
	startID := ""
	for _, b := range blocks {
		if b.Type == "static" || rollbackTargets[b.ID] {
			continue // never runs forward — neither a seed nor a resume point
		}
		rec, ok := run.Blocks[b.ID]
		if ok && rec.Outcome == OutcomeOK && b.ID != verifyBlockID {
			rec.PreviousRun = true // the re-record marker: this block did not run in the new session
			preSeeded[b.ID] = rec
			continue
		}
		remaining[b.ID] = true
		if startID == "" {
			startID = b.ID
		}
	}

	// Demote to a fixed point: a demoted block joins the remaining set, so
	// the producers IT consumes demote on the next pass (transitive data
	// chains re-run whole; re-run ORDER comes from the existing gating /
	// materialization at run time, not from here).
	consumes := ConsumerScan(blocks)
	demoted := map[string]bool{}
	for changed := true; changed; {
		changed = false
		for id := range remaining {
			for _, p := range consumes[id] {
				if _, ok := preSeeded[p]; ok {
					delete(preSeeded, p)
					remaining[p] = true
					demoted[p] = true
					changed = true
				}
			}
		}
	}
	// Demoted in document order (map iteration is random; the list is
	// user-facing via messages/tests).
	var demotedOrdered []string
	for _, b := range blocks {
		if demoted[b.ID] {
			demotedOrdered = append(demotedOrdered, b.ID)
		}
	}

	return Seed{
		PreSeeded: preSeeded,
		Demoted:   demotedOrdered,
		StartID:   startID,
		Fresh:     len(preSeeded) == 0,
	}
}

// Preseed installs a retry seed's records into the journal's in-memory
// skeleton WITHOUT writing anything: the lazy contract holds — the previous
// run's journal file stays intact until a block actually records in THIS
// session — and when the first real Record fires, the skeleton (including
// these previous_run re-records, with their previous durations) persists with
// it, so the journal file is complete from its first write. Nil-receiver
// safe, like every Journal method.
func (j *Journal) Preseed(records map[string]BlockRecord) {
	if j == nil {
		return
	}
	for id, rec := range records {
		rec.PreviousRun = true
		j.run.Blocks[id] = rec
	}
}
