package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/author"
	"github.com/Townk/ai-playbook/internal/frontmatter"
)

// fakeLookup is a deterministic env lookup over a fixed map (the driver seam in
// tests).
func fakeLookup(m map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		v, ok := m[name]
		return v, ok
	}
}

// TestFinalizeDoc_BackfillsFreshFrontMatter asserts the happy path: a raw doc that
// ALREADY carries front matter plus a `# Playbook — X` H1 referencing $ANDROID_HOME
// is re-finalized into a SINGLE fresh front-matter block carrying name=X +
// description/category/tags + env.ANDROID_HOME (value + why). The OLD front matter
// is gone (idempotent) and the literate body is preserved.
func TestFinalizeDoc_BackfillsFreshFrontMatter(t *testing.T) {
	raw := "---\nname: STALE\ndescription: an old description\ntags:\n  - stale\n---\n\n" +
		"# Playbook — X\n\nRun `echo $ANDROID_HOME` to confirm.\n"

	metaFn := func(string) (author.Metadata, error) {
		return author.Metadata{
			Description: "Build an Android app",
			Category:    "Android / build",
			Tags:        []string{"android", "gradle"},
			ImportantEnvVars: []author.EnvVarNote{
				{Name: "ANDROID_HOME", Why: "SDK location the Gradle build resolves against"},
			},
		}, nil
	}
	lookup := fakeLookup(map[string]string{"ANDROID_HOME": "/Users/me/Library/Android/sdk"})

	full, err := finalizeDoc(raw, metaFn, lookup, "2026-06-25", "/home/me/proj", "")
	if err != nil {
		t.Fatalf("finalizeDoc returned error: %v", err)
	}

	// Exactly one front-matter block: the leading "---\n" plus exactly one closing
	// "\n---\n". A nested/old block would push this count past 1.
	if got := strings.Count(full, "\n---\n"); got != 1 {
		t.Fatalf("expected exactly one front-matter block, got %d closing fences:\n%s", got, full)
	}
	if strings.Contains(full, "STALE") || strings.Contains(full, "an old description") {
		t.Errorf("old front matter not dropped (not idempotent):\n%s", full)
	}

	fm, body, ok := frontmatter.Parse(full)
	if !ok {
		t.Fatalf("output does not parse back as front matter:\n%s", full)
	}
	if fm.Name != "X" {
		t.Errorf("name = %q, want X", fm.Name)
	}
	if fm.Description != "Build an Android app" {
		t.Errorf("description = %q", fm.Description)
	}
	if fm.Category != "Android / build" {
		t.Errorf("category = %q", fm.Category)
	}
	if strings.Join(fm.Tags, ",") != "android,gradle" {
		t.Errorf("tags = %v", fm.Tags)
	}
	if fm.Created != "2026-06-25" {
		t.Errorf("created = %q", fm.Created)
	}
	if fm.ProjectRoot != "/home/me/proj" {
		t.Errorf("project_root = %q", fm.ProjectRoot)
	}
	ah, ok := fm.Env["ANDROID_HOME"]
	if !ok {
		t.Fatalf("env missing ANDROID_HOME: %+v", fm.Env)
	}
	if ah.Value != "/Users/me/Library/Android/sdk" {
		t.Errorf("ANDROID_HOME value = %q", ah.Value)
	}
	if ah.Why != "SDK location the Gradle build resolves against" {
		t.Errorf("ANDROID_HOME why = %q", ah.Why)
	}
	if body != "# Playbook — X\n\nRun `echo $ANDROID_HOME` to confirm.\n" {
		t.Errorf("body not preserved: %q", body)
	}
}

