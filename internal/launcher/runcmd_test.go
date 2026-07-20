package launcher

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Townk/ai-playbook/internal/autorun"
	"github.com/Townk/ai-playbook/internal/runlog"
	"github.com/Townk/ai-playbook/internal/ui"
	"github.com/Townk/ai-playbook/pkg/store"
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

// TestResolveRunArgs_JUnitFlag covers the --junit wiring: it threads the path
// through (with --auto) and is a usage error without --auto, like --with-env.
func TestResolveRunArgs_JUnitFlag(t *testing.T) {
	ra, err := resolveRunArgs([]string{"--auto", "--junit", "out/report.xml", "--file", "x.md"})
	if err != nil || ra.JUnitPath != "out/report.xml" {
		t.Fatalf("--auto --junit: %+v err=%v", ra, err)
	}
	if _, err := resolveRunArgs([]string{"--junit", "out/report.xml", "--file", "x.md"}); err == nil {
		t.Error("--junit without --auto must error")
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
// resolves the heuristic project root, sets it on the run driver via
// Options.ProjectRoot, and renders the ORIGINAL stored file as-is (no model
// adapt). runPlaybook delegates to runFile(meta.Path), so project_bound is
// decided by the FILE's own front matter (re-parsed by runFile) — the storeLoadFn
// stub's Meta.ProjectBound is irrelevant here and deliberately left false.
func TestRunPlaybook_ProjectBoundSetsProjectRoot(t *testing.T) {
	origLoad, origPR, origUI := storeLoadFn, projectRootFn, uiRunFn
	t.Cleanup(func() { storeLoadFn, projectRootFn, uiRunFn = origLoad, origPR, origUI })
	dir := t.TempDir()
	path := filepath.Join(dir, "pb.md")
	content := "---\nname: pb\nproject_bound: true\n---\n# Playbook — T\n\n```bash {id=fix}\ncd $PROJECT_ROOT\n```\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	storeLoadFn = func(string) (store.Meta, string, error) {
		return store.Meta{Slug: "pb", Path: path}, "", nil
	}
	projectRootFn = func() string { return "/new/proj" }
	var got ui.Options
	uiRunFn = func(o ui.Options) int { got = o; return 0 } // no real viewer
	if code := runPlaybook("pb", ui.Options{}); code != 0 {
		t.Fatalf("runPlaybook = %d", code)
	}
	if got.ProjectRoot != "/new/proj" {
		t.Fatalf("project_bound run must set Options.ProjectRoot to the heuristic root, got %q", got.ProjectRoot)
	}
}

// ---- runPlaybook: non-project_bound renders as-is (runFile semantics), no PROJECT_ROOT ----

const storedBody = "# Build\n\n```bash {id=verify}\nmake\n```\n"

// TestRunPlaybook_NonProjectBoundRendersAsIs verifies a non-project_bound stored
// playbook renders via `run --file <original path> --cwd <dir-of-file>` — runFile's
// own rule for a non-project_bound front-matter file (F4: opens in the file's own
// directory so its relative paths resolve) — with NO PROJECT_ROOT set. This used to
// render a front-matter-stripped temp copy with NO --cwd at all (A2b); runFile
// semantics now win uniformly for a stored run, exactly like `run --file` on the
// identical path would.
func TestRunPlaybook_NonProjectBoundRendersAsIs(t *testing.T) {
	origLoad, origUI := storeLoadFn, uiRunFn
	t.Cleanup(func() { storeLoadFn, uiRunFn = origLoad, origUI })
	dir := t.TempDir()
	path := filepath.Join(dir, "build.md")
	content := "---\nname: build\n---\n" + storedBody
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	withArgs(t, []string{"ai-playbook", "run", "build"})
	storeLoadFn = func(slug string) (store.Meta, string, error) {
		return store.Meta{Slug: "build", Path: path}, "", nil
	}
	var got ui.Options
	uiRunFn = func(o ui.Options) int { got = o; return 0 }

	if code := runPlaybook("build", ui.Options{}); code != 0 {
		t.Fatalf("runPlaybook = %d", code)
	}
	if got.ProjectRoot != "" {
		t.Fatalf("non-project_bound run must NOT set ProjectRoot; got %q", got.ProjectRoot)
	}
	if got.File != path {
		t.Errorf("non-project_bound run: File = %q, want the ORIGINAL stored path %q (not a temp copy)", got.File, path)
	}
	if got.Cwd != dir {
		t.Errorf("non-project_bound run: Cwd = %q, want %q (dir of the stored file)", got.Cwd, dir)
	}
}

// TestRunPlaybook_EnvFrontMatterReachesViewer verifies a stored playbook run via the
// slug path reaches the viewer with its front matter INTACT — runPlaybook delegates
// to runFile(meta.Path), the SAME code path `run --file` uses, so the confirmation
// gate's env: map and the description subtitle are not silently dropped. Regression
// coverage for A2b: the old renderStored round-trip wrote store.Load's
// front-matter-STRIPPED body to a temp file, so ui.Main's loadPlaybookSource found no
// env: map for a stored run even though `run --file` on the identical file kept it.
func TestRunPlaybook_EnvFrontMatterReachesViewer(t *testing.T) {
	origLoad, origUI := storeLoadFn, uiRunFn
	t.Cleanup(func() { storeLoadFn, uiRunFn = origLoad, origUI })
	dir := t.TempDir()
	path := filepath.Join(dir, "chapter.md")
	content := "---\nname: Chapter\nenv:\n  DATA_DIR:\n    value: /tmp/x\n    why: test\n---\n# Chapter\n\n```bash {id=go}\necho hi\n```\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	storeLoadFn = func(slug string) (store.Meta, string, error) {
		return store.Meta{Slug: "chapter", Path: path}, "", nil
	}
	var got ui.Options
	uiRunFn = func(o ui.Options) int { got = o; return 0 }

	if code := runPlaybook("chapter", ui.Options{}); code != 0 {
		t.Fatalf("runPlaybook = %d", code)
	}
	if got.ProjectRoot != "" {
		t.Fatalf("non-project_bound run must NOT set ProjectRoot; got %q", got.ProjectRoot)
	}
	if got.File != path {
		t.Fatalf("runPlaybook: File = %q, want the ORIGINAL file %q (not a temp copy)", got.File, path)
	}
	data, err := os.ReadFile(got.File)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "env:") || !strings.Contains(string(data), "DATA_DIR") {
		t.Errorf("stored-run file must retain front matter (env: DATA_DIR); got:\n%s", data)
	}
}

