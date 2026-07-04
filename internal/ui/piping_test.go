package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// runToResults runs cmd (flattening any tea.Batch) and returns every resultMsg it
// produces, ignoring ticks/flash. It lets a test drive the orchestrator-backed run
// path (and its chain advance) without the real Bubble Tea event loop.
func runToResults(cmd tea.Cmd) []resultMsg {
	if cmd == nil {
		return nil
	}
	var out []resultMsg
	switch v := cmd().(type) {
	case resultMsg:
		out = append(out, v)
	case tea.BatchMsg:
		for _, c := range v {
			out = append(out, runToResults(c)...)
		}
	}
	return out
}

// driveRun processes the first cmd's resultMsgs through handleResult, following
// any chain-advance cmds, until the run (and its from-chain) settles.
func driveRun(t *testing.T, m model, cmd tea.Cmd) model {
	t.Helper()
	queue := runToResults(cmd)
	for len(queue) > 0 {
		rm := queue[0]
		queue = queue[1:]
		tm, next := m.handleResult(rm)
		m = tm.(model)
		queue = append(queue, runToResults(next)...)
	}
	return m
}

// pipePlaybook builds a two-block piped playbook: prod appends a byte to counter
// (so re-runs are countable) and prints DATA on stdout; cons reads stdin via cat.
func pipePlaybook(counter, prodTail string) string {
	return "# Pipe\n\n" +
		"```bash {id=prod}\nprintf x >> " + counter + "; printf DATA" + prodTail + "\n```\n\n" +
		"```bash {id=cons from=prod}\ncat\n```\n"
}

func counterRuns(t *testing.T, counter string) int {
	t.Helper()
	b, err := os.ReadFile(counter)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("read counter: %v", err)
	}
	return len(b)
}

// Clicking a consumer whose from= producer has not run materializes the chain:
// the producer runs exactly once, its stdout pipes into the consumer's stdin, and
// both end ok.
func TestChain_MaterializesUnrunProducerOnce(t *testing.T) {
	m := newInProcModel(t)
	counter := filepath.Join(t.TempDir(), "cnt")
	m.md = pipePlaybook(counter, "")
	m.width, m.height = 80, 24
	m.reflow()

	m, cmd := m.runOrChain(Button{Kind: "run", BlockID: "cons", Payload: m.blockCommand("cons")})
	if len(m.chainQueue) != 1 || m.chainQueue[0] != "cons" {
		t.Fatalf("chainQueue = %v, want [cons] (producer dispatched first)", m.chainQueue)
	}
	m = driveRun(t, m, cmd)

	if got := m.blockStates["prod"].Status; got != "ok" {
		t.Errorf("prod status = %q, want ok", got)
	}
	if got := m.blockStates["cons"].Status; got != "ok" {
		t.Errorf("cons status = %q, want ok", got)
	}
	if n := counterRuns(t, counter); n != 1 {
		t.Errorf("producer ran %d times, want exactly 1", n)
	}
	// The consumer's stdin was the producer's retained stdout ("DATA").
	if lp := m.blockStates["cons"].Logpath; lp != "" {
		b, _ := os.ReadFile(lp)
		if !strings.Contains(string(b), "DATA") {
			t.Errorf("consumer output = %q, want it to contain the piped DATA", string(b))
		}
		_ = os.Remove(lp)
	}
}

// A producer already ok this session is NOT re-run when its consumer runs — the
// retained capture serves; the chain is just the consumer.
func TestChain_OkProducerNotRerun(t *testing.T) {
	m := newInProcModel(t)
	counter := filepath.Join(t.TempDir(), "cnt")
	m.md = pipePlaybook(counter, "")
	m.width, m.height = 80, 24
	m.reflow()

	// Run the producer standalone first (pre-materializes its capture).
	mm, pcmd := m.runOrChain(Button{Kind: "run", BlockID: "prod", Payload: m.blockCommand("prod")})
	m = driveRun(t, mm, pcmd)
	if n := counterRuns(t, counter); n != 1 {
		t.Fatalf("after standalone prod run, counter = %d, want 1", n)
	}

	// Now run the consumer: fromChain must be just [cons] (no producer to re-run).
	if chain := m.fromChain("cons"); len(chain) != 1 || chain[0] != "cons" {
		t.Fatalf("fromChain(cons) with prod ok = %v, want [cons]", chain)
	}
	m2, ccmd := m.runOrChain(Button{Kind: "run", BlockID: "cons", Payload: m.blockCommand("cons")})
	if len(m2.chainQueue) != 0 {
		t.Fatalf("chainQueue = %v, want empty (ok producer not queued)", m2.chainQueue)
	}
	m2 = driveRun(t, m2, ccmd)
	if n := counterRuns(t, counter); n != 1 {
		t.Errorf("producer re-ran (counter = %d, want 1) — an ok producer must not re-run", n)
	}
	if got := m2.blockStates["cons"].Status; got != "ok" {
		t.Errorf("cons status = %q, want ok", got)
	}
	if lp := m2.blockStates["cons"].Logpath; lp != "" {
		_ = os.Remove(lp)
	}
}