// TestFinalizeDoc_MetadataErrorStillAssembles asserts that a metaFn ERROR does not
// abort assembly: the output still carries name/env/provenance with empty model
// fields, and the error is surfaced to the caller for logging.
func TestFinalizeDoc_MetadataErrorStillAssembles(t *testing.T) {
	raw := "# Playbook — Y\n\nUse $ANDROID_HOME.\n"
	metaFn := func(string) (author.Metadata, error) {
		return author.Metadata{}, errors.New("classify boom")
	}
	lookup := fakeLookup(map[string]string{"ANDROID_HOME": "/sdk"})

	full, err := finalizeDoc(raw, metaFn, lookup, "2026-06-25", "/home/me/proj", "")
	if err == nil {
		t.Fatal("expected the metadata error to be surfaced")
	}
	fm, _, ok := frontmatter.Parse(full)
	if !ok {
		t.Fatalf("output should still be a valid front-matter doc:\n%s", full)
	}
	if fm.Name != "Y" {
		t.Errorf("name = %q, want Y", fm.Name)
	}
	if fm.Description != "" || fm.Category != "" || len(fm.Tags) != 0 {
		t.Errorf("model fields should be empty on error: %+v", fm)
	}
	if _, ok := fm.Env["ANDROID_HOME"]; !ok {
		t.Errorf("env should still be captured: %+v", fm.Env)
	}
	if fm.Created == "" || fm.ProjectRoot == "" {
		t.Errorf("provenance should be present: %+v", fm)
	}
}

// TestFinalizeDoc_NoHeading asserts a doc with NO H1 assembles with an empty name
// rather than crashing.
func TestFinalizeDoc_NoHeading(t *testing.T) {
	raw := "just a transcript, no heading here.\n"
	metaFn := func(string) (author.Metadata, error) { return author.Metadata{}, nil }

	full, err := finalizeDoc(raw, metaFn, fakeLookup(nil), "2026-06-25", "/home/me/proj", "")
	if err != nil {
		t.Fatalf("finalizeDoc returned error: %v", err)
	}
	fm, body, ok := frontmatter.Parse(full)
	if !ok {
		t.Fatalf("output should still be a valid front-matter doc:\n%s", full)
	}
	if fm.Name != "" {
		t.Errorf("name should be empty with no H1, got %q", fm.Name)
	}
	if body != raw {
		t.Errorf("body should be unchanged with no H1: %q", body)
	}
}

// TestFinalizeDoc_PreservesDependsOn verifies an existing depends_on front-matter
// field survives finalizeDoc's rebuild (which otherwise discards the parsed old
// front matter entirely).
func TestFinalizeDoc_PreservesDependsOn(t *testing.T) {
	raw := "---\nname: STALE\ndepends_on:\n  - dep-a\n---\n\n" +
		"# Playbook — X\n\nDo the thing.\n"

	metaFn := func(string) (author.Metadata, error) { return author.Metadata{}, nil }

	full, err := finalizeDoc(raw, metaFn, fakeLookup(nil), "2026-06-25", "/home/me/proj", "")
	if err != nil {
		t.Fatalf("finalizeDoc returned error: %v", err)
	}
	if !strings.Contains(full, "depends_on") || !strings.Contains(full, "dep-a") {
		t.Fatalf("depends_on not preserved:\n%s", full)
	}
	fm, _, ok := frontmatter.Parse(full)
	if !ok {
		t.Fatalf("output does not parse back as front matter:\n%s", full)
	}
	if strings.Join(fm.DependsOn, ",") != "dep-a" {
		t.Errorf("DependsOn = %v, want [dep-a]", fm.DependsOn)
	}
}

// TestFinalizeSummary verifies the one-line summary reads name/category/tags/env
// back from the assembled artifact.
func TestFinalizeSummary(t *testing.T) {
	full := "---\nname: X\ncategory: Android / build\ntags:\n  - b\n  - a\nenv:\n  ANDROID_HOME:\n    value: /sdk\n---\n\n# Playbook — X\n\nstep\n"
	got := finalizeSummary("/tmp/x.md", full)
	want := `finalized /tmp/x.md — name="X" category="Android / build" tags=[a,b] env=1 vars`
	if got != want {
		t.Errorf("summary = %q\nwant      %q", got, want)
	}
}

// TestFrontMatterBlock verifies --dry-run extracts only the leading front-matter
// block (through the closing fence), not the body.
func TestFrontMatterBlock(t *testing.T) {
	full := "---\nname: X\n---\n\n# Playbook — X\n\nstep\n"
	got := frontMatterBlock(full)
	want := "---\nname: X\n---\n"
	if got != want {
		t.Errorf("block = %q, want %q", got, want)
	}
	if frontMatterBlock("no front matter\n") != "" {
		t.Errorf("a doc without a leading fence should yield an empty block")
	}
}
