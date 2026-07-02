package launcher

import (
	"os"
	"path/filepath"
	"reflect"
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
	if _, err := resolveRunArgs([]string{"--assisted", "--auto-rollback", "--file", "x.md"}); err == nil {
		t.Error("--assisted with --auto-rollback must error (mutually exclusive: assisted owns post-failure flow via its own manual button)")
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

// ---- autoRun: depends_on chain wiring (--auto owns the WHOLE chain) ----

// TestAutoRun_Chain_OrderAndAbort verifies autoRun runs a parent's dependency
// chain in topological order (deps before the parent) and aborts on the first
// non-zero dependency exit — later dependencies AND the parent are NEVER
// invoked, and the returned code is the failing dependency's.
func TestAutoRun_Chain_OrderAndAbort(t *testing.T) {
	dir := t.TempDir()
	bPath := writeDepPlaybook(t, dir, "b", "")
	aPath := writeDepPlaybook(t, dir, "a", "depends_on:\n  - b\n")
	parentPath := writeDepPlaybook(t, dir, "parent", "depends_on:\n  - a\n")

	defer swap(&storePathForFn, func(slug string) (string, bool) {
		switch slug {
		case "a":
			return aPath, true
		case "b":
			return bPath, true
		}
		return "", false
	})()

	var order []string
	restore := swap(&autorunRunFn, func(rc autorun.RunConfig) int {
		order = append(order, rc.Slug)
		if !rc.SuppressUndeclaredWarning {
			t.Errorf("chain run of %s: SuppressUndeclaredWarning = false, want true", rc.Slug)
		}
		return 0
	})
	defer restore()

	ra := runArgs{Kind: "file", Value: parentPath, Mode: modeAuto}
	if code := autoRun(ra); code != 0 {
		t.Fatalf("autoRun = %d, want 0", code)
	}
	if want := []string{"b", "a", "parent"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}

	// Abort: b fails → a and the parent are NEVER invoked; the returned code
	// is b's.
	order = nil
	autorunRunFn = func(rc autorun.RunConfig) int {
		order = append(order, rc.Slug)
		if rc.Slug == "b" {
			return 5
		}
		return 0
	}
	if code := autoRun(ra); code != 5 {
		t.Fatalf("autoRun abort = %d, want 5", code)
	}
	if want := []string{"b"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("abort order = %v, want %v (a and the parent never invoked)", order, want)
	}
}

// TestAutoRun_Chain_SuppressionAndUnionWarning verifies the --auto +
// --with-env chain: every chain RunConfig is suppressed (no per-playbook
// undeclared-override warning), and the ONE union warning is checked against
// the parent's + every dependency's declared vars — a key only a dependency
// declares (ONLY_DEP) must NOT be flagged; a key no playbook declares (GHOST)
// must be.
func TestAutoRun_Chain_SuppressionAndUnionWarning(t *testing.T) {
	dir := t.TempDir()
	bPath := writeDepPlaybook(t, dir, "b", "env:\n  ONLY_DEP:\n    value: v\n    why: w\n")
	parentPath := writeDepPlaybook(t, dir, "parent", "depends_on:\n  - b\n")

	defer swap(&storePathForFn, func(slug string) (string, bool) {
		if slug == "b" {
			return bPath, true
		}
		return "", false
	})()

	var configs []autorun.RunConfig
	restore := swap(&autorunRunFn, func(rc autorun.RunConfig) int {
		configs = append(configs, rc)
		return 0
	})
	defer restore()

	ra := runArgs{
		Kind: "file", Value: parentPath, Mode: modeAuto,
		EnvOverrides: map[string]string{"ONLY_DEP": "x", "GHOST": "y"},
	}
	out := captureStdout(t, func() {
		if code := autoRun(ra); code != 0 {
			t.Fatalf("autoRun = %d, want 0", code)
		}
	})

	if len(configs) != 2 {
		t.Fatalf("want 2 chain runs (b + parent), got %d: %+v", len(configs), configs)
	}
	for _, rc := range configs {
		if !rc.SuppressUndeclaredWarning {
			t.Errorf("%s: SuppressUndeclaredWarning = false, want true", rc.Slug)
		}
	}

	if !strings.Contains(out, "ignoring undeclared variable GHOST") {
		t.Errorf("union warning must flag GHOST (declared by no playbook); got:\n%s", out)
	}
	if strings.Contains(out, "ONLY_DEP") {
		t.Errorf("union warning must NOT flag ONLY_DEP (declared by the dependency); got:\n%s", out)
	}
}

// TestAutoRun_DanglingDep_Exit2 verifies a dangling dependency (spied loader
// reports it not found) is caught BEFORE anything runs: autoRun returns 2 and
// autorunRunFn is never called.
func TestAutoRun_DanglingDep_Exit2(t *testing.T) {
	dir := t.TempDir()
	parentPath := writeDepPlaybook(t, dir, "parent", "depends_on:\n  - ghost\n")

	defer swap(&storePathForFn, func(string) (string, bool) { return "", false })()

	called := false
	restore := swap(&autorunRunFn, func(autorun.RunConfig) int { called = true; return 0 })
	defer restore()

	ra := runArgs{Kind: "file", Value: parentPath, Mode: modeAuto}
	if code := autoRun(ra); code != 2 {
		t.Fatalf("autoRun dangling dep = %d, want 2", code)
	}
	if called {
		t.Error("autorunRunFn must never be called when a dependency is dangling")
	}
}

// TestAutoRun_StoredParent_EnvReachesRunner verifies a STORED (--playbook)
// parent's declared front-matter env reaches the runner via --auto. Every
// other chain test in this file drives a "file" parent; loadParent's
// "playbook" branch (storeLoadFn → re-read + parse meta.Path) has its own
// code path and, before Task 3, the old autoRun "playbook" branch never
// parsed the file at all — silently dropping a stored parent's declared env.
// A future refactor reverting that branch must fail THIS test.
func TestAutoRun_StoredParent_EnvReachesRunner(t *testing.T) {
	dir := t.TempDir()
	parentPath := writeDepPlaybook(t, dir, "parent", "env:\n  FOO:\n    value: bar\n    why: test\n")

	withStoreLoadFn(t, func(slug string) (store.Meta, string, error) {
		if slug == "parent" {
			return store.Meta{Slug: "parent", Path: parentPath}, "", nil
		}
		return store.Meta{}, "", os.ErrNotExist
	})

	var gotRC autorun.RunConfig
	restore := swap(&autorunRunFn, func(rc autorun.RunConfig) int {
		gotRC = rc
		return 0
	})
	defer restore()

	ra := runArgs{Kind: "playbook", Value: "parent", Mode: modeAuto}
	if code := autoRun(ra); code != 0 {
		t.Fatalf("autoRun = %d, want 0", code)
	}
	if gotRC.EnvVars["FOO"].Value != "bar" {
		t.Fatalf("EnvVars[FOO] = %+v, want value bar (a stored parent's fm.Env must reach the runner)", gotRC.EnvVars["FOO"])
	}
}

// TestAutoRun_StoredParent_WithDependsOn_OrderAndEnv extends the lock-in: a
// STORED parent that ALSO declares depends_on must have loadParent surface
// fm.DependsOn (not just fm.Env) — resolveChain must run the dependency
// first, and both the dependency's and the stored parent's declared env must
// reach their respective RunConfigs.
func TestAutoRun_StoredParent_WithDependsOn_OrderAndEnv(t *testing.T) {
	dir := t.TempDir()
	depPath := writeDepPlaybook(t, dir, "dep", "env:\n  DEP_VAR:\n    value: depval\n    why: test\n")
	parentPath := writeDepPlaybook(t, dir, "parent",
		"env:\n  FOO:\n    value: bar\n    why: test\ndepends_on:\n  - dep\n")

	withStoreLoadFn(t, func(slug string) (store.Meta, string, error) {
		if slug == "parent" {
			return store.Meta{Slug: "parent", Path: parentPath}, "", nil
		}
		return store.Meta{}, "", os.ErrNotExist
	})
	defer swap(&storePathForFn, func(slug string) (string, bool) {
		if slug == "dep" {
			return depPath, true
		}
		return "", false
	})()

	var order []string
	var configs []autorun.RunConfig
	restore := swap(&autorunRunFn, func(rc autorun.RunConfig) int {
		order = append(order, rc.Slug)
		configs = append(configs, rc)
		return 0
	})
	defer restore()

	ra := runArgs{Kind: "playbook", Value: "parent", Mode: modeAuto}
	if code := autoRun(ra); code != 0 {
		t.Fatalf("autoRun = %d, want 0", code)
	}
	if want := []string{"dep", "parent"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	if len(configs) != 2 {
		t.Fatalf("want 2 RunConfigs (dep + stored parent), got %d: %+v", len(configs), configs)
	}
	if configs[0].EnvVars["DEP_VAR"].Value != "depval" {
		t.Errorf("dep EnvVars[DEP_VAR] = %+v, want value depval", configs[0].EnvVars["DEP_VAR"])
	}
	if configs[1].EnvVars["FOO"].Value != "bar" {
		t.Errorf("stored parent EnvVars[FOO] = %+v, want value bar", configs[1].EnvVars["FOO"])
	}
}

// ---- RunMain: depends_on wiring for the non-auto (interactive/assisted) path ----

// TestRunMain_NonAuto_DanglingDep_Exit2 verifies the interactive dispatch also
// gates on depends_on issues before ever opening the viewer.
func TestRunMain_NonAuto_DanglingDep_Exit2(t *testing.T) {
	dir := t.TempDir()
	parentPath := writeDepPlaybook(t, dir, "parent", "depends_on:\n  - ghost\n")

	defer swap(&storePathForFn, func(string) (string, bool) { return "", false })()

	withArgs(t, []string{"ai-playbook", "run", "--file", parentPath})
	called := false
	withUIMainFn(t, func() int { called = true; return 0 })

	if code := RunMain(); code != 2 {
		t.Fatalf("RunMain dangling dep = %d, want 2", code)
	}
	if called {
		t.Error("ui.Main must never be called when a dependency is dangling")
	}
}

// TestRunMain_NonAuto_DepsRunHeadlessBeforeViewer verifies a non-auto
// (interactive) parent with depends_on runs its dependency chain headless
// FIRST (SuppressUndeclaredWarning true, no --with-env), then still dispatches
// the parent itself to the viewer as always.
func TestRunMain_NonAuto_DepsRunHeadlessBeforeViewer(t *testing.T) {
	dir := t.TempDir()
	depPath := writeDepPlaybook(t, dir, "dep", "")
	parentPath := writeDepPlaybook(t, dir, "parent", "depends_on:\n  - dep\n")

	defer swap(&storePathForFn, func(slug string) (string, bool) {
		if slug == "dep" {
			return depPath, true
		}
		return "", false
	})()

	depRan := false
	restore := swap(&autorunRunFn, func(rc autorun.RunConfig) int {
		if rc.Slug == "dep" {
			depRan = true
			if !rc.SuppressUndeclaredWarning {
				t.Error("dep chain run must suppress its own undeclared warning")
			}
		}
		return 0
	})
	defer restore()

	withArgs(t, []string{"ai-playbook", "run", "--file", parentPath})
	viewerRan := false
	withUIMainFn(t, func() int { viewerRan = true; return 0 })

	if code := RunMain(); code != 0 {
		t.Fatalf("RunMain = %d, want 0", code)
	}
	if !depRan {
		t.Error("the dependency must run headless before the viewer")
	}
	if !viewerRan {
		t.Error("the parent must still dispatch to the viewer")
	}
}

// TestRunMain_NonAuto_DepFailure_AbortsBeforeViewer verifies a failing
// dependency aborts the whole run before the parent's viewer ever opens, and
// RunMain returns the dependency's exit code.
func TestRunMain_NonAuto_DepFailure_AbortsBeforeViewer(t *testing.T) {
	dir := t.TempDir()
	depPath := writeDepPlaybook(t, dir, "dep", "")
	parentPath := writeDepPlaybook(t, dir, "parent", "depends_on:\n  - dep\n")

	defer swap(&storePathForFn, func(slug string) (string, bool) {
		if slug == "dep" {
			return depPath, true
		}
		return "", false
	})()

	restore := swap(&autorunRunFn, func(autorun.RunConfig) int { return 3 })
	defer restore()

	withArgs(t, []string{"ai-playbook", "run", "--file", parentPath})
	viewerRan := false
	withUIMainFn(t, func() int { viewerRan = true; return 0 })

	if code := RunMain(); code != 3 {
		t.Fatalf("RunMain dep failure = %d, want 3", code)
	}
	if viewerRan {
		t.Error("the parent's viewer must never open when a dependency fails")
	}
}

func TestParseWithEnv(t *testing.T) {
	// inline JSON
	m, err := parseWithEnv(`{"A":"1","B":"two"}`)
	if err != nil || m["A"] != "1" || m["B"] != "two" {
		t.Fatalf("inline: m=%v err=%v", m, err)
	}
	// leading whitespace still detected as inline
	if m, err := parseWithEnv("  {\"A\":\"1\"}"); err != nil || m["A"] != "1" {
		t.Fatalf("ws-inline: m=%v err=%v", m, err)
	}
	// file path
	dir := t.TempDir()
	f := filepath.Join(dir, "env.json")
	if werr := os.WriteFile(f, []byte(`{"C":"3"}`), 0o644); werr != nil {
		t.Fatal(werr)
	}
	if m, err := parseWithEnv(f); err != nil || m["C"] != "3" {
		t.Fatalf("file: m=%v err=%v", m, err)
	}
	// malformed JSON
	if _, err := parseWithEnv(`{bad`); err == nil {
		t.Error("malformed inline JSON must error")
	}
	// non-string value
	if _, err := parseWithEnv(`{"A":1}`); err == nil {
		t.Error("non-string value must error")
	}
	// unreadable file
	if _, err := parseWithEnv(filepath.Join(dir, "nope.json")); err == nil {
		t.Error("unreadable file must error")
	}
}

func TestResolveRunArgs_WithEnv(t *testing.T) {
	// --with-env requires --auto
	if _, err := resolveRunArgs([]string{"--file", "p.md", "--with-env", `{"A":"1"}`}); err == nil {
		t.Error("--with-env without --auto must error")
	}
	// --auto + inline JSON populates EnvOverrides
	ra, err := resolveRunArgs([]string{"--file", "p.md", "--auto", "--with-env", `{"A":"1"}`})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ra.EnvOverrides["A"] != "1" {
		t.Errorf("EnvOverrides = %v, want A=1", ra.EnvOverrides)
	}
	// bad JSON surfaces as an error (caller maps to exit 2)
	if _, err := resolveRunArgs([]string{"--file", "p.md", "--auto", "--with-env", `{bad`}); err == nil {
		t.Error("bad --with-env JSON must error")
	}
}