// TestRunMain_UnknownSlug_Exit1 verifies an unknown slug (store.Load error) is a
// clear error (exit 1) and ui.Main is never reached.
func TestRunMain_UnknownSlug_Exit1(t *testing.T) {
	withArgs(t, []string{"ai-playbook", "run", "no-such-slug"})
	withStoreLoadFn(t, func(string) (store.Meta, string, error) {
		return store.Meta{}, "", os.ErrNotExist
	})
	called := false
	withUIRunFn(t, func(ui.Options) int { called = true; return 0 })

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
	withUIRunFn(t, func(ui.Options) int { called = true; return 0 })

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
	origUI := uiRunFn
	t.Cleanup(func() { uiRunFn = origUI })
	dir := t.TempDir()
	raw := filepath.Join(dir, "raw.md")
	if err := os.WriteFile(raw, []byte("# Just a doc\n\nno front matter here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withArgs(t, []string{"ai-playbook", "run", "--file", raw})
	var got ui.Options
	uiRunFn = func(o ui.Options) int { got = o; return 0 }

	if code := RunMain(); code != 0 {
		t.Fatalf("RunMain raw file: want exit 0, got %d", code)
	}
	if got.ProjectRoot != "" {
		t.Errorf("raw file must NOT set ProjectRoot; got %q", got.ProjectRoot)
	}
	if got.File != raw {
		t.Errorf("RunMain raw file: File = %q, want %q", got.File, raw)
	}
	if got.Cwd != "" {
		t.Errorf("RunMain raw file: Cwd = %q, want \"\" (no front matter → ui derives cwd from the file)", got.Cwd)
	}
}

// TestRunMain_FileProjectBound_SetsProjectRoot verifies a --file WITH a
// project_bound front matter resolves the heuristic project root, sets it via
// Options.ProjectRoot, renders the stripped body as-is, and opens at Cwd <root>.
func TestRunMain_FileProjectBound_SetsProjectRoot(t *testing.T) {
	origPR, origUI := projectRootFn, uiRunFn
	t.Cleanup(func() { projectRootFn, uiRunFn = origPR, origUI })
	dir := t.TempDir()
	fmFile := filepath.Join(dir, "build.md")
	body := "# Build\n\n```bash {id=verify}\ncd $PROJECT_ROOT && make\n```\n"
	content := "---\nname: Build\nproject_bound: true\n---\n" + body
	if err := os.WriteFile(fmFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	withArgs(t, []string{"ai-playbook", "run", "--file", fmFile})
	projectRootFn = func() string { return "/the/proj" }
	var got ui.Options
	uiRunFn = func(o ui.Options) int { got = o; return 0 }

	if code := RunMain(); code != 0 {
		t.Fatalf("RunMain fm file: want exit 0, got %d", code)
	}
	if got.ProjectRoot != "/the/proj" {
		t.Fatalf("project_bound --file must set Options.ProjectRoot; got %q", got.ProjectRoot)
	}
	if got.Cwd != "/the/proj" {
		t.Errorf("project_bound --file: Cwd = %q, want /the/proj", got.Cwd)
	}
	// The ORIGINAL file is passed to ui.Run (which strips the front matter for display
	// and extracts the env map for the gate) — NOT a stripped temp copy.
	if got.File != fmFile {
		t.Errorf("project_bound --file: File = %q, want the original %q (not a temp copy)", got.File, fmFile)
	}
	_ = body
}

// TestRunMain_FileWithFrontMatter_PassesOriginalFile verifies a --file WITH front
// matter (non-project_bound) renders the ORIGINAL file — not a stripped temp copy —
// so ui.Main extracts the env map for the confirmation gate (F25), and opens at
// --cwd <dir-of-file> so the body's relative paths resolve (F4). No PROJECT_ROOT.
func TestRunMain_FileWithFrontMatter_PassesOriginalFile(t *testing.T) {
	origUI := uiRunFn
	t.Cleanup(func() { uiRunFn = origUI })
	dir := t.TempDir()
	fmFile := filepath.Join(dir, "chapter.md")
	content := "---\nname: Chapter\nenv:\n  DATA_DIR:\n    value: /tmp/x\n    why: test\n---\n# Chapter\n\n```bash {id=go}\necho hi\n```\n"
	if err := os.WriteFile(fmFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	withArgs(t, []string{"ai-playbook", "run", "--file", fmFile})
	var got ui.Options
	uiRunFn = func(o ui.Options) int { got = o; return 0 }

	if code := RunMain(); code != 0 {
		t.Fatalf("RunMain fm file: want exit 0, got %d", code)
	}
	if got.ProjectRoot != "" {
		t.Errorf("non-project_bound --file must NOT set ProjectRoot; got %q", got.ProjectRoot)
	}
	if got.File != fmFile {
		t.Fatalf("fm file: File = %q, want the original %q", got.File, fmFile)
	}
	if got.Cwd != dir {
		t.Errorf("fm file: Cwd = %q, want %q (dir of file)", got.Cwd, dir)
	}
}

// TestRunMain_FileProjectBound_ResolvesDeclaredProjectRoot verifies a project_bound
// --file with an explicit project_root resolves it relative to the heuristic repo
// root (projectRootFn) and uses THAT as PROJECT_ROOT + --cwd — not the bare repo root.
func TestRunMain_FileProjectBound_ResolvesDeclaredProjectRoot(t *testing.T) {
	origPR, origUI := projectRootFn, uiRunFn
	t.Cleanup(func() { projectRootFn, uiRunFn = origPR, origUI })
	dir := t.TempDir()
	fmFile := filepath.Join(dir, "portable.md")
	content := "---\nname: Portable\nproject_bound: true\nproject_root: sub/proj\n---\n# Portable\n\n```bash {id=go}\necho hi\n```\n"
	if err := os.WriteFile(fmFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	withArgs(t, []string{"ai-playbook", "run", "--file", fmFile})
	projectRootFn = func() string { return "/repo" }
	var got ui.Options
	uiRunFn = func(o ui.Options) int { got = o; return 0 }

	if code := RunMain(); code != 0 {
		t.Fatalf("RunMain = %d", code)
	}
	want := filepath.Join("/repo", "sub/proj")
	if got.ProjectRoot != want {
		t.Fatalf("declared project_root: Options.ProjectRoot = %q, want %q", got.ProjectRoot, want)
	}
	if got.Cwd != want {
		t.Errorf("declared project_root: Cwd = %q, want %q", got.Cwd, want)
	}
	if got.File != fmFile {
		t.Errorf("declared project_root: File = %q, want original %q", got.File, fmFile)
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
	withUIRunFn(t, func(ui.Options) int { called = true; return 0 })

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
	withUIRunFn(t, func(ui.Options) int { viewerRan = true; return 0 })

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
	withUIRunFn(t, func(ui.Options) int { viewerRan = true; return 0 })

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

// TestBlocksFor_CarriesLang locks the payload-assembly wiring: blocksFor must
// propagate each block's fence language into autorun.Block.Lang so the headless
// runner can drive a script block through its interpreter (the --auto fix). A
// python block must arrive as Lang "python", Kind KindRun.
func TestBlocksFor_CarriesLang(t *testing.T) {
	body := "```python {id=py}\nprint('hi')\n```\n\n```bash {id=sh}\nls\n```\n"
	got := blocksFor(body)
	byID := map[string]autorun.Block{}
	for _, b := range got {
		byID[b.ID] = b
	}
	if byID["py"].Lang != "python" {
		t.Fatalf("python block Lang = %q, want %q", byID["py"].Lang, "python")
	}
	if byID["py"].Kind != autorun.KindRun {
		t.Fatalf("python block Kind = %v, want KindRun", byID["py"].Kind)
	}
	if byID["sh"].Lang != "bash" {
		t.Fatalf("shell block Lang = %q, want %q", byID["sh"].Lang, "bash")
	}
}

// TestBlocksFor_CarriesTimeout locks the timeout= threading: blocksFor must
// propagate each block's parsed Timeout into autorun.Block.Timeout so the
// headless runner enforces the declared ceiling (block-timeout spec).
func TestBlocksFor_CarriesTimeout(t *testing.T) {
	body := "```bash {id=slow timeout=15m}\nsleep 1\n```\n\n```bash {id=fast}\nls\n```\n"
	got := blocksFor(body)
	byID := map[string]autorun.Block{}
	for _, b := range got {
		byID[b.ID] = b
	}
	if byID["slow"].Timeout != 15*time.Minute {
		t.Fatalf("slow block Timeout = %v, want 15m", byID["slow"].Timeout)
	}
	if byID["fast"].Timeout != 0 {
		t.Fatalf("undeclared block Timeout = %v, want 0", byID["fast"].Timeout)
	}
}

// ---- run-journal wiring (v0.12.3 R1): both paths resolve identity at run start ----

// TestRunFile_JournalOptionsThreaded verifies a raw `run --file` resolves the
// journal path (shared data root + kb project key + path-sha1 run key), the
// absolute playbook path, and the content sha256 into ui.Options — the ui
// package never derives these itself.
func TestRunFile_JournalOptionsThreaded(t *testing.T) {
	origPR, origUI := projectRootFn, uiRunFn
	t.Cleanup(func() { projectRootFn, uiRunFn = origPR, origUI })
	dataRoot := t.TempDir()
	t.Setenv("AI_PLAYBOOK_DATA_DIR", dataRoot)
	projectRootFn = func() string { return "/my/proj" }

	dir := t.TempDir()
	path := filepath.Join(dir, "pb.md")
	content := "# PB\n\n```bash {id=go}\ntrue\n```\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	var got ui.Options
	uiRunFn = func(o ui.Options) int { got = o; return 0 }

	if code := runFile(path, "", ui.Options{}); code != 0 {
		t.Fatalf("runFile = %d", code)
	}
	abs, _ := filepath.Abs(path)
	wantPath := runlog.Path(dataRoot, "/my/proj", runlog.RunKey("", abs))
	if got.JournalPath != wantPath {
		t.Errorf("JournalPath = %q, want %q", got.JournalPath, wantPath)
	}
	if got.JournalPlaybookPath != abs {
		t.Errorf("JournalPlaybookPath = %q, want %q", got.JournalPlaybookPath, abs)
	}
	if got.JournalContentHash != runlog.ContentHash(content) {
		t.Errorf("JournalContentHash = %q, want the sha256 of the raw file", got.JournalContentHash)
	}
}

// TestRunPlaybook_JournalKeyIsSlug verifies a stored run keys its journal on
// the STORE SLUG (the playbook's stable identity), not its file location.
func TestRunPlaybook_JournalKeyIsSlug(t *testing.T) {
	origLoad, origPR, origUI := storeLoadFn, projectRootFn, uiRunFn
	t.Cleanup(func() { storeLoadFn, projectRootFn, uiRunFn = origLoad, origPR, origUI })
	dataRoot := t.TempDir()
	t.Setenv("AI_PLAYBOOK_DATA_DIR", dataRoot)
	projectRootFn = func() string { return "/my/proj" }

	dir := t.TempDir()
	path := filepath.Join(dir, "build.md")
	if err := os.WriteFile(path, []byte("---\nname: build\n---\n"+storedBody), 0o644); err != nil {
		t.Fatal(err)
	}
	storeLoadFn = func(string) (store.Meta, string, error) {
		return store.Meta{Slug: "build", Path: path}, "", nil
	}
	var got ui.Options
	uiRunFn = func(o ui.Options) int { got = o; return 0 }

	if code := runPlaybook("build", ui.Options{}); code != 0 {
		t.Fatalf("runPlaybook = %d", code)
	}
	if want := runlog.Path(dataRoot, "/my/proj", "build"); got.JournalPath != want {
		t.Errorf("JournalPath = %q, want the slug-keyed %q", got.JournalPath, want)
	}
}

// TestRunMain_Auto_JournalThreaded verifies the --auto branch resolves the
// SAME journal identity the viewer path would and threads it into the autorun
// run config (a raw file keys on its path sha1; the store slug is not
// synthesized from the filename).
func TestRunMain_Auto_JournalThreaded(t *testing.T) {
	origRun, origPR := autorunRunFn, projectRootFn
	t.Cleanup(func() { autorunRunFn, projectRootFn = origRun, origPR })
	dataRoot := t.TempDir()
	t.Setenv("AI_PLAYBOOK_DATA_DIR", dataRoot)
	projectRootFn = func() string { return "/my/proj" }

	dir := t.TempDir()
	f := filepath.Join(dir, "auto.md")
	content := "# Auto\n\n```bash {id=a}\necho a\n```\n"
	if err := os.WriteFile(f, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	withArgs(t, []string{"ai-playbook", "run", "--auto", "--file", f})

	var got autorun.RunConfig
	autorunRunFn = func(rc autorun.RunConfig) int { got = rc; return 0 }
	if code := RunMain(); code != 0 {
		t.Fatalf("RunMain auto = %d", code)
	}
	abs, _ := filepath.Abs(f)
	wantPath := runlog.Path(dataRoot, "/my/proj", runlog.RunKey("", abs))
	if got.JournalPath != wantPath {
		t.Errorf("JournalPath = %q, want %q (path-sha1 key — the filename slug is not an identity)", got.JournalPath, wantPath)
	}
	if got.JournalPlaybookPath != abs || got.JournalContentHash != runlog.ContentHash(content) {
		t.Errorf("journal identity = (%q, %q), want (%q, sha256 of the raw file)",
			got.JournalPlaybookPath, got.JournalContentHash, abs)
	}
}
