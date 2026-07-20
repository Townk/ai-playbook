// Package runlog is the durable per-playbook run journal (the v0.12.3
// run-journal/retry spec, Decisions 1–2): both run paths — the viewer and
// `run --auto` — persist the latest run's state to
//
//	<data-root>/projects/<project_key>/runs/<run_key>.json
//
// where <project_key> is the shared sha1 project key (kb/cache convention,
// kb.ProjectKey) and <run_key> is the store slug for stored playbooks or the
// sha1 of the absolute file path for `--file` runs. One file per playbook per
// project, overwritten by each run (latest run only), updated after EVERY
// block result via write-temp+rename so a kill mid-run loses at most the
// in-flight block.
//
// Journals are ADVISORY metadata: a missing or corrupt journal never breaks a
// run (callers treat it as "never run"), and a journal write failure is a
// one-time stderr note, never a run failure. The Journal writer type is the
// single shared writer both paths use, so they can never drift in shape.
package runlog

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Townk/ai-playbook/internal/kb"
)

// Block-level outcomes (BlockRecord.Outcome). A block undone by a rollback
// chain is re-recorded OutcomeRolledBack — it is NOT ok, a retry re-runs it.
// Run-level outcomes (Run.Outcome) reuse OK/Failed/Stopped; "" means the run
// is still in flight (or died mid-run), which retry treats as resumable.
const (
	OutcomeOK         = "ok"
	OutcomeFailed     = "failed"
	OutcomeStopped    = "stopped"
	OutcomeRolledBack = "rolled-back"
)

// Run is one journaled playbook run: the playbook's identity (path + content
// sha256), the run window, the overall outcome, the first failed/stopped
// block id, and the per-block records.
//
// Blocks is a MAP keyed by block id, not an ordered slice: the journal's
// consumers (retry pre-seeding, the list column) look records up BY ID, a
// rollback/re-run re-record is a plain overwrite (never an append), and
// document order always comes from the playbook itself — persisting a second
// ordering could only drift. encoding/json sorts map keys, so the serialized
// form stays deterministic (golden-testable).
type Run struct {
	PlaybookPath string    `json:"playbook_path"`
	ContentHash  string    `json:"content_hash"`
	Started      time.Time `json:"started"`
	// Finished stays zero (omitted) until the run finalizes; a journal with no
	// finished/outcome is a run that died mid-flight — resumable, like failed.
	Finished time.Time `json:"finished,omitzero"`
	Outcome  string    `json:"outcome,omitempty"`
	// FirstFailure is the id of the first block whose record went
	// failed/stopped and was not later re-recorded ok — the natural retry
	// pickup point and the id the plain-`run` hint names.
	FirstFailure string                 `json:"first_failure,omitempty"`
	Blocks       map[string]BlockRecord `json:"blocks"`
}

// BlockRecord is one block's journaled result.
type BlockRecord struct {
	Outcome  string
	Exit     int
	Duration time.Duration
	// TimedOutAfter is the formatted effective ceiling ("1s", "10m") when the
	// run was killed at its timeout; "" otherwise (the batch-8 JSON field
	// precedent, autorun.StepResult.TimedOutAfter).
	TimedOutAfter string `json:",omitempty"`
	// PreviousRun marks a record re-recorded from a PRIOR run's journal by a
	// retry pre-seed (R2) — the block did not run in this session.
	PreviousRun bool `json:",omitempty"`
}

// jsonBlockRecord is BlockRecord's pinned wire shape: Duration serializes as
// the human-readable Go duration string ("1.5s"), not nanoseconds.
type jsonBlockRecord struct {
	Outcome       string `json:"outcome"`
	Exit          int    `json:"exit"`
	Duration      string `json:"duration"`
	TimedOutAfter string `json:"timed_out_after,omitempty"`
	PreviousRun   bool   `json:"previous_run,omitempty"`
}

// MarshalJSON pins BlockRecord's wire shape (readable duration string).
func (b BlockRecord) MarshalJSON() ([]byte, error) {
	return json.Marshal(jsonBlockRecord{
		Outcome:       b.Outcome,
		Exit:          b.Exit,
		Duration:      b.Duration.String(),
		TimedOutAfter: b.TimedOutAfter,
		PreviousRun:   b.PreviousRun,
	})
}

