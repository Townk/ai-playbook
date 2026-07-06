// retry_test.go — `run --retry` (v0.12.3 R2): flag resolution, the gate
// ladder (messages + exit codes), seed threading into both run paths, and the
// live --auto end-to-end resumes (skip + from=-demotion re-run).
package launcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Townk/ai-playbook/internal/autorun"
	"github.com/Townk/ai-playbook/internal/runlog"
	"github.com/Townk/ai-playbook/internal/ui"
	"github.com/Townk/ai-playbook/pkg/playbook"
)

func TestResolveRunArgs_Retry(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want runMode
	}{
		{"default viewer", []string{"--retry", "my-slug"}, modeDefault},
		{"composes with --auto", []string{"--auto", "--retry", "my-slug"}, modeAuto},
		{"composes with --assisted", []string{"--assisted", "--retry", "my-slug"}, modeAssisted},
		{"composes with --file", []string{"--retry", "--file", "pb.md"}, modeDefault},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ra, err := resolveRunArgs(tc.args)
			if err != nil {
				t.Fatalf("resolveRunArgs(%v): %v", tc.args, err)
			}
			if !ra.Retry {
				t.Error("Retry = false, want true")
			}
			if ra.Mode != tc.want {
				t.Errorf("Mode = %v, want %v", ra.Mode, tc.want)
			}
		})
	}
	if ra, err := resolveRunArgs([]string{"my-slug"}); err != nil || ra.Retry {
		t.Errorf("no --retry flag: Retry = %v err = %v, want false/nil", ra.Retry, err)
	}
}

// retryBlocks is the canonical 3-block document the gate tests seed against.
var retryBlocks = playbook.ParseBlocks(
	"```bash {id=one}\ntrue\n```\n\n```bash {id=two}\ntrue\n```\n\n```bash {id=verify}\ntrue\n```\n")

// saveRetryJournal writes a journal for the gate tests and returns its path.
func saveRetryJournal(t *testing.T, run runlog.Run) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "j.json")
	if run.Started.IsZero() {
		run.Started = time.Now()
	}
	if err := runlog.Save(path, run); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestRetryGate_Ladder table-tests the gates in order: message + exit code +
