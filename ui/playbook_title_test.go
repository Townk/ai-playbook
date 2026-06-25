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
	r, title, subtitle, err := loadPlaybookSource(pb)
	if err != nil {
		t.Fatal(err)
	}
	if title != "Playbook — Y" {
		t.Fatalf("title = %q, want %q", title, "Playbook — Y")
	}
	if subtitle != "" {
		t.Fatalf("no front matter → subtitle must be empty, got %q", subtitle)
	}
	got, _ := io.ReadAll(r)
	if !strings.HasPrefix(string(got), "# Playbook — Y") {
		t.Fatalf("body must start at the H1 (preamble stripped), got %q", string(got))
	}

	noH1 := filepath.Join(dir, "transcript.md")
	if err := os.WriteFile(noH1, []byte("just a transcript\nno h1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r2, title2, subtitle2, err := loadPlaybookSource(noH1)
	if err != nil {
		t.Fatal(err)
	}
	if title2 != "" {
		t.Fatalf("no-H1 file must have empty title, got %q", title2)
	}
	if subtitle2 != "" {
		t.Fatalf("no-H1 file must have empty subtitle, got %q", subtitle2)
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

// renderBody drops the leading H1 (shown in the header) to avoid a double title,
// while m.md keeps the H1 for commit/save.
func TestRenderBodyDropsTitleWhenHeaderShowsIt(t *testing.T) {
	m := newModel("agent", "# Playbook — X\n\nstep one\n")
	m.title = "Playbook — X"
	body := m.renderBody()
	if strings.Contains(body, "# Playbook — X") {
		t.Errorf("renderBody must drop the H1 title row; got %q", body)
	}
	if !strings.Contains(body, "step one") {
		t.Errorf("renderBody must keep the content; got %q", body)
	}
	if !strings.Contains(m.md, "# Playbook — X") {
		t.Errorf("m.md must keep the H1 for commit; got %q", m.md)
	}
	// No title set → render the doc unchanged.
	m2 := newModel("agent", "# Heading\n\nbody\n")
	if m2.renderBody() != m2.md {
		t.Errorf("no title → renderBody must equal m.md")
	}
}

// fmPlaybook is a finalized playbook document with a leading front-matter block
// (name + description), then the H1 body — the shape served/run paths receive.
const fmPlaybook = "---\n" +
	"name: Playbook — Compiling an Android Application\n" +
	"description: Compile the app and fix the SDK path\n" +
	"category: Android / build\n" +
	"---\n\n" +
	"# Playbook — Compiling an Android Application\n\n## Step 1\nrun the build\n"

// TestLoadPlaybookDocument_FrontMatter verifies the load-time parser strips the
// front matter, takes the title from fm.Name, the subtitle from fm.Description,
// and returns an FM-free body that still carries the H1.
func TestLoadPlaybookDocument_FrontMatter(t *testing.T) {
	title, subtitle, body := loadPlaybookDocument(fmPlaybook)
	if title != "Playbook — Compiling an Android Application" {
		t.Errorf("title = %q", title)
	}
	if subtitle != "Compile the app and fix the SDK path" {
		t.Errorf("subtitle = %q", subtitle)
	}
	if strings.Contains(body, "---") || strings.Contains(body, "category:") {
		t.Errorf("body must be FM-free, got %q", body)
	}
	if !strings.HasPrefix(body, "# Playbook — Compiling an Android Application") {
		t.Errorf("body must start at the H1, got %q", body)
	}
}

// TestLoadPlaybookDocument_NoFrontMatter verifies a document without front matter
// degrades to H1-derived title and an empty subtitle (no regression).
func TestLoadPlaybookDocument_NoFrontMatter(t *testing.T) {
	doc := "preamble\n\n# Playbook — X\n\nstep\n"
	title, subtitle, body := loadPlaybookDocument(doc)
	if title != "Playbook — X" {
		t.Errorf("title = %q, want H1-derived", title)
	}
	if subtitle != "" {
		t.Errorf("no front matter → subtitle must be empty, got %q", subtitle)
	}
	if !strings.HasPrefix(body, "# Playbook — X") {
		t.Errorf("body must start at the H1 (preamble stripped), got %q", body)
	}
}

// TestLoadPlaybookSource_FrontMatter verifies the file loader surfaces the FM
// title + subtitle and a body free of YAML and the H1 (renderBody hides the H1).
func TestLoadPlaybookSource_FrontMatter(t *testing.T) {
	dir := t.TempDir()
	pb := filepath.Join(dir, "fm.md")
	if err := os.WriteFile(pb, []byte(fmPlaybook), 0o644); err != nil {
		t.Fatal(err)
	}
	r, title, subtitle, err := loadPlaybookSource(pb)
	if err != nil {
		t.Fatal(err)
	}
	if title != "Playbook — Compiling an Android Application" {
		t.Errorf("title = %q", title)
	}
	if subtitle != "Compile the app and fix the SDK path" {
		t.Errorf("subtitle = %q", subtitle)
	}
	got, _ := io.ReadAll(r)
	if strings.Contains(string(got), "category:") {
		t.Errorf("reader body must be FM-free, got %q", string(got))
	}
}

// TestModelFromFMPlaybook_HidesYAMLAndH1 verifies that a model carrying an FM
// playbook (title + subtitle set, body = FM-stripped) renders neither the raw
// YAML nor (via renderBody) the H1, and the header region shows the subtitle.
func TestModelFromFMPlaybook_HidesYAMLAndH1(t *testing.T) {
	title, subtitle, body := loadPlaybookDocument(fmPlaybook)
	m := newModel("agent", body)
	m.title = title
	m.subtitle = subtitle
	m.width, m.height = 100, 24

	rb := m.renderBody()
	if strings.Contains(rb, "category:") || strings.Contains(rb, "description:") {
		t.Errorf("renderBody must not contain raw YAML, got %q", rb)
	}
	if strings.Contains(rb, "# Playbook — Compiling") {
		t.Errorf("renderBody must hide the leading H1, got %q", rb)
	}
	if !strings.Contains(rb, "Step 1") {
		t.Errorf("renderBody must keep the content, got %q", rb)
	}

	// The rendered pane (header region) includes the subtitle caption.
	out := strings.Join(m.normalLines(), "\n")
	if !strings.Contains(out, "Compile the app and fix the SDK path") {
		t.Errorf("rendered header must include the subtitle, got:\n%s", out)
	}
	if !strings.Contains(out, "Playbook — Compiling an Android Application") {
		t.Errorf("rendered header must include the title")
	}
}

// TestSubtitleRowString verifies the subtitle row renders the description when set
// and is empty otherwise (no extra header row), and that subtitleRows tracks it.
func TestSubtitleRowString(t *testing.T) {
	m := newModel("agent", "")
	if m.subtitleRowString() != "" || m.subtitleRows() != 0 {
		t.Fatalf("no subtitle → empty row and 0 rows")
	}
	m.subtitle = "a one-line description"
	if !strings.Contains(m.subtitleRowString(), "a one-line description") {
		t.Fatalf("subtitle row must contain the description, got %q", m.subtitleRowString())
	}
	if m.subtitleRows() != 1 {
		t.Fatalf("subtitle set → subtitleRows must be 1, got %d", m.subtitleRows())
	}
}

// TestNoSubtitleNoExtraHeaderRow verifies a no-FM model emits no subtitle row in
// the rendered pane and keeps the body height it would have without a subtitle.
func TestNoSubtitleNoExtraHeaderRow(t *testing.T) {
	m := newModel("agent", "# Playbook — X\n\nstep\n")
	m.title = "Playbook — X"
	m.width, m.height = 100, 24
	withoutBody := m.body()

	m.subtitle = "desc"
	if m.body() != withoutBody-1 {
		t.Fatalf("subtitle must consume one body row: with=%d without=%d", m.body(), withoutBody)
	}
	if m.bodyTop() != 4 { // leading(1)+header(1)+subtitle(1)+top-pad(1)
		t.Fatalf("bodyTop with a subtitle (non-cached) = %d, want 4", m.bodyTop())
	}
}
