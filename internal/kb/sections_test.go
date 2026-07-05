package kb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readFile reads a file for assertions, failing the test on error.
func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

// ── Routing: the four kinds land in the right file + section ──────────────────

func TestAppend_GlobalSystemAndUser(t *testing.T) {
	root := t.TempDir()
	if err := Append(root, "", KindSystem, "", "ripgrep is installed"); err != nil {
		t.Fatal(err)
	}
	if err := Append(root, "", KindUser, "", "prefers vim"); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, GlobalPath(root))
	want := "## System\n- ripgrep is installed\n\n## User\n- prefers vim\n"
	if got != want {
		t.Fatalf("global file =\n%q\nwant\n%q", got, want)
	}
	// A global write must never create a project file.
	if _, err := os.Stat(Path(root, "/p")); !os.IsNotExist(err) {
		t.Fatalf("global write created a project file (err=%v)", err)
	}
}

func TestAppend_ProjectEnvironment(t *testing.T) {
	root := t.TempDir()
	if err := Append(root, "/p", KindEnvironment, "", "uses bazel"); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, Path(root, "/p"))
	want := "<!-- meta: project-root: /p -->\n\n## Environment\n- uses bazel\n"
	if got != want {
		t.Fatalf("project file =\n%q\nwant\n%q", got, want)
	}
	// A project write must never create the global file.
	if _, err := os.Stat(GlobalPath(root)); !os.IsNotExist(err) {
		t.Fatalf("project write created the global file (err=%v)", err)
	}
}

func TestAppend_ProjectTopicSubsection(t *testing.T) {
	root := t.TempDir()
	if err := Append(root, "/p", KindEnvironment, "", "uses bazel"); err != nil {
		t.Fatal(err)
	}
	if err := Append(root, "/p", KindTopic, "Database", "pg needs PGPASSWORD"); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, Path(root, "/p"))
	want := "<!-- meta: project-root: /p -->\n\n" +
		"## Environment\n- uses bazel\n\n" +
		"## Topics\n### Database\n- pg needs PGPASSWORD\n"
	if got != want {
		t.Fatalf("project file =\n%q\nwant\n%q", got, want)
	}
}

// A case-insensitive topic match reuses the existing subsection, preserving its
// STORED casing (the first submitted casing).
func TestAppend_TopicCaseInsensitiveMatchPreservesStoredCasing(t *testing.T) {
	root := t.TempDir()
	if err := Append(root, "/p", KindTopic, "Database", "first"); err != nil {
		t.Fatal(err)
	}
	if err := Append(root, "/p", KindTopic, "database", "second"); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, Path(root, "/p"))
	if strings.Count(got, "### ") != 1 {
		t.Fatalf("expected one topic subsection, got:\n%s", got)
	}
	if !strings.Contains(got, "### Database\n") {
		t.Fatalf("stored casing not preserved:\n%s", got)
	}
	if !strings.Contains(got, "- first\n") || !strings.Contains(got, "- second\n") {
		t.Fatalf("both facts should land under the one subsection:\n%s", got)
	}
}

// ── Write-dedup ───────────────────────────────────────────────────────────────

func TestAppend_DedupExact(t *testing.T) {
	root := t.TempDir()
	if err := Append(root, "/p", KindEnvironment, "", "uses bazel"); err != nil {
		t.Fatal(err)
	}
	if err := Append(root, "/p", KindEnvironment, "", "uses bazel"); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, Path(root, "/p"))
	if strings.Count(got, "- uses bazel") != 1 {
		t.Fatalf("exact duplicate should be skipped:\n%s", got)
	}
}

func TestAppend_DedupNormalized(t *testing.T) {
	root := t.TempDir()
	if err := Append(root, "/p", KindEnvironment, "", "uses bazel"); err != nil {
		t.Fatal(err)
	}
	// Case + whitespace variant: normalized-equal, must be skipped.
	if err := Append(root, "/p", KindEnvironment, "", "  USES   bazel  "); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, Path(root, "/p"))
	if n := strings.Count(got, "uses bazel"); n != 1 {
		t.Fatalf("normalized duplicate should be skipped, got %d bullets:\n%s", n, got)
	}
}

