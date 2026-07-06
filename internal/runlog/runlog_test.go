package runlog

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// sampleRun is a fully-populated Run for round-trip/golden tests. Times are
// fixed UTC instants so the serialized form is stable.
func sampleRun() Run {
	return Run{
		PlaybookPath: "/proj/deploy.md",
		ContentHash:  "c0ffee",
		Started:      time.Date(2026, 7, 5, 10, 0, 0, 0, time.UTC),
		Finished:     time.Date(2026, 7, 5, 10, 0, 42, 0, time.UTC),
		Outcome:      OutcomeFailed,
		FirstFailure: "migrate",
		Blocks: map[string]BlockRecord{
			"build":   {Outcome: OutcomeOK, Exit: 0, Duration: 1500 * time.Millisecond},
			"migrate": {Outcome: OutcomeFailed, Exit: 1, Duration: 200 * time.Millisecond, TimedOutAfter: "10s"},
			"stage":   {Outcome: OutcomeRolledBack, Exit: 0, Duration: 2 * time.Second, PreviousRun: true},
		},
	}
}

func TestRunKey(t *testing.T) {
	sum := sha1.Sum([]byte("/abs/path/pb.md"))
	tests := []struct {
		name, slug, path, want string
	}{
		{"slug wins", "deploy-app", "/abs/path/pb.md", "deploy-app"},
		{"empty slug hashes the abs path", "", "/abs/path/pb.md", hex.EncodeToString(sum[:])},
	}
	for _, tc := range tests {
		if got := RunKey(tc.slug, tc.path); got != tc.want {
			t.Errorf("%s: RunKey(%q, %q) = %q, want %q", tc.name, tc.slug, tc.path, got, tc.want)
		}
	}
}

func TestPath_UsesSharedProjectKey(t *testing.T) {
	sum := sha1.Sum([]byte("/home/me/proj"))
	key := hex.EncodeToString(sum[:])
	want := filepath.Join("/data", "projects", key, "runs", "deploy.json")
	if got := Path("/data", "/home/me/proj", "deploy"); got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}

func TestContentHash(t *testing.T) {
	sum := sha256.Sum256([]byte("# playbook\n"))
	if got, want := ContentHash("# playbook\n"), hex.EncodeToString(sum[:]); got != want {
		t.Errorf("ContentHash = %q, want %q", got, want)
	}
}

func TestSaveLoad_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "runs", "deploy.json")
	want := sampleRun()
	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round-trip mismatch:\ngot  %+v\nwant %+v", got, want)
	}
}

// TestSave_GoldenShape pins the on-disk JSON: field names, readable duration
// strings, sorted block keys, omitted-when-empty optionals. R2/R3 consume this
// exact shape — a change here is a contract change.
func TestSave_GoldenShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deploy.json")
	if err := Save(path, sampleRun()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	want := `{
  "playbook_path": "/proj/deploy.md",
  "content_hash": "c0ffee",
  "started": "2026-07-05T10:00:00Z",
  "finished": "2026-07-05T10:00:42Z",
  "outcome": "failed",
  "first_failure": "migrate",
  "blocks": {
    "build": {
      "outcome": "ok",
      "exit": 0,
      "duration": "1.5s"
    },
    "migrate": {
      "outcome": "failed",
      "exit": 1,
      "duration": "200ms",
      "timed_out_after": "10s"
    },
    "stage": {
      "outcome": "rolled-back",
      "exit": 0,
      "duration": "2s",
      "previous_run": true
    }
  }
}
`
	if string(data) != want {
		t.Errorf("journal JSON shape drifted:\ngot:\n%s\nwant:\n%s", data, want)
	}
}

// TestSave_UnfinishedOmitsFinished verifies a mid-run journal omits the zero
// Finished/Outcome instead of serializing a year-1 timestamp.
func TestSave_UnfinishedOmitsFinished(t *testing.T) {
	path := filepath.Join(t.TempDir(), "j.json")
	r := sampleRun()
	r.Finished = time.Time{}
	r.Outcome = ""
	if err := Save(path, r); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, _ := os.ReadFile(path)
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatal(err)
	}
	if _, ok := top["finished"]; ok || strings.Contains(string(data), "0001-01-01") {
		t.Errorf("unfinished journal must omit finished:\n%s", data)
	}
	if _, ok := top["outcome"]; ok {
		t.Errorf("in-flight journal must omit outcome:\n%s", data)
	}
}

func TestLoad_Missing_WrapsErrNotExist(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Load(missing) error = %v, want wrapped fs.ErrNotExist", err)
	}
}

