package playbook

import (
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/ui"
)

func sample() Playbook {
	return Playbook{
		Title: "Restore the Gradle wrapper",
		Intro: "You ran `gg build` and it failed.",
		Sections: []Section{
			{Heading: "Goal & error", Content: []ContentItem{
				{Kind: "text", Text: "The wrapper jar is missing."},
				{Kind: "code", Lang: "console", Code: "Error: GradleWrapperMain", Static: true},
				{Kind: "callout", Text: "This directory is a git repo."},
			}},
			{Heading: "Fix", Content: []ContentItem{
				{Kind: "text", Text: "Restore the jar:"},
				{Kind: "code", Lang: "bash", Code: "gradle wrapper", ID: "fix"},
				{Kind: "text", Text: "Now the build works."},
			}},
		},
		Verify: &Step{Lang: "bash", Code: "gg build", Needs: []string{"fix"}},
		Meta:   Meta{Description: "Restore the wrapper", ProjectBound: true},
	}
}

func TestRender_Golden(t *testing.T) {
	got := Render(sample())
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
	if !ui.ValidatePlaybook(Render(sample())) {
		t.Fatalf("rendered playbook failed ui.ValidatePlaybook:\n%s", Render(sample()))
	}
}

// A callout renders as a GitHub-style admonition (`> [!TYPE]`) the viewer styles;
// the type comes from Admonition, defaulting to NOTE when omitted.
func TestRender_CalloutAdmonition(t *testing.T) {
	pb := Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
		{Kind: "callout", Admonition: "warning", Text: "destroys data"},
		{Kind: "callout", Text: "an aside"},
		{Kind: "code", Lang: "bash", Code: "x", ID: "a"},
	}}}}
	got := Render(pb)
	if !strings.Contains(got, "> [!WARNING]\n> destroys data") {
		t.Errorf("typed callout must emit its admonition marker:\n%s", got)
	}
	if !strings.Contains(got, "> [!NOTE]\n> an aside") {
		t.Errorf("untyped callout must default to NOTE:\n%s", got)
	}
}

func TestRender_FileBlock(t *testing.T) {
	pb := Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
		{Kind: "code", Lang: "go", File: "cmd/x/main.go", ID: "new", Code: "package main\n"}}}}}
	out := Render(pb)
	if !strings.Contains(out, "file=cmd/x/main.go") {
		t.Fatalf("fence missing file= tag:\n%s", out)
	}
}

// Code items with no id get a deterministic auto id; static items get no id.
func TestRender_AutoIDsAndStatic(t *testing.T) {
	pb := Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
		{Kind: "code", Lang: "bash", Code: "a"},
		{Kind: "code", Lang: "console", Code: "out", Static: true},
		{Kind: "code", Lang: "bash", Code: "b"},
	}}}}
	got := Render(pb)
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
