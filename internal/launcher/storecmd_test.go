package launcher

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Townk/ai-playbook/internal/store"
)

// fixedNow anchors age-sensitive tests to a stable point in time.
var fixedNow = time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)

// sampleMeta is the canonical fixture — its Name/Description/Category/Tags
// produce the display string "Build — compile · ci · go" that the FZF layout
// test asserts.
var sampleMeta = store.Meta{
	Slug:        "build-app",
	Name:        "Build",
	Description: "compile",
	Category:    "ci",
	Tags:        []string{"go"},
	Path:        "/store/build-app.md",
	Created:     fixedNow.Add(-3 * 24 * time.Hour),
}

// withIndexFn replaces indexFn for the duration of t.
func withIndexFn(t *testing.T, fn func() ([]store.Meta, error)) {
	t.Helper()
	old := indexFn
	indexFn = fn
	t.Cleanup(func() { indexFn = old })
}

// withSearchFn replaces searchFn for the duration of t.
func withSearchFn(t *testing.T, fn func(string) ([]store.Meta, error)) {
	t.Helper()
	old := searchFn
	searchFn = fn
	t.Cleanup(func() { searchFn = old })
}

// withArgs replaces os.Args for the duration of t.
func withArgs(t *testing.T, args []string) {
	t.Helper()
	old := os.Args
	os.Args = args
	t.Cleanup(func() { os.Args = old })
}

// ---- formatFuzzy — THE load-bearing test; FZF pairing depends on this layout ----

func TestFormatFuzzy_FieldLayout(t *testing.T) {
	out := formatFuzzy([]store.Meta{sampleMeta})
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("want 1 line, got %d:\n%s", len(lines), out)
	}
	fields := strings.Split(lines[0], "\x1f")
	if len(fields) != 3 {
		t.Fatalf("want 3 \\x1f-delimited fields, got %d: %q", len(fields), lines[0])
	}
	wantDisplay := "Build — compile · ci · go"
	if fields[0] != wantDisplay {
		t.Errorf("field1 (display) = %q, want %q", fields[0], wantDisplay)
	}
	if fields[1] != sampleMeta.Slug {
		t.Errorf("field2 (slug) = %q, want %q", fields[1], sampleMeta.Slug)
	}
	if fields[2] != sampleMeta.Path {
		t.Errorf("field3 (path) = %q, want %q", fields[2], sampleMeta.Path)
	}
}

func TestFormatFuzzy_EmptyOptionalFields(t *testing.T) {
	m := store.Meta{
		Slug: "bare",
		Name: "Bare",
		Path: "/store/bare.md",
		// Description, Category, Tags all zero — no separators should appear.
	}
	out := formatFuzzy([]store.Meta{m})
	// Three fields must still be present.
	line := strings.TrimRight(out, "\n")
	fields := strings.Split(line, "\x1f")
	if len(fields) != 3 {
		t.Fatalf("want 3 fields, got %d: %q", len(fields), line)
	}
	// Display should be just the name — no stray "—" or "·".
	if fields[0] != "Bare" {
		t.Errorf("display = %q, want %q", fields[0], "Bare")
	}
	if strings.Contains(fields[0], "—") || strings.Contains(fields[0], "·") {
		t.Errorf("display has unexpected separator characters: %q", fields[0])
	}
}

func TestFormatFuzzy_MultiTags_CommaJoined(t *testing.T) {
	m := store.Meta{
		Slug: "multi",
		Name: "Multi",
		Tags: []string{"alpha", "beta"},
		Path: "/store/multi.md",
	}
	out := formatFuzzy([]store.Meta{m})
	line := strings.TrimRight(out, "\n")
	fields := strings.Split(line, "\x1f")
	if len(fields) != 3 {
		t.Fatalf("want 3 fields, got %d: %q", len(fields), line)
	}
	if !strings.Contains(fields[0], "alpha, beta") {
		t.Errorf("display %q: expected comma-joined tags 'alpha, beta'", fields[0])
	}
}

func TestFormatFuzzy_MultipleLines(t *testing.T) {
	m2 := store.Meta{Slug: "other", Name: "Other", Path: "/store/other.md"}
	out := formatFuzzy([]store.Meta{sampleMeta, m2})
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d", len(lines))
	}
}

// ---- formatHuman ----

