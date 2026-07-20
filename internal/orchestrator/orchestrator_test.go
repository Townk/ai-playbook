package orchestrator

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Townk/ai-playbook/internal/mux"
	"github.com/Townk/ai-playbook/pkg/driver"
)

// newTestDriver spawns a controlled-rc zsh (a minimal .zshrc — no p10k/mise), the
// same fixture approach as driver_test.go, so the orchestrator tests drive a real
// shell deterministically.
func newTestDriver(t *testing.T) *driver.Driver {
	t.Helper()
	zdot := t.TempDir()
	rc := "tfn() { print -r -- FN_OK }\n"
	if err := os.WriteFile(filepath.Join(zdot, ".zshrc"), []byte(rc), 0644); err != nil {
		t.Fatal(err)
	}
	// Pin zsh: this fixture is zsh-specific (ZDOTDIR rc, `print`). The default now
	// honors $SHELL (bash on CI), so don't rely on the ambient default.
	d, err := driver.Open(driver.Options{Shell: "zsh", Env: append(os.Environ(), "ZDOTDIR="+zdot)})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// recMux records the payloads handed to Copy/Play.
type recMux struct {
	copied []string
	played []string
}

func (m *recMux) Copy(text string) error { m.copied = append(m.copied, text); return nil }
func (m *recMux) Play(cmd string) error  { m.played = append(m.played, cmd); return nil }

// The load-bearing one: value-passing persists across separate Do(run) calls.
func TestValuePassingAcrossBlocks(t *testing.T) {
	o := New(newTestDriver(t), &recMux{})

	if r, err := o.Do(Action{Kind: KindRun, ID: "a", Payload: "print -r -- HELLO"}); err != nil || r.Out != "HELLO" || r.Exit != 0 {
		t.Fatalf("block a → %+v err=%v", r, err)
	}
	// A later block (no id) reads block a's exported output.
	r, err := o.Do(Action{Kind: KindRun, Payload: "print -r -- got:$APB_OUT_a"})
	if err != nil {
		t.Fatalf("block b err=%v", err)
	}
	if r.Out != "got:HELLO" {
		t.Errorf("APB_OUT_a did not propagate → %q (want got:HELLO)", r.Out)
	}
	// LAST_* and APB_EXIT_<id> propagate too.
	if r, err := o.Do(Action{Kind: KindRun, Payload: "print -r -- last:$LAST_EXCODE exit:$APB_EXIT_a"}); err != nil || r.Out != "last:0 exit:0" {
		t.Errorf("LAST_EXCODE/APB_EXIT_a → %q err=%v", r.Out, err)
	}
}

// sanitized key: a non-[A-Za-z0-9_] char in the id maps to _.
func TestValuePassingKeySanitized(t *testing.T) {
	o := New(newTestDriver(t), &recMux{})
	if _, err := o.Do(Action{Kind: KindRun, ID: "step-1", Payload: "print -r -- X"}); err != nil {
		t.Fatal(err)
	}
	if r, _ := o.Do(Action{Kind: KindRun, Payload: "print -r -- $APB_OUT_step_1"}); r.Out != "X" {
		t.Errorf("sanitized key APB_OUT_step_1 → %q (want X)", r.Out)
	}
}

// stop: a long run is interrupted by a concurrent Do(stop) and returns promptly.
func TestStopInterruptsRun(t *testing.T) {
	o := New(newTestDriver(t), &recMux{})
	done := make(chan driver.Result, 1)
	go func() {
		r, _ := o.Do(Action{Kind: KindRun, Payload: "sleep 30"})
		done <- r
	}()
	// Wait until the command is actually running (a foreground pgrp appears).
	for i := 0; i < 150; i++ {
		time.Sleep(40 * time.Millisecond)
		if o.Drv.Pgrp() > 0 {
			break
		}
	}
	if _, err := o.Do(Action{Kind: KindStop}); err != nil {
		t.Fatalf("stop err=%v", err)
	}
	select {
	case r := <-done:
		if r.Exit == 0 && !r.TimedOut {
			t.Errorf("stopped run should not report clean success → %+v", r)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("stop did not end the run")
	}
}

func TestCopyPlayRecorded(t *testing.T) {
	m := &recMux{}
	o := New(newTestDriver(t), m)
	if _, err := o.Do(Action{Kind: KindCopy, Payload: "to-clip"}); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Do(Action{Kind: KindPlay, Payload: "to-pane"}); err != nil {
		t.Fatal(err)
	}
	if len(m.copied) != 1 || m.copied[0] != "to-clip" {
		t.Errorf("copy not recorded → %v", m.copied)
	}
	if len(m.played) != 1 || m.played[0] != "to-pane" {
		t.Errorf("play not recorded → %v", m.played)
	}
}

// The re-engagement kinds (regenerate / followup) still name UI buttons, but they
// never route through Do — reaching it is a wiring bug, surfaced as the distinct
// ErrMisrouted (NOT ErrNotImplemented) so it can't masquerade as "not available".
func TestReengageKindsAreMisrouted(t *testing.T) {
	o := New(newTestDriver(t), &recMux{})
	for _, k := range []Kind{KindRegenerate, KindFollowup} {
		if _, err := o.Do(Action{Kind: k}); !errors.Is(err, ErrMisrouted) {
			t.Errorf("%s → err=%v (want ErrMisrouted)", k, err)
		}
	}
}

// newTestDriverIn spawns a controlled-rc zsh whose session cwd is dir, so a
// git-apply run executes inside the temp repo.
func newTestDriverIn(t *testing.T, dir string) *driver.Driver {
	t.Helper()
	zdot := t.TempDir()
	if err := os.WriteFile(filepath.Join(zdot, ".zshrc"), []byte("\n"), 0644); err != nil {
		t.Fatal(err)
	}
	d, err := driver.Open(driver.Options{
		Shell: "zsh", // zsh-specific fixture; default now honors $SHELL (bash on CI)
		Env:   append(os.Environ(), "ZDOTDIR="+zdot),
		Cwd:   dir,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// sh runs a shell command in dir, failing the test on a non-zero exit.
func sh(t *testing.T, dir, command string) {
	t.Helper()
	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sh %q: %v\n%s", command, err, out)
	}
}

// recFloat records SpawnFloat calls (the view-diff float mux double).
type recFloat struct {
	spawned []mux.SpawnOptions
	err     error
}

func (f *recFloat) DumpScreen(string) (string, error) { return "", nil }
func (f *recFloat) SpawnFloat(o mux.SpawnOptions) error {
	f.spawned = append(f.spawned, o)
	return f.err
}
func (f *recFloat) SpawnInputFloat(mux.SpawnOptions) error { return nil }
func (f *recFloat) SpawnPane(mux.SpawnOptions) error       { return nil }
func (f *recFloat) SpawnDocked(mux.SpawnOptions) error     { return nil }
func (f *recFloat) TypeInto(string, string) error          { return nil }

// fakeFloat is a lightweight float fake with a pluggable SpawnFloat callback,
// used to assert the exact Cmd passed by viewDiff.
type fakeFloat struct {
	spawn func(mux.SpawnOptions) error
}

func (f *fakeFloat) DumpScreen(string) (string, error)      { return "", nil }
func (f *fakeFloat) SpawnFloat(o mux.SpawnOptions) error    { return f.spawn(o) }
func (f *fakeFloat) SpawnInputFloat(mux.SpawnOptions) error { return nil }
func (f *fakeFloat) SpawnPane(mux.SpawnOptions) error       { return nil }
func (f *fakeFloat) SpawnDocked(mux.SpawnOptions) error     { return nil }
func (f *fakeFloat) TypeInto(string, string) error          { return nil }

// fakeDockMux records SpawnDocked calls (the EditSource test double).
type fakeDockMux struct {
	dock func(mux.SpawnOptions) error
}

func (f *fakeDockMux) DumpScreen(string) (string, error)      { return "", nil }
func (f *fakeDockMux) SpawnFloat(mux.SpawnOptions) error      { return nil }
func (f *fakeDockMux) SpawnInputFloat(mux.SpawnOptions) error { return nil }
func (f *fakeDockMux) SpawnPane(mux.SpawnOptions) error       { return nil }
func (f *fakeDockMux) SpawnDocked(o mux.SpawnOptions) error   { return f.dock(o) }
func (f *fakeDockMux) TypeInto(string, string) error          { return nil }

// TestEditSource_SpawnsDocked asserts that EditSource spawns `editor … path`
// via SpawnDocked (tiled, not floating).
func TestEditSource_SpawnsDocked(t *testing.T) {
	var got mux.SpawnOptions
	o := &Orchestrator{Float: &fakeDockMux{dock: func(opts mux.SpawnOptions) error { got = opts; return nil }}}
	_ = o.EditSource("nano", "/store/x.md")
	if len(got.Cmd) < 2 || got.Cmd[0] != "nano" || got.Cmd[len(got.Cmd)-1] != "/store/x.md" {
		t.Fatalf("EditSource must spawn `nano … /store/x.md` docked, got %v", got.Cmd)
	}
	if got.Floating {
		t.Error("EditSource must spawn docked (Floating=false), got Floating=true")
	}
	if got.Name != "edit" {
		t.Errorf("EditSource pane Name = %q, want \"edit\"", got.Name)
	}
}

// TestViewDiff_SpawnsSelfDiffSubcommand asserts that viewDiff spawns
// `<self> diff <patch>` rather than the old external chain (hunk/delta/less).
func TestViewDiff_SpawnsSelfDiffSubcommand(t *testing.T) {
	var got mux.SpawnOptions
	o := &Orchestrator{Float: &fakeFloat{spawn: func(opts mux.SpawnOptions) error { got = opts; return nil }}}
	_ = o.viewDiff("fix", "--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n")
	if len(got.Cmd) < 2 || got.Cmd[1] != "diff" {
		t.Fatalf("viewDiff must spawn `<self> diff <patch>`, got %v", got.Cmd)
	}
}

// apply-diff changes the file (Exit 0), undo-diff reverts it; the driver runs git
// apply inside the temp repo (Cwd).
func TestApplyUndoDiff(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	sh(t, repo, "git init -q && git config user.email t@t && git config user.name t")
	target := filepath.Join(repo, "hello.txt")
	if err := os.WriteFile(target, []byte("one\n"), 0644); err != nil {
		t.Fatal(err)
	}
	sh(t, repo, "git add hello.txt && git commit -q -m init")

	// A real unified diff that changes "one" → "two".
	patch := "" +
		"diff --git a/hello.txt b/hello.txt\n" +
		"--- a/hello.txt\n" +
		"+++ b/hello.txt\n" +
		"@@ -1 +1 @@\n" +
		"-one\n" +
		"+two\n"

	o := New(newTestDriverIn(t, repo), &recMux{})

	r, err := o.Do(Action{Kind: KindApplyDiff, ID: "fix", Payload: patch})
	if err != nil {
		t.Fatalf("apply err=%v", err)
	}
	if r.Exit != 0 {
		t.Fatalf("apply Exit=%d (want 0) stderr=%q", r.Exit, r.Err)
	}
	if b, _ := os.ReadFile(target); string(b) != "two\n" {
		t.Fatalf("apply did not change file → %q", b)
	}

	r, err = o.Do(Action{Kind: KindUndoDiff, ID: "fix", Payload: patch})
	if err != nil {
		t.Fatalf("undo err=%v", err)
	}
	if r.Exit != 0 {
		t.Fatalf("undo Exit=%d (want 0) stderr=%q", r.Exit, r.Err)
	}
	if b, _ := os.ReadFile(target); string(b) != "one\n" {
		t.Fatalf("undo did not revert file → %q", b)
	}
}

// A malformed patch → non-zero Exit (failure feedback), no error.
func TestApplyDiffMalformed(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	sh(t, repo, "git init -q")
	o := New(newTestDriverIn(t, repo), &recMux{})
	r, err := o.Do(Action{Kind: KindApplyDiff, ID: "x", Payload: "this is not a patch\n"})
	if err != nil {
		t.Fatalf("malformed apply returned error (want non-zero Exit): %v", err)
	}
	if r.Exit == 0 {
		t.Fatalf("malformed patch Exit=0 (want non-zero); stderr=%q", r.Err)
	}
}

// view-diff opens a float with the patch + a viewer command, anchored to the
// session cwd. With no Float mux it is a graceful no-op.
func TestViewDiff(t *testing.T) {
	repo := t.TempDir()
	d := newTestDriverIn(t, repo)

	// No Float wired → no-op success.
	o := New(d, &recMux{})
	if _, err := o.Do(Action{Kind: KindViewDiff, ID: "fix", Payload: "diff --git a/f b/f\n"}); err != nil {
		t.Fatalf("view-diff with nil Float should be no-op, got %v", err)
	}

	// With a Float mux → SpawnFloat called with a diff:<id> name, repo cwd, and a
	// viewer command whose last arg is the temp patch file.
	rf := &recFloat{}
	o = New(d, &recMux{}).WithFloat(rf)
	if _, err := o.Do(Action{Kind: KindViewDiff, ID: "fix", Payload: "diff --git a/f b/f\n"}); err != nil {
		t.Fatalf("view-diff err=%v", err)
	}
	if len(rf.spawned) != 1 {
		t.Fatalf("SpawnFloat calls = %d, want 1", len(rf.spawned))
	}
	opt := rf.spawned[0]
	if opt.Name != "diff:fix" {
		t.Errorf("float name = %q, want diff:fix", opt.Name)
	}
	if opt.Cwd != repo {
		t.Errorf("float cwd = %q, want %q", opt.Cwd, repo)
	}
	if len(opt.Cmd) == 0 {
		t.Fatal("float Cmd empty")
	}
	last := opt.Cmd[len(opt.Cmd)-1]
	if !strings.HasSuffix(last, ".patch") {
		t.Errorf("viewer's last arg = %q, want a .patch temp file", last)
	}
}

func TestKindString(t *testing.T) {
	cases := map[Kind]string{
		KindCopy: "copy", KindPlay: "play", KindRun: "run", KindStop: "stop",
		KindViewDiff: "view-diff", KindApplyDiff: "apply-diff", KindUndoDiff: "undo-diff",
		KindRegenerate: "regenerate", KindFollowup: "followup",
	}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("Kind(%d).String() = %q, want %q", k, got, want)
		}
	}
}

// newTestOrchInDir builds an Orchestrator whose driver cwd is dir, mirroring
// the pattern used by the apply/undo tests.
func newTestOrchInDir(t *testing.T, dir string) *Orchestrator {
	t.Helper()
	return &Orchestrator{Drv: newTestDriverIn(t, dir)}
}

// TestCreateFile_WritesAndUndoDeletes: create writes a new file; undo deletes it.
func TestCreateFile_WritesAndUndoDeletes(t *testing.T) {
	dir := t.TempDir()
	o := newTestOrchInDir(t, dir)
	payload := EncodeFileAction("sub/new.txt", "hello\n")
	if res, _ := o.Do(Action{Kind: KindCreateFile, Payload: payload}); res.Exit != 0 {
		t.Fatalf("create: %+v", res)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "sub/new.txt")); string(got) != "hello\n" {
		t.Fatalf("file not written: %q", got)
	}
	if res, _ := o.Do(Action{Kind: KindUndoCreate, Payload: payload}); res.Exit != 0 {
		t.Fatalf("undo: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "sub/new.txt")); !os.IsNotExist(err) {
		t.Fatal("undo of a new file must delete it")
	}
}

// TestCreateFile_OverwriteUndoRestores: create overwrites an existing file;
// undo restores the original content.
func TestCreateFile_OverwriteUndoRestores(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.txt"), []byte("ORIG\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := newTestOrchInDir(t, dir)
	payload := EncodeFileAction("x.txt", "NEW\n")
	if _, err := o.Do(Action{Kind: KindCreateFile, Payload: payload}); err != nil {
		t.Fatalf("create err: %v", err)
	}
	if _, err := o.Do(Action{Kind: KindUndoCreate, Payload: payload}); err != nil {
		t.Fatalf("undo err: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "x.txt")); string(got) != "ORIG\n" {
		t.Fatalf("undo of an overwrite must restore the backup, got %q", got)
	}
}

// TestCreateFile_TrailingNewline locks the contract that createFile always ensures
// a trailing newline on the written file, even when the payload body has none (as
// the UI produces after render-trimming). Without this, a .go file created via a
// file= block would fail gofmt and violate POSIX.
func TestCreateFile_TrailingNewline(t *testing.T) {
	dir := t.TempDir()
	o := newTestOrchInDir(t, dir)

	// Simulate the UI path: body is render-trimmed, no trailing newline.
	payload := EncodeFileAction("x.go", "package main")
	if res, _ := o.Do(Action{Kind: KindCreateFile, Payload: payload}); res.Exit != 0 {
		t.Fatalf("create: %+v", res)
	}
	got, err := os.ReadFile(filepath.Join(dir, "x.go"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "package main\n" {
		t.Fatalf("createFile must add a trailing newline to a trimmed body, got %q", string(got))
	}
}

// TestCreateBackupsConcurrent exercises the createBackups map from concurrent
// Do goroutines — exactly what two quick create/undo button clicks trigger via
// the UI's tea.Cmd goroutines. Without a lock this is a concurrent map
// read/write (a data race under -race, and a runtime panic in practice). Uses
// absolute paths so no live driver is needed (the race is on the map, not the
// shell). RED today; GREEN once createBackups is mutex-guarded.
func TestCreateBackupsConcurrent(t *testing.T) {
	dir := t.TempDir()
	o := &Orchestrator{}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		payload := EncodeFileAction(filepath.Join(dir, fmt.Sprintf("f%d.txt", i)), "x")
		wg.Add(2)
		go func() { defer wg.Done(); _, _ = o.Do(Action{Kind: KindCreateFile, Payload: payload}) }()
		go func() { defer wg.Done(); _, _ = o.Do(Action{Kind: KindUndoCreate, Payload: payload}) }()
	}
	wg.Wait()
}

// TestCheckDrift_CleanAppliedDrifted verifies the three DriftVerdict states:
// DriftClean (patch not yet applied), DriftApplied (already applied), and
// DriftDrifted (target changed incompatibly).
func TestCheckDrift_CleanAppliedDrifted(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	o := newTestOrchInDir(t, dir)
	sh(t, dir, "git init -q && git config user.email t@t && git config user.name t")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sh(t, dir, "git add f.txt && git commit -qm init")

	patch := "--- a/f.txt\n+++ b/f.txt\n@@ -1,3 +1,3 @@\n one\n-two\n+TWO\n three\n"

	// Fresh repo: patch should apply forward → DriftClean.
	if v, err := o.CheckDrift(patch); v != DriftClean {
		t.Fatalf("fresh patch should be DriftClean, got %v (err=%v)", v, err)
	}

	// Apply the patch so the file is at the post-patch state.
	if r, err := o.Do(Action{Kind: KindApplyDiff, Payload: patch}); err != nil || r.Exit != 0 {
		t.Fatalf("o.Do(ApplyDiff) failed: exit=%d err=%v stderr=%q", r.Exit, err, r.Err)
	}

	// Patch already applied → reverse should succeed → DriftApplied.
	if v, err := o.CheckDrift(patch); v != DriftApplied {
		t.Fatalf("applied patch should be DriftApplied, got %v (err=%v)", v, err)
	}

	// Overwrite the file with incompatible content → neither direction applies → DriftDrifted.
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("totally\ndifferent\ncontent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if v, err := o.CheckDrift(patch); v != DriftDrifted {
		t.Fatalf("changed target should be DriftDrifted, got %v (err=%v)", v, err)
	}
}

// TestCheckDrift_NoRunContention asserts CheckDrift does NOT serialize behind an
// in-flight Run: git apply --check runs directly (exec.Command), never touching the
// driver's runMu. Pre-change CheckDrift went through the session shell and blocked
// on runMu for the whole run; here a long block holds runMu while CheckDrift must
// still complete promptly. The bound is generous (well under the run's sleep) to
// avoid flake.
func TestCheckDrift_NoRunContention(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	o := newTestOrchInDir(t, dir)
	sh(t, dir, "git init -q && git config user.email t@t && git config user.name t")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sh(t, dir, "git add f.txt && git commit -qm init")
	patch := "--- a/f.txt\n+++ b/f.txt\n@@ -1,3 +1,3 @@\n one\n-two\n+TWO\n three\n"

	// Launch a long block that holds runMu for the whole sleep.
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = o.Do(Action{Kind: KindRun, Payload: "sleep 5"})
	}()
	// Give the block time to be dispatched and acquire runMu.
	time.Sleep(500 * time.Millisecond)

	// CheckDrift must complete WITHOUT waiting for the 5s block to finish.
	start := time.Now()
	v, err := o.CheckDrift(patch)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("CheckDrift err=%v", err)
	}
	if v != DriftClean {
		t.Fatalf("CheckDrift = %v; want DriftClean", v)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("CheckDrift took %v while a Run was in flight — it is contending on runMu", elapsed)
	}

	// Stop the block and let the goroutine unwind before the driver is closed.
	o.Drv.Stop()
	<-done
}

// EffectiveTimeout is the single "which ceiling applies" rule: a positive
// declared timeout wins; zero/negative falls back to the 10-minute default.
func TestEffectiveTimeout(t *testing.T) {
	if got := EffectiveTimeout(90 * time.Second); got != 90*time.Second {
		t.Errorf("declared 90s → %v, want 90s", got)
	}
	if got := EffectiveTimeout(0); got != 10*time.Minute {
		t.Errorf("undeclared → %v, want the 10m default", got)
	}
	if got := EffectiveTimeout(-time.Second); got != 10*time.Minute {
		t.Errorf("negative → %v, want the 10m default", got)
	}
}

// Do(KindRun) hands the driver the DECLARED timeout when the action carries a
// positive one, and the package default otherwise — captured via the runIDFn
// seam so no live shell is needed.
func TestDoRun_TimeoutDeclaredVsDefault(t *testing.T) {
	var got []time.Duration
	orig := runIDFn
	runIDFn = func(_ *driver.Driver, _, _, _ string, timeout time.Duration) driver.Result {
		got = append(got, timeout)
		return driver.Result{Exit: 0}
	}
	defer func() { runIDFn = orig }()

	o := New(nil, &recMux{})
	if _, err := o.Do(Action{Kind: KindRun, ID: "a", Payload: "x", Timeout: 15 * time.Minute}); err != nil {
		t.Fatalf("declared: %v", err)
	}
	if _, err := o.Do(Action{Kind: KindRun, ID: "b", Payload: "y"}); err != nil {
		t.Fatalf("undeclared: %v", err)
	}
	want := []time.Duration{15 * time.Minute, 10 * time.Minute}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("driver received timeouts %v, want %v", got, want)
	}
}

// FormatTimeout trims time.Duration.String's zero-valued trailing units so the
// "timed out after <d>" message reads like the timeout= an author would
// declare (Go-normalized forms like 90s → 1m30s are kept as-is).
func TestFormatTimeout(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{10 * time.Minute, "10m"},
		{time.Hour, "1h"},
		{90 * time.Second, "1m30s"},
		{15 * time.Minute, "15m"},
		{time.Second, "1s"},
	}
	for _, c := range cases {
		if got := FormatTimeout(c.d); got != c.want {
			t.Errorf("FormatTimeout(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

// TestParseKind covers the string→Kind mapping and its unknown fallback.
func TestParseKind(t *testing.T) {
	cases := map[string]Kind{
		"copy": KindCopy, "play": KindPlay, "run": KindRun, "stop": KindStop,
		"diff": KindViewDiff, "view-diff": KindViewDiff, "apply-diff": KindApplyDiff,
		"undo-diff": KindUndoDiff,
		"create":    KindCreateFile, "undo-create": KindUndoCreate,
		"regenerate": KindRegenerate, "followup": KindFollowup,
	}
	for s, want := range cases {
		got, ok := ParseKind(s)
		if !ok || got != want {
			t.Errorf("ParseKind(%q) = (%v,%v), want (%v,true)", s, got, ok, want)
		}
	}
	if _, ok := ParseKind("definitely-not-a-kind"); ok {
		t.Error("unknown kind must not parse")
	}
}

// TestDriftTargetPath covers the patch-target resolution: b/-prefixed relative
// paths join the project root, absolute paths pass through, and an unparseable
// patch errors.
func TestDriftTargetPath(t *testing.T) {
	dir := t.TempDir()
	o := newTestOrchInDir(t, dir)
	patch := "--- a/conf/app.toml\n+++ b/conf/app.toml\n@@ -1 +1 @@\n-a\n+b\n"
	got, err := o.DriftTargetPath(patch)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "conf/app.toml"); got != want {
		t.Errorf("relative target = %q, want %q", got, want)
	}
	abs := "--- /etc/app.toml\n+++ /etc/app.toml\n@@ -1 +1 @@\n-a\n+b\n"
	if got, err := o.DriftTargetPath(abs); err != nil || got != "/etc/app.toml" {
		t.Errorf("absolute target = (%q,%v), want /etc/app.toml", got, err)
	}
	if _, err := o.DriftTargetPath("not a patch"); err == nil {
		t.Error("an unparseable patch must error")
	}
}
