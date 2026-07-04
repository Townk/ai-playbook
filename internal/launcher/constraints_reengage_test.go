package launcher

import (
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/author"
	"github.com/Townk/ai-playbook/internal/capture"
	"github.com/Townk/ai-playbook/internal/config"
	"github.com/Townk/ai-playbook/internal/driver"
	"github.com/Townk/ai-playbook/internal/kb"
	"github.com/Townk/ai-playbook/internal/reengage"
)

// reengagePrompts folds the session constraints into the per-kind system prompt via
// author.WithConstraints for ALL FOUR re-engagement kinds. These tests pin two
// properties from the refuse-solution spec §1:
//
//   - with NIL constraints the built system prompt is BYTE-IDENTICAL to the raw
//     per-kind author prompt (no active constraint ⇒ prompts unchanged from today);
//   - with a non-empty list the constraints section is injected for every kind.
func TestReengagePrompts_NilConstraintsByteIdentical(t *testing.T) {
	cfg := config.Default()
	req := capture.Request{ // empty ProjectRoot ⇒ kb.Load is empty ⇒ deterministic
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
			want: author.SystemPrompt(req, author.KnowledgeBase(kb.Load(req.ProjectRoot)), driver.ResolveShellName(cfg.Driver.Shell)),
		},
		{
			name: "followup",
			kind: reengage.KindReengageFollowup,
			want: author.FollowupPrompt(req, change),
		},
		{
			name: "finalplaybook",
			kind: reengage.KindReengageFinalPlaybook,
			want: author.FinalPlaybookPrompt(req, base, change),
		},
		{
			name: "driftregen",
			kind: reengage.KindReengageDriftRegen,
			want: func() string { sys, _ := author.DriftRegenPrompt(base, change); return sys }(),
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