// A failed chain step stops the chain: the downstream consumer never starts.
func TestChain_FailureStopsChain(t *testing.T) {
	m := newInProcModel(t)
	counter := filepath.Join(t.TempDir(), "cnt")
	m.md = pipePlaybook(counter, "; exit 1") // producer fails
	m.width, m.height = 80, 24
	m.reflow()

	m, cmd := m.runOrChain(Button{Kind: "run", BlockID: "cons", Payload: m.blockCommand("cons")})
	m = driveRun(t, m, cmd)

	if got := m.blockStates["prod"].Status; got != "failed" {
		t.Errorf("prod status = %q, want failed", got)
	}
	if got := m.blockStates["cons"].Status; got != "" {
		t.Errorf("cons status = %q, want empty (never ran — the chain stopped)", got)
	}
	if len(m.chainQueue) != 0 {
		t.Errorf("chainQueue = %v, want cleared after failure", m.chainQueue)
	}
}

// Render gating: a from= consumer is NOT button-gated (its run button renders and
// it carries a "⇐ from:" annotation, no "⊘ needs:" blocker), while a needs=
// consumer IS gated (⊘, no run button) until its need is ok.
func TestRender_FromDoesNotGate_NeedsStillGates(t *testing.T) {
	md := "```bash {id=prod}\ntrue\n```\n\n" +
		"```bash {id=n1 needs=prod}\ntrue\n```\n\n" +
		"```bash {id=f1 from=prod}\ncat\n```\n"
	// prod has not run.
	lines, buttons, _ := Render(md, 80, RenderOpts{States: map[string]blockRunState{}})

	hasRun := func(id string) bool {
		for _, b := range buttons {
			if b.Kind == "run" && b.BlockID == id {
				return true
			}
		}
		return false
	}
	if hasRun("n1") {
		t.Error("needs= consumer must be gated (no run button) while its need is unmet")
	}
	if !hasRun("f1") {
		t.Error("from= consumer must keep its run button (the chain materializes on click)")
	}

	var text strings.Builder
	for _, ln := range lines {
		text.WriteString(strip(ln.Text))
		text.WriteByte('\n')
	}
	body := text.String()
	if !strings.Contains(body, "⊘ needs: prod") {
		t.Errorf("needs= consumer must show the ⊘ needs blocker:\n%s", body)
	}
	if !strings.Contains(body, "⇐ from: prod") {
		t.Errorf("from= consumer must show the ⇐ from annotation:\n%s", body)
	}
}

// Assisted (GUIDED) cadence hosts the chain as separate ready steps: the producer
// is surfaced BEFORE the consumer (NextRunnable folds from= into effective needs),
// so each chain step flows through its own per-step confirmation. Once the
// producer is ok, the consumer becomes the ready step.
func TestAssisted_ChainSurfacesEachStepInOrder(t *testing.T) {
	m := newModel("T", "```bash {id=prod}\nprintf DATA\n```\n\n```bash {id=cons from=prod}\ncat\n```\n")
	m.width, m.height = 80, 24
	m.assisted = true
	m.reflow()

	if first := m.assistedNextID(); first != "prod" {
		t.Fatalf("assisted ready step = %q, want prod (producer surfaced first)", first)
	}
	m.blockStates["prod"] = blockRunState{Status: "ok"}
	if next := m.assistedNextID(); next != "cons" {
		t.Fatalf("after prod ok, assisted ready step = %q, want cons", next)
	}
}

// resolveStdin is STAT-verified: a from= edge whose producer capture file does not
// yet exist resolves to "" (→ </dev/null), never trusting the path string alone.
func TestResolveStdin_MissingCaptureFallsBack(t *testing.T) {
	m := newInProcModel(t)
	m.md = "```bash {id=cons from=prod}\ncat\n```\n"
	m.width, m.height = 80, 24
	m.reflow()
	// prod never ran → its capture file does not exist → resolveStdin must be "".
	if p := m.resolveStdin("cons"); p != "" {
		t.Errorf("resolveStdin with no producer capture = %q, want empty (missing file ⇒ </dev/null)", p)
	}
}