func TestLoad_Corrupt_ZeroValuePlusError(t *testing.T) {
	for name, content := range map[string]string{
		"garbage":      "{not json",
		"bad duration": `{"blocks":{"a":{"outcome":"ok","exit":0,"duration":"lots"}}}`,
	} {
		path := filepath.Join(t.TempDir(), "j.json")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := Load(path)
		if err == nil {
			t.Errorf("%s: Load(corrupt) must error", name)
		}
		if !reflect.DeepEqual(got, Run{}) {
			t.Errorf("%s: Load(corrupt) must return the zero Run, got %+v", name, got)
		}
	}
}

// TestSave_FailedWriteKeepsExistingIntact simulates the crash-safety property:
// when the temp-file write cannot complete (read-only dir), the previously
// saved journal must remain fully readable — Save never touches it in place.
func TestSave_FailedWriteKeepsExistingIntact(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "j.json")
	want := sampleRun()
	if err := Save(path, want); err != nil {
		t.Fatalf("initial Save: %v", err)
	}
	if err := os.Chmod(dir, 0o555); err != nil { // no new temp file can be created
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	changed := want
	changed.Outcome = OutcomeOK
	if err := Save(path, changed); err == nil {
		t.Fatal("Save into a read-only dir must error (cannot create the temp file)")
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("original journal must still load: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("original journal corrupted by failed Save:\ngot  %+v\nwant %+v", got, want)
	}
}

// TestSave_NoStrayTempFiles verifies a successful Save leaves only the journal
// (the temp file was renamed, not copied).
func TestSave_NoStrayTempFiles(t *testing.T) {
	dir := t.TempDir()
	if err := Save(filepath.Join(dir, "j.json"), sampleRun()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "j.json" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("dir after Save = %v, want exactly [j.json]", names)
	}
}

func TestOpen_EmptyPathIsOff(t *testing.T) {
	if j := Open("", "/pb.md", "hash"); j != nil {
		t.Errorf("Open(\"\") = %v, want nil (journaling off)", j)
	}
	// Every method must be a nil-receiver no-op.
	var j *Journal
	j.Record("a", BlockRecord{Outcome: OutcomeOK})
	j.MarkRolledBack("a")
	j.Remove("a")
	j.Finalize()
}

// TestJournal_IncrementalLifecycle drives the shared writer through a full
// run under the LAZY contract: Open writes NOTHING (the previous run's
// journal stays byte-identical until a block actually records), the FIRST
// Record persists the skeleton + record together, every later mutation lands
// on disk, rollback re-records preserve history, and Finalize stamps
// outcome + finished.
func TestJournal_IncrementalLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs", "deploy.json")
	// The previous run's (failed) journal must SURVIVE Open untouched.
	if err := Save(path, sampleRun()); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	j := Open(path, "/proj/deploy.md", "beef")
	if j == nil {
		t.Fatal("Open returned nil for a non-empty path")
	}
	t0 := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	j.now = func() time.Time { return t0 }

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the previous journal must still exist after Open: %v", err)
	}
	if string(after) != string(before) {
		t.Errorf("Open must not write — previous journal changed:\n%s", after)
	}

	// Block 1 ok — the FIRST record creates this run's journal (skeleton +
	// record together): identity flips to this run's and the stale blocks go.
	j.Record("build", BlockRecord{Outcome: OutcomeOK, Exit: 0, Duration: time.Second})
	r, err := Load(path)
	if err != nil {
		t.Fatalf("first Record must create the journal: %v", err)
	}
	if r.ContentHash != "beef" || r.Started.IsZero() || len(r.Blocks) != 1 {
		t.Errorf("first Record must persist this run's fresh skeleton + record, got %+v", r)
	}
	if r.Blocks["build"].Outcome != OutcomeOK || r.FirstFailure != "" {
		t.Errorf("after ok record: %+v", r)
	}

	// Block 2 fails → FirstFailure.
	j.Record("migrate", BlockRecord{Outcome: OutcomeFailed, Exit: 1, Duration: time.Second})
	r, _ = Load(path)
	if r.FirstFailure != "migrate" {
		t.Errorf("FirstFailure = %q, want migrate", r.FirstFailure)
	}

	// Another failure does NOT displace the first.
	j.Record("verify", BlockRecord{Outcome: OutcomeStopped, Exit: 130, Duration: time.Second})
	r, _ = Load(path)
	if r.FirstFailure != "migrate" {
		t.Errorf("FirstFailure displaced to %q", r.FirstFailure)
	}

	// The failed block re-run ok clears FirstFailure (verify's stop then owns
	// the next failure slot on a subsequent failure).
	j.Record("migrate", BlockRecord{Outcome: OutcomeOK, Exit: 0, Duration: time.Second})
	r, _ = Load(path)
	if r.FirstFailure != "" {
		t.Errorf("FirstFailure = %q after the failed block re-ran ok, want empty", r.FirstFailure)
	}

	// Rollback re-record: outcome flips, exit/duration history preserved.
	j.MarkRolledBack("build")
	r, _ = Load(path)
	if got := r.Blocks["build"]; got.Outcome != OutcomeRolledBack || got.Duration != time.Second {
		t.Errorf("rolled-back re-record = %+v, want outcome rolled-back with duration kept", got)
	}

	// Remove drops the record entirely.
	j.Remove("verify")
	r, _ = Load(path)
	if _, ok := r.Blocks["verify"]; ok {
		t.Error("Remove must drop the block record")
	}

	// Finalize: stopped beats ok, failed beats stopped; here no failed/stopped
	// remain → ok? verify(stopped) was removed, migrate ok, build rolled-back
	// (neutral) → outcome ok.
	j.Finalize()
	r, _ = Load(path)
	if r.Outcome != OutcomeOK || !r.Finished.Equal(t0) {
		t.Errorf("Finalize: outcome=%q finished=%v, want ok @ %v", r.Outcome, r.Finished, t0)
	}
}

