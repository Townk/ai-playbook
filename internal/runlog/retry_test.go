package runlog

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/Townk/ai-playbook/pkg/playbook"
)

// shellBlock builds a runnable shell block for the scan/seed tables.
func shellBlock(id, payload string) playbook.Block {
	return playbook.Block{ID: id, Type: "shell", Lang: "bash", Payload: payload}
}

func TestConsumerScan_Table(t *testing.T) {
	cases := []struct {
		name   string
		blocks []playbook.Block
		want   map[string][]string
	}{
		{
			name: "from= edge",
			blocks: []playbook.Block{
				shellBlock("gen", "echo data"),
				{ID: "use", Type: "shell", Lang: "bash", From: "gen", Payload: "grep -q data"},
			},
			want: map[string][]string{"use": {"gen"}},
		},
		{
			name: "each APB form",
			blocks: []playbook.Block{
				shellBlock("a", "true"),
				shellBlock("b", "true"),
				shellBlock("c", "true"),
				shellBlock("d", "true"),
				shellBlock("e", "true"),
				shellBlock("w", `echo "$APB_OUT_a"`),
				shellBlock("x", "echo ${APB_ERR_b}"),
				shellBlock("y", "cat $APB_OUT_FILE_c"),
				shellBlock("z", "cat $APB_ERR_FILE_d"),
				shellBlock("g", `test "$APB_EXIT_e" = 0`),
			},
			want: map[string][]string{
				"w": {"a"}, "x": {"b"}, "y": {"c"}, "z": {"d"}, "g": {"e"},
			},
		},
		{
			name: "sanitized id my-id resolves APB_OUT_my_id",
			blocks: []playbook.Block{
				shellBlock("my-id", "true"),
				shellBlock("use", "echo $APB_OUT_my_id"),
			},
			want: map[string][]string{"use": {"my-id"}},
		},
		{
			name: "static payload references are content, not consumption",
			blocks: []playbook.Block{
				shellBlock("a", "true"),
				{ID: "doc", Type: "static", Lang: "text", Payload: "the run exports $APB_OUT_a"},
				{ID: "mk", Type: "create", File: "x.sh", Payload: "echo $APB_OUT_a"},
				{ID: "patch", Type: "diff", Lang: "diff", Payload: "+echo $APB_OUT_a"},
			},
			want: map[string][]string{},
		},
		{
			name: "no substring or left-boundary false positives",
			blocks: []playbook.Block{
				shellBlock("build", "true"),
				shellBlock("u1", "echo $APB_OUT_build_dir"), // longer var ≠ build
				shellBlock("u2", "echo $XAPB_OUT_build"),    // different variable
			},
			want: map[string][]string{},
		},
		{
			name: "FILE_-prefixed block id resolves both readings",
			blocks: []playbook.Block{
				shellBlock("FILE_x", "true"),
				shellBlock("x", "true"),
				shellBlock("use", "cat $APB_OUT_FILE_x"),
			},
			// APB_OUT_FILE_x is OUT of FILE_x or OUT_FILE of x — both real.
			want: map[string][]string{"use": {"FILE_x", "x"}},
		},
		{
			name: "self-reference is not an edge",
			blocks: []playbook.Block{
				shellBlock("loop", "echo $APB_OUT_loop"),
			},
			want: map[string][]string{},
		},
		{
			name: "run (script) payloads are scanned",
			blocks: []playbook.Block{
				shellBlock("a", "true"),
				{ID: "py", Type: "run", Lang: "python", Payload: `import os; print(os.environ["APB_OUT_a"])`},
			},
			want: map[string][]string{"py": {"a"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ConsumerScan(tc.blocks)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ConsumerScan = %v, want %v", got, tc.want)
			}
		})
	}
}

// seedRun builds a Run whose Blocks carry the given outcomes.
func seedRun(outcomes map[string]string) Run {
	r := Run{Outcome: OutcomeFailed, Blocks: map[string]BlockRecord{}}
	for id, o := range outcomes {
		r.Blocks[id] = BlockRecord{Outcome: o, Duration: 42 * time.Millisecond}
	}
	return r
}

