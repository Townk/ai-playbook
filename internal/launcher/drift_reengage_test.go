package launcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/config"
	"github.com/Townk/ai-playbook/internal/kb"
	"github.com/Townk/ai-playbook/internal/reengage"
	"github.com/Townk/ai-playbook/internal/ui"
)

// TestDriftRegenReengage_DriftOnly verifies the run-viewer re-engagement is wired for
// drift regenerate only: DriftRegenOnly set, Events present, and any non-drift kind is
// refused (so the followup/authoring affordances can't accidentally use it).
func TestDriftRegenReengage_DriftOnly(t *testing.T) {
	re := driftRegenReengage("")
	if re == nil || !re.DriftRegenOnly || re.Events == nil {
		t.Fatalf("driftRegenReengage must be DriftRegenOnly with Events wired; got %+v", re)
	}
	if _, _, err := re.Events(reengage.KindReengageFollowup, "", "", nil); err == nil {
		t.Error("drift-only Events must refuse a non-drift-regen kind")
	}
}

// TestDriftRegenPrompts_ProjectBoundRecallsProjectSet pins the K3 review fix: a
// project-bound standalone playbook reopened via `run --file` resolves its project
// root before the drift-regen wiring, so drift regen must recall the PROJECT set
// (Environment/Topics) too — not just the global set. A non-project-bound file
// (projectRoot "") stays global-only.
func TestDriftRegenPrompts_ProjectBoundRecallsProjectSet(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AI_PLAYBOOK_DATA_DIR", root)
	const projectRoot = "/home/me/bound-proj"

	// One fact in each set.
	if err := os.WriteFile(kb.GlobalPath(root), []byte("## System\n- macOS on Apple Silicon\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pp := kb.Path(root, projectRoot)
	if err := os.MkdirAll(filepath.Dir(pp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pp, []byte("## Environment\n- deploys via fly.io\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()

	// Project-bound: BOTH sets fold into the drift system prompt.
	sys, user := driftRegenPrompts("current file", "stale patch", nil, projectRoot, cfg)
	for _, want := range []string{
		"### About this machine and user",
		"macOS on Apple Silicon",
		"### About this project",
		"deploys via fly.io",
	} {
		if !strings.Contains(sys, want) {
			t.Errorf("project-bound drift prompt missing %q\n%s", want, sys)
		}
	}
	if !strings.Contains(user, "stale patch") {
		t.Errorf("drift user prompt missing the stale patch:\n%s", user)
	}

	// Non-project-bound (projectRoot ""): global set only, no project subheading.
	sys, _ = driftRegenPrompts("current file", "stale patch", nil, "", cfg)
	if !strings.Contains(sys, "macOS on Apple Silicon") {
		t.Errorf("non-bound drift prompt must still recall the global set:\n%s", sys)
	}
	if strings.Contains(sys, "### About this project") || strings.Contains(sys, "deploys via fly.io") {
		t.Errorf("non-bound drift prompt must not carry a project set:\n%s", sys)
	}
}

// TestRunViewer_WiresDriftRegenReengage verifies the `run --file` viewer path sets the
// drift-regen re-engagement context on ui.Options (so a standalone playbook can
// regenerate a drifted diff).
func TestRunViewer_WiresDriftRegenReengage(t *testing.T) {
	origUI := uiRunFn
	t.Cleanup(func() { uiRunFn = origUI })
	var got *reengage.Reengage
	uiRunFn = func(o ui.Options) int { got = o.Reengage; return 0 }
	withArgs(t, []string{"ai-playbook", "run", "--file", "/x.md"})

	runViewer("/x.md", "", ui.Options{})

	if got == nil || !got.DriftRegenOnly {
		t.Fatalf("runViewer must wire a DriftRegenOnly reengage; got %+v", got)
	}
}
