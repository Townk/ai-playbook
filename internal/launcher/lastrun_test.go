// lastrun_test.go — v0.12.3 R3 discoverability: the plain-`run` retry hint
// (spec Decision 5) and the `list` last-outcome column (spec Decision 6).
// Journals stay ADVISORY throughout: no hint/list path may alter exit codes
// or break on a missing/corrupt journal.
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
	"github.com/Townk/ai-playbook/pkg/store"
)

// ---- the plain-run hint (pure computation) ----

// TestRetryHint_Table pins the hint ladder: only a hash-matching journal
// whose last run failed/stopped — or was interrupted mid-flight with real
// block records — and whose retry seed is non-Fresh hints; everything else
// is silent.
func TestRetryHint_Table(t *testing.T) {
	const hash = "matching-hash"
	const cmd = "ai-playbook run --retry my-pb"
	failedRun := runlog.Run{
		ContentHash: hash,
		Finished:    time.Now().Add(-2 * time.Hour),
		Outcome:     runlog.OutcomeFailed,
		Blocks: map[string]runlog.BlockRecord{
			"one": {Outcome: runlog.OutcomeOK, Duration: time.Second},
			"two": {Outcome: runlog.OutcomeFailed, Exit: 7},
		},
	}

	// demotedBlocks: block two consumes block one's capture ($APB_OUT_one),
	// so a prior-ok `one` under a failed `two` demotes and the seed goes
	// Fresh — the reviewer's finding-1 scenario.
	demotedBlocks := playbook.ParseBlocks(
		"```bash {id=one}\necho x\n```\n\n```bash {id=two}\ngrep y \"$APB_OUT_one\"\n```\n\n```bash {id=verify}\ntrue\n```\n")

	cases := []struct {
		name   string
		path   func(t *testing.T) string
		blocks []playbook.Block // nil ⇒ retryBlocks
		want   []string         // all must appear; empty ⇒ the hint must be ""
	}{
		{
			name: "failed run hints with id, age, and the retry command",
			path: func(t *testing.T) string { return saveRetryJournal(t, failedRun) },
			want: []string{`last run failed at "two"`, "(2h ago)", "'" + cmd + "'", "resumes there"},
		},
		{
			name: "stopped run hints too",
			path: func(t *testing.T) string {
				stopped := failedRun
				stopped.Outcome = runlog.OutcomeStopped
				stopped.Blocks = map[string]runlog.BlockRecord{
					"one": {Outcome: runlog.OutcomeOK},
					"two": {Outcome: runlog.OutcomeStopped},
				}
				return saveRetryJournal(t, stopped)
			},
			want: []string{`last run stopped at "two"`, "resumes there"},
		},
		{
			name: "the id comes from the RetrySeed derivation, never first_failure",
			path: func(t *testing.T) string {
				r := failedRun
				r.FirstFailure = "two" // stale/misleading; block one is the real pickup
				r.Blocks = map[string]runlog.BlockRecord{
					"one": {Outcome: runlog.OutcomeFailed, Exit: 1},
					"two": {Outcome: runlog.OutcomeOK},
				}
				return saveRetryJournal(t, r)
			},
			want: []string{`last run failed at "one"`},
		},
		{
			name: "a fresh failure reads as just now",
			path: func(t *testing.T) string {
				r := failedRun
				r.Finished = time.Now().Add(-10 * time.Second)
				return saveRetryJournal(t, r)
			},
			want: []string{"(just now)"},
		},
		{
			name: "no finished timestamp falls back to started",
			path: func(t *testing.T) string {
				r := failedRun
				r.Finished = time.Time{}
				r.Started = time.Now().Add(-3 * 24 * time.Hour)
				return saveRetryJournal(t, r)
			},
			want: []string{"(3d ago)"},
		},
		{
			name: "success journal is silent",
			path: func(t *testing.T) string {
				r := failedRun
				r.Outcome = runlog.OutcomeOK
				return saveRetryJournal(t, r)
			},
		},
		{
			name: "interrupted (mid-flight) journal hints — the crash case",
			path: func(t *testing.T) string {
				return saveRetryJournal(t, runlog.Run{
					ContentHash: hash,
					Started:     time.Now().Add(-2 * time.Hour),
					Blocks: map[string]runlog.BlockRecord{
						"one": {Outcome: runlog.OutcomeOK, Duration: time.Second},
					},
				})
			},
			want: []string{`last run was interrupted at "two"`, "(2h ago)", "resumes there"},
		},
		{
			name: "mid-flight with no block records is silent (view-then-crash shape)",
			path: func(t *testing.T) string {
				return saveRetryJournal(t, runlog.Run{
					ContentHash: hash,
					Started:     time.Now().Add(-time.Hour),
					Blocks:      map[string]runlog.BlockRecord{},
				})
			},
		},
		{
			name: "no outcome but a finished stamp is silent (mangled shape)",
			path: func(t *testing.T) string {
				r := failedRun
				r.Outcome = ""
				return saveRetryJournal(t, r)
			},
		},
		{
			name:   "all-demoted seed is silent (the hint must not contradict --retry's fresh-run degradation)",
			path:   func(t *testing.T) string { return saveRetryJournal(t, failedRun) },
			blocks: demotedBlocks,
		},
		{
			name: "no ok blocks (Fresh seed) is silent — --retry adds nothing over the fresh run",
			path: func(t *testing.T) string {
				r := failedRun
				r.Blocks = map[string]runlog.BlockRecord{
					"one": {Outcome: runlog.OutcomeFailed, Exit: 1},
				}
				return saveRetryJournal(t, r)
			},
		},
		{
			name: "absent journal is silent",
			path: func(t *testing.T) string { return filepath.Join(t.TempDir(), "absent.json") },
		},
		{
			name: "corrupt journal is silent (advisory)",
			path: func(t *testing.T) string {
				p := filepath.Join(t.TempDir(), "j.json")
				if err := os.WriteFile(p, []byte("{not json"), 0o644); err != nil {
					t.Fatal(err)
				}
				return p
			},
		},
		{
			name: "hash drift is silent (a drifted playbook gets no misleading hint)",
			path: func(t *testing.T) string {
				r := failedRun
				r.ContentHash = "some-other-hash"
				return saveRetryJournal(t, r)
			},
		},
		{
			name: "no journal path (journaling unavailable) is silent",
			path: func(*testing.T) string { return "" },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			blocks := tc.blocks
			if blocks == nil {
				blocks = retryBlocks
			}
			got := retryHint(tc.path(t), hash, blocks, cmd)
			if len(tc.want) == 0 {
				if got != "" {
					t.Fatalf("retryHint = %q, want silence", got)
				}
				return
			}
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("retryHint = %q, want it to contain %q", got, w)
				}
			}
		})
	}
}