// whether the run proceeds.
func TestRetryGate_Ladder(t *testing.T) {
	const hash = "matching-hash"
	failedRun := runlog.Run{
		ContentHash: hash,
		Outcome:     runlog.OutcomeFailed,
		Blocks: map[string]runlog.BlockRecord{
			"one": {Outcome: runlog.OutcomeOK, Duration: time.Second},
			"two": {Outcome: runlog.OutcomeFailed, Exit: 7},
		},
	}

	cases := []struct {
		name        string
		path        func(t *testing.T) string
		wantCode    int
		wantProceed bool
		wantSeedIDs []string // nil ⇒ no seed (refused or fresh)
		wantMsg     string
	}{
		{
			name:     "no journal path (journaling unavailable)",
			path:     func(*testing.T) string { return "" },
			wantCode: 1, wantProceed: false,
			wantMsg: "no run journal available",
		},
		{
			name:     "never run (no journal file)",
			path:     func(t *testing.T) string { return filepath.Join(t.TempDir(), "absent.json") },
			wantCode: 1, wantProceed: false,
			wantMsg: "no previous run recorded",
		},
		{
			name: "corrupt journal reads as no journal",
			path: func(t *testing.T) string {
				p := filepath.Join(t.TempDir(), "j.json")
				if err := os.WriteFile(p, []byte("{not json"), 0o644); err != nil {
					t.Fatal(err)
				}
				return p
			},
			wantCode: 1, wantProceed: false,
			wantMsg: "run journal unreadable",
		},
		{
			name: "last run succeeded",
			path: func(t *testing.T) string {
				return saveRetryJournal(t, runlog.Run{ContentHash: hash, Outcome: runlog.OutcomeOK,
					Blocks: map[string]runlog.BlockRecord{"one": {Outcome: runlog.OutcomeOK}}})
			},
			wantCode: 0, wantProceed: false,
			wantMsg: "last run succeeded — nothing to resume",
		},
		{
			name: "content hash mismatch refuses",
			path: func(t *testing.T) string {
				drifted := failedRun
				drifted.ContentHash = "some-other-hash"
				return saveRetryJournal(t, drifted)
			},
			wantCode: 1, wantProceed: false,
			wantMsg: "playbook changed since the failed run",
		},
		{
			name: "no ok blocks degrades to fresh",
			path: func(t *testing.T) string {
				return saveRetryJournal(t, runlog.Run{ContentHash: hash, Outcome: runlog.OutcomeFailed,
					Blocks: map[string]runlog.BlockRecord{"one": {Outcome: runlog.OutcomeFailed, Exit: 1}}})
			},
			wantCode: 0, wantProceed: true, wantSeedIDs: nil,
			wantMsg: "no prior progress to carry over — running fresh",
		},
		{
			name:     "resumable run yields the seed",
			path:     func(t *testing.T) string { return saveRetryJournal(t, failedRun) },
			wantCode: 0, wantProceed: true, wantSeedIDs: []string{"one"},
			wantMsg: `resuming at "two"`,
		},
		{
			name: "mid-flight journal (no outcome) is resumable",
			path: func(t *testing.T) string {
				inFlight := failedRun
				inFlight.Outcome = ""
				return saveRetryJournal(t, inFlight)
			},
			wantCode: 0, wantProceed: true, wantSeedIDs: []string{"one"},
			wantMsg: "resuming at",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var seed map[string]runlog.BlockRecord
			var code int
			var proceed bool
			msg := captureStderr(t, func() {
				seed, code, proceed = retryGate(tc.path(t), hash, retryBlocks)
			})
			if code != tc.wantCode || proceed != tc.wantProceed {
				t.Errorf("gate = (code %d, proceed %v), want (%d, %v)", code, proceed, tc.wantCode, tc.wantProceed)
			}
			if !strings.Contains(msg, tc.wantMsg) {
				t.Errorf("stderr = %q, want it to contain %q", msg, tc.wantMsg)
			}
			if len(tc.wantSeedIDs) == 0 && len(seed) != 0 {
				t.Errorf("seed = %v, want none", seed)
			}
			for _, id := range tc.wantSeedIDs {
				if _, ok := seed[id]; !ok {
					t.Errorf("seed missing %q (have %v)", id, seed)
				}
			}
		})
	}
}

// retryEnv pins the journal-identity inputs (data root + project root) so a
// test-run journal resolves deterministically.
func retryEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AI_PLAYBOOK_DATA_DIR", t.TempDir())
	restore := swap(&projectRootFn, func() string { return "/retry/proj" })
	t.Cleanup(restore)
}

// writeFailedJournalFor journals a failed prior run for the playbook file
// (raw bytes hashed like the write path does), with block one ok and block
// two failed.
func writeFailedJournalFor(t *testing.T, slug, file string) {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	jPath, _, jHash := journalIdentity(slug, file, string(raw))
	if jPath == "" {
		t.Fatal("journalIdentity returned no path")
	}
	run := runlog.Run{
		PlaybookPath: file,
		ContentHash:  jHash,
		Started:      time.Now(),
		Finished:     time.Now(),
		Outcome:      runlog.OutcomeFailed,
		FirstFailure: "two",
		Blocks: map[string]runlog.BlockRecord{
			"one": {Outcome: runlog.OutcomeOK, Duration: 1200 * time.Millisecond},
			"two": {Outcome: runlog.OutcomeFailed, Exit: 7},
		},
	}
	if err := runlog.Save(jPath, run); err != nil {
		t.Fatal(err)
	}
}

const retryPlaybookMD = "```bash {id=one}\ntrue\n```\n\n```bash {id=two}\ntrue\n```\n\n```bash {id=verify}\ntrue\n```\n"

