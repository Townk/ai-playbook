package launcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/kb"
)

// withKBConfigFn replaces kbConfigFn for the duration of t.
func withKBConfigFn(t *testing.T, root string, budget int) {
	t.Helper()
	old := kbConfigFn
	kbConfigFn = func() (string, int, error) { return root, budget, nil }
	t.Cleanup(func() { kbConfigFn = old })
}

// withKBConfigFnErr replaces kbConfigFn with one that always errors.
func withKBConfigFnErr(t *testing.T, errMsg string) {
	t.Helper()
	old := kbConfigFn
	kbConfigFn = func() (string, int, error) { return "", 0, errFor(errMsg) }
	t.Cleanup(func() { kbConfigFn = old })
}

type kbTestErr string

func (e kbTestErr) Error() string { return string(e) }
func errFor(msg string) error     { return kbTestErr(msg) }

// withKBProjectRootFn replaces kbProjectRootFn for the duration of t.
func withKBProjectRootFn(t *testing.T, root string) {
	t.Helper()
	old := kbProjectRootFn
	kbProjectRootFn = func() string { return root }
	t.Cleanup(func() { kbProjectRootFn = old })
}

// ---- KBMain dispatch ----

func TestKBMain_NoSubcommand_Exit2(t *testing.T) {
	withArgs(t, []string{"ai-playbook", "kb"})
	if code := KBMain(); code != 2 {
		t.Errorf("KBMain no subcommand: want exit 2, got %d", code)
	}
}

func TestKBMain_UnknownSubcommand_Exit2(t *testing.T) {
	withArgs(t, []string{"ai-playbook", "kb", "bogus"})
	if code := KBMain(); code != 2 {
		t.Errorf("KBMain unknown subcommand: want exit 2, got %d", code)
	}
}

func TestKBMain_RoutesToList(t *testing.T) {
	root := t.TempDir()
	withArgs(t, []string{"ai-playbook", "kb", "list"})
	withKBConfigFn(t, root, 4096)
	out := captureStdout(t, func() {
		if code := KBMain(); code != 0 {
			t.Errorf("KBMain list: want exit 0, got %d", code)
		}
	})
	if !strings.Contains(out, "global") {
		t.Errorf("KBMain list output missing global row: %q", out)
	}
}

// ---- kb show ----

func TestKBShow_Default_BothSetsEmpty(t *testing.T) {
	root := t.TempDir()
	withKBConfigFn(t, root, 4096)
	withKBProjectRootFn(t, "/proj/a")
	out := captureStdout(t, func() {
		if code := kbShowMain(nil); code != 0 {
			t.Errorf("kbShowMain: want exit 0, got %d", code)
		}
	})
	if !strings.Contains(out, "== global ==") {
		t.Errorf("show default: missing global header: %q", out)
	}
	if !strings.Contains(out, "== project (/proj/a) ==") {
		t.Errorf("show default: missing project header: %q", out)
	}
	if strings.Count(out, "(empty)") != 2 {
		t.Errorf("show default with no files: want 2 (empty) markers, got %q", out)
	}
}

func TestKBShow_Default_BothSetsWithContent(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "knowledge.md"), "## System\n- uses zsh\n")
	projRoot := "/proj/b"
	writeFile(t, projectKBPath(root, projRoot), "## Environment\n- go 1.26\n")
	withKBConfigFn(t, root, 4096)
	withKBProjectRootFn(t, projRoot)

	out := captureStdout(t, func() { kbShowMain(nil) })
	if !strings.Contains(out, "uses zsh") {
		t.Errorf("show default: missing global fact: %q", out)
	}
	if !strings.Contains(out, "go 1.26") {
		t.Errorf("show default: missing project fact: %q", out)
	}
	// global before project.
	if strings.Index(out, "uses zsh") > strings.Index(out, "go 1.26") {
		t.Errorf("show default: global must come before project: %q", out)
	}
}

func TestKBShow_GlobalFlag_NarrowsToGlobalOnly(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "knowledge.md"), "## System\n- uses zsh\n")
	withKBConfigFn(t, root, 4096)
	withKBProjectRootFn(t, "/proj/c")

	out := captureStdout(t, func() { kbShowMain([]string{"--global"}) })
	if !strings.Contains(out, "== global ==") {
		t.Errorf("show --global: missing global header: %q", out)
	}
	if strings.Contains(out, "project (") {
		t.Errorf("show --global: must not print a project section: %q", out)
	}
}