// TestRetryCommand pins the concrete command the hint names, per run form.
func TestRetryCommand(t *testing.T) {
	cases := []struct {
		name string
		ra   runArgs
		want string
	}{
		{"slug", runArgs{Kind: "playbook", Value: "my-pb"}, "ai-playbook run --retry my-pb"},
		{"file", runArgs{Kind: "file", Value: "/tmp/pb.md"}, "ai-playbook run --retry --file /tmp/pb.md"},
		{"auto", runArgs{Kind: "playbook", Value: "my-pb", Mode: modeAuto}, "ai-playbook run --auto --retry my-pb"},
		{"assisted", runArgs{Kind: "playbook", Value: "my-pb", Mode: modeAssisted}, "ai-playbook run --assisted --retry my-pb"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := retryCommand(tc.ra); got != tc.want {
				t.Errorf("retryCommand = %q, want %q", got, tc.want)
			}
		})
	}
}

// ---- the hint wired into both run paths ----

// TestRunMain_PlainRunPrintsHint: a plain viewer run over a failed journal
// prints ONE stderr hint, keeps stdout clean, threads NO retry seed, and the
// run proceeds (the viewer's exit code is untouched).
func TestRunMain_PlainRunPrintsHint(t *testing.T) {
	retryEnv(t)
	file := filepath.Join(t.TempDir(), "pb.md")
	if err := os.WriteFile(file, []byte(retryPlaybookMD), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFailedJournalFor(t, "", file)

	withArgs(t, []string{"ai-playbook", "run", "--file", file})
	var got ui.Options
	withUIRunFn(t, func(o ui.Options) int { got = o; return 3 })

	code := -1
	var msg string
	stdout := captureStdout(t, func() {
		msg = captureStderr(t, func() { code = RunMain() })
	})
	if code != 3 {
		t.Errorf("RunMain = %d, want the viewer's own exit code 3 (the hint never affects it)", code)
	}
	if !strings.Contains(msg, `last run failed at "two"`) || !strings.Contains(msg, "resumes there") {
		t.Errorf("stderr = %q, want the retry hint", msg)
	}
	if strings.Count(msg, "resumes there") != 1 {
		t.Errorf("stderr = %q, want exactly ONE hint line", msg)
	}
	if !strings.Contains(msg, "--retry --file "+file) {
		t.Errorf("stderr = %q, want the hint to name the concrete retry command", msg)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want it clean (the hint is stderr-only)", stdout)
	}
	if got.RetrySeed != nil {
		t.Errorf("RetrySeed = %v, want nil on a plain run (the hint never seeds)", got.RetrySeed)
	}
}

// TestRunMain_PlainRun_SilentCases: success journal, absent journal, and a
// drifted document print no hint on a plain run.
func TestRunMain_PlainRun_SilentCases(t *testing.T) {
	cases := []struct {
		name string
		prep func(t *testing.T, file string)
	}{
		{"absent journal", func(*testing.T, string) {}},
		{"success journal", func(t *testing.T, file string) {
			raw, _ := os.ReadFile(file)
			jPath, _, jHash := journalIdentity("", file, string(raw))
			if err := runlog.Save(jPath, runlog.Run{ContentHash: jHash, Started: time.Now(),
				Finished: time.Now(), Outcome: runlog.OutcomeOK,
				Blocks: map[string]runlog.BlockRecord{"one": {Outcome: runlog.OutcomeOK}}}); err != nil {
				t.Fatal(err)
			}
		}},
		{"drifted document", func(t *testing.T, file string) {
			writeFailedJournalFor(t, "", file)
			if err := os.WriteFile(file, []byte(retryPlaybookMD+"\nnew prose\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"corrupt journal", func(t *testing.T, file string) {
			raw, _ := os.ReadFile(file)
			jPath, _, _ := journalIdentity("", file, string(raw))
			if err := os.MkdirAll(filepath.Dir(jPath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(jPath, []byte("{not json"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			retryEnv(t)
			file := filepath.Join(t.TempDir(), "pb.md")
			if err := os.WriteFile(file, []byte(retryPlaybookMD), 0o644); err != nil {
				t.Fatal(err)
			}
			tc.prep(t, file)

			withArgs(t, []string{"ai-playbook", "run", "--file", file})
			withUIRunFn(t, func(ui.Options) int { return 0 })

			code := -1
			msg := captureStderr(t, func() { code = RunMain() })
			if code != 0 {
				t.Errorf("RunMain = %d, want 0 (journals are advisory)", code)
			}
			if strings.Contains(msg, "resumes there") {
				t.Errorf("stderr = %q, want NO hint", msg)
			}
		})
	}
}

// TestAutoRun_PlainRunPrintsHint: the --auto path prints the same hint (naming
// the --auto retry form) and still runs fresh with no seed.
func TestAutoRun_PlainRunPrintsHint(t *testing.T) {
	retryEnv(t)
	file := filepath.Join(t.TempDir(), "pb.md")
	if err := os.WriteFile(file, []byte(retryPlaybookMD), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFailedJournalFor(t, "", file)

	withArgs(t, []string{"ai-playbook", "run", "--auto", "--file", file})
	var got autorun.RunConfig
	restore := swap(&autorunRunFn, func(rc autorun.RunConfig) int { got = rc; return 0 })
	t.Cleanup(restore)

	code := -1
	msg := captureStderr(t, func() { code = RunMain() })
	if code != 0 {
		t.Fatalf("RunMain = %d, want 0", code)
	}
	if !strings.Contains(msg, `last run failed at "two"`) {
		t.Errorf("stderr = %q, want the retry hint", msg)
	}
	if !strings.Contains(msg, "--auto --retry --file "+file) {
		t.Errorf("stderr = %q, want the --auto retry form in the hint", msg)
	}
	if got.RetrySeed != nil {
		t.Errorf("RetrySeed = %v, want nil on a plain run", got.RetrySeed)
	}
}

// ---- the list last-outcome column ----

// listEnv pins the journal-resolution inputs for the list tests and returns
// the fixed project root.
func listEnv(t *testing.T) string {
	t.Helper()
	t.Setenv("AI_PLAYBOOK_DATA_DIR", t.TempDir())
	const proj = "/list/proj"
	restore := swap(&projectRootFn, func() string { return proj })
	t.Cleanup(restore)
	return proj
}

// saveListJournal writes a journal for slug under the CURRENT project key —
// the same runlog.Path the run path writes through.
func saveListJournal(t *testing.T, slug string, run runlog.Run) {
	t.Helper()
	path := runlog.Path(os.Getenv("AI_PLAYBOOK_DATA_DIR"), projectRootFn(), slug)
	if err := runlog.Save(path, run); err != nil {
		t.Fatal(err)
	}
}

// TestLastRunCells_Table: ✓/✗/– cells with elapsed, sourced from the current
// project's journals; corruption yields – with ONE stderr note total.
func TestLastRunCells_Table(t *testing.T) {
	listEnv(t)
	started := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	saveListJournal(t, "ok-pb", runlog.Run{
		Started: started, Finished: started.Add(4200 * time.Millisecond),
		Outcome: runlog.OutcomeOK,
		Blocks:  map[string]runlog.BlockRecord{"one": {Outcome: runlog.OutcomeOK}},
	})
	saveListJournal(t, "failed-pb", runlog.Run{
		Started: started, Finished: started.Add(90 * time.Second),
		Outcome: runlog.OutcomeFailed,
		Blocks:  map[string]runlog.BlockRecord{"one": {Outcome: runlog.OutcomeFailed}},
	})
	saveListJournal(t, "stopped-pb", runlog.Run{
		Started: started, Finished: started.Add(30 * time.Second),
		Outcome: runlog.OutcomeStopped,
		Blocks:  map[string]runlog.BlockRecord{"one": {Outcome: runlog.OutcomeStopped}},
	})
	// Mid-flight journal: the run died before finalize — no outcome, no end.
	saveListJournal(t, "midflight-pb", runlog.Run{
		Started: started,
		Blocks:  map[string]runlog.BlockRecord{"one": {Outcome: runlog.OutcomeOK}},
	})
	// Two corrupt journals — the note must still print ONCE.
	for _, slug := range []string{"corrupt-pb", "corrupt2-pb"} {
		path := runlog.Path(os.Getenv("AI_PLAYBOOK_DATA_DIR"), projectRootFn(), slug)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A journal for the same slug under ANOTHER project must not leak in.
	otherProj := runlog.Path(os.Getenv("AI_PLAYBOOK_DATA_DIR"), "/other/proj", "other-proj-pb")
	if err := runlog.Save(otherProj, runlog.Run{Started: started, Finished: started.Add(time.Second),
		Outcome: runlog.OutcomeOK, Blocks: map[string]runlog.BlockRecord{"one": {Outcome: runlog.OutcomeOK}}}); err != nil {
		t.Fatal(err)
	}

	metas := []store.Meta{
		{Slug: "ok-pb"}, {Slug: "failed-pb"}, {Slug: "stopped-pb"},
		{Slug: "midflight-pb"}, {Slug: "corrupt-pb"}, {Slug: "corrupt2-pb"},
		{Slug: "never-pb"}, {Slug: "other-proj-pb"},
	}
	var cells []string
	msg := captureStderr(t, func() { cells = lastRunCells(metas) })

	want := []string{"✓ 4.2s", "✗ 1m30s", "✗ 30s", "✗", "–", "–", "–", "–"}
	if len(cells) != len(want) {
		t.Fatalf("lastRunCells returned %d cells, want %d", len(cells), len(want))
	}
	for i := range want {
		if cells[i] != want[i] {
			t.Errorf("cell[%d] (%s) = %q, want %q", i, metas[i].Slug, cells[i], want[i])
		}
	}
	if !strings.Contains(msg, "unreadable") {
		t.Errorf("stderr = %q, want the corruption note", msg)
	}
	if got := strings.Count(strings.TrimRight(msg, "\n"), "\n") + 1; got != 1 {
		t.Errorf("stderr = %q, want exactly ONE note line (not per-row spam), got %d", msg, got)
	}
}

// TestLastRunCells_NoRunsDir: a project that never ran anything (no runs dir
// at all) yields – for every row and no stderr noise.
func TestLastRunCells_NoRunsDir(t *testing.T) {
	listEnv(t)
	var cells []string
	msg := captureStderr(t, func() {
		cells = lastRunCells([]store.Meta{{Slug: "a"}, {Slug: "b"}})
	})
	for i, c := range cells {
		if c != "–" {
			t.Errorf("cell[%d] = %q, want %q", i, c, "–")
		}
	}
	if msg != "" {
		t.Errorf("stderr = %q, want silence (a missing runs dir is not an error)", msg)
	}
}

// TestHumanElapsed pins the elapsed formatting (FormatTimeout-style trimming,
// coarse rounding).
func TestHumanElapsed(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{4234 * time.Millisecond, "4.2s"},
		{90 * time.Second, "1m30s"},
		{10 * time.Minute, "10m"},
		{time.Hour, "1h"},
		{350 * time.Millisecond, "350ms"},
		{0, "0s"},
		{-time.Second, "0s"},
	}
	for _, tc := range cases {
		if got := humanElapsed(tc.d); got != tc.want {
			t.Errorf("humanElapsed(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

// TestFormatHuman_LastRunColumn: the human table gains the LAST RUN column;
// a nil cells slice degrades to – (never run).
func TestFormatHuman_LastRunColumn(t *testing.T) {
	out := formatHuman([]store.Meta{sampleMeta}, []string{"✓ 4.2s"})
	if !strings.Contains(out, "LAST RUN") {
		t.Errorf("formatHuman: missing LAST RUN header:\n%s", out)
	}
	if !strings.Contains(out, "✓ 4.2s") {
		t.Errorf("formatHuman: missing the outcome cell:\n%s", out)
	}
	out = formatHuman([]store.Meta{sampleMeta}, nil)
	if !strings.Contains(out, "–") {
		t.Errorf("formatHuman with nil cells: want – fallback:\n%s", out)
	}
}

// TestListMain_LastRunColumn: end-to-end — `list` joins the store index with
// the current project's journals in the human format.
func TestListMain_LastRunColumn(t *testing.T) {
	listEnv(t)
	started := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	saveListJournal(t, "build-app", runlog.Run{
		Started: started, Finished: started.Add(2 * time.Second),
		Outcome: runlog.OutcomeOK,
		Blocks:  map[string]runlog.BlockRecord{"one": {Outcome: runlog.OutcomeOK}},
	})
	withArgs(t, []string{"ai-playbook", "list"})
	withIndexFn(t, func() ([]store.Meta, error) {
		return []store.Meta{sampleMeta, {Slug: "never-run", Name: "Never", Path: "/store/never-run.md"}}, nil
	})

	code := -1
	out := captureStdout(t, func() { code = ListMain() })
	if code != 0 {
		t.Fatalf("ListMain = %d, want 0", code)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want header + 2 rows, got %d:\n%s", len(lines), out)
	}
	if !strings.Contains(lines[0], "LAST RUN") {
		t.Errorf("header = %q, want LAST RUN column", lines[0])
	}
	var buildRow, neverRow string
	for _, l := range lines[1:] {
		if strings.Contains(l, "Build") {
			buildRow = l
		}
		if strings.Contains(l, "Never") {
			neverRow = l
		}
	}
	if !strings.Contains(buildRow, "✓ 2s") {
		t.Errorf("build row = %q, want the ✓ 2s cell", buildRow)
	}
	if !strings.HasSuffix(strings.TrimRight(neverRow, " "), "–") {
		t.Errorf("never-run row = %q, want the – cell", neverRow)
	}
}
