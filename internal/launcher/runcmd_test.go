package launcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/store"
)

// ---- resolveRunArgs — the run argument resolution matrix ----

func TestResolveRunArgs_Matrix(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		wantKind  string
		wantValue string
		wantErr   bool
	}{
		{"bare positional ⇒ playbook", []string{"build"}, "playbook", "build", false},
		{"--playbook flag", []string{"--playbook", "build"}, "playbook", "build", false},
		{"--file flag", []string{"--file", "/p.md"}, "file", "/p.md", false},
		{"both --playbook and --file → error", []string{"--playbook", "build", "--file", "/p.md"}, "", "", true},
		{"positional + --file → error", []string{"build", "--file", "/p.md"}, "", "", true},
		{"none → error", []string{}, "", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kind, value, err := resolveRunArgs(c.args)
			if c.wantErr {
				if err == nil {
					t.Fatalf("resolveRunArgs(%v): want error, got (%q,%q,nil)", c.args, kind, value)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveRunArgs(%v): unexpected error: %v", c.args, err)
			}
			if kind != c.wantKind {
				t.Errorf("kind = %q, want %q", kind, c.wantKind)
			}
			if value != c.wantValue {
				t.Errorf("value = %q, want %q", value, c.wantValue)
			}
		})
	}
}

// ---- seam helpers ----

func withStoreLoadFn(t *testing.T, fn func(string) (store.Meta, string, error)) {
	t.Helper()
	old := storeLoadFn
	storeLoadFn = fn
	t.Cleanup(func() { storeLoadFn = old })
}

func withAdaptModelFn(t *testing.T, fn func(sys, user string) (string, error)) {
	t.Helper()
	old := adaptModelFn
	adaptModelFn = fn
	t.Cleanup(func() { adaptModelFn = old })
}

func withProjectRootFn(t *testing.T, fn func() string) {
	t.Helper()
	old := projectRootFn
	projectRootFn = fn
	t.Cleanup(func() { projectRootFn = old })
}

const validAdapted = "# Build the app\n\n```bash {id=verify}\nmake build\n```\n"
const originalBody = "# Build\n\n```bash {id=verify}\nmake\n```\n"

// ---- adaptOnRun: junk-guard + valid path ----

// TestAdaptOnRun_JunkFallsBackToOriginal verifies a junk adaptation (not a valid
// playbook) falls back to the original body with no banner.
func TestAdaptOnRun_JunkFallsBackToOriginal(t *testing.T) {
	meta := store.Meta{Slug: "build-app"}
	junk := "I adapted it for you, it's all set." // no H1, no runnable block
	renderFile, origFile, bannerSlug, err := adaptOnRun(meta, originalBody, "/tmp/x", func(sys, user string) (string, error) {
		return junk, nil
	})
	if err != nil {
		t.Fatalf("adaptOnRun: unexpected error: %v", err)
	}
	t.Cleanup(func() { os.Remove(renderFile); os.Remove(origFile) })
	if bannerSlug != "" {
		t.Errorf("junk adaptation: bannerSlug = %q, want \"\"", bannerSlug)
	}
	got, _ := os.ReadFile(renderFile)
	if string(got) != originalBody {
		t.Errorf("junk adaptation: renderFile = %q, want the ORIGINAL body %q", got, originalBody)
	}
}

// TestAdaptOnRun_ValidUsesAdapted verifies a valid adaptation is rendered with the
// banner slug set and the original preserved for the diff.
func TestAdaptOnRun_ValidUsesAdapted(t *testing.T) {
	meta := store.Meta{Slug: "build-app"}
	renderFile, origFile, bannerSlug, err := adaptOnRun(meta, originalBody, "/tmp/x", func(sys, user string) (string, error) {
		return validAdapted, nil
	})
	if err != nil {
		t.Fatalf("adaptOnRun: unexpected error: %v", err)
	}
	t.Cleanup(func() { os.Remove(renderFile); os.Remove(origFile) })
	if bannerSlug != "build-app" {
		t.Errorf("valid adaptation: bannerSlug = %q, want \"build-app\"", bannerSlug)
	}
	got, _ := os.ReadFile(renderFile)
	if string(got) != validAdapted {
		t.Errorf("valid adaptation: renderFile = %q, want the ADAPTED body %q", got, validAdapted)
	}
	orig, _ := os.ReadFile(origFile)
	if string(orig) != originalBody {
		t.Errorf("valid adaptation: origFile = %q, want the ORIGINAL body %q", orig, originalBody)
	}
}

// TestAdaptOnRun_PromptNamesTarget verifies the adaptFn is handed a system prompt
// naming the target dir (the AdaptPrompt wiring) and the original body as the user
// message.
func TestAdaptOnRun_PromptNamesTarget(t *testing.T) {
	meta := store.Meta{Slug: "build-app", Name: "Build"}
	var gotSys, gotUser string
	rf, of, _, err := adaptOnRun(meta, originalBody, "/Users/me/proj", func(sys, user string) (string, error) {
		gotSys, gotUser = sys, user
		return validAdapted, nil
	})
	if err != nil {
		t.Fatalf("adaptOnRun: %v", err)
	}
	t.Cleanup(func() { os.Remove(rf); os.Remove(of) })
	if !strings.Contains(gotSys, "/Users/me/proj") {
		t.Errorf("adaptFn system prompt must name the target dir; got:\n%s", gotSys)
	}
	if gotUser != originalBody {
		t.Errorf("adaptFn user message = %q, want the original body", gotUser)
	}
}

// ---- resolveTargetDir ----