func TestKBShow_ProjectFlag_NarrowsToGivenPath(t *testing.T) {
	root := t.TempDir()
	otherProj := "/other/project"
	writeFile(t, projectKBPath(root, otherProj), "## Environment\n- custom fact\n")
	withKBConfigFn(t, root, 4096)
	withKBProjectRootFn(t, "/cwd/project") // must NOT be used

	out := captureStdout(t, func() { kbShowMain([]string{"--project", otherProj}) })
	if strings.Contains(out, "== global ==") {
		t.Errorf("show --project: must not print a global section: %q", out)
	}
	if !strings.Contains(out, "== project (/other/project) ==") {
		t.Errorf("show --project: wrong/missing project header: %q", out)
	}
	if !strings.Contains(out, "custom fact") {
		t.Errorf("show --project: missing project fact: %q", out)
	}
}

func TestKBShow_BothFlags_ShowsBothWithGivenProject(t *testing.T) {
	root := t.TempDir()
	otherProj := "/other/project2"
	writeFile(t, filepath.Join(root, "knowledge.md"), "## System\n- global fact\n")
	writeFile(t, projectKBPath(root, otherProj), "## Environment\n- proj fact\n")
	withKBConfigFn(t, root, 4096)

	out := captureStdout(t, func() { kbShowMain([]string{"--global", "--project", otherProj}) })
	if !strings.Contains(out, "global fact") || !strings.Contains(out, "proj fact") {
		t.Errorf("show --global --project: want both sets, got %q", out)
	}
}

func TestKBShow_ConfigError_Exit1(t *testing.T) {
	withKBConfigFnErr(t, "boom")
	if code := kbShowMain(nil); code != 1 {
		t.Errorf("kbShowMain config error: want exit 1, got %d", code)
	}
}

func TestKBShow_UnexpectedArg_Exit2(t *testing.T) {
	if code := kbShowMain([]string{"extra"}); code != 2 {
		t.Errorf("kbShowMain unexpected arg: want exit 2, got %d", code)
	}
}

// ---- kb edit ----

func TestKBEdit_NoEditor_Exit1(t *testing.T) {
	withEnv(t, "EDITOR", "")
	if code := kbEditMain(nil); code != 1 {
		t.Errorf("kbEditMain no EDITOR: want exit 1, got %d", code)
	}
}

func TestKBEdit_Default_OpensProjectFile(t *testing.T) {
	root := t.TempDir()
	withEnv(t, "EDITOR", "vi")
	withKBConfigFn(t, root, 4096)
	withKBProjectRootFn(t, "/proj/d")

	var gotPath string
	withEditorSpawn(t, func(editor, path string) error {
		gotPath = path
		return nil
	})
	if code := kbEditMain(nil); code != 0 {
		t.Errorf("kbEditMain default: want exit 0, got %d", code)
	}
	want := projectKBPath(root, "/proj/d")
	if gotPath != want {
		t.Errorf("kbEditMain default: path = %q, want %q", gotPath, want)
	}
}

func TestKBEdit_GlobalFlag_OpensGlobalFile(t *testing.T) {
	root := t.TempDir()
	withEnv(t, "EDITOR", "vi")
	withKBConfigFn(t, root, 4096)

	var gotPath string
	withEditorSpawn(t, func(editor, path string) error {
		gotPath = path
		return nil
	})
	if code := kbEditMain([]string{"--global"}); code != 0 {
		t.Errorf("kbEditMain --global: want exit 0, got %d", code)
	}
	want := filepath.Join(root, "knowledge.md")
	if gotPath != want {
		t.Errorf("kbEditMain --global: path = %q, want %q", gotPath, want)
	}
}

func TestKBEdit_ProjectFlag_OpensGivenProject(t *testing.T) {
	root := t.TempDir()
	withEnv(t, "EDITOR", "vi")
	withKBConfigFn(t, root, 4096)
	withKBProjectRootFn(t, "/cwd/project") // must NOT be used

	var gotPath string
	withEditorSpawn(t, func(editor, path string) error {
		gotPath = path
		return nil
	})
	kbEditMain([]string{"--project", "/explicit/project"})
	want := projectKBPath(root, "/explicit/project")
	if gotPath != want {
		t.Errorf("kbEditMain --project: path = %q, want %q", gotPath, want)
	}
}