// Dedup is scoped to the target section/subsection: the same text under a
// different topic is NOT a duplicate.
func TestAppend_DedupScopedPerSubsection(t *testing.T) {
	root := t.TempDir()
	if err := Append(root, "/p", KindTopic, "alpha", "shared fact"); err != nil {
		t.Fatal(err)
	}
	if err := Append(root, "/p", KindTopic, "beta", "shared fact"); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, Path(root, "/p"))
	if n := strings.Count(got, "- shared fact"); n != 2 {
		t.Fatalf("cross-subsection text should not dedup, got %d:\n%s", n, got)
	}
}

// ── Legacy lazy migration ─────────────────────────────────────────────────────

// A legacy unsectioned project file is READ as if its bullets lived under
// ## Environment (read-side only — the file on disk is untouched).
func TestLoadProject_LegacyReadsAsEnvironment(t *testing.T) {
	root := t.TempDir()
	writeLegacy(t, root, "/p", "- old fact\n- another\n")
	got := string(LoadProject(root, "/p"))
	want := "## Environment\n- old fact\n- another\n"
	if got != want {
		t.Fatalf("legacy read =\n%q\nwant\n%q", got, want)
	}
	// The file on disk is unchanged by a read.
	if raw := readFile(t, Path(root, "/p")); raw != "- old fact\n- another\n" {
		t.Fatalf("read mutated the legacy file: %q", raw)
	}
}

// An already-sectioned file reads back verbatim (no double-wrap).
func TestLoadProject_SectionedReadsVerbatim(t *testing.T) {
	root := t.TempDir()
	if err := Append(root, "/p", KindEnvironment, "", "uses bazel"); err != nil {
		t.Fatal(err)
	}
	raw := readFile(t, Path(root, "/p"))
	if got := string(LoadProject(root, "/p")); got != raw {
		t.Fatalf("sectioned read not verbatim:\n%q\nvs on-disk\n%q", got, raw)
	}
}

// The first sectioned write rewrites a legacy file into sectioned form,
// preserving every legacy bullet under ## Environment and adding the meta line.
func TestAppend_FirstSectionedWriteMigratesLegacy(t *testing.T) {
	root := t.TempDir()
	writeLegacy(t, root, "/p", "- old fact\n- another\n")
	if err := Append(root, "/p", KindEnvironment, "", "new fact"); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, Path(root, "/p"))
	want := "<!-- meta: project-root: /p -->\n\n" +
		"## Environment\n- old fact\n- another\n- new fact\n"
	if got != want {
		t.Fatalf("migrated file =\n%q\nwant\n%q", got, want)
	}
}

// Migration also fires when the first sectioned write is a topic: legacy bullets
// are preserved under ## Environment and the topic is added under ## Topics.
func TestAppend_FirstTopicWriteMigratesLegacy(t *testing.T) {
	root := t.TempDir()
	writeLegacy(t, root, "/p", "- legacy env\n")
	if err := Append(root, "/p", KindTopic, "db", "a lesson"); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, Path(root, "/p"))
	want := "<!-- meta: project-root: /p -->\n\n" +
		"## Environment\n- legacy env\n\n" +
		"## Topics\n### db\n- a lesson\n"
	if got != want {
		t.Fatalf("migrated file =\n%q\nwant\n%q", got, want)
	}
}

// Migration of a legacy file with hand-written prose lines and a blank-line
// paragraph break among the bullets pins the non-bullet behavior as INTENTIONAL:
// (a) every bullet is preserved under ## Environment, (b) every prose line's
// TEXT is preserved (no content loss), (c) the documented reflow applies —
// prose and bullets folded in their original order, blank lines dropped
// (canonical spacing is re-emitted by render).
func TestAppend_MigrationPreservesProseDropsBlankLines(t *testing.T) {
	root := t.TempDir()
	legacy := "Setup notes written by hand.\n" +
		"- old fact\n" +
		"\n" +
		"A second paragraph of prose.\n" +
		"- another fact\n"
	writeLegacy(t, root, "/p", legacy)
	if err := Append(root, "/p", KindEnvironment, "", "new fact"); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, Path(root, "/p"))
	want := "<!-- meta: project-root: /p -->\n\n" +
		"## Environment\n" +
		"Setup notes written by hand.\n" +
		"- old fact\n" +
		"A second paragraph of prose.\n" +
		"- another fact\n" +
		"- new fact\n"
	if got != want {
		t.Fatalf("migrated file =\n%q\nwant\n%q", got, want)
	}
}