// TestResolveTargetDir_ExistingWorkdir verifies an existing workdir is used as-is.
func TestResolveTargetDir_ExistingWorkdir(t *testing.T) {
	dir := t.TempDir()
	got := resolveTargetDir(store.Meta{Workdir: dir})
	if got != dir {
		t.Errorf("resolveTargetDir = %q, want the existing workdir %q", got, dir)
	}
}

// TestResolveTargetDir_EmptyWorkdir_OffMuxFallsBackToProjectRoot verifies that with
// no workdir and no mux (the off-mux edge — tests have no zellij), resolveTargetDir
// falls back to capture.ProjectRoot rather than blocking on a float.
func TestResolveTargetDir_EmptyWorkdir_OffMuxFallsBackToProjectRoot(t *testing.T) {
	withProjectRootFn(t, func() string { return "/the/project/root" })
	got := resolveTargetDir(store.Meta{Workdir: ""})
	if got != "/the/project/root" {
		t.Errorf("off-mux empty workdir: resolveTargetDir = %q, want the project-root fallback", got)
	}
}

// ---- RunMain: the playbook branch routes through adapt ----

// TestRunMain_Playbook_RoutesThroughAdapt verifies a bare slug is store.Load'd,
// adapted (via the injected adaptModelFn), and rendered as
// `run --file <renderFile> --cwd <target> --adapted-from <slug> --orig-file <orig>`.
func TestRunMain_Playbook_RoutesThroughAdapt(t *testing.T) {
	dir := t.TempDir()
	withArgs(t, []string{"ai-playbook", "run", "build-app"})
	withStoreLoadFn(t, func(slug string) (store.Meta, string, error) {
		if slug != "build-app" {
			t.Fatalf("store.Load got slug %q", slug)
		}
		return store.Meta{Slug: "build-app", Workdir: dir}, originalBody, nil
	})
	adaptCalled := false
	withAdaptModelFn(t, func(sys, user string) (string, error) {
		adaptCalled = true
		return validAdapted, nil
	})

	var captured []string
	withUIMainFn(t, func() int {
		captured = append([]string{}, os.Args...)
		return 0
	})

	if code := RunMain(); code != 0 {
		t.Fatalf("RunMain playbook: want exit 0, got %d", code)
	}
	if !adaptCalled {
		t.Fatal("RunMain playbook: the adapt model call must run")
	}
	// {bin, run, --file, <rf>, --cwd, <dir>, --adapted-from, build-app, --orig-file, <of>}
	if len(captured) != 10 {
		t.Fatalf("RunMain playbook: reshaped args = %v (len %d), want 10", captured, len(captured))
	}
	if captured[1] != "run" || captured[2] != "--file" {
		t.Errorf("RunMain playbook: args[1:3] = %v, want [run --file]", captured[1:3])
	}
	if captured[4] != "--cwd" || captured[5] != dir {
		t.Errorf("RunMain playbook: --cwd not %q; args = %v", dir, captured)
	}
	if captured[6] != "--adapted-from" || captured[7] != "build-app" {
		t.Errorf("RunMain playbook: --adapted-from build-app missing; args = %v", captured)
	}
	if captured[8] != "--orig-file" {
		t.Errorf("RunMain playbook: --orig-file missing; args = %v", captured)
	}
	got, _ := os.ReadFile(captured[3])
	if string(got) != validAdapted {
		t.Errorf("RunMain playbook: render file = %q, want adapted body", got)
	}
	os.Remove(captured[3])
	os.Remove(captured[9])
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

// ---- RunMain: the --file branch (raw as-is vs has-front-matter→adapt) ----

// TestRunMain_FileRaw_RendersAsIs verifies a raw file (NO front matter) renders
// as-is via `run --file <path>` with no adapt (no --adapted-from).
func TestRunMain_FileRaw_RendersAsIs(t *testing.T) {
	dir := t.TempDir()
	raw := filepath.Join(dir, "raw.md")
	if err := os.WriteFile(raw, []byte("# Just a doc\n\nno front matter here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withArgs(t, []string{"ai-playbook", "run", "--file", raw})
	withAdaptModelFn(t, func(string, string) (string, error) {
		t.Fatal("raw file must NOT call the adapt model")
		return "", nil
	})
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

// TestRunMain_FileWithFrontMatter_Adapts verifies a file WITH front matter adapts
// (the adapt model runs, the render carries the --adapted-from banner).
func TestRunMain_FileWithFrontMatter_Adapts(t *testing.T) {
	dir := t.TempDir()
	wd := t.TempDir()
	fmFile := filepath.Join(dir, "build.md")
	content := "---\nname: Build\nworkdir: " + wd + "\n---\n" + originalBody
	if err := os.WriteFile(fmFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	withArgs(t, []string{"ai-playbook", "run", "--file", fmFile})
	adaptCalled := false
	withAdaptModelFn(t, func(string, string) (string, error) {
		adaptCalled = true
		return validAdapted, nil
	})
	var captured []string
	withUIMainFn(t, func() int {
		captured = append([]string{}, os.Args...)
		return 0
	})

	if code := RunMain(); code != 0 {
		t.Fatalf("RunMain fm file: want exit 0, got %d", code)
	}
	if !adaptCalled {
		t.Fatal("RunMain fm file: a front-matter file must adapt")
	}
	joined := strings.Join(captured, " ")
	if !strings.Contains(joined, "--adapted-from build") {
		t.Errorf("RunMain fm file: expected --adapted-from banner; args = %v", captured)
	}
	if !strings.Contains(joined, "--cwd "+wd) {
		t.Errorf("RunMain fm file: expected --cwd %s; args = %v", wd, captured)
	}
	// cleanup temp render/orig files
	for i, a := range captured {
		if a == "--file" || a == "--orig-file" {
			os.Remove(captured[i+1])
		}
	}
}