func TestKBEdit_GlobalAndProject_Exit2(t *testing.T) {
	withEnv(t, "EDITOR", "vi")
	if code := kbEditMain([]string{"--global", "--project", "/x"}); code != 2 {
		t.Errorf("kbEditMain --global --project: want exit 2, got %d", code)
	}
}

// ---- kb search ----

func TestKBSearch_MissingQuery_Exit2(t *testing.T) {
	if code := kbSearchMain(nil); code != 2 {
		t.Errorf("kbSearchMain no query: want exit 2, got %d", code)
	}
}

// A flag AFTER the query is a usage error, not a silent drop: Go's flag
// package stops parsing at the first positional, so `kb search docker --all`
// leaves --all unparsed — it must fail loudly (exit 2) instead of quietly
// searching without --all. (`--all docker` is the working order, covered by
// TestKBSearch_All_SpansEveryProject.)
func TestKBSearch_FlagAfterQuery_Exit2(t *testing.T) {
	root := t.TempDir()
	withKBConfigFn(t, root, 4096)
	withKBProjectRootFn(t, "/proj/x")
	if code := kbSearchMain([]string{"docker", "--all"}); code != 2 {
		t.Errorf("kbSearchMain flag after query: want exit 2, got %d", code)
	}
}

// Any second positional (not just a flag-shaped one) is likewise a usage error.
func TestKBSearch_ExtraPositional_Exit2(t *testing.T) {
	root := t.TempDir()
	withKBConfigFn(t, root, 4096)
	withKBProjectRootFn(t, "/proj/x")
	if code := kbSearchMain([]string{"docker", "compose"}); code != 2 {
		t.Errorf("kbSearchMain extra positional: want exit 2, got %d", code)
	}
}

func TestKBSearch_DefaultScope_GlobalAndCurrentProject(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "knowledge.md"), "## System\n- Uses Docker Compose\n- unrelated fact\n")
	projRoot := "/proj/e"
	writeFile(t, projectKBPath(root, projRoot), "## Environment\n- docker registry is private\n")
	otherProj := "/proj/other"
	writeFile(t, projectKBPath(root, otherProj), "## Environment\n- docker swarm here too\n")

	withKBConfigFn(t, root, 4096)
	withKBProjectRootFn(t, projRoot)

	out := captureStdout(t, func() {
		if code := kbSearchMain([]string{"docker"}); code != 0 {
			t.Errorf("kbSearchMain: want exit 0, got %d", code)
		}
	})
	if !strings.Contains(out, "Uses Docker Compose") {
		t.Errorf("search default: missing case-insensitive global match: %q", out)
	}
	if !strings.Contains(out, "docker registry is private") {
		t.Errorf("search default: missing current-project match: %q", out)
	}
	if strings.Contains(out, "docker swarm here too") {
		t.Errorf("search default: must NOT include other project without --all: %q", out)
	}
	if strings.Contains(out, "unrelated fact") {
		t.Errorf("search default: must not match unrelated bullet: %q", out)
	}
}

func TestKBSearch_All_SpansEveryProject(t *testing.T) {
	root := t.TempDir()
	projRoot := "/proj/f"
	writeFile(t, projectKBPath(root, projRoot), "## Environment\n- docker registry is private\n")
	otherProj := "/proj/other2"
	writeFile(t, projectKBPath(root, otherProj), "## Environment\n- docker swarm here too\n")

	withKBConfigFn(t, root, 4096)
	withKBProjectRootFn(t, projRoot)

	out := captureStdout(t, func() { kbSearchMain([]string{"--all", "docker"}) })
	if !strings.Contains(out, "docker registry is private") || !strings.Contains(out, "docker swarm here too") {
		t.Errorf("search --all: want matches from every project: %q", out)
	}
}