// TestJournal_FinalizeOutcomePrecedence table-tests the run-level outcome
// derivation: any failed → failed; else any stopped → stopped; else ok.
func TestJournal_FinalizeOutcomePrecedence(t *testing.T) {
	tests := []struct {
		name string
		recs map[string]string // id → outcome
		want string
	}{
		{"all ok", map[string]string{"a": OutcomeOK, "b": OutcomeOK}, OutcomeOK},
		{"failed wins", map[string]string{"a": OutcomeOK, "b": OutcomeFailed, "c": OutcomeStopped}, OutcomeFailed},
		{"stopped beats ok", map[string]string{"a": OutcomeOK, "b": OutcomeStopped}, OutcomeStopped},
		{"rolled-back is neutral", map[string]string{"a": OutcomeRolledBack, "b": OutcomeOK}, OutcomeOK},
	}
	for _, tc := range tests {
		path := filepath.Join(t.TempDir(), "j.json")
		j := Open(path, "/pb.md", "h")
		for id, out := range tc.recs {
			j.Record(id, BlockRecord{Outcome: out})
		}
		j.Finalize()
		r, err := Load(path)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if r.Outcome != tc.want {
			t.Errorf("%s: outcome = %q, want %q", tc.name, r.Outcome, tc.want)
		}
	}
}

// TestJournal_NothingRecordedWritesNothing pins the lazy contract end to end
// (the view-then-quit clobber regression, review finding 1): Open + Finalize
// with NO block records must leave a previous FAILED journal byte-identical —
// a viewer session that never ran a block (or a render-only degraded viewer,
// or a zero-block run) can never rewrite history to an empty "ok". With no
// prior journal, no file may appear either.
func TestJournal_NothingRecordedWritesNothing(t *testing.T) {
	// Prior failed journal survives an open+finalize untouched.
	path := filepath.Join(t.TempDir(), "j.json")
	if err := Save(path, sampleRun()); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	j := Open(path, "/proj/deploy.md", "beef")
	j.Finalize()
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("previous journal must survive: %v", err)
	}
	if string(after) != string(before) {
		t.Errorf("open+quit rewrote the previous journal:\ngot:\n%s\nwant it untouched", after)
	}

	// No prior journal → still nothing on disk.
	empty := filepath.Join(t.TempDir(), "j.json")
	j2 := Open(empty, "/pb.md", "h")
	j2.Finalize()
	if _, err := os.Stat(empty); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("no-record run must not create a journal file (stat err = %v)", err)
	}
}

// TestJournal_SaveFailureIsAdvisory: a journal rooted somewhere unwritable
// must not panic or fail the caller — Open still returns a working (silent)
// journal and every method stays callable.
func TestJournal_SaveFailureIsAdvisory(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// runs dir cannot be created below a regular file.
	j := Open(filepath.Join(blocker, "runs", "j.json"), "/pb.md", "h")
	if j == nil {
		t.Fatal("Open must return a journal even when the first save fails (advisory)")
	}
	j.Record("a", BlockRecord{Outcome: OutcomeFailed, Exit: 1})
	j.MarkRolledBack("a")
	j.Finalize()
}