func TestRetrySeed_Table(t *testing.T) {
	cases := []struct {
		name        string
		blocks      []playbook.Block
		run         Run
		wantSeeded  []string
		wantDemoted []string
		wantStart   string
		wantFresh   bool
	}{
		{
			name: "basic resume: ok pre-seeded, failed is the start",
			blocks: []playbook.Block{
				shellBlock("one", "true"), shellBlock("two", "true"), shellBlock("three", "true"),
			},
			run:        seedRun(map[string]string{"one": OutcomeOK, "two": OutcomeFailed}),
			wantSeeded: []string{"one"},
			wantStart:  "two",
		},
		{
			name: "verify is never pre-seeded",
			blocks: []playbook.Block{
				shellBlock("one", "true"), shellBlock("two", "true"), shellBlock("verify", "true"),
			},
			run:        seedRun(map[string]string{"one": OutcomeOK, "two": OutcomeFailed, "verify": OutcomeOK}),
			wantSeeded: []string{"one"},
			wantStart:  "two",
		},
		{
			name: "stopped and rolled-back resume like failed",
			blocks: []playbook.Block{
				shellBlock("one", "true"), shellBlock("two", "true"), shellBlock("three", "true"),
			},
			run:        seedRun(map[string]string{"one": OutcomeRolledBack, "two": OutcomeStopped}),
			wantStart:  "one",
			wantFresh:  true,
			wantSeeded: nil,
		},
		{
			name: "empty first_failure tolerated: start derives from records",
			blocks: []playbook.Block{
				shellBlock("a", "true"), shellBlock("b", "true"), shellBlock("c", "true"),
			},
			// A-failed→B-failed→A-rerun-ok leaves first_failure "" (R1 note 5).
			run: Run{Outcome: OutcomeFailed, FirstFailure: "", Blocks: map[string]BlockRecord{
				"a": {Outcome: OutcomeOK},
				"b": {Outcome: OutcomeFailed},
			}},
			wantSeeded: []string{"a"},
			wantStart:  "b",
		},
		{
			name: "from= consumer demotes its ok producer",
			blocks: []playbook.Block{
				shellBlock("keep", "true"),
				shellBlock("gen", "echo data"),
				{ID: "use", Type: "shell", Lang: "bash", From: "gen", Payload: "grep -q data"},
			},
			run:         seedRun(map[string]string{"keep": OutcomeOK, "gen": OutcomeOK, "use": OutcomeFailed}),
			wantSeeded:  []string{"keep"},
			wantDemoted: []string{"gen"},
			wantStart:   "use",
		},
		{
			name: "APB_EXIT reference demotes its ok producer",
			blocks: []playbook.Block{
				shellBlock("keep", "true"),
				shellBlock("check", "true"),
				shellBlock("gate", `test "$APB_EXIT_check" = 0`),
			},
			run:         seedRun(map[string]string{"keep": OutcomeOK, "check": OutcomeOK, "gate": OutcomeFailed}),
			wantSeeded:  []string{"keep"},
			wantDemoted: []string{"check"},
			wantStart:   "gate",
		},
		{
			name: "APB reference demotes its ok producer",
			blocks: []playbook.Block{
				shellBlock("token", "echo t0k3n"),
				shellBlock("call", `curl -H "X-Auth: $APB_OUT_token" x`),
			},
			run:         seedRun(map[string]string{"token": OutcomeOK, "call": OutcomeFailed}),
			wantDemoted: []string{"token"},
			wantStart:   "call",
			wantFresh:   true, // the only ok block demoted → degrade to fresh
		},
		{
			name: "producer feeding only prior-ok blocks stays seeded",
			blocks: []playbook.Block{
				shellBlock("gen", "echo data"),
				{ID: "use", Type: "shell", Lang: "bash", From: "gen", Payload: "grep -q data"},
				shellBlock("later", "false"),
			},
			run:        seedRun(map[string]string{"gen": OutcomeOK, "use": OutcomeOK, "later": OutcomeFailed}),
			wantSeeded: []string{"gen", "use"},
			wantStart:  "later",
		},
		{
			name: "demotion is transitive through a data chain",
			blocks: []playbook.Block{
				shellBlock("keep", "true"),
				shellBlock("a", "echo a"),
				{ID: "b", Type: "shell", Lang: "bash", From: "a", Payload: "sed s/a/b/"},
				{ID: "c", Type: "shell", Lang: "bash", From: "b", Payload: "grep -q b"},
			},
			run: seedRun(map[string]string{
				"keep": OutcomeOK, "a": OutcomeOK, "b": OutcomeOK, "c": OutcomeFailed,
			}),
			wantSeeded:  []string{"keep"},
			wantDemoted: []string{"a", "b"},
			wantStart:   "c",
		},
		{
			name: "no ok blocks at all is fresh",
			blocks: []playbook.Block{
				shellBlock("one", "true"), shellBlock("two", "true"),
			},
			run:       seedRun(map[string]string{"one": OutcomeFailed}),
			wantStart: "one",
			wantFresh: true,
		},
		{
			name: "static blocks and rollback targets are neither seeds nor resume points",
			blocks: []playbook.Block{
				{ID: "note", Type: "static", Lang: "text", Payload: "docs"},
				{ID: "apply", Type: "shell", Lang: "bash", Payload: "true", Rollback: "undo"},
				shellBlock("undo", "true"),
				shellBlock("boom", "false"),
			},
			run:        seedRun(map[string]string{"apply": OutcomeOK, "boom": OutcomeFailed}),
			wantSeeded: []string{"apply"},
			wantStart:  "boom",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seed := RetrySeed(tc.blocks, tc.run)
			var seeded []string
			for id := range seed.PreSeeded {
				seeded = append(seeded, id)
			}
			sort.Strings(seeded)
			want := append([]string(nil), tc.wantSeeded...)
			sort.Strings(want)
			if !reflect.DeepEqual(seeded, want) {
				t.Errorf("PreSeeded ids = %v, want %v", seeded, want)
			}
			if !reflect.DeepEqual(seed.Demoted, tc.wantDemoted) {
				t.Errorf("Demoted = %v, want %v", seed.Demoted, tc.wantDemoted)
			}
			if seed.StartID != tc.wantStart {
				t.Errorf("StartID = %q, want %q", seed.StartID, tc.wantStart)
			}
			if seed.Fresh != tc.wantFresh {
				t.Errorf("Fresh = %v, want %v", seed.Fresh, tc.wantFresh)
			}
		})
	}
}

