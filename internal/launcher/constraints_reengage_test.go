package launcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/author"
	"github.com/Townk/ai-playbook/internal/capture"
	"github.com/Townk/ai-playbook/internal/config"
	"github.com/Townk/ai-playbook/internal/kb"
	"github.com/Townk/ai-playbook/internal/reengage"
	"github.com/Townk/ai-playbook/pkg/driver"
)

// reengagePrompts folds the session constraints into the per-kind system prompt via
// author.WithConstraints for ALL FOUR re-engagement kinds. These tests pin two
// properties from the refuse-solution spec §1:
//
//   - with NIL constraints the built system prompt is BYTE-IDENTICAL to the raw
//     per-kind author prompt (no active constraint ⇒ prompts unchanged from today);
//   - with a non-empty list the constraints section is injected for every kind.
func TestReengagePrompts_NilConstraintsByteIdentical(t *testing.T) {
	t.Setenv("AI_PLAYBOOK_DATA_DIR", t.TempDir()) // empty KB dir ⇒ recall folds nothing ⇒ deterministic
	cfg := config.Default()
	req := capture.Request{ // empty ProjectRoot ⇒ recall is empty ⇒ deterministic
		Kind:        "error",
		Command:     "make build",
		Exit:        "2",
		UserRequest: "fix my broken build",
		Scrollback:  "gcc: command not found",
	}
	const base = "# Playbook — set up the build\n\n```bash {id=verify}\nmake build\n```\n"
	const change = "the resolved troubleshoot content"

	cases := []struct {
		name string
		kind reengage.ReengageKind
		want string // the raw per-kind prompt the launcher used before this feature
	}{
		{
			name: "regenerate",
			kind: reengage.KindReengageRegenerate,
			want: author.SystemPrompt(req, "", "", driver.ResolveShellName(cfg.Driver.Shell)),
		},
		{
			name: "followup",
			kind: reengage.KindReengageFollowup,
			want: author.FollowupPrompt(req, change, "", ""),
		},
		{
			name: "finalplaybook",
			kind: reengage.KindReengageFinalPlaybook,
			want: author.FinalPlaybookPrompt(req, base, change, "", ""),
		},
		{
			name: "driftregen",
			kind: reengage.KindReengageDriftRegen,
			want: func() string { sys, _ := author.DriftRegenPrompt(base, change, "", ""); return sys }(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sys, _ := reengagePrompts(req, tc.kind, base, change, nil, cfg)
			if sys != tc.want {
				t.Errorf("nil-constraints system prompt is not byte-identical to the raw author prompt\n--- got ---\n%s\n--- want ---\n%s", sys, tc.want)
			}
		})
	}
}

// TestReengagePrompts_KBFoldAndConstraintsOrdering pins the combined case: with
// BOTH the knowledge fold and the constraints non-empty, every kind's system
// prompt reads [base][kb-fold][constraints] — the base builder text first, the
// "## What we already know…" fold next, and the constraints section LAST (it is
// appended after the fold by construction; this locks that ordering).
func TestReengagePrompts_KBFoldAndConstraintsOrdering(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AI_PLAYBOOK_DATA_DIR", root)
	const projectRoot = "/home/me/ordered-proj"
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
	req := capture.Request{
		Kind:        "error",
		Command:     "make build",
		Exit:        "2",
		UserRequest: "fix my broken build",
		ProjectRoot: projectRoot,
	}
	const base = "# Playbook — set up the build\n\n```bash {id=verify}\nmake build\n```\n"
	const change = "the resolved troubleshoot content"
	constraints := []string{"no docker"}

	const kbHeading = "## What we already know about this project"
	const constraintsHeading = "## Constraints (user-rejected approaches)"

	cases := []struct {
		name       string
		kind       reengage.ReengageKind
		baseMarker string // text from the kind's base builder, emitted before any fold
	}{
		{"regenerate", reengage.KindReengageRegenerate, "You are a terminal assistant"},
		{"followup", reengage.KindReengageFollowup, "helping debug a terminal fix"},
		{"finalplaybook", reengage.KindReengageFinalPlaybook, "Literate-Config playbook"},
		{"driftregen", reengage.KindReengageDriftRegen, "no longer applies"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sys, _ := reengagePrompts(req, tc.kind, base, change, constraints, cfg)
			iBase := strings.Index(sys, tc.baseMarker)
			iKB := strings.Index(sys, kbHeading)
			iCon := strings.Index(sys, constraintsHeading)
			if iBase < 0 || iKB < 0 || iCon < 0 {
				t.Fatalf("missing part: base=%d kb=%d constraints=%d\n%s", iBase, iKB, iCon, sys)
			}
			if iBase >= iKB || iKB >= iCon {
				t.Errorf("ordering must be [base][kb-fold][constraints]; got base=%d kb=%d constraints=%d\n%s", iBase, iKB, iCon, sys)
			}
			// Both sets folded (global then project).
			iG := strings.Index(sys, "macOS on Apple Silicon")
			iP := strings.Index(sys, "deploys via fly.io")
			if iG < 0 || iP < 0 || iG >= iP {
				t.Errorf("both sets must fold, global first; got global=%d project=%d\n%s", iG, iP, sys)
			}
		})
	}
}

func TestReengagePrompts_ConstraintsSectionInjectedAllKinds(t *testing.T) {
	cfg := config.Default()
	req := capture.Request{Kind: "error", Command: "make build", Exit: "2", UserRequest: "fix my broken build"}
	const base = "# Playbook — set up the build\n\n```bash {id=verify}\nmake build\n```\n"
	const change = "the resolved troubleshoot content"

	const heading = "## Constraints (user-rejected approaches)"
	constraints := []string{"no docker"}

	for _, kind := range []reengage.ReengageKind{
		reengage.KindReengageRegenerate,
		reengage.KindReengageFollowup,
		reengage.KindReengageFinalPlaybook,
		reengage.KindReengageDriftRegen,
	} {
		sys, _ := reengagePrompts(req, kind, base, change, constraints, cfg)
		if !strings.Contains(sys, heading) {
			t.Errorf("kind %v: constraints section missing\n%s", kind, sys)
		}
		if !strings.Contains(sys, "- no docker") {
			t.Errorf("kind %v: constraint bullet missing\n%s", kind, sys)
		}
	}
}