// UnmarshalJSON is the inverse of MarshalJSON. A malformed duration is a
// parse error — the whole journal is then corrupt, which callers treat as
// "never run".
func (b *BlockRecord) UnmarshalJSON(data []byte) error {
	var w jsonBlockRecord
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	d, err := time.ParseDuration(w.Duration)
	if err != nil {
		return fmt.Errorf("block record duration %q: %w", w.Duration, err)
	}
	*b = BlockRecord{
		Outcome:       w.Outcome,
		Exit:          w.Exit,
		Duration:      d,
		TimedOutAfter: w.TimedOutAfter,
		PreviousRun:   w.PreviousRun,
	}
	return nil
}

// RunKey derives the journal's per-playbook key: the store slug when the run
// came from the store, else the lowercase hex sha1 of the playbook's absolute
// file path (`run --file`). `run <slug>` and `run --file <that stored file>`
// intentionally journal separately — the slug is the stored playbook's stable
// identity; a raw file's identity is its location.
func RunKey(slug, absFilePath string) string {
	if slug != "" {
		return slug
	}
	sum := sha1.Sum([]byte(absFilePath))
	return hex.EncodeToString(sum[:])
}

// Path returns the journal file path for a playbook run under dataRoot (the
// shared data-root resolver's result, cache/kb convention):
// <dataRoot>/projects/<sha1(projectRoot)>/runs/<runKey>.json. The project key
// is kb.ProjectKey — the SAME derivation the KB and cache layouts use.
func Path(dataRoot, projectRoot, runKey string) string {
	return filepath.Join(dataRoot, "projects", kb.ProjectKey(projectRoot), "runs", runKey+".json")
}

// ContentHash returns the lowercase hex sha256 of the playbook markdown —
// the retry content-hash gate's identity (a drifted document never resumes).
func ContentHash(md string) string {
	sum := sha256.Sum256([]byte(md))
	return hex.EncodeToString(sum[:])
}

// Load reads a journal. A missing file returns an error wrapping
// os.ErrNotExist; a corrupt file returns the zero Run and an error — callers
// treat both as "never run" (journals are advisory).
func Load(path string) (Run, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Run{}, err
	}
	var r Run
	if err := json.Unmarshal(data, &r); err != nil {
		return Run{}, fmt.Errorf("corrupt run journal %s: %w", path, err)
	}
	return r, nil
}

// Save writes run to path crash-safely: MkdirAll, write to a temp file in the
// SAME directory, then rename over path (atomic on POSIX same-fs) — a kill
// mid-write leaves the previous journal intact.
func Save(path string, run Run) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, ".run-journal-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// Journal is the incremental journal writer BOTH run paths share (the viewer
// via ui.Options journal fields, --auto via autorun's run config), so the two
// can never produce different shapes. All methods are nil-receiver-safe: a
// nil *Journal (journaling off) is a no-op, so callers never need their own
// off-switch checks.
//
// The journal is LAZY: Open only builds the in-memory skeleton — nothing
// touches disk until a block actually records (the first Record /
// MarkRolledBack persists the skeleton and the record together), and Finalize
// is a no-op while nothing has recorded. A view-then-quit viewer session, a
// render-only degraded fallback, or a zero-block run therefore NEVER
// overwrites the previous run's journal with an empty "ok" — the prior
// failure (the retry state this package exists to preserve) survives anything
// short of a new block result. From the first record on, every update
// persists immediately, as before.
//
// Journal failures are ADVISORY: the first Save error prints one stderr note
// and the run continues; no method returns an error.
type Journal struct {
	path   string
	run    Run
	dirty  bool // a block recorded — this run owns the journal file now
	warned bool
	now    func() time.Time // seam for deterministic tests
	// prev holds the pre-session journal file bytes (nil when none existed).
	// A session that ends with ZERO block records — every recorded block was
	// undone again (run-undo-quit) — restores it at Finalize, so the saves made
	// mid-session don't leave a net-nothing run's `{blocks: {}}` (or a bogus
	// "ok") in place of the previous run's journal.
	prev []byte
}

// Open builds a fresh journal for the identified playbook. It does NOT write:
// any previous journal at path stays intact until this run's first block
// result records (only a real run overwrites history). An empty path returns
// nil (journaling off).
func Open(path, playbookPath, contentHash string) *Journal {
	if path == "" {
		return nil
	}
	j := &Journal{path: path, now: time.Now}
	if b, err := os.ReadFile(path); err == nil {
		j.prev = b // snapshot for the net-nothing-session restore (Finalize)
	}
	j.run = Run{
		PlaybookPath: playbookPath,
		ContentHash:  contentHash,
		Started:      j.now(),
		Blocks:       map[string]BlockRecord{},
	}
	return j
}

