package ui

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Townk/ai-playbook/internal/driver"
	"github.com/Townk/ai-playbook/internal/orchestrator"
)

// newInProcModel builds a model wired to a real orchestrator over a controlled-rc
// zsh (a minimal .zshrc — no p10k/mise), the same fixture approach as the driver
// and orchestrator tests, so the in-process path drives a real shell
// deterministically without touching the user's environment.
func newInProcModel(t *testing.T) model {
	t.Helper()
	zdot := t.TempDir()
	if err := os.WriteFile(filepath.Join(zdot, ".zshrc"), []byte("\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Pin zsh: zsh-specific fixture; the default now honors $SHELL (bash on CI).
	d, err := driver.Open(driver.Options{Shell: "zsh", Env: append(os.Environ(), "ZDOTDIR="+zdot)})
	if err != nil {
		t.Fatalf("driver.Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	m := newModel("agent", "")
	m.orch = orchestrator.New(d, &cliMux{})
	return m
}

// runMsg synchronously runs the tea.Cmd emitAction returns for a run button and
// returns the resulting message (the Cmd runs the orchestrator off the event
// loop; here we just call it directly).
func runMsg(t *testing.T, m model, b Button) interface{} {
	t.Helper()
	cmd := m.emitAction(b)
	if cmd == nil {
		t.Fatalf("emitAction returned nil cmd in in-process mode for kind=%q", b.Kind)
	}
	return cmd()
}

// In in-process mode, triggering a run button invokes the orchestrator and yields
// a resultMsg with the right id/exit, and the logfile holds the command output.
func TestInProcessRunYieldsResultMsg(t *testing.T) {
	m := newInProcModel(t)

	msg := runMsg(t, m, Button{Kind: "run", BlockID: "hello", Payload: "print -r -- hi"})
	res, ok := msg.(resultMsg)
	if !ok {
		t.Fatalf("got %T, want resultMsg", msg)
	}
	if res.ID != "hello" {
		t.Errorf("id = %q, want %q", res.ID, "hello")
	}
	if res.Exit != 0 {
		t.Errorf("exit = %d, want 0", res.Exit)
	}
	if res.Logpath == "" {
		t.Fatal("logpath empty; want a temp logfile")
	}
	b, err := os.ReadFile(res.Logpath)
	if err != nil {
		t.Fatalf("read logfile: %v", err)
	}
	if got := string(b); got != "hi" {
		t.Errorf("logfile = %q, want %q", got, "hi")
	}
	_ = os.Remove(res.Logpath)
}

// A non-zero exit propagates to the resultMsg.
func TestInProcessRunNonZeroExit(t *testing.T) {
	m := newInProcModel(t)
	msg := runMsg(t, m, Button{Kind: "run", BlockID: "boom", Payload: "(exit 3)"})
	res, ok := msg.(resultMsg)
	if !ok {
		t.Fatalf("got %T, want resultMsg", msg)
	}
	if res.ID != "boom" || res.Exit != 3 {
		t.Errorf("got id=%q exit=%d, want id=boom exit=3", res.ID, res.Exit)
	}
	if res.Logpath != "" {
		_ = os.Remove(res.Logpath)
	}
}

// Driving the run button through the real Update loop transitions the block state
// to "failed" on a non-zero exit (proves the resultMsg bridge plugs into the
// existing handler unchanged).
func TestInProcessRunUpdatesBlockState(t *testing.T) {
	m := newInProcModel(t)
	cmd := m.emitAction(Button{Kind: "run", BlockID: "v", Payload: "(exit 1)"})
	if cmd == nil {
		t.Fatal("nil cmd")
	}
	msg := cmd()
	nm, _ := m.Update(msg)
	got := nm.(model).blockStates["v"].Status
	if got != "failed" {
		t.Errorf("status = %q, want failed", got)
	}
	if res, ok := msg.(resultMsg); ok && res.Logpath != "" {
		_ = os.Remove(res.Logpath)
	}
}

// copy/play go through the orchestrator + Mux and surface no resultMsg (nil msg).
func TestInProcessCopyPlayNoResult(t *testing.T) {
	m := newInProcModel(t)
	mux := m.orch.Mux.(*cliMux)

	if msg := m.emitAction(Button{Kind: "play", BlockID: "p", Payload: "echo hi"})(); msg != nil {
		t.Errorf("play msg = %v, want nil", msg)
	}
	if played := mux.Played(); len(played) != 1 || played[0] != "echo hi" {
		t.Errorf("play not recorded → %v", played)
	}
}

// TestCliMuxPlayConcurrent exercises cliMux.Play from two goroutines at once —
// the orchestrator dispatches play through a tea.Cmd that runs off the event loop,
// so two play buttons resolving concurrently append to the same slice. Run under
// -race, an unguarded append trips the detector; the mutex makes it clean, and all
// records survive. (A1b.)
func TestCliMuxPlayConcurrent(t *testing.T) {
	mux := &cliMux{}
	const n = 100
	var wg sync.WaitGroup
	wg.Add(2)
	for g := 0; g < 2; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < n; i++ {
				_ = mux.Play("echo hi")
			}
		}()
	}
	wg.Wait()
	if got := len(mux.Played()); got != 2*n {
		t.Errorf("recorded %d plays, want %d (a lost append means the mutex isn't holding)", got, 2*n)
	}
}

// Still-deferred kinds resolve to a statusMsg, never a crash, in in-process mode.
func TestInProcessDeferredKindStatus(t *testing.T) {
	m := newInProcModel(t)
	for _, kind := range []string{"regenerate", "followup"} {
		cmd := m.emitAction(Button{Kind: kind, BlockID: "x"})
		if cmd == nil {
			t.Fatalf("%s: nil cmd", kind)
		}
		msg := cmd()
		if _, ok := msg.(statusMsg); !ok {
			t.Errorf("%s → %T, want statusMsg", kind, msg)
		}
	}
}

// apply-diff / undo-diff now drive the orchestrator and yield a resultMsg (the
// model's resultMsg handler flips the apply⇄undo toggle off st.Action + Exit).
// Here the payload is not a valid patch and the cwd is not a repo, so the run
// fails — what matters is the bridge produces a resultMsg, not a statusMsg.
func TestInProcessApplyUndoYieldResultMsg(t *testing.T) {
	m := newInProcModel(t)
	for _, kind := range []string{"apply-diff", "undo-diff"} {
		cmd := m.emitAction(Button{Kind: kind, BlockID: "fix", Payload: "not a patch\n"})
		if cmd == nil {
			t.Fatalf("%s: nil cmd", kind)
		}
		msg := cmd()
		res, ok := msg.(resultMsg)
		if !ok {
			t.Fatalf("%s → %T, want resultMsg", kind, msg)
		}
		if res.ID != "fix" {
			t.Errorf("%s id = %q, want fix", kind, res.ID)
		}
		if res.Logpath != "" {
			_ = os.Remove(res.Logpath)
		}
	}
}

// view-diff is fire-and-forget: with no Float mux wired it is a graceful no-op
// returning a nil message (never a statusMsg / crash).
func TestInProcessViewDiffNoResult(t *testing.T) {
	m := newInProcModel(t)
	cmd := m.emitAction(Button{Kind: "view-diff", BlockID: "fix", Payload: "diff --git a/f b/f\n"})
	if cmd == nil {
		t.Fatal("view-diff: nil cmd")
	}
	if msg := cmd(); msg != nil {
		t.Errorf("view-diff msg = %T, want nil", msg)
	}
}

// Sanity: in FIFO mode (no orch, no fifo) emitAction is a void no-op returning nil.
func TestEmitActionNoOrchNoFifoReturnsNil(t *testing.T) {
	m := model{}
	if cmd := m.emitAction(Button{Kind: "run", BlockID: "x", Payload: "ls"}); cmd != nil {
		t.Errorf("emitAction in no-orch/no-fifo mode returned non-nil cmd")
	}
}

// orchWithReengageBody builds a minimal *orchestrator.Orchestrator (no live driver
// needed) with a Reengage whose Body closure returns the given function. Used by the
// per-stream structured-render tests below; the Cmd returned by begin* is never
// called, so the nil Drv/Mux are harmless.
func orchWithReengageBody(body func() string) *orchestrator.Orchestrator {
	return &orchestrator.Orchestrator{
		Reengage: &orchestrator.Reengage{Body: body},
	}
}

// TestRearm_StructuredFinalPlaybook: beginFinalPlaybookGenerate must set
// m.structured=true and wire m.bodyProvider to Reengage.Body so the stream EOF
// renders the fresh captured playbook rather than clobbering with a stale one.
func TestRearm_StructuredFinalPlaybook(t *testing.T) {
	want := "# Re-authored\n"
	m := model{
		orch:   orchWithReengageBody(func() string { return want }),
		width:  80,
		height: 24,
	}
	_ = m.beginFinalPlaybookGenerate("", "troubleshoot content")
	if !m.structured {
		t.Fatal("beginFinalPlaybookGenerate must set structured=true")
	}
	if m.bodyProvider == nil {
		t.Fatal("beginFinalPlaybookGenerate must wire bodyProvider to Reengage.Body")
	}
	if got := m.bodyProvider(); got != want {
		t.Fatalf("bodyProvider not wired to Reengage.Body: got %q want %q", got, want)
	}
}

// TestRearm_StructuredRegenerate: beginRegenerate (orch path) must set
// m.structured=true and wire m.bodyProvider to Reengage.Body.
func TestRearm_StructuredRegenerate(t *testing.T) {
	want := "# Regenerated\n"
	m := model{
		orch:   orchWithReengageBody(func() string { return want }),
		width:  80,
		height: 24,
	}
	_ = m.beginRegenerate()
	if !m.structured {
		t.Fatal("beginRegenerate must set structured=true")
	}
	if m.bodyProvider == nil {
		t.Fatal("beginRegenerate must wire bodyProvider to Reengage.Body")
	}
	if got := m.bodyProvider(); got != want {
		t.Fatalf("bodyProvider not wired to Reengage.Body: got %q want %q", got, want)
	}
}

// TestRearm_FollowupNotStructured: beginFollowupInProc (the markdown APPEND path)
// must clear m.structured so its streamed markdown is NOT clobbered at EOF by the
// bodyProvider re-render.
func TestRearm_FollowupNotStructured(t *testing.T) {
	m := model{
		structured: true,
		orch:       orchWithReengageBody(func() string { return "x" }),
		width:      80,
		height:     24,
	}
	_ = m.beginFollowupInProc("failed output")
	if m.structured {
		t.Fatal("beginFollowupInProc must clear structured so its markdown is not clobbered at EOF")
	}
}

// kindOf maps every UI kind to the orchestrator's Kind (and ErrNotImplemented for
// deferred ones is the orchestrator's concern, exercised above).
func TestKindOfMapping(t *testing.T) {
	cases := map[string]orchestrator.Kind{
		"run": orchestrator.KindRun, "stop": orchestrator.KindStop,
		"copy": orchestrator.KindCopy, "play": orchestrator.KindPlay,
		"diff": orchestrator.KindViewDiff, "view-diff": orchestrator.KindViewDiff,
		"apply-diff": orchestrator.KindApplyDiff, "undo-diff": orchestrator.KindUndoDiff,
		"regenerate": orchestrator.KindRegenerate, "followup": orchestrator.KindFollowup,
	}
	for s, want := range cases {
		got, ok := kindOf(s)
		if !ok || got != want {
			t.Errorf("kindOf(%q) = %v ok=%v, want %v", s, got, ok, want)
		}
	}
	if _, ok := kindOf("toggle"); ok {
		t.Error("kindOf(toggle) should be false (pager-local)")
	}
}