// TestRunMain_RetryThreadsSeedIntoViewer: the viewer path receives the
// pre-seed via ui.Options.RetrySeed — with previous_run records — and the run
// proceeds.
func TestRunMain_RetryThreadsSeedIntoViewer(t *testing.T) {
	retryEnv(t)
	file := filepath.Join(t.TempDir(), "pb.md")
	if err := os.WriteFile(file, []byte(retryPlaybookMD), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFailedJournalFor(t, "", file)

	withArgs(t, []string{"ai-playbook", "run", "--retry", "--file", file})
	var got ui.Options
	withUIRunFn(t, func(o ui.Options) int { got = o; return 0 })

	code := 0
	_ = captureStderr(t, func() { code = RunMain() })
	if code != 0 {
		t.Fatalf("RunMain = %d, want 0", code)
	}
	rec, ok := got.RetrySeed["one"]
	if !ok {
		t.Fatalf("RetrySeed = %v, want block one pre-seeded", got.RetrySeed)
	}
	if !rec.PreviousRun || rec.Duration != 1200*time.Millisecond {
		t.Errorf("seeded record = %+v, want previous_run with the previous duration", rec)
	}
	if _, ok := got.RetrySeed["two"]; ok {
		t.Error("the failed block must not be pre-seeded")
	}
	if _, ok := got.RetrySeed["verify"]; ok {
		t.Error("verify must never be pre-seeded")
	}
}

// TestRunMain_RetrySuccessJournal_Exit0: gate 2 — a succeeded last run says
// "nothing to resume", exits 0, and never opens the viewer.
func TestRunMain_RetrySuccessJournal_Exit0(t *testing.T) {
	retryEnv(t)
	file := filepath.Join(t.TempDir(), "pb.md")
	if err := os.WriteFile(file, []byte(retryPlaybookMD), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(file)
	jPath, _, jHash := journalIdentity("", file, string(raw))
	if err := runlog.Save(jPath, runlog.Run{ContentHash: jHash, Started: time.Now(), Outcome: runlog.OutcomeOK,
		Blocks: map[string]runlog.BlockRecord{"one": {Outcome: runlog.OutcomeOK}}}); err != nil {
		t.Fatal(err)
	}

	withArgs(t, []string{"ai-playbook", "run", "--retry", "--file", file})
	called := false
	withUIRunFn(t, func(ui.Options) int { called = true; return 0 })

	code := -1
	msg := captureStderr(t, func() { code = RunMain() })
	if code != 0 {
		t.Errorf("RunMain = %d, want 0 (success case)", code)
	}
	if called {
		t.Error("the viewer must not open when there is nothing to resume")
	}
	if !strings.Contains(msg, "nothing to resume") {
		t.Errorf("stderr = %q, want the nothing-to-resume message", msg)
	}
}

// TestRunMain_RetryNoJournal_Exit1: gate 1 — never run → message + exit 1, no
// viewer.
func TestRunMain_RetryNoJournal_Exit1(t *testing.T) {
	retryEnv(t)
	file := filepath.Join(t.TempDir(), "pb.md")
	if err := os.WriteFile(file, []byte(retryPlaybookMD), 0o644); err != nil {
		t.Fatal(err)
	}
	withArgs(t, []string{"ai-playbook", "run", "--retry", "--file", file})
	called := false
	withUIRunFn(t, func(ui.Options) int { called = true; return 0 })

	code := -1
	msg := captureStderr(t, func() { code = RunMain() })
	if code != 1 || called {
		t.Errorf("RunMain = %d (viewer called: %v), want exit 1 and no viewer", code, called)
	}
	if !strings.Contains(msg, "no previous run recorded") {
		t.Errorf("stderr = %q, want the no-journal message", msg)
	}
}

// TestRunMain_RetryHashMismatch_Exit1: gate 3 — the playbook drifted since
// the failed run → refusal + exit 1, no viewer.
func TestRunMain_RetryHashMismatch_Exit1(t *testing.T) {
	retryEnv(t)
	file := filepath.Join(t.TempDir(), "pb.md")
	if err := os.WriteFile(file, []byte(retryPlaybookMD), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFailedJournalFor(t, "", file)
	// Drift the document AFTER the journaled run.
	if err := os.WriteFile(file, []byte(retryPlaybookMD+"\nnew prose\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	withArgs(t, []string{"ai-playbook", "run", "--retry", "--file", file})
	called := false
	withUIRunFn(t, func(ui.Options) int { called = true; return 0 })

	code := -1
	msg := captureStderr(t, func() { code = RunMain() })
	if code != 1 || called {
		t.Errorf("RunMain = %d (viewer called: %v), want exit 1 and no viewer", code, called)
	}
	if !strings.Contains(msg, "playbook changed since the failed run") {
		t.Errorf("stderr = %q, want the drift refusal", msg)
	}
}

// TestAutoRun_RetryThreadsSeed: the --auto path threads the same seed into
// autorun.RunConfig.RetrySeed through the same gate.
func TestAutoRun_RetryThreadsSeed(t *testing.T) {
	retryEnv(t)
	file := filepath.Join(t.TempDir(), "pb.md")
	if err := os.WriteFile(file, []byte(retryPlaybookMD), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFailedJournalFor(t, "", file)

	withArgs(t, []string{"ai-playbook", "run", "--auto", "--retry", "--file", file})
	var got autorun.RunConfig
	restore := swap(&autorunRunFn, func(rc autorun.RunConfig) int { got = rc; return 0 })
	t.Cleanup(restore)

	code := -1
	_ = captureStderr(t, func() { code = RunMain() })
	if code != 0 {
		t.Fatalf("RunMain = %d, want 0", code)
	}
	rec, ok := got.RetrySeed["one"]
	if !ok || !rec.PreviousRun {
		t.Errorf("RetrySeed = %v, want block one pre-seeded with previous_run", got.RetrySeed)
	}
	if _, ok := got.RetrySeed["two"]; ok {
		t.Error("the failed block must not be pre-seeded")
	}
}

// TestAutoRun_Retry_EndToEnd_Live is THE live resume: a real 3-block playbook
// fails at block two, the environment is fixed out-of-band, and
// `--auto --retry` skips block one, re-runs two, runs verify, and finalizes
// an ok journal whose block one is re-recorded previous_run: true.
func TestAutoRun_Retry_EndToEnd_Live(t *testing.T) {
	if testing.Short() {
		t.Skip("opens a real driver")
	}
	retryEnv(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "pb.md")
	ranOne := filepath.Join(dir, "ran-one")
	flag := filepath.Join(dir, "flag")
	md := "```bash {id=one}\necho ran >> " + ranOne + "\n```\n\n" +
		"```bash {id=two}\ntest -f " + flag + "\n```\n\n" +
		"```bash {id=verify}\ntest -f " + flag + "\n```\n"
	if err := os.WriteFile(file, []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}

	// Run 1: fails at two (the flag file does not exist yet).
	withArgs(t, []string{"ai-playbook", "run", "--auto", "--file", file})
	code := -1
	_ = captureStderr(t, func() { code = RunMain() })
	if code == 0 {
		t.Fatal("run 1 must fail at block two")
	}
	raw, _ := os.ReadFile(file)
	jPath, _, _ := journalIdentity("", file, string(raw))
	run1, err := runlog.Load(jPath)
	if err != nil {
		t.Fatalf("run-1 journal: %v", err)
	}
	if run1.Outcome != runlog.OutcomeFailed || run1.Blocks["one"].Outcome != runlog.OutcomeOK {
		t.Fatalf("run-1 journal = outcome %q one %q, want failed with one ok", run1.Outcome, run1.Blocks["one"].Outcome)
	}

	// The out-of-band fix.
	if err := os.WriteFile(flag, []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Run 2: --auto --retry skips one, re-runs two, verify passes.
	withArgs(t, []string{"ai-playbook", "run", "--auto", "--retry", "--file", file})
	msg := captureStderr(t, func() { code = RunMain() })
	if code != 0 {
		t.Fatalf("retry run = %d, want 0 (stderr: %s)", code, msg)
	}
	if !strings.Contains(msg, `resuming at "two"`) {
		t.Errorf("stderr = %q, want the resume note", msg)
	}
	run2, err := runlog.Load(jPath)
	if err != nil {
		t.Fatalf("run-2 journal: %v", err)
	}
	if run2.Outcome != runlog.OutcomeOK {
		t.Errorf("run-2 outcome = %q, want ok", run2.Outcome)
	}
	one := run2.Blocks["one"]
	if one.Outcome != runlog.OutcomeOK || !one.PreviousRun {
		t.Errorf("one = %+v, want ok re-recorded with previous_run: true", one)
	}
	if two := run2.Blocks["two"]; two.Outcome != runlog.OutcomeOK || two.PreviousRun {
		t.Errorf("two = %+v, want a fresh ok record", two)
	}
	if v := run2.Blocks["verify"]; v.Outcome != runlog.OutcomeOK || v.PreviousRun {
		t.Errorf("verify = %+v, want a fresh ok record (never pre-seeded)", v)
	}
	// Block one must have run exactly ONCE across both sessions (the skip is
	// real, not a silent re-run).
	data, err := os.ReadFile(ranOne)
	if err != nil {
		t.Fatalf("ran-one: %v", err)
	}
	if got := strings.Count(string(data), "ran"); got != 1 {
		t.Errorf("block one ran %d time(s), want exactly 1 (retry must skip it)", got)
	}
}

// TestAutoRun_Retry_FromDemotion_EndToEnd_Live: a from= producer that was ok
// in run 1 is DEMOTED when its consumer failed — the retry re-runs the
// producer (its capture is gone) and pipes the fresh capture into the
// consumer, while an unrelated ok block stays skipped.
func TestAutoRun_Retry_FromDemotion_EndToEnd_Live(t *testing.T) {
	if testing.Short() {
		t.Skip("opens a real driver")
	}
	retryEnv(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "pb.md")
	genRuns := filepath.Join(dir, "gen-runs")
	flag := filepath.Join(dir, "flag")
	md := "```bash {id=keep}\ntrue\n```\n\n" +
		"```bash {id=gen}\necho ran >> " + genRuns + "\necho data\n```\n\n" +
		"```bash {id=use from=gen}\ntest -f " + flag + " && grep -q data\n```\n\n" +
		"```bash {id=verify}\ntest -f " + flag + "\n```\n"
	if err := os.WriteFile(file, []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}

	withArgs(t, []string{"ai-playbook", "run", "--auto", "--file", file})
	code := -1
	_ = captureStderr(t, func() { code = RunMain() })
	if code == 0 {
		t.Fatal("run 1 must fail at the consumer")
	}

	if err := os.WriteFile(flag, []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	withArgs(t, []string{"ai-playbook", "run", "--auto", "--retry", "--file", file})
	msg := captureStderr(t, func() { code = RunMain() })
	if code != 0 {
		t.Fatalf("retry run = %d, want 0 (stderr: %s)", code, msg)
	}
	if !strings.Contains(msg, "re-running gen") {
		t.Errorf("stderr = %q, want the demotion note naming gen", msg)
	}

	raw, _ := os.ReadFile(file)
	jPath, _, _ := journalIdentity("", file, string(raw))
	run2, err := runlog.Load(jPath)
	if err != nil {
		t.Fatalf("run-2 journal: %v", err)
	}
	if run2.Outcome != runlog.OutcomeOK {
		t.Errorf("run-2 outcome = %q, want ok", run2.Outcome)
	}
	if keep := run2.Blocks["keep"]; !keep.PreviousRun {
		t.Errorf("keep = %+v, want previous_run (it feeds nothing remaining, so it stays skipped)", keep)
	}
	if gen := run2.Blocks["gen"]; gen.PreviousRun || gen.Outcome != runlog.OutcomeOK {
		t.Errorf("gen = %+v, want a FRESH ok record (demoted producer re-ran)", gen)
	}
	// The consumer passed `grep -q data` — the re-run producer's capture was
	// actually piped — and gen ran once per session.
	if use := run2.Blocks["use"]; use.Outcome != runlog.OutcomeOK {
		t.Errorf("use = %+v, want ok (piped from the re-run producer)", use)
	}
	data, err := os.ReadFile(genRuns)
	if err != nil {
		t.Fatalf("gen-runs: %v", err)
	}
	if got := strings.Count(string(data), "ran"); got != 2 {
		t.Errorf("gen ran %d time(s) total, want 2 (once per session — the demotion re-ran it)", got)
	}
}