// TestRetrySeed_RecordsCarryPreviousRunAndDuration: a pre-seeded record keeps
// its previous duration/exit and is marked PreviousRun for the honest
// re-record at first save.
func TestRetrySeed_RecordsCarryPreviousRunAndDuration(t *testing.T) {
	blocks := []playbook.Block{shellBlock("one", "true"), shellBlock("two", "false")}
	run := Run{Outcome: OutcomeFailed, Blocks: map[string]BlockRecord{
		"one": {Outcome: OutcomeOK, Exit: 0, Duration: 1500 * time.Millisecond},
		"two": {Outcome: OutcomeFailed, Exit: 7},
	}}
	seed := RetrySeed(blocks, run)
	rec, ok := seed.PreSeeded["one"]
	if !ok {
		t.Fatal("one must be pre-seeded")
	}
	if !rec.PreviousRun || rec.Duration != 1500*time.Millisecond || rec.Outcome != OutcomeOK {
		t.Errorf("seeded record = %+v, want ok/1.5s/previous_run", rec)
	}
}

// TestJournalPreseed_LazyThenPersistedWithFirstRecord pins the
// seed-into-lazy-journal choice: Preseed writes NOTHING (the previous journal
// survives a view-then-quit retry session), and the first real Record
// persists the seeded records together with it — the journal file is complete
// from its first write.
func TestJournalPreseed_LazyThenPersistedWithFirstRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "j.json")
	j := Open(path, "/proj/pb.md", "hash")
	j.Preseed(map[string]BlockRecord{
		"one": {Outcome: OutcomeOK, Duration: 2 * time.Second, PreviousRun: true},
	})
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Preseed must not write (lazy contract); stat err = %v", err)
	}
	j.Finalize() // nothing recorded — still a no-op
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Finalize after only Preseed must not write; stat err = %v", err)
	}

	j.Record("two", BlockRecord{Outcome: OutcomeOK, Exit: 0, Duration: time.Second})
	run, err := Load(path)
	if err != nil {
		t.Fatalf("Load after first record: %v", err)
	}
	one := run.Blocks["one"]
	if one.Outcome != OutcomeOK || !one.PreviousRun || one.Duration != 2*time.Second {
		t.Errorf("seeded record = %+v, want ok/previous_run/2s persisted with the first real record", one)
	}
	if two := run.Blocks["two"]; two.PreviousRun {
		t.Errorf("fresh record must not carry previous_run: %+v", two)
	}

	// A manual re-run of the pre-seeded block re-records normally: the
	// overwrite clears previous_run.
	j.Record("one", BlockRecord{Outcome: OutcomeOK, Exit: 0, Duration: 3 * time.Second})
	run, err = Load(path)
	if err != nil {
		t.Fatalf("Load after re-record: %v", err)
	}
	one = run.Blocks["one"]
	if one.PreviousRun || one.Duration != 3*time.Second {
		t.Errorf("re-recorded block = %+v, want previous_run cleared + new duration", one)
	}
}

// TestJournalPreseed_NilSafe: a nil journal ignores Preseed like every other
// method.
func TestJournalPreseed_NilSafe(t *testing.T) {
	var j *Journal
	j.Preseed(map[string]BlockRecord{"a": {Outcome: OutcomeOK}}) // must not panic
}

// TestJournalRemove_LazyIsInMemoryOnly (review finding 3): removing a
// pre-seeded record while the journal is still lazy must write NOTHING (the
// prior journal survives) and must not dirty the journal (a later Finalize
// alone still stamps nothing); the first real Record then persists the
// post-undo truth — without the removed record.
func TestJournalRemove_LazyIsInMemoryOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "j.json")
	j := Open(path, "/proj/pb.md", "hash")
	j.Preseed(map[string]BlockRecord{
		"one": {Outcome: OutcomeOK, Duration: time.Second, PreviousRun: true},
		"two": {Outcome: OutcomeOK, Duration: time.Second, PreviousRun: true},
	})
	j.Remove("one")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Remove on a lazy journal must not write; stat err = %v", err)
	}
	j.Finalize()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Finalize after only Preseed+Remove must not write (dirty must stay unset); stat err = %v", err)
	}

	j.Record("three", BlockRecord{Outcome: OutcomeOK})
	run, err := Load(path)
	if err != nil {
		t.Fatalf("Load after first record: %v", err)
	}
	if _, ok := run.Blocks["one"]; ok {
		t.Error("the removed seed must not reappear in the persisted journal")
	}
	if _, ok := run.Blocks["two"]; !ok {
		t.Error("the surviving seed must persist with the first real record")
	}
}