// A dedup no-op against a legacy bullet must NOT rewrite the file (idempotent).
func TestAppend_DedupAgainstLegacyLeavesFileUntouched(t *testing.T) {
	root := t.TempDir()
	writeLegacy(t, root, "/p", "- uses bazel\n")
	if err := Append(root, "/p", KindEnvironment, "", "uses bazel"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, Path(root, "/p")); got != "- uses bazel\n" {
		t.Fatalf("dedup no-op rewrote the legacy file: %q", got)
	}
}

// ── Meta write-once ───────────────────────────────────────────────────────────

func TestAppend_MetaWrittenOnce(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"a", "b", "c"} {
		if err := Append(root, "/proj/root", KindEnvironment, "", f); err != nil {
			t.Fatal(err)
		}
	}
	got := readFile(t, Path(root, "/proj/root"))
	if n := strings.Count(got, "<!-- meta:"); n != 1 {
		t.Fatalf("meta line written %d times, want 1:\n%s", n, got)
	}
	name, ok := ProjectName(got)
	if !ok || name != "/proj/root" {
		t.Fatalf("ProjectName = (%q,%v), want (/proj/root,true)", name, ok)
	}
}

func TestProjectName_AbsentIsFalse(t *testing.T) {
	if name, ok := ProjectName("## Environment\n- x\n"); ok {
		t.Fatalf("ProjectName should be false without a meta line, got %q", name)
	}
}

// ── Contract violations ───────────────────────────────────────────────────────

func TestAppend_ContractViolations(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		name        string
		projectRoot string
		kind        Kind
		topic       string
		fact        string
		wantErr     bool
	}{
		{"unknown kind", "/p", Kind("bogus"), "", "f", true},
		{"empty kind", "/p", Kind(""), "", "f", true},
		{"topic with non-topic kind", "/p", KindEnvironment, "db", "f", true},
		{"topic with system kind", "", KindSystem, "db", "f", true},
		{"missing topic for topic kind", "/p", KindTopic, "", "f", true},
		{"project kind without project root", "", KindEnvironment, "", "f", true},
		{"topic kind without project root", "", KindTopic, "db", "f", true},
		{"global kind allows empty project root", "", KindSystem, "", "f", false},
		{"valid environment", "/p", KindEnvironment, "", "f", false},
		{"valid topic", "/p", KindTopic, "db", "f", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Append(root, c.projectRoot, c.kind, c.topic, c.fact)
			if (err != nil) != c.wantErr {
				t.Fatalf("Append err = %v, wantErr = %v", err, c.wantErr)
			}
		})
	}
}

func TestAppend_EmptyFactIsNoop(t *testing.T) {
	root := t.TempDir()
	if err := Append(root, "/p", KindEnvironment, "", "   \n"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(Path(root, "/p")); !os.IsNotExist(err) {
		t.Fatalf("empty fact should not create a file (err=%v)", err)
	}
}

// ── Global / project separation ───────────────────────────────────────────────

func TestLoadGlobal_ReadsGlobalFileOnly(t *testing.T) {
	root := t.TempDir()
	if err := Append(root, "", KindSystem, "", "global truth"); err != nil {
		t.Fatal(err)
	}
	if err := Append(root, "/p", KindEnvironment, "", "project truth"); err != nil {
		t.Fatal(err)
	}
	g := string(LoadGlobal(root))
	if !strings.Contains(g, "global truth") || strings.Contains(g, "project truth") {
		t.Fatalf("LoadGlobal leaked project content:\n%s", g)
	}
	p := string(LoadProject(root, "/p"))
	if !strings.Contains(p, "project truth") || strings.Contains(p, "global truth") {
		t.Fatalf("LoadProject leaked global content:\n%s", p)
	}
}

func TestGlobalPath_Layout(t *testing.T) {
	if got := GlobalPath("/root"); got != "/root/knowledge.md" {
		t.Fatalf("GlobalPath = %q, want /root/knowledge.md", got)
	}
}

// writeLegacy writes a flat, unsectioned legacy KB file for projectRoot.
func writeLegacy(t *testing.T, root, projectRoot, content string) {
	t.Helper()
	p := Path(root, projectRoot)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
