package launcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/autorun"
	"github.com/Townk/ai-playbook/internal/store"
)

// ---- resolveRunArgs — the run argument resolution matrix ----

func TestResolveRunArgs_Matrix(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		wantKind  string
		wantValue string
		wantAuto  bool
		wantErr   bool
	}{
		{"bare positional ⇒ playbook", []string{"build"}, "playbook", "build", false, false},
		{"--playbook flag", []string{"--playbook", "build"}, "playbook", "build", false, false},
		{"--file flag", []string{"--file", "/p.md"}, "file", "/p.md", false, false},
		{"--auto-rollback with --file", []string{"--auto-rollback", "--file", "/p.md"}, "file", "/p.md", true, false},
		{"--file without --auto-rollback → auto false", []string{"--file", "/p.md"}, "file", "/p.md", false, false},
		{"both --playbook and --file → error", []string{"--playbook", "build", "--file", "/p.md"}, "", "", false, true},
		{"positional + --file → error", []string{"build", "--file", "/p.md"}, "", "", false, true},
		{"none → error", []string{}, "", "", false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ra, err := resolveRunArgs(c.args)
			if c.wantErr {
				if err == nil {
					t.Fatalf("resolveRunArgs(%v): want error, got (%+v,nil)", c.args, ra)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveRunArgs(%v): unexpected error: %v", c.args, err)
			}
			if ra.Kind != c.wantKind {
				t.Errorf("kind = %q, want %q", ra.Kind, c.wantKind)
			}
			if ra.Value != c.wantValue {
				t.Errorf("value = %q, want %q", ra.Value, c.wantValue)
			}
			if ra.AutoRollback != c.wantAuto {
				t.Errorf("autoRollback = %v, want %v", ra.AutoRollback, c.wantAuto)
			}
		})
	}
}

// TestResolveRunArgs_AutoFlags covers the --auto / --no-auto-rollback flag
// wiring: --auto sets Mode; --no-auto-rollback without --auto is a usage
// error; --auto combined with --auto-rollback is a usage error (the two
// rollback opt-ins are mutually exclusive across run modes).
func TestResolveRunArgs_AutoFlags(t *testing.T) {
	ra, err := resolveRunArgs([]string{"--auto", "--file", "x.md"})
	if err != nil || ra.Mode != modeAuto || ra.Kind != "file" || ra.Value != "x.md" {
		t.Fatalf("--auto: %+v err=%v", ra, err)
	}
	if _, err := resolveRunArgs([]string{"--no-auto-rollback", "--file", "x.md"}); err == nil {
		t.Error("--no-auto-rollback without --auto must error")
	}
	if _, err := resolveRunArgs([]string{"--auto", "--auto-rollback", "--file", "x.md"}); err == nil {
		t.Error("--auto with --auto-rollback must error")
	}
}

// TestResolveRunArgs_Assisted covers the --assisted flag: it sets
// Mode: modeAssisted, and is mutually exclusive with --auto (assisted rides
// the interactive viewer; auto is headless — the two are incompatible).
func TestResolveRunArgs_Assisted(t *testing.T) {
	ra, err := resolveRunArgs([]string{"--assisted", "--file", "x.md"})
	if err != nil || ra.Mode != modeAssisted || ra.Kind != "file" || ra.Value != "x.md" {
		t.Fatalf("--assisted: %+v err=%v", ra, err)
	}
	if _, err := resolveRunArgs([]string{"--assisted", "--auto", "--file", "x.md"}); err == nil {
		t.Error("--assisted with --auto must error (mutually exclusive)")
	}
	if _, err := resolveRunArgs([]string{"--assisted", "--no-auto-rollback", "--file", "x.md"}); err == nil {
		t.Error("--assisted with --no-auto-rollback must error (that flag is --auto only)")
	}
}

// ---- seam helpers ----

func withStoreLoadFn(t *testing.T, fn func(string) (store.Meta, string, error)) {
	t.Helper()
	old := storeLoadFn
	storeLoadFn = fn
	t.Cleanup(func() { storeLoadFn = old })
}

// ---- runPlaybook: the project_bound run gate ----