func TestFormatHuman_ContainsNameAndDescription(t *testing.T) {
	out := formatHuman([]store.Meta{sampleMeta})
	if !strings.Contains(out, "Build") {
		t.Errorf("formatHuman: missing name 'Build' in output:\n%s", out)
	}
	if !strings.Contains(out, "compile") {
		t.Errorf("formatHuman: missing description 'compile' in output:\n%s", out)
	}
	if !strings.Contains(out, "NAME") {
		t.Errorf("formatHuman: missing header 'NAME' in output:\n%s", out)
	}
}

func TestFormatHuman_AlignedColumns(t *testing.T) {
	metas := []store.Meta{
		{Name: "Short", Description: "A", Category: "x", Created: fixedNow.Add(-time.Hour)},
		{Name: "VeryLongName", Description: "B", Category: "y", Created: fixedNow.Add(-2 * time.Hour)},
	}
	out := formatHuman(metas)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	// Header + 2 data rows.
	if len(lines) < 3 {
		t.Fatalf("want ≥3 lines, got %d:\n%s", len(lines), out)
	}
	if !strings.Contains(lines[1], "Short") {
		t.Errorf("row 1 missing 'Short': %q", lines[1])
	}
	if !strings.Contains(lines[2], "VeryLongName") {
		t.Errorf("row 2 missing 'VeryLongName': %q", lines[2])
	}
}

// ---- formatJSON ----

func TestFormatJSON_RoundTrip(t *testing.T) {
	out, err := formatJSON([]store.Meta{sampleMeta})
	if err != nil {
		t.Fatalf("formatJSON: %v", err)
	}
	var got []store.Meta
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 element, got %d", len(got))
	}
	if got[0].Slug != sampleMeta.Slug {
		t.Errorf("Slug = %q, want %q", got[0].Slug, sampleMeta.Slug)
	}
	if got[0].Name != sampleMeta.Name {
		t.Errorf("Name = %q, want %q", got[0].Name, sampleMeta.Name)
	}
	if got[0].Category != sampleMeta.Category {
		t.Errorf("Category = %q, want %q", got[0].Category, sampleMeta.Category)
	}
}

// ---- ListMain: format flag parsing ----

func TestListMain_DefaultHuman(t *testing.T) {
	withArgs(t, []string{"ai-playbook", "list"})
	withIndexFn(t, func() ([]store.Meta, error) { return []store.Meta{sampleMeta}, nil })
	if code := ListMain(); code != 0 {
		t.Errorf("ListMain default format: want 0, got %d", code)
	}
}

func TestListMain_FormatHuman(t *testing.T) {
	withArgs(t, []string{"ai-playbook", "list", "--format", "human"})
	withIndexFn(t, func() ([]store.Meta, error) { return []store.Meta{sampleMeta}, nil })
	if code := ListMain(); code != 0 {
		t.Errorf("ListMain --format human: want 0, got %d", code)
	}
}

func TestListMain_FormatFuzzy(t *testing.T) {
	withArgs(t, []string{"ai-playbook", "list", "--format", "fuzzy-data-source"})
	withIndexFn(t, func() ([]store.Meta, error) { return []store.Meta{sampleMeta}, nil })
	if code := ListMain(); code != 0 {
		t.Errorf("ListMain --format fuzzy-data-source: want 0, got %d", code)
	}
}

func TestListMain_FormatJSON(t *testing.T) {
	withArgs(t, []string{"ai-playbook", "list", "--format", "json"})
	withIndexFn(t, func() ([]store.Meta, error) { return []store.Meta{sampleMeta}, nil })
	if code := ListMain(); code != 0 {
		t.Errorf("ListMain --format json: want 0, got %d", code)
	}
}

func TestListMain_UnknownFormat_Exit2(t *testing.T) {
	withArgs(t, []string{"ai-playbook", "list", "--format", "xml"})
	withIndexFn(t, func() ([]store.Meta, error) { return []store.Meta{sampleMeta}, nil })
	if code := ListMain(); code != 2 {
		t.Errorf("ListMain unknown format: want exit 2, got %d", code)
	}
}

func TestListMain_EmptyStore_Exit0(t *testing.T) {
	withArgs(t, []string{"ai-playbook", "list"})
	withIndexFn(t, func() ([]store.Meta, error) { return nil, nil })
	if code := ListMain(); code != 0 {
		t.Errorf("ListMain empty store: want exit 0, got %d", code)
	}
}

// ---- SearchMain ----

func TestSearchMain_MissingQuery_Exit2(t *testing.T) {
	withArgs(t, []string{"ai-playbook", "search"})
	if code := SearchMain(); code != 2 {
		t.Errorf("SearchMain no query: want exit 2, got %d", code)
	}
}