func TestKBSearch_NameResolution_MetaLineAndFallback(t *testing.T) {
	root := t.TempDir()
	// A project with a meta line — real name should be resolved.
	namedProj := "/named/project"
	writeFile(t, projectKBPath(root, namedProj), "<!-- meta: project-root: /named/project -->\n\n## Environment\n- alpha fact\n")
	// A project without a meta line (legacy, unsectioned) — falls back to the sha1 key.
	legacyKeyDir := "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	legacyPath := filepath.Join(root, "projects", legacyKeyDir, "knowledge.md")
	writeFile(t, legacyPath, "- alpha legacy fact\n")

	withKBConfigFn(t, root, 4096)
	withKBProjectRootFn(t, "/cwd/irrelevant")

	out := captureStdout(t, func() { kbSearchMain([]string{"--all", "alpha"}) })
	if !strings.Contains(out, "== /named/project ==") {
		t.Errorf("search --all: want resolved name header, got %q", out)
	}
	if !strings.Contains(out, "== "+legacyKeyDir+" ==") {
		t.Errorf("search --all: want sha1-key fallback header, got %q", out)
	}
}

func TestKBSearch_NoMatches_Exit0NoStdout(t *testing.T) {
	root := t.TempDir()
	withKBConfigFn(t, root, 4096)
	withKBProjectRootFn(t, "/proj/g")

	out := captureStdout(t, func() {
		if code := kbSearchMain([]string{"nope"}); code != 0 {
			t.Errorf("kbSearchMain no matches: want exit 0, got %d", code)
		}
	})
	if out != "" {
		t.Errorf("kbSearchMain no matches: want empty stdout, got %q", out)
	}
}

// ---- kb list ----

func TestKBList_EmptyKB_GlobalRowOnly(t *testing.T) {
	root := t.TempDir()
	withKBConfigFn(t, root, 4096)

	out := captureStdout(t, func() {
		if code := kbListMain(nil); code != 0 {
			t.Errorf("kbListMain: want exit 0, got %d", code)
		}
	})
	if !strings.Contains(out, "global") {
		t.Errorf("kbListMain empty: missing global row: %q", out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 { // header + global row, no project rows
		t.Errorf("kbListMain empty: want 2 lines (header+global), got %d: %q", len(lines), out)
	}
}

func TestKBList_WithProjects(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "knowledge.md"), "## System\n- a\n- b\n")
	proj := "/proj/h"
	writeFile(t, projectKBPath(root, proj), "<!-- meta: project-root: /proj/h -->\n\n## Environment\n- c\n")
	withKBConfigFn(t, root, 4096)

	out := captureStdout(t, func() { kbListMain(nil) })
	if !strings.Contains(out, "/proj/h") {
		t.Errorf("kbListMain: missing project name/path: %q", out)
	}
}

// ---- pure helpers ----

func TestKBBullets_ExtractsAcrossSections(t *testing.T) {
	content := "## System\n- one\n\n## User\n- two\n### sub\n- three\n"
	got := kbBullets(content)
	want := []string{"one", "two", "three"}
	if len(got) != len(want) {
		t.Fatalf("kbBullets = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("kbBullets[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestMatchBullets_CaseInsensitive(t *testing.T) {
	content := "## System\n- Uses DOCKER\n- unrelated\n"
	got := matchBullets(content, "docker")
	if len(got) != 1 || got[0] != "Uses DOCKER" {
		t.Errorf("matchBullets case-insensitive = %v", got)
	}
}

func TestMatchBullets_EmptyQuery_NoMatches(t *testing.T) {
	if got := matchBullets("## System\n- fact\n", ""); got != nil {
		t.Errorf("matchBullets empty query = %v, want nil", got)
	}
}

func TestRenderKBSection_Empty(t *testing.T) {
	out := renderKBSection("global", "")
	if !strings.Contains(out, "== global ==") || !strings.Contains(out, "(empty)") {
		t.Errorf("renderKBSection empty = %q", out)
	}
}

func TestRenderKBSection_WithContent(t *testing.T) {
	out := renderKBSection("global", "## System\n- fact\n")
	if !strings.Contains(out, "== global ==") || !strings.Contains(out, "- fact") {
		t.Errorf("renderKBSection content = %q", out)
	}
}

// ---- test fixtures ----

// writeFile writes content to path, creating parent directories as needed.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// projectKBPath resolves a project's knowledge-file path the same way
// production code does (kb.Path), so fixtures land exactly where kbcmd.go's
// helpers expect to find them.
func projectKBPath(root, projectRoot string) string {
	return kb.Path(root, projectRoot)
}