// TestRunPlaybook_ProjectBoundSetsProjectRoot verifies a project_bound playbook
// resolves the heuristic project root, sets it on the run driver via the
// setProjectRootFn seam, and renders the stored body as-is (no model adapt).
func TestRunPlaybook_ProjectBoundSetsProjectRoot(t *testing.T) {
	origLoad, origPR, origUI, origSPR := storeLoadFn, projectRootFn, uiMainFn, setProjectRootFn
	t.Cleanup(func() { storeLoadFn, projectRootFn, uiMainFn, setProjectRootFn = origLoad, origPR, origUI, origSPR })
	storeLoadFn = func(string) (store.Meta, string, error) {
		return store.Meta{Slug: "pb", ProjectBound: true}, "# Playbook — T\n\n```bash {id=fix}\ncd $PROJECT_ROOT\n```\n", nil
	}
	projectRootFn = func() string { return "/new/proj" }
	var gotPR string
	setProjectRootFn = func(p string) { gotPR = p }
	uiMainFn = func() int { return 0 } // no real viewer
	if code := runPlaybook("pb"); code != 0 {
		t.Fatalf("runPlaybook = %d", code)
	}
	if gotPR != "/new/proj" {
		t.Fatalf("project_bound run must set PROJECT_ROOT to the heuristic root, got %q", gotPR)
	}
}

// ---- runPlaybook: non-project_bound renders as-is, no PROJECT_ROOT ----

const storedBody = "# Build\n\n```bash {id=verify}\nmake\n```\n"

// TestRunPlaybook_NonProjectBoundRendersAsIs verifies a non-project_bound playbook
// is rendered verbatim via `run --file <tmp>` with NO --cwd and NO PROJECT_ROOT set
// (no model adapt, no banner flags).
func TestRunPlaybook_NonProjectBoundRendersAsIs(t *testing.T) {
	origLoad, origUI, origSPR := storeLoadFn, uiMainFn, setProjectRootFn
	t.Cleanup(func() { storeLoadFn, uiMainFn, setProjectRootFn = origLoad, origUI, origSPR })
	withArgs(t, []string{"ai-playbook", "run", "build"})
	storeLoadFn = func(slug string) (store.Meta, string, error) {
		return store.Meta{Slug: "build", ProjectBound: false}, storedBody, nil
	}
	setProjectRootFn = func(string) { t.Fatal("non-project_bound run must NOT set PROJECT_ROOT") }
	var captured []string
	uiMainFn = func() int { captured = append([]string{}, os.Args...); return 0 }

	if code := runPlaybook("build"); code != 0 {
		t.Fatalf("runPlaybook = %d", code)
	}
	// {bin, run, --file, <tmp>} — no --cwd, no banner flags.
	if len(captured) != 4 || captured[1] != "run" || captured[2] != "--file" {
		t.Fatalf("non-project_bound run: args = %v, want [bin run --file <tmp>]", captured)
	}
	got, _ := os.ReadFile(captured[3])
	if string(got) != storedBody {
		t.Errorf("non-project_bound run: render file = %q, want the stored body verbatim", got)
	}
	os.Remove(captured[3])
}

// TestRunMain_UnknownSlug_Exit1 verifies an unknown slug (store.Load error) is a
// clear error (exit 1) and ui.Main is never reached.
func TestRunMain_UnknownSlug_Exit1(t *testing.T) {
	withArgs(t, []string{"ai-playbook", "run", "no-such-slug"})
	withStoreLoadFn(t, func(string) (store.Meta, string, error) {
		return store.Meta{}, "", os.ErrNotExist
	})
	called := false
	withUIMainFn(t, func() int { called = true; return 0 })

	if code := RunMain(); code != 1 {
		t.Errorf("RunMain unknown slug: want exit 1, got %d", code)
	}
	if called {
		t.Error("RunMain unknown slug: ui.Main must not be called")
	}
}

// TestRunMain_NoArgs_Exit2 verifies missing a source is a usage error (exit 2).
func TestRunMain_NoArgs_Exit2(t *testing.T) {
	withArgs(t, []string{"ai-playbook", "run"})
	called := false
	withUIMainFn(t, func() int { called = true; return 0 })

	if code := RunMain(); code != 2 {
		t.Errorf("RunMain no args: want exit 2, got %d", code)
	}
	if called {
		t.Error("RunMain no args: ui.Main must not be called")
	}
}

