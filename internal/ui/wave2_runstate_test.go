package ui

import (
	"strings"
	"testing"
)

// TestRunActionDisabledWhenOk (F7) verifies a shell block that already ran ok has its
// run AND play actions disabled (no clickable button registered) — a dimmed "done" cue —
// while an idle block keeps both actions.
func TestRunActionDisabledWhenOk(t *testing.T) {
	md := "```bash {id=go}\necho hi\n```\n"

	// muxActive=true so the Play action is in scope (it needs an origin pane).
	_, okButtons, _ := Render(md, 80, RenderOpts{States: map[string]blockRunState{"go": {Status: "ok"}}, MuxActive: true})
	if b := buttonForBlock(okButtons, "go", "run"); b != nil {
		t.Errorf("run action must be disabled (no button) once the block ran ok; got %+v", b)
	}
	if b := buttonForBlock(okButtons, "go", "play"); b != nil {
		t.Errorf("play action must be disabled (no button) once the block ran ok; got %+v", b)
	}

	_, idleButtons, _ := Render(md, 80, RenderOpts{States: map[string]blockRunState{}, MuxActive: true})
	if b := buttonForBlock(idleButtons, "go", "run"); b == nil {
		t.Error("run action must be present when the block is idle")
	}
	if b := buttonForBlock(idleButtons, "go", "play"); b == nil {
		t.Error("play action must be present when the block is idle")
	}
}

// TestFollowupGatedOnReengage (F10) verifies the "try another fix" affordance only
// appears when in-process re-engagement is available. A plain `run --file` (no reengage)
// must not show it; an authoring session (reengage available) still does.
func TestFollowupGatedOnReengage(t *testing.T) {
	md := "```bash {id=go}\nfalse\n```\n"
	states := map[string]blockRunState{"go": {Status: "failed", Exit: 1}}

	// reengage unavailable → no button, no text.
	lines, buttons, _ := Render(md, 80, RenderOpts{States: states, NoReengage: true})
	if b := buttonForBlock(buttons, "go", "followup"); b != nil {
		t.Error("followup button must be hidden when re-engagement is unavailable")
	}
	if strings.Contains(joinText(lines), "try another fix") {
		t.Error("'try another fix' text must not appear when re-engagement is unavailable")
	}

	// reengage available → button shows.
	_, buttons2, _ := Render(md, 80, RenderOpts{States: states})
	if b := buttonForBlock(buttons2, "go", "followup"); b == nil {
		t.Error("followup button should appear when re-engagement is available")
	}
}

// TestResetDependents (F18) verifies undoing a block clears the run-state of every block
// that transitively needs it (so no stale "✓ ran" lingers), leaving the block itself and
// independent blocks untouched.
func TestResetDependents(t *testing.T) {
	blocks := []Block{
		{ID: "diff"},
		{ID: "b", Needs: []string{"diff"}},
		{ID: "c", Needs: []string{"b"}},
		{ID: "indep"},
	}
	states := map[string]blockRunState{
		"diff":  {Status: "ok"},
		"b":     {Status: "ok"},
		"c":     {Status: "ok"},
		"indep": {Status: "ok"},
	}
	resetDependents(states, blocks, "diff")

	if _, ok := states["b"]; ok {
		t.Error("direct dependent b must be reset (removed)")
	}
	if _, ok := states["c"]; ok {
		t.Error("transitive dependent c must be reset (removed)")
	}
	if states["diff"].Status != "ok" {
		t.Error("the undone block itself must be untouched by resetDependents")
	}
	if states["indep"].Status != "ok" {
		t.Error("an independent block must be untouched")
	}
}

// TestHeaderShowsFailureIndicator (F11) verifies the header surfaces a playbook-level
// "a step failed" indicator once any block fails, and shows nothing when all is well.
func TestHeaderShowsFailureIndicator(t *testing.T) {
	m := newModel("T", "```bash {id=a}\ntrue\n```\n")

	header := func() string { return strip(strings.Join(m.titleLines(), "\n")) }
	if strings.Contains(header(), "a step failed") {
		t.Error("header must not show a failure indicator when nothing has failed")
	}

	m.blockStates["a"] = blockRunState{Status: "failed", Exit: 1}
	if !strings.Contains(header(), "a step failed") {
		t.Errorf("header must surface 'a step failed' after a block fails; got %q", header())
	}
}
