package ui

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadPlaybookSource_SetsTitleAndStrips verifies the run-from-file / cached-serve
// source loader sets the title from the playbook H1 and strips preamble; a file with
// no H1 is returned unchanged with an empty title.
func TestLoadPlaybookSource_SetsTitleAndStrips(t *testing.T) {
	dir := t.TempDir()

	pb := filepath.Join(dir, "playbook.md")
	if err := os.WriteFile(pb, []byte("intro preamble\n\n# Playbook — Y\n\nstep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, title, err := loadPlaybookSource(pb)
	if err != nil {
		t.Fatal(err)
	}
	if title != "Playbook — Y" {
		t.Fatalf("title = %q, want %q", title, "Playbook — Y")
	}
	got, _ := io.ReadAll(r)
	if !strings.HasPrefix(string(got), "# Playbook — Y") {
		t.Fatalf("body must start at the H1 (preamble stripped), got %q", string(got))
	}

	noH1 := filepath.Join(dir, "transcript.md")
	if err := os.WriteFile(noH1, []byte("just a transcript\nno h1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r2, title2, err := loadPlaybookSource(noH1)
	if err != nil {
		t.Fatal(err)
	}
	if title2 != "" {
		t.Fatalf("no-H1 file must have empty title, got %q", title2)
	}
	got2, _ := io.ReadAll(r2)
	if string(got2) != "just a transcript\nno h1\n" {
		t.Fatalf("no-H1 file must be unchanged, got %q", string(got2))
	}
}

// TestPlaybookHeading_StripsPreambleAndExtractsTitle verifies that preamble prose
// above the first H1 is removed and the heading text is returned as the title.
func TestPlaybookHeading_StripsPreambleAndExtractsTitle(t *testing.T) {
	md := "Here is some preamble prose.\nAnother line.\n\n" +
		"# Playbook — Compiling an Android Application\n\n" +
		"## Step 1\nbody text\n"
	title, body := playbookHeading(md)

	wantTitle := "Playbook — Compiling an Android Application"
	if title != wantTitle {
		t.Fatalf("title = %q, want %q", title, wantTitle)
	}
	if strings.HasPrefix(body, "Here is some preamble") {
		t.Fatalf("preamble was not stripped: %q", body)
	}
	if !strings.HasPrefix(body, "# Playbook — Compiling an Android Application") {
		t.Fatalf("body must start at the H1, got: %q", body)
	}
	if !strings.Contains(body, "## Step 1") {
		t.Fatalf("body must retain content after the H1, got: %q", body)
	}
}

// TestPlaybookHeading_NoH1Unchanged verifies that a transcript with no H1 is left
// untouched and the title is empty (do NOT strip a non-playbook).
func TestPlaybookHeading_NoH1Unchanged(t *testing.T) {
	md := "Just some narration.\nNo headings here.\n## Subheading is not H1\n"
	title, body := playbookHeading(md)
	if title != "" {
		t.Fatalf("title = %q, want empty", title)
	}
	if body != md {
		t.Fatalf("body must be unchanged, got: %q", body)
	}
}

// TestPlaybookHeading_TrimsHeadingSpace verifies leading/trailing whitespace in the
// heading text is trimmed.
func TestPlaybookHeading_TrimsHeadingSpace(t *testing.T) {
	md := "#    Playbook — Spaced Title   \n\nbody\n"
	title, body := playbookHeading(md)
	if title != "Playbook — Spaced Title" {
		t.Fatalf("title = %q, want trimmed", title)
	}
	if !strings.HasPrefix(body, "#    Playbook — Spaced Title") {
		t.Fatalf("body must start at the H1 line, got: %q", body)
	}
}

// TestHeaderUsesTitleWhenSet verifies header() renders the playbook title when set,
// and falls back to the default "ai-assist — <harness>" otherwise.
func TestHeaderUsesTitleWhenSet(t *testing.T) {
	m := newModel("agent", "")
	if got := m.header(); !strings.Contains(got, "ai-assist — agent") {
		t.Fatalf("default header must contain %q, got %q", "ai-assist — agent", got)
	}

	m.title = "Playbook — Compiling an Android Application"
	got := m.header()
	if !strings.Contains(got, "Playbook — Compiling an Android Application") {
		t.Fatalf("header must contain the title, got %q", got)
	}
	if strings.Contains(got, "ai-assist — agent") {
		t.Fatalf("header must NOT contain the default label when title set, got %q", got)
	}
	if !strings.Contains(got, "▓▓▓") {
		t.Fatalf("header must keep the ▓▓▓ styling, got %q", got)
	}
}

// TestFinalDraftEOFStripsAndSetsTitle verifies that a finalDraft EOF strips m.md to
// the H1 and sets m.title from the playbook heading.
func TestFinalDraftEOFStripsAndSetsTitle(t *testing.T) {
	m := newModel("agent", "preamble above\n\n# Playbook — X\n\nstep body\n")
	m.width, m.height = 80, 24
	m.finalDraft = true
	m.dirty = true

	nm, _ := m.Update(streamEventsMsg{eof: true})
	got := nm.(model)

	if got.title != "Playbook — X" {
		t.Fatalf("title = %q, want %q", got.title, "Playbook — X")
	}
	if strings.HasPrefix(got.md, "preamble above") {
		t.Fatalf("finalDraft EOF must strip preamble, md = %q", got.md)
	}
	if !strings.HasPrefix(got.md, "# Playbook — X") {
		t.Fatalf("md must start at the H1, got %q", got.md)
	}
}

// TestNonFinalDraftEOFDoesNotStrip verifies a non-finalDraft EOF (a troubleshoot
// transcript) leaves m.md and m.title untouched.
func TestNonFinalDraftEOFDoesNotStrip(t *testing.T) {
	orig := "preamble above\n\n# Some heading\n\nbody\n"
	m := newModel("agent", orig)
	m.width, m.height = 80, 24
	m.finalDraft = false
	m.dirty = true

	nm, _ := m.Update(streamEventsMsg{eof: true})
	got := nm.(model)

	if got.title != "" {
		t.Fatalf("non-finalDraft EOF must not set a title, got %q", got.title)
	}
	if got.md != orig {
		t.Fatalf("non-finalDraft EOF must not strip md, got %q", got.md)
	}
}