// Double-click repro (reviewer finding): while a chain's producer is still
// running (its cmd dispatched but its result not yet landed — the "slow
// producer" window), a second click on the consumer must be a total no-op. The
// pre-fix code recomputed fromChain, saw prod.Status=="running" (≠"ok"), and
// re-queued AND re-dispatched the producer — running its side effects twice.
func TestChain_DoubleClickDoesNotRedispatchProducer(t *testing.T) {
	m := newInProcModel(t)
	counter := filepath.Join(t.TempDir(), "cnt")
	m.md = pipePlaybook(counter, "")
	m.width, m.height = 80, 24
	m.reflow()

	// First click: producer dispatched. cmd1 is held UN-invoked — the model is in
	// the mid-chain window (prod "running", result not landed).
	m, cmd1 := m.runOrChain(Button{Kind: "run", BlockID: "cons", Payload: m.blockCommand("cons")})
	if got := m.blockStates["prod"].Status; got != "running" {
		t.Fatalf("after first click, prod status = %q, want running", got)
	}

	// Second click mid-window: no dispatch, no queue growth.
	m2, cmd2 := m.runOrChain(Button{Kind: "run", BlockID: "cons", Payload: m.blockCommand("cons")})
	if cmd2 != nil {
		t.Error("second click mid-chain must not dispatch anything (producer would run twice)")
	}
	if len(m2.chainQueue) != 1 || m2.chainQueue[0] != "cons" {
		t.Errorf("second click mid-chain: chainQueue = %v, want [cons] unchanged", m2.chainQueue)
	}
	// The queued consumer's run button is inert for the whole window (hint path).
	if !m2.buttonInert(Button{Kind: "run", BlockID: "cons"}) {
		t.Error("a chain member's run button must be inert while the chain is in flight")
	}

	// Let the producer finish; the chain drives to completion — exactly one run.
	m2 = driveRun(t, m2, cmd1)
	if n := counterRuns(t, counter); n != 1 {
		t.Errorf("producer ran %d times, want exactly 1", n)
	}
	if got := m2.blockStates["cons"].Status; got != "ok" {
		t.Errorf("cons status = %q, want ok", got)
	}
	if got, gotq := m2.chainStep, len(m2.chainQueue); got != "" || gotq != 0 {
		t.Errorf("chain must settle clean: chainStep=%q chainQueue len=%d", got, gotq)
	}
	if lp := m2.blockStates["cons"].Logpath; lp != "" {
		_ = os.Remove(lp)
	}
}

// The chain-advance guard verifies the result is the EXPECTED in-flight step's:
// an unrelated block's ok result mid-chain must neither advance nor clear the
// chain (runMu serializes runs, so an unrelated result can land mid-window).
func TestChain_UnrelatedResultDoesNotAdvance(t *testing.T) {
	md := "```bash {id=prod}\nprintf DATA\n```\n\n" +
		"```bash {id=cons from=prod}\ncat\n```\n\n" +
		"```bash {id=other}\ntrue\n```\n"
	m := newModel("T", md)
	m.width, m.height = 80, 24
	m.reflow()
	m.chainStep = "prod"
	m.chainQueue = []string{"cons"}
	m.blockStates["prod"] = blockRunState{Status: "running"}
	m.blockStates["other"] = blockRunState{Status: "running"}

	tm, _ := m.handleResult(resultMsg{ID: "other", Exit: 0})
	m2 := tm.(model)
	if m2.chainStep != "prod" || len(m2.chainQueue) != 1 || m2.chainQueue[0] != "cons" {
		t.Errorf("unrelated ok result mutated the chain: step=%q queue=%v", m2.chainStep, m2.chainQueue)
	}
	if got := m2.blockStates["cons"].Status; got != "" {
		t.Errorf("cons started off an unrelated result: status=%q", got)
	}

	// And an unrelated FAILED result must not clear the chain either.
	tm, _ = m2.handleResult(resultMsg{ID: "other", Exit: 1})
	m3 := tm.(model)
	if m3.chainStep != "prod" || len(m3.chainQueue) != 1 {
		t.Errorf("unrelated failed result cleared the chain: step=%q queue=%v", m3.chainStep, m3.chainQueue)
	}
}

