package ui

import (
	"strings"
	"testing"
)

// ---- ValidatePlaybook ----

func TestValidatePlaybook_RealPlaybook(t *testing.T) {
	md := "# Build the app\n\nDo this:\n\n```bash {id=x}\nmake build\n```\n"
	if !ValidatePlaybook(md) {
		t.Fatalf("ValidatePlaybook: a real playbook (H1 + runnable block) must be valid")
	}
}

func TestValidatePlaybook_Narration(t *testing.T) {
	// No H1 and no runnable block — a narration, not a playbook.
	if ValidatePlaybook("I ran the build for you and it worked great.") {
		t.Fatalf("ValidatePlaybook: a narration (no H1 / no runnable block) must be invalid")
	}
	// An H1 but no runnable block is still not a playbook.
	if ValidatePlaybook("# A title\n\njust prose, nothing to run\n") {
		t.Fatalf("ValidatePlaybook: an H1 with no runnable block must be invalid")
	}
}

// ---- adapted-from banner ----

// TestAdaptedBanner_RendersInSubtitle verifies the "adapted from <slug>" banner
// text appears in the rendered frame when adaptedFrom is set (it reuses the
// subtitle slot).
func TestAdaptedBanner_RendersInSubtitle(t *testing.T) {
	m := newModel("agent", "# Build\n\n```bash {id=x}\nmake\n```\n")
	m.width, m.height = 100, 30
	m.adaptedFrom = "build-app"
	m.subtitle = adaptedBanner("build-app")
	m.reflow()
	out := strip(m.viewString())
	if !strings.Contains(out, "adapted from build-app") {
		t.Fatalf("expected the 'adapted from build-app' banner in the view, got:\n%s", out)
	}
}

// ---- d keybind toggles the diff overlay ----

func TestDiffKeybind_TogglesDiffView(t *testing.T) {
	m := newModel("agent", "# Build\n\n```bash {id=x}\nmake build\n```\n")
	m.width, m.height = 100, 30
	m.adaptedFrom = "build-app"
	m.origDoc = "# Build\n\n```bash {id=x}\nmake\n```\n"
	m.reflow()

	// `d` raises the diff overlay.
	nm, _ := m.Update(key("d"))
	dm := nm.(model)
	if !dm.diffMode {
		t.Fatalf("`d` must raise the diff overlay (diffMode=true)")
	}
	if len(dm.diffLines) == 0 {
		t.Fatalf("the diff overlay must build its diff lines on open")
	}
	out := strip(dm.viewString())
	if !strings.Contains(out, "original → adapted") {
		t.Fatalf("the diff view must render the original→adapted header, got:\n%s", out)
	}
	// The diff must surface the changed line (adapted adds "make build", removes "make").
	if !strings.Contains(out, "make build") {
		t.Fatalf("the diff view must show the adapted line, got:\n%s", out)
	}

	// `d` again dismisses it.
	nm2, _ := dm.Update(key("d"))
	if nm2.(model).diffMode {
		t.Fatalf("a second `d` must dismiss the diff overlay")
	}
}

// TestDiffKeybind_InertWithoutAdaptedFrom verifies `d` is a no-op for a normal
// (non-adapted) render.
func TestDiffKeybind_InertWithoutAdaptedFrom(t *testing.T) {
	m := newModel("agent", "# Build\n\n```bash {id=x}\nmake\n```\n")
	m.width, m.height = 100, 30
	m.reflow()
	nm, _ := m.Update(key("d"))
	if nm.(model).diffMode {
		t.Fatalf("`d` must be inert when adaptedFrom is empty")
	}
}
