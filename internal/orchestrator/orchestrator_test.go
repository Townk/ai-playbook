package orchestrator

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Townk/ai-playbook/internal/capture"
	"github.com/Townk/ai-playbook/internal/driver"
	"github.com/Townk/ai-playbook/internal/frontmatter"
	"github.com/Townk/ai-playbook/internal/mux"
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

func TestDeferredKindsNotImplemented(t *testing.T) {
	o := New(newTestDriver(t), &recMux{})
	// apply-diff / undo-diff / view-diff are implemented as of stage 4c-i; only
	// regenerate / followup remain deferred.
	for _, k := range []Kind{KindRegenerate, KindFollowup} {
		if _, err := o.Do(Action{Kind: k}); !errors.Is(err, ErrNotImplemented) {
			t.Errorf("%s → err=%v (want ErrNotImplemented)", k, err)
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

// TestBuildFrontMatter_NoWorkdir verifies buildFrontMatter no longer writes the
// workdir field (Phase B2a: portability is via PROJECT_ROOT, not a stored workdir).
func TestBuildFrontMatter_NoWorkdir(t *testing.T) {
	home, _ := os.UserHomeDir()
	projRoot := filepath.Join(home, "Projects", "myapp")
	re := &Reengage{
		Req: capture.Request{
			ProjectRoot: projRoot,
			UserRequest: "fix the build",
		},
		EnvLookup: func(string) (string, bool) { return "", false },
		Metadata:  nil,
	}
	body := "# Playbook — Fix Build\n\nDo the thing.\n"
	fm := re.buildFrontMatter(body)
	if fm.Workdir != "" {
		t.Errorf("Workdir = %q, want \"\" (workdir is no longer written)", fm.Workdir)
	}
	// the assembled FM must NOT carry a workdir: key (omitempty drops it)
	assembled := frontmatter.Assemble(fm)
	if strings.Contains(assembled, "workdir:") {
		t.Errorf("assembled FM must not carry a workdir: key:\n%s", assembled)
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

// TestCommitPlaybook_HonorsStoreDir asserts that CommitPlaybook writes the .md
// file under Reengage.StoreDir when it is set, NOT under dataRoot/playbooks.
func TestCommitPlaybook_HonorsStoreDir(t *testing.T) {
	storeDir := t.TempDir()
	dataRoot := t.TempDir()

	re := &Reengage{
		StoreDir:  storeDir,
		DataRoot:  dataRoot,
		Req:       capture.Request{},
		EnvLookup: func(string) (string, bool) { return "", false },
	}
	o := New(nil, &recMux{}).WithReengage(re)

	body := "# Playbook — StoreDir Test\n\nVerify the injected store dir is used.\n"
	path, err := o.CommitPlaybook(body)
	if err != nil {
		t.Fatalf("CommitPlaybook: %v", err)
	}

	// File must land under storeDir (the injected value), not under dataRoot/playbooks.
	if !strings.HasPrefix(path, storeDir) {
		t.Errorf("CommitPlaybook path = %q, want prefix %q", path, storeDir)
	}
	badDir := filepath.Join(dataRoot, "playbooks")
	if strings.HasPrefix(path, badDir) {
		t.Errorf("CommitPlaybook used dataRoot fallback: path = %q", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("returned path does not exist: %v", err)
	}
}

func TestBuildFrontMatter_ProjectBound(t *testing.T) {
	re := &Reengage{
		Req:      capture.Request{},
		Metadata: func(string) (PlaybookMeta, error) { return PlaybookMeta{Description: "d", ProjectBound: true}, nil },
	}
	fm := re.buildFrontMatter("# Playbook — T\n\n```bash {id=fix}\nx\n```\n")
	if !fm.ProjectBound {
		t.Fatalf("buildFrontMatter must copy ProjectBound from the seam meta")
	}
	if fm.Description != "d" {
		t.Fatalf("description = %q, want d", fm.Description)
	}
}

// TestBuildFrontMatter_DeclaresProjectRoot asserts that buildFrontMatter injects
// PROJECT_ROOT into the env map when the metadata seam returns ProjectBound: true.
func TestBuildFrontMatter_DeclaresProjectRoot(t *testing.T) {
	re := &Reengage{
		Req: capture.Request{},
		Metadata: func(string) (PlaybookMeta, error) {
			return PlaybookMeta{ProjectBound: true}, nil
		},
	}
	fm := re.buildFrontMatter("# Playbook — T\n\n```bash {id=fix}\ncd $PROJECT_ROOT\n```\n")
	if _, ok := fm.Env["PROJECT_ROOT"]; !ok {
		t.Fatalf("project_bound front matter must declare PROJECT_ROOT, got env=%v", fm.Env)
	}
}

// TestCommitPlaybook_NoStoreDir_FallsBackToDataRoot asserts the back-compat
// path: when StoreDir is empty, CommitPlaybook writes under dataRoot/playbooks.
func TestCommitPlaybook_NoStoreDir_FallsBackToDataRoot(t *testing.T) {
	dataRoot := t.TempDir()

	re := &Reengage{
		// StoreDir deliberately left empty → must fall back to dataRoot/playbooks.
		DataRoot:  dataRoot,
		Req:       capture.Request{},
		EnvLookup: func(string) (string, bool) { return "", false },
	}
	o := New(nil, &recMux{}).WithReengage(re)

	body := "# Playbook — Fallback Test\n\nVerify the dataRoot fallback.\n"
	path, err := o.CommitPlaybook(body)
	if err != nil {
		t.Fatalf("CommitPlaybook: %v", err)
	}

	wantPrefix := filepath.Join(dataRoot, "playbooks")
	if !strings.HasPrefix(path, wantPrefix) {
		t.Errorf("CommitPlaybook path = %q, want prefix %q", path, wantPrefix)
	}
}