func TestSearchMain_WithQuery(t *testing.T) {
	withArgs(t, []string{"ai-playbook", "search", "build"})
	withSearchFn(t, func(q string) ([]store.Meta, error) {
		if q != "build" {
			t.Errorf("searchFn: want query %q, got %q", "build", q)
		}
		return []store.Meta{sampleMeta}, nil
	})
	if code := SearchMain(); code != 0 {
		t.Errorf("SearchMain with query: want 0, got %d", code)
	}
}

func TestSearchMain_UnknownFormat_Exit2(t *testing.T) {
	withArgs(t, []string{"ai-playbook", "search", "--format", "xml", "build"})
	if code := SearchMain(); code != 2 {
		t.Errorf("SearchMain unknown format: want exit 2, got %d", code)
	}
}

func TestSearchMain_EmptyResults_Exit0(t *testing.T) {
	withArgs(t, []string{"ai-playbook", "search", "notfound"})
	withSearchFn(t, func(string) ([]store.Meta, error) { return nil, nil })
	if code := SearchMain(); code != 0 {
		t.Errorf("SearchMain empty results: want exit 0, got %d", code)
	}
}

func TestSearchMain_QueryPassedToSearchFn(t *testing.T) {
	var gotQuery string
	withArgs(t, []string{"ai-playbook", "search", "--format", "json", "my-query"})
	withSearchFn(t, func(q string) ([]store.Meta, error) {
		gotQuery = q
		return []store.Meta{sampleMeta}, nil
	})
	SearchMain()
	if gotQuery != "my-query" {
		t.Errorf("searchFn received query %q, want %q", gotQuery, "my-query")
	}
}

// ---- withPathForFn seam helper ----

// withPathForFn replaces pathForFn for the duration of t.
func withPathForFn(t *testing.T, fn func(string) (string, bool)) {
	t.Helper()
	old := pathForFn
	pathForFn = fn
	t.Cleanup(func() { pathForFn = old })
}

// withEditorSpawn replaces editorSpawn for the duration of t.
func withEditorSpawn(t *testing.T, fn func(string, string) error) {
	t.Helper()
	old := editorSpawn
	editorSpawn = fn
	t.Cleanup(func() { editorSpawn = old })
}

// withEnv sets an env var for the duration of t and restores the original value.
func withEnv(t *testing.T, key, val string) {
	t.Helper()
	old, hadOld := os.LookupEnv(key)
	if val == "" {
		os.Unsetenv(key)
	} else {
		os.Setenv(key, val)
	}
	t.Cleanup(func() {
		if hadOld {
			os.Setenv(key, old)
		} else {
			os.Unsetenv(key)
		}
	})
}

// ---- resolveShow ----

func TestResolveShow_KnownSlug(t *testing.T) {
	const wantPath = "/store/build-app.md"
	withPathForFn(t, func(slug string) (string, bool) {
		if slug == "build-app" {
			return wantPath, true
		}
		return "", false
	})
	path, ok := resolveShow("build-app")
	if !ok {
		t.Fatal("resolveShow: want ok=true for known slug")
	}
	if path != wantPath {
		t.Errorf("resolveShow: path = %q, want %q", path, wantPath)
	}
}

func TestResolveShow_UnknownSlug(t *testing.T) {
	withPathForFn(t, func(string) (string, bool) { return "", false })
	_, ok := resolveShow("no-such-slug")
	if ok {
		t.Error("resolveShow: want ok=false for unknown slug")
	}
}

// ---- ShowMain ----

func TestShowMain_MissingArg_Exit2(t *testing.T) {
	withArgs(t, []string{"ai-playbook", "show"})
	if code := ShowMain(); code != 2 {
		t.Errorf("ShowMain missing arg: want exit 2, got %d", code)
	}
}

func TestShowMain_UnknownSlug_Exit1(t *testing.T) {
	withArgs(t, []string{"ai-playbook", "show", "no-such-slug"})
	withPathForFn(t, func(string) (string, bool) { return "", false })
	if code := ShowMain(); code != 1 {
		t.Errorf("ShowMain unknown slug: want exit 1, got %d", code)
	}
}