// Record journals one block result, overwriting any previous record for the
// id (a re-run replaces, never appends). It maintains FirstFailure: the first
// failed/stopped id is captured, and a later ok re-record of that SAME block
// clears it (the failure was overcome by hand).
func (j *Journal) Record(id string, rec BlockRecord) {
	if j == nil {
		return
	}
	j.dirty = true
	j.run.Blocks[id] = rec
	switch rec.Outcome {
	case OutcomeFailed, OutcomeStopped:
		if j.run.FirstFailure == "" {
			j.run.FirstFailure = id
		}
	case OutcomeOK:
		if j.run.FirstFailure == id {
			j.run.FirstFailure = ""
		}
	}
	j.save()
}

// MarkRolledBack re-records a previously-run block as rolled-back — its
// forward effect was undone by a rollback chain — preserving the record's
// exit/duration history. It overwrites the existing record's outcome (spec
// Decision 2: an undone block is NOT ok; a retry re-runs it).
func (j *Journal) MarkRolledBack(id string) {
	if j == nil {
		return
	}
	j.dirty = true
	rec := j.run.Blocks[id]
	rec.Outcome = OutcomeRolledBack
	j.run.Blocks[id] = rec
	j.save()
}

// Remove drops a block's record entirely — used when the viewer undoes a
// block (or re-locks its dependents), returning it to the never-ran state.
// A removed FirstFailure is cleared with it.
//
// While the journal is still LAZY (!dirty — nothing has recorded this
// session), the removal is in-memory ONLY: a retry session whose FIRST action
// undoes a pre-seeded block (Preseed creates records without dirtying) must
// not overwrite the previous run's failed journal with an outcome-less
// skeleton. Deliberately, dirty is NOT set either — that would let a later
// Finalize stamp a bogus "ok" from the remaining seeds (the R1 clobber
// class). The eventual first real Record persists the post-undo truth.
func (j *Journal) Remove(id string) {
	if j == nil {
		return
	}
	if _, ok := j.run.Blocks[id]; !ok {
		return
	}
	delete(j.run.Blocks, id)
	if j.run.FirstFailure == id {
		j.run.FirstFailure = ""
	}
	if !j.dirty {
		return
	}
	j.save()
}

// Finalize stamps the run's end: Finished plus the run-level Outcome derived
// from the block records — failed if ANY block record is failed, else stopped
// if any is stopped, else ok. Rolled-back records are neutral (the failure
// that triggered the rollback is what fails the run). When NOTHING ever
// recorded (a view-then-quit session, a degraded render-only viewer) it is a
// no-op — no run happened, so no journal is written and any previous run's
// journal stays intact. A session whose records NETTED to zero (run-undo-quit:
// every recorded block was undone again) restores the pre-session journal
// instead — the mid-session saves already replaced the file on disk, and
// finalizing would stamp `{outcome: ok, blocks: {}}` over the previous run's
// history (`list` would show ✓ for a session that netted nothing).
func (j *Journal) Finalize() {
	if j == nil || !j.dirty {
		return
	}
	if len(j.run.Blocks) == 0 {
		j.restorePrev()
		return
	}
	j.run.Finished = j.now()
	j.run.Outcome = runOutcome(j.run.Blocks)
	j.save()
}

// restorePrev puts the pre-session journal back on disk (or removes the file
// when the session started with none), advisory like every journal write.
func (j *Journal) restorePrev() {
	if j.prev == nil {
		_ = os.Remove(j.path)
		return
	}
	_ = os.WriteFile(j.path, j.prev, 0o644)
}

// runOutcome derives the run-level outcome from the block records.
func runOutcome(blocks map[string]BlockRecord) string {
	out := OutcomeOK
	for _, rec := range blocks {
		switch rec.Outcome {
		case OutcomeFailed:
			return OutcomeFailed
		case OutcomeStopped:
			out = OutcomeStopped
		}
	}
	return out
}

// save persists the journal, advisory-only: the first failure prints ONE
// stderr note and the run continues (later attempts stay silent but keep
// trying — a transient condition may clear).
func (j *Journal) save() {
	if err := Save(j.path, j.run); err != nil && !j.warned {
		j.warned = true
		fmt.Fprintf(os.Stderr, "ai-playbook: run journal unavailable (%v) — continuing without journaling\n", err)
	}
}
