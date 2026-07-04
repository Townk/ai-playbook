package draft_test

import (
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/draft"
	"github.com/Townk/ai-playbook/internal/ui"
	"github.com/Townk/ai-playbook/pkg/playbook"
	"github.com/Townk/ai-playbook/pkg/playbook/frontmatter"
	"github.com/Townk/ai-playbook/pkg/playbook/validate"
)

func sample() draft.Playbook {
	return draft.Playbook{
		Title: "Restore the Gradle wrapper",
		Intro: "You ran `gg build` and it failed.",
		Sections: []draft.Section{
			{Heading: "Goal & error", Content: []draft.ContentItem{
				{Kind: "text", Text: "The wrapper jar is missing."},
				{Kind: "code", Lang: "console", Code: "Error: GradleWrapperMain", Static: true},
				{Kind: "callout", Text: "This directory is a git repo."},
			}},
			{Heading: "Fix", Content: []draft.ContentItem{
				{Kind: "text", Text: "Restore the jar:"},
				{Kind: "code", Lang: "bash", Code: "gradle wrapper", ID: "fix"},
				{Kind: "text", Text: "Now the build works."},
			}},
		},
		Verify: &draft.Step{Lang: "bash", Code: "gg build", Needs: []string{"fix"}},
		Meta:   draft.Meta{Description: "Restore the wrapper", ProjectBound: true},
	}
}