// ---- runFile: raw as-is vs has-front-matter gate ----

// TestRunMain_FileRaw_RendersAsIs verifies a raw file (NO front matter) renders
// as-is via `run --file <path>` with no adapt and no PROJECT_ROOT.
func TestRunMain_FileRaw_RendersAsIs(t *testing.T) {
	origSPR := setProjectRootFn
	t.Cleanup(func() { setProjectRootFn = origSPR })
	dir := t.TempDir()
	raw := filepath.Join(dir, "raw.md")
	if err := os.WriteFile(raw, []byte("# Just a doc\n\nno front matter here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withArgs(t, []string{"ai-playbook", "run", "--file", raw})
	setProjectRootFn = func(string) { t.Fatal("raw file must NOT set PROJECT_ROOT") }
	var captured []string
	withUIMainFn(t, func() int {
		captured = append([]string{}, os.Args...)
		return 0
	})

	if code := RunMain(); code != 0 {
		t.Fatalf("RunMain raw file: want exit 0, got %d", code)
	}
	if len(captured) != 4 || captured[2] != "--file" || captured[3] != raw {
		t.Errorf("RunMain raw file: args = %v, want [bin run --file %s]", captured, raw)
	}
}

// TestRunMain_FileProjectBound_SetsProjectRoot verifies a --file WITH a
// project_bound front matter resolves the heuristic project root, sets it via the
// setProjectRootFn seam, renders the stripped body as-is, and opens at --cwd <root>.
func TestRunMain_FileProjectBound_SetsProjectRoot(t *testing.T) {
	origPR, origSPR := projectRootFn, setProjectRootFn
	t.Cleanup(func() { projectRootFn, setProjectRootFn = origPR, origSPR })
	dir := t.TempDir()
	fmFile := filepath.Join(dir, "build.md")
	body := "# Build\n\n```bash {id=verify}\ncd $PROJECT_ROOT && make\n```\n"
	content := "---\nname: Build\nproject_bound: true\n---\n" + body
	if err := os.WriteFile(fmFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	withArgs(t, []string{"ai-playbook", "run", "--file", fmFile})
	projectRootFn = func() string { return "/the/proj" }
	var gotPR string
	setProjectRootFn = func(p string) { gotPR = p }
	var captured []string
	withUIMainFn(t, func() int {
		captured = append([]string{}, os.Args...)
		return 0
	})

	if code := RunMain(); code != 0 {
		t.Fatalf("RunMain fm file: want exit 0, got %d", code)
	}
	if gotPR != "/the/proj" {
		t.Fatalf("project_bound --file must set PROJECT_ROOT; got %q", gotPR)
	}
	joined := strings.Join(captured, " ")
	if !strings.Contains(joined, "--cwd /the/proj") {
		t.Errorf("project_bound --file: expected --cwd /the/proj; args = %v", captured)
	}
	// The ORIGINAL file is passed to ui.Main (which strips the front matter for display
	// and extracts the env map for the gate) — NOT a stripped temp copy.
	if captured[3] != fmFile {
		t.Errorf("project_bound --file: file = %q, want the original %q (not a temp copy)", captured[3], fmFile)
	}
	_ = body
}

// TestRunMain_FileWithFrontMatter_PassesOriginalFile verifies a --file WITH front
// matter (non-project_bound) renders the ORIGINAL file — not a stripped temp copy —
// so ui.Main extracts the env map for the confirmation gate (F25), and opens at
// --cwd <dir-of-file> so the body's relative paths resolve (F4). No PROJECT_ROOT.
func TestRunMain_FileWithFrontMatter_PassesOriginalFile(t *testing.T) {
	origSPR := setProjectRootFn
	t.Cleanup(func() { setProjectRootFn = origSPR })
	dir := t.TempDir()
	fmFile := filepath.Join(dir, "chapter.md")
	content := "---\nname: Chapter\nenv:\n  DATA_DIR:\n    value: /tmp/x\n    why: test\n---\n# Chapter\n\n```bash {id=go}\necho hi\n```\n"
	if err := os.WriteFile(fmFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	withArgs(t, []string{"ai-playbook", "run", "--file", fmFile})
	setProjectRootFn = func(string) { t.Fatal("non-project_bound --file must NOT set PROJECT_ROOT") }
	var captured []string
	withUIMainFn(t, func() int { captured = append([]string{}, os.Args...); return 0 })

	if code := RunMain(); code != 0 {
		t.Fatalf("RunMain fm file: want exit 0, got %d", code)
	}
	if len(captured) != 6 || captured[2] != "--file" || captured[3] != fmFile || captured[4] != "--cwd" {
		t.Fatalf("fm file: args = %v, want [bin run --file %s --cwd %s]", captured, fmFile, dir)
	}
	if captured[5] != dir {
		t.Errorf("fm file: --cwd = %q, want %q (dir of file)", captured[5], dir)
	}
}

// TestRunMain_FileProjectBound_ResolvesDeclaredProjectRoot verifies a project_bound
// --file with an explicit project_root resolves it relative to the heuristic repo
// root (projectRootFn) and uses THAT as PROJECT_ROOT + --cwd — not the bare repo root.
func TestRunMain_FileProjectBound_ResolvesDeclaredProjectRoot(t *testing.T) {
	origPR, origSPR := projectRootFn, setProjectRootFn
	t.Cleanup(func() { projectRootFn, setProjectRootFn = origPR, origSPR })
	dir := t.TempDir()
	fmFile := filepath.Join(dir, "portable.md")
	content := "---\nname: Portable\nproject_bound: true\nproject_root: sub/proj\n---\n# Portable\n\n```bash {id=go}\necho hi\n```\n"
	if err := os.WriteFile(fmFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	withArgs(t, []string{"ai-playbook", "run", "--file", fmFile})
	projectRootFn = func() string { return "/repo" }
	var gotPR string
	setProjectRootFn = func(p string) { gotPR = p }
	var captured []string
	withUIMainFn(t, func() int { captured = append([]string{}, os.Args...); return 0 })

	if code := RunMain(); code != 0 {
		t.Fatalf("RunMain = %d", code)
	}
	want := filepath.Join("/repo", "sub/proj")
	if gotPR != want {
		t.Fatalf("declared project_root: PROJECT_ROOT = %q, want %q", gotPR, want)
	}
	if joined := strings.Join(captured, " "); !strings.Contains(joined, "--cwd "+want) {
		t.Errorf("declared project_root: expected --cwd %s; args = %v", want, captured)
	}
	if captured[3] != fmFile {
		t.Errorf("declared project_root: file = %q, want original %q", captured[3], fmFile)
	}
}

// ---- RunMain: the --auto headless branch ----

// TestRunMain_AutoBranch_CallsAutorun verifies `run --auto --file <md>` never
// opens a viewer: it converts the parsed blocks to autorun.Block and calls the
// autorunRunFn seam, with AutoRollback defaulting to true (no
// --no-auto-rollback given).
func TestRunMain_AutoBranch_CallsAutorun(t *testing.T) {
	origRun := autorunRunFn
	t.Cleanup(func() { autorunRunFn = origRun })

	dir := t.TempDir()
	f := filepath.Join(dir, "auto.md")
	content := "# Auto\n\n```bash {id=a}\necho a\n```\n\n```bash {id=b needs=a}\necho b\n```\n"
	if err := os.WriteFile(f, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	withArgs(t, []string{"ai-playbook", "run", "--auto", "--file", f})

	var gotAutoRB bool
	var gotIDs []string
	autorunRunFn = func(rc autorun.RunConfig) int {
		gotAutoRB = rc.AutoRollback
		for _, b := range rc.Blocks {
			gotIDs = append(gotIDs, b.ID)
		}
		return 0
	}

	if code := RunMain(); code != 0 {
		t.Fatalf("RunMain auto = %d", code)
	}
	if !gotAutoRB {
		t.Error("auto default must set AutoRollback=true")
	}
	if len(gotIDs) != 2 || gotIDs[0] != "a" || gotIDs[1] != "b" {
		t.Errorf("blocks converted = %v, want [a b]", gotIDs)
	}
}