// Stopping the in-flight chain step ends the chain: the stopped-result early
// return must clear the chain state, or the queued members stay inert forever.
func TestChain_StoppedStepClearsChain(t *testing.T) {
	m := newModel("T", pipePlaybook("/dev/null", ""))
	m.width, m.height = 80, 24
	m.reflow()
	m.chainStep = "prod"
	m.chainQueue = []string{"cons"}
	m.blockStates["prod"] = blockRunState{Status: "running", Stopped: true}

	tm, _ := m.handleResult(resultMsg{ID: "prod", Exit: 143})
	m2 := tm.(model)
	if m2.chainStep != "" || len(m2.chainQueue) != 0 {
		t.Errorf("stopped chain step must clear the chain: step=%q queue=%v", m2.chainStep, m2.chainQueue)
	}
	if m2.buttonInert(Button{Kind: "run", BlockID: "cons"}) {
		t.Error("cons must not stay inert after the chain ended")
	}
}

// A from= cycle document (validation rejects it, but the walker must not depend
// on that) terminates bounded through fromChain — pins the seen-set guard.
func TestFromChain_CycleTerminates(t *testing.T) {
	m := newModel("T", "```bash {id=a from=b}\ncat\n```\n\n```bash {id=b from=a}\ncat\n```\n")
	m.width, m.height = 80, 24
	m.reflow()
	chain := m.fromChain("a")
	if len(chain) > 2 {
		t.Fatalf("fromChain over a cycle = %v, want bounded (≤2 ids)", chain)
	}
	if chain[len(chain)-1] != "a" {
		t.Errorf("fromChain must end with the clicked consumer: %v", chain)
	}
}

// resetDependents follows from= edges transitively: undoing a producer re-locks
// its from= consumer AND the consumer's own needs= dependents.
func TestResetDependents_FollowsFromEdges(t *testing.T) {
	md := "```bash {id=prod}\nprintf DATA\n```\n\n" +
		"```bash {id=cons from=prod}\ncat\n```\n\n" +
		"```bash {id=down needs=cons}\ntrue\n```\n"
	_, _, blocks := Render(md, 80, RenderOpts{})
	states := map[string]blockRunState{
		"prod": {Status: "ok"},
		"cons": {Status: "ok"},
		"down": {Status: "ok"},
	}
	resetDependents(states, blocks, "prod")
	if got := states["prod"].Status; got != "ok" {
		t.Errorf("root prod must be untouched: %q", got)
	}
	if _, ok := states["cons"]; ok {
		t.Error("cons (from=prod) must be reset when prod is undone")
	}
	if _, ok := states["down"]; ok {
		t.Error("down (needs=cons) must be reset transitively through the from= edge")
	}
}

// TestChain_ProducePythonFilterConsume_EndToEnd is the Phase 6 close-out
// end-to-end pin (v0.11 P5): the flagship three-block pipeline — a shell
// producer, a python filter reading sys.stdin, and a shell consumer — run
// through the real fake-free viewer path (newInProcModel: a real orchestrator
// over a real zsh, no fakes). Clicking the LAST block's run button must
// materialize the whole from= chain in document-independent order (prod →
// filter → cons), each step its own status, with the producer's raw bytes
// reaching the python filter's stdin and the filter's transformed stdout
// reaching the consumer's stdin in turn.
func TestChain_ProducePythonFilterConsume_EndToEnd(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	m := newInProcModel(t)
	m.md = "```bash {id=prod}\nprintf 'a\\nb\\nc'\n```\n\n" +
		"```python {id=filter from=prod}\n" +
		"import sys\n" +
		"for line in sys.stdin:\n" +
		"    print(line.strip().upper())\n" +
		"```\n\n" +
		"```bash {id=cons from=filter}\ncat\n```\n"
	m.width, m.height = 80, 24
	m.reflow()

	m, cmd := m.runOrChain(Button{Kind: "run", BlockID: "cons", Payload: m.blockCommand("cons")})
	if want := []string{"filter", "cons"}; len(m.chainQueue) != 2 || m.chainQueue[0] != want[0] || m.chainQueue[1] != want[1] {
		t.Fatalf("chainQueue = %v, want %v (prod dispatched first, filter+cons queued)", m.chainQueue, want)
	}
	m = driveRun(t, m, cmd)

	for _, id := range []string{"prod", "filter", "cons"} {
		if got := m.blockStates[id].Status; got != "ok" {
			t.Errorf("%s status = %q, want ok", id, got)
		}
	}
	lp := m.blockStates["cons"].Logpath
	if lp == "" {
		t.Fatal("cons has no logpath")
	}
	b, err := os.ReadFile(lp)
	if err != nil {
		t.Fatalf("read cons log: %v", err)
	}
	_ = os.Remove(lp)
	got := string(b)
	for _, want := range []string{"A", "B", "C"} {
		if !strings.Contains(got, want) {
			t.Errorf("consumer output = %q, want it to contain the filtered/uppercased %q", got, want)
		}
	}
}