// TestShowMain_KnownSlug_ReshapesArgs verifies that ShowMain reshapes os.Args
// to {bin, "run", path} before delegating to ui.Main. We intercept via the
// uiMainFn seam so no TTY is required.
func TestShowMain_KnownSlug_ReshapesArgs(t *testing.T) {
	const wantPath = "/store/build-app.md"
	withArgs(t, []string{"ai-playbook", "show", "build-app"})
	withPathForFn(t, func(slug string) (string, bool) {
		if slug == "build-app" {
			return wantPath, true
		}
		return "", false
	})

	var capturedArgs []string
	withUIMainFn(t, func() int {
		capturedArgs = append([]string{}, os.Args...)
		return 0
	})

	code := ShowMain()
	if code != 0 {
		t.Errorf("ShowMain known slug: want exit 0, got %d", code)
	}
	if len(capturedArgs) < 3 {
		t.Fatalf("ShowMain: os.Args not reshaped; got %v", capturedArgs)
	}
	if capturedArgs[1] != "run" {
		t.Errorf("ShowMain: os.Args[1] = %q, want %q", capturedArgs[1], "run")
	}
	if capturedArgs[2] != wantPath {
		t.Errorf("ShowMain: os.Args[2] = %q, want %q", capturedArgs[2], wantPath)
	}
	// No --cached flag: read-only render must NOT show the cached badge.
	for _, a := range capturedArgs {
		if a == "--cached" {
			t.Errorf("ShowMain: unexpected --cached in reshaped args: %v", capturedArgs)
		}
	}
}

// withUIMainFn replaces uiMainFn for the duration of t.
func withUIMainFn(t *testing.T, fn func() int) {
	t.Helper()
	old := uiMainFn
	uiMainFn = fn
	t.Cleanup(func() { uiMainFn = old })
}

// withSetSourcePathFn replaces setSourcePathFn for the duration of t.
func withSetSourcePathFn(t *testing.T, fn func(string)) {
	t.Helper()
	old := setSourcePathFn
	setSourcePathFn = fn
	t.Cleanup(func() { setSourcePathFn = old })
}

// TestShowMain_SetsSourcePath verifies that ShowMain calls setSourcePathFn with
// the resolved store path (the real .md file) so the viewer model can offer an
// [edit] button for file-backed playbooks.
func TestShowMain_SetsSourcePath(t *testing.T) {
	const wantPath = "/store/known.md"
	withArgs(t, []string{"ai-playbook", "show", "known-slug"})
	withPathForFn(t, func(slug string) (string, bool) {
		if slug == "known-slug" {
			return wantPath, true
		}
		return "", false
	})
	withUIMainFn(t, func() int { return 0 })

	var got string
	withSetSourcePathFn(t, func(p string) { got = p })

	code := ShowMain()
	if code != 0 {
		t.Errorf("ShowMain: want exit 0, got %d", code)
	}
	if got != wantPath {
		t.Fatalf("ShowMain must call setSourcePathFn with the store path; got %q, want %q", got, wantPath)
	}
}

// ---- EditMain ----

func TestEditMain_MissingArg_Exit2(t *testing.T) {
	withArgs(t, []string{"ai-playbook", "edit"})
	if code := EditMain(); code != 2 {
		t.Errorf("EditMain missing arg: want exit 2, got %d", code)
	}
}

func TestEditMain_NoEditor_Exit1(t *testing.T) {
	withArgs(t, []string{"ai-playbook", "edit", "build-app"})
	withEnv(t, "EDITOR", "")
	if code := EditMain(); code != 1 {
		t.Errorf("EditMain no EDITOR: want exit 1, got %d", code)
	}
}

func TestEditMain_UnknownSlug_Exit1(t *testing.T) {
	withArgs(t, []string{"ai-playbook", "edit", "no-such-slug"})
	withEnv(t, "EDITOR", "vi")
	withPathForFn(t, func(string) (string, bool) { return "", false })
	if code := EditMain(); code != 1 {
		t.Errorf("EditMain unknown slug: want exit 1, got %d", code)
	}
}

func TestEditMain_HappyPath(t *testing.T) {
	const wantPath = "/store/build-app.md"
	withArgs(t, []string{"ai-playbook", "edit", "build-app"})
	withEnv(t, "EDITOR", "vi")
	withPathForFn(t, func(slug string) (string, bool) {
		if slug == "build-app" {
			return wantPath, true
		}
		return "", false
	})

	var gotEditor, gotPath string
	withEditorSpawn(t, func(editor, path string) error {
		gotEditor = editor
		gotPath = path
		return nil
	})

	code := EditMain()
	if code != 0 {
		t.Errorf("EditMain happy path: want exit 0, got %d", code)
	}
	if gotEditor != "vi" {
		t.Errorf("editorSpawn: editor = %q, want %q", gotEditor, "vi")
	}
	if gotPath != wantPath {
		t.Errorf("editorSpawn: path = %q, want %q", gotPath, wantPath)
	}
}