func TestRender_Golden(t *testing.T) {
	got := draft.Render(sample())
	want := "# Restore the Gradle wrapper\n" +
		"\nYou ran `gg build` and it failed.\n" +
		"\n## Goal & error\n" +
		"\nThe wrapper jar is missing.\n" +
		"\n```console {static}\nError: GradleWrapperMain\n```\n" +
		"\n> [!NOTE]\n> This directory is a git repo.\n" +
		"\n## Fix\n" +
		"\nRestore the jar:\n" +
		"\n```bash {id=fix}\ngradle wrapper\n```\n" +
		"\nNow the build works.\n" +
		"\n```bash {id=verify needs=fix}\ngg build\n```\n"
	if got != want {
		t.Fatalf("render mismatch:\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}
}

// The rendered body MUST be a valid playbook the viewer accepts.
func TestRender_IsValidPlaybook(t *testing.T) {
	if !ui.ValidatePlaybook(draft.Render(sample())) {
		t.Fatalf("rendered playbook failed ui.ValidatePlaybook:\n%s", draft.Render(sample()))
	}
}

// A callout renders as a GitHub-style admonition (`> [!TYPE]`) the viewer styles;
// the type comes from Admonition, defaulting to NOTE when omitted.
func TestRender_CalloutAdmonition(t *testing.T) {
	pb := draft.Playbook{Title: "T", Sections: []draft.Section{{Heading: "S", Content: []draft.ContentItem{
		{Kind: "callout", Admonition: "warning", Text: "destroys data"},
		{Kind: "callout", Text: "an aside"},
		{Kind: "code", Lang: "bash", Code: "x", ID: "a"},
	}}}}
	got := draft.Render(pb)
	if !strings.Contains(got, "> [!WARNING]\n> destroys data") {
		t.Errorf("typed callout must emit its admonition marker:\n%s", got)
	}
	if !strings.Contains(got, "> [!NOTE]\n> an aside") {
		t.Errorf("untyped callout must default to NOTE:\n%s", got)
	}
}

// TestRender_FromTag verifies from= renders in the fence tag (ADR-0010) so
// the AI can author piped playbooks, and that a verify step can declare it too.
func TestRender_FromTag(t *testing.T) {
	pb := draft.Playbook{Title: "T", Sections: []draft.Section{{Heading: "S", Content: []draft.ContentItem{
		{Kind: "code", Lang: "bash", Code: "echo hi", ID: "produce"},
		{Kind: "code", Lang: "python", Code: "import sys", ID: "consume", From: "produce"},
	}}}, Verify: &draft.Step{Lang: "bash", Code: "true", From: "consume"}}
	out := draft.Render(pb)
	if !strings.Contains(out, "```python {id=consume from=produce}\n") {
		t.Fatalf("fence missing from= tag:\n%s", out)
	}
	if !strings.Contains(out, "```bash {id=verify from=consume}\n") {
		t.Fatalf("verify fence missing from= tag:\n%s", out)
	}
	if !ui.ValidatePlaybook(out) {
		t.Fatalf("rendered playbook with from= failed ui.ValidatePlaybook:\n%s", out)
	}
}

// TestRender_FromAttributeOrder pins the FULL fence-tag attribute order —
// {id=… from=… file=… needs=… rollback=…} — with every attribute present at
// once. Render-only (the combination is not semantically valid); the order
// is a contract downstream goldens and re-parsers rely on.
func TestRender_FromAttributeOrder(t *testing.T) {
	pb := draft.Playbook{Title: "T", Sections: []draft.Section{{Heading: "S", Content: []draft.ContentItem{
		{Kind: "code", Lang: "bash", Code: "x", ID: "all", From: "src", File: "out.txt", Needs: []string{"a", "b"}, Rollback: "undo"},
	}}}}
	out := draft.Render(pb)
	if !strings.Contains(out, "```bash {id=all from=src file=out.txt needs=a,b rollback=undo}\n") {
		t.Fatalf("fence attribute order changed:\n%s", out)
	}
}

// TestValidate_Render_RoundTrip_FileValidatorAgrees is the lockstep
// INVARIANT: a from=-bearing playbook draft.Validate ACCEPTS must, once
// rendered and re-parsed through the public schema owner, pass
// pkg/playbook/validate.Check with no Error findings. This is the exact
// divergence the review round proved (a non-Static lang=console consumer
// passed draft but failed the file validator) — the classifier is now shared
// (playbook.ClassifyType), and this test keeps the two ends agreeing.
func TestValidate_Render_RoundTrip_FileValidatorAgrees(t *testing.T) {
	pb := draft.Playbook{Title: "T", Sections: []draft.Section{{Heading: "S", Content: []draft.ContentItem{
		{Kind: "code", Lang: "bash", Code: "echo hi", ID: "produce"},
		{Kind: "code", Lang: "python", Code: "import sys; print(sys.stdin.read())", ID: "consume", From: "produce"},
	}}}, Verify: &draft.Step{Lang: "bash", Code: "true", From: "consume"}}
	if err := draft.Validate(pb, true); err != nil {
		t.Fatalf("draft.Validate rejected the round-trip fixture: %v", err)
	}
	body := draft.Render(pb)
	pbBlocks := playbook.ParseBlocks(body)
	blocks := make([]validate.Block, 0, len(pbBlocks))
	for _, b := range pbBlocks {
		blocks = append(blocks, validate.Block{
			ID: b.ID, Type: b.Type, Lang: b.Lang, Needs: b.Needs, Static: b.Static, From: b.From,
		})
	}
	fm := frontmatter.FrontMatter{Name: "N", Description: "D", Category: "C", Created: "2026-07-04"}
	if fs := validate.Check(body, fm, true, blocks, 0); validate.HasError(fs) {
		t.Fatalf("file validator rejected what draft.Validate accepted:\n%+v\n--- body ---\n%s", fs, body)
	}
}

func TestRender_FileBlock(t *testing.T) {
	pb := draft.Playbook{Title: "T", Sections: []draft.Section{{Heading: "S", Content: []draft.ContentItem{
		{Kind: "code", Lang: "go", File: "cmd/x/main.go", ID: "new", Code: "package main\n"}}}}}
	out := draft.Render(pb)
	if !strings.Contains(out, "file=cmd/x/main.go") {
		t.Fatalf("fence missing file= tag:\n%s", out)
	}
}

// Finding A7a: when the code payload itself contains a run of backticks as
// long as (or longer than) a plain ``` fence, that fence must widen so the
// embedded run cannot close the block early. CommonMark's rule is that a
// fence closes only at a run >= the OPENING run's length, so widening to
// longest-run-in-payload + 1 guarantees the payload can never close it.
func TestFence_GuardsAgainstBacktickPayload(t *testing.T) {
	pb := draft.Playbook{Title: "T", Sections: []draft.Section{{Heading: "S", Content: []draft.ContentItem{
		{Kind: "code", Lang: "markdown", Code: "before\n````\nnested fence\n````\nafter", ID: "a"},
	}}}}
	out := draft.Render(pb)
	if !strings.Contains(out, "`````markdown {id=a}\n") {
		t.Errorf("fence should widen to 5 backticks (longest run 4 + 1):\n%s", out)
	}
	if !ui.ValidatePlaybook(out) {
		t.Fatalf("rendered playbook with backtick payload failed ui.ValidatePlaybook (fence closed early):\n%s", out)
	}
}

// Code items with no id get a deterministic auto id; static items get no id.
func TestRender_AutoIDsAndStatic(t *testing.T) {
	pb := draft.Playbook{Title: "T", Sections: []draft.Section{{Heading: "S", Content: []draft.ContentItem{
		{Kind: "code", Lang: "bash", Code: "a"},
		{Kind: "code", Lang: "console", Code: "out", Static: true},
		{Kind: "code", Lang: "bash", Code: "b"},
	}}}}
	got := draft.Render(pb)
	if !strings.Contains(got, "```bash {id=step-1}\na\n```") {
		t.Errorf("first auto id wrong:\n%s", got)
	}
	if !strings.Contains(got, "```console {static}\nout\n```") {
		t.Errorf("static block should carry only {static}:\n%s", got)
	}
	if !strings.Contains(got, "```bash {id=step-2}\nb\n```") {
		t.Errorf("auto id must skip the static block:\n%s", got)
	}
}
