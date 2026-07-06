package ui

import (
	"testing"
	"time"

	"github.com/Townk/ai-playbook/pkg/playbook"
)

// ---- display: a timed-out run says so; a plain failure is unchanged ----

// A failed block whose result was a timeout kill renders "✗ timed out after
// <effective duration>" (block-timeout spec, Decision 4) — the duration the
// run actually got, trimmed of zero units (10m0s → 10m).
func TestRunRegionTimedOutForm(t *testing.T) {
	st := map[string]blockRunState{"a": {
		Status: "failed", Exit: 143, TimedOut: true, TimedOutAfter: 10 * time.Minute,
	}}
	lines, _, _ := Render("```bash {id=a}\nsleep 999\n```\n", 80, RenderOpts{States: st})
	if !linesContain(lines, "timed out after 10m (exit 143)") {
		t.Fatalf("timed-out failure must render the effective duration; lines:\n%s", dumpLines(lines))
	}
	if linesContain(lines, "✗ failed") {
		t.Fatal("a timed-out run must not also render the plain-failure form")
	}
}

// A declared timeout= shows ITS duration, not the default.
func TestRunRegionTimedOutFormDeclared(t *testing.T) {
	st := map[string]blockRunState{"a": {
		Status: "failed", Exit: -1, TimedOut: true, TimedOutAfter: 90 * time.Second,
	}}
	lines, _, _ := Render("```bash {id=a}\nsleep 999\n```\n", 80, RenderOpts{States: st})
	if !linesContain(lines, "timed out after 1m30s (exit -1)") {
		t.Fatalf("declared-timeout kill must name the declared ceiling; lines:\n%s", dumpLines(lines))
	}
}

// A plain (non-timeout) failure keeps its existing form.
func TestRunRegionPlainFailureUnchanged(t *testing.T) {
	st := map[string]blockRunState{"a": {Status: "failed", Exit: 1}}
	lines, _, _ := Render("```bash {id=a}\nfalse\n```\n", 80, RenderOpts{States: st})
	if !linesContain(lines, "✗ failed (exit 1)") {
		t.Fatalf("plain failure form changed; lines:\n%s", dumpLines(lines))
	}
	if linesContain(lines, "timed out") {
		t.Fatal("a plain failure must not read as a timeout")
	}
}

// dumpLines flattens rendered lines (color-stripped) for a failure message.
func dumpLines(lines []Line) string {
	out := ""
	for _, l := range lines {
		out += strip(l.Text) + "\n"
	}
	return out
}

// ---- resultMsg → blockRunState carry ----

// handleResult must carry the timed-out marker and the effective duration into
// the block's run state (the backlog item: resultMsg used to DROP TimedOut).
func TestHandleResultCarriesTimedOut(t *testing.T) {
	m := newModel("agent", "```bash {id=a}\nsleep 999\n```\n")
	m.reflow()
	nm := mustModel(m.Update(resultMsg{ID: "a", Exit: 143, TimedOut: true, TimedOutAfter: 10 * time.Minute}))
	st := nm.blockStates["a"]
	if st.Status != "failed" {
		t.Fatalf("status = %q, want failed", st.Status)
	}
	if !st.TimedOut || st.TimedOutAfter != 10*time.Minute {
		t.Fatalf("TimedOut/After = %v/%v, want true/10m", st.TimedOut, st.TimedOutAfter)
	}
	// A later plain failure on the same block clears the stale timed-out form.
	nm2 := mustModel(nm.Update(resultMsg{ID: "a", Exit: 1}))
	if st := nm2.blockStates["a"]; st.TimedOut {
		t.Fatal("a subsequent plain failure must clear TimedOut")
	}
}

// ---- threading: the viewer conversion carries Block.Timeout end-to-end ----

// A block declaring timeout=1s is killed by the driver at ~1s (not the 10m
// default), and the resultMsg reports TimedOut with the DECLARED effective
// duration — proving orchCmd threads Block.Timeout through Action.Timeout to
// the driver. Uses the same real-shell fixture as the other in-process tests.
func TestInProcessRunHonorsBlockTimeout(t *testing.T) {
	m := newInProcModel(t)
	m.blocks = []Block{{ID: "slow", Type: "shell", Lang: "bash", Payload: "sleep 30", Timeout: time.Second}}

	start := time.Now()
	msg := runMsg(t, m, Button{Kind: "run", BlockID: "slow", Payload: "sleep 30"})
	elapsed := time.Since(start)

	res, ok := msg.(resultMsg)
	if !ok {
		t.Fatalf("got %T, want resultMsg", msg)
	}
	if !res.TimedOut {
		t.Fatalf("a run killed at its ceiling must report TimedOut: %+v", res)
	}
	if res.TimedOutAfter != time.Second {
		t.Errorf("TimedOutAfter = %v, want the declared 1s", res.TimedOutAfter)
	}
	// Way under the sleep AND the 10m default: the declared ceiling fired.
	if elapsed > 20*time.Second {
		t.Errorf("run took %v; the declared 1s timeout did not apply", elapsed)
	}
}

// ---- threading: the rollback conversion carries Block.Timeout too ----

func TestToAutorunBlocksCarriesTimeout(t *testing.T) {
	m := newModel("agent", "")
	m.blocks = playbook.ParseBlocks("```bash {id=a timeout=5m}\ntrue\n```\n")
	ab, _ := m.toAutorunBlocks()
	if len(ab) != 1 {
		t.Fatalf("want 1 block, got %d", len(ab))
	}
	if ab[0].Timeout != 5*time.Minute {
		t.Fatalf("autorun.Block.Timeout = %v, want 5m", ab[0].Timeout)
	}
}
