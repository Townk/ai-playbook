package autorun

import (
	"strings"
	"testing"
	"time"

	"github.com/Townk/ai-playbook/internal/runlog"
)

// TestExecute_PreseedSkipsAndResumes: pre-seeded steps never run — each is
// reported "↷ skipped (previous run)" — execution resumes at the first non-ok
// step in the existing order, and the journal ends complete: seeded records
// re-recorded ok with previous_run + the previous duration, fresh records
// without, and an ok run outcome.
func TestExecute_PreseedSkipsAndResumes(t *testing.T) {
	blocks := []Block{
		{ID: "one", Kind: KindRun, Command: "one"},
		{ID: "two", Kind: KindRun, Command: "two", Needs: []string{"one"}},
		{ID: "verify", Kind: KindRun, Command: "verify", Needs: []string{"two"}},
	}
	j, path := journalFor(t)
	var out strings.Builder
	r := &fakeRunner{}
	code := Execute(Config{
		Blocks:  blocks,
		Out:     &out,
		Journal: j,
		Preseed: map[string]runlog.BlockRecord{
			"one": {Outcome: runlog.OutcomeOK, Duration: 1500 * time.Millisecond, PreviousRun: true},
		},
	}, r)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	for _, id := range r.calls {
		if id == "one" {
			t.Error("pre-seeded step one must not run")
		}
	}
	if len(r.calls) != 2 || r.calls[0] != "two" || r.calls[1] != "verify" {
		t.Errorf("calls = %v, want [two verify] (resume at the first non-ok step)", r.calls)
	}
	if !strings.Contains(out.String(), "[one] ↷ skipped (previous run)") {
		t.Errorf("output missing the skip line:\n%s", out.String())
	}
	// The summary reports the seeded step as skipped.
	if !strings.Contains(out.String(), "skipped") {
		t.Errorf("summary missing the skipped row:\n%s", out.String())
	}

	run, err := runlog.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	one := run.Blocks["one"]
	if one.Outcome != runlog.OutcomeOK || !one.PreviousRun || one.Duration != 1500*time.Millisecond {
		t.Errorf("one = %+v, want ok/previous_run/1.5s (the seed re-recorded honestly)", one)
	}
	if two := run.Blocks["two"]; two.Outcome != runlog.OutcomeOK || two.PreviousRun {
		t.Errorf("two = %+v, want a fresh ok record without previous_run", two)
	}
	if _, ok := run.Blocks["verify"]; !ok {
		t.Error("verify must have a fresh record")
	}
	if run.Outcome != runlog.OutcomeOK {
		t.Errorf("run outcome = %q, want ok", run.Outcome)
	}
}

// TestExecute_PreseedSatisfiesNeeds: a step whose needs= are covered only by
// the pre-seed is runnable — the existing gating is reused, not re-implemented.
func TestExecute_PreseedSatisfiesNeeds(t *testing.T) {
	blocks := []Block{
		{ID: "a", Kind: KindRun, Command: "a"},
		{ID: "b", Kind: KindRun, Command: "b", Needs: []string{"a"}},
	}
	r := &fakeRunner{}
	code := Execute(Config{
		Blocks:  blocks,
		Out:     &strings.Builder{},
		Preseed: map[string]runlog.BlockRecord{"a": {Outcome: runlog.OutcomeOK}},
	}, r)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if len(r.calls) != 1 || r.calls[0] != "b" {
		t.Errorf("calls = %v, want [b] (a's pre-seed satisfies b's needs)", r.calls)
	}
}

// TestExecute_PreseedDemotedProducerRuns: a demoted producer is simply ABSENT
// from the pre-seed — the ordinary ordering re-runs it before its consumer.
func TestExecute_PreseedDemotedProducerRuns(t *testing.T) {
	blocks := []Block{
		{ID: "keep", Kind: KindRun, Command: "keep"},
		{ID: "gen", Kind: KindRun, Command: "gen"},
		{ID: "use", Kind: KindRun, Command: "use", From: "gen"},
	}
	r := &fakeRunner{}
	code := Execute(Config{
		Blocks:  blocks,
		Out:     &strings.Builder{},
		Preseed: map[string]runlog.BlockRecord{"keep": {Outcome: runlog.OutcomeOK}},
	}, r)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if len(r.calls) != 2 || r.calls[0] != "gen" || r.calls[1] != "use" {
		t.Errorf("calls = %v, want [gen use] (the demoted producer re-runs first)", r.calls)
	}
}

// TestExecute_PreseedJournalStaysLazy: a retry session that dies before any
// real step records must leave the previous journal file untouched — Preseed
// alone never writes.
func TestExecute_PreseedJournalStaysLazy(t *testing.T) {
	j, path := journalFor(t)
	// Every block pre-seeded → nothing left to run → no record → no write.
	code := Execute(Config{
		Blocks:  []Block{{ID: "a", Kind: KindRun, Command: "a"}},
		Out:     &strings.Builder{},
		Journal: j,
		Preseed: map[string]runlog.BlockRecord{"a": {Outcome: runlog.OutcomeOK}},
	}, &fakeRunner{})
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if _, err := runlog.Load(path); err == nil {
		t.Error("journal file must not exist — no real step ever recorded (lazy contract)")
	}
}
