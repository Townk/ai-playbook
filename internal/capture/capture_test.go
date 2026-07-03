package capture

import (
	"errors"
	"os"
	"testing"

	"github.com/Townk/ai-playbook/internal/mux"
)

// fakeAtuin is the injectable last-command source.
type fakeAtuin struct {
	lc  LastCommand
	err error
}

func (f fakeAtuin) Last() (LastCommand, error) { return f.lc, f.err }

// fakeMux returns a canned screen dump.
type fakeMux struct {
	screen   string
	lastPane string
}

func (f *fakeMux) DumpScreen(pane string) (string, error) {
	f.lastPane = pane
	return f.screen, nil
}
func (f *fakeMux) SpawnFloat(mux.SpawnOptions) error      { return mux.ErrNotImplemented }
func (f *fakeMux) SpawnInputFloat(mux.SpawnOptions) error { return mux.ErrNotImplemented }
func (f *fakeMux) SpawnPane(mux.SpawnOptions) error       { return mux.ErrNotImplemented }
func (f *fakeMux) SpawnDocked(mux.SpawnOptions) error     { return mux.ErrNotImplemented }
func (f *fakeMux) TypeInto(string, string) error          { return mux.ErrNotImplemented }

// noGit makes project-root resolution deterministic in tests (no live git).
func noGit(dir string) (string, bool) { return "", false }

func TestSliceScrollback_Golden(t *testing.T) {
	// Golden outputs captured from the shell awk slice.
	t.Run("normal_drops_trailing_prompt", func(t *testing.T) {
		dump := "user@host proj % make build\n" +
			"gcc -c main.c\n" +
			"main.c:10:5: error: undeclared identifier 'foo'\n" +
			"make: *** [build] Error 1\n" +
			"user@host proj %\n"
		want := "user@host proj % make build\n" +
			"gcc -c main.c\n" +
			"main.c:10:5: error: undeclared identifier 'foo'\n" +
			"make: *** [build] Error 1"
		if got := SliceScrollback(dump, "make build", 200); got != want {
			t.Fatalf("got:\n%q\nwant:\n%q", got, want)
		}
	})

	t.Run("retyped_at_bottom_anchors_earlier", func(t *testing.T) {
		dump := "user@host proj % make build\n" +
			"gcc -c main.c\n" +
			"error: boom\n" +
			"user@host proj % make build\n"
		want := "user@host proj % make build\n" +
			"gcc -c main.c\n" +
			"error: boom"
		if got := SliceScrollback(dump, "make build", 200); got != want {
			t.Fatalf("got:\n%q\nwant:\n%q", got, want)
		}
	})

	t.Run("no_anchor_tail_max", func(t *testing.T) {
		dump := "line a\nline b\nline c\n"
		want := "line b\nline c"
		if got := SliceScrollback(dump, "nonexistent", 2); got != want {
			t.Fatalf("got:\n%q\nwant:\n%q", got, want)
		}
	})

	t.Run("empty_dump", func(t *testing.T) {
		if got := SliceScrollback("", "x", 200); got != "" {
			t.Fatalf("got %q", got)
		}
	})
}

func TestCapture_FailureAssembly(t *testing.T) {
	fm := &fakeMux{screen: "$ make\nboom: error\n$ "}
	r := Capture(Options{
		Mux:           fm,
		Atuin:         fakeAtuin{lc: LastCommand{Command: "make", Exit: "2", Dir: "/work/proj", Duration: "1500"}},
		PaneID:        "terminal_7",
		GitToplevelFn: noGit,
	})
	if r.Kind != "error" {
		t.Fatalf("kind = %q, want error", r.Kind)
	}
	if r.Command != "make" || r.Exit != "2" || r.DurationMs != "1500" {
		t.Fatalf("command fields wrong: %+v", r)
	}
	if r.CWD != "/work/proj" || r.ProjectRoot != "/work/proj" {
		t.Fatalf("cwd/root wrong: %+v", r)
	}
	if r.PaneID != "terminal_7" || fm.lastPane != "terminal_7" {
		t.Fatalf("pane wrong: %q / %q", r.PaneID, fm.lastPane)
	}
	if r.Project.Name != "proj" {
		t.Fatalf("project name = %q", r.Project.Name)
	}
	// scrollback sliced: anchored on "make" line (line 1), drop trailing prompt.
	want := "$ make\nboom: error"
	if r.Scrollback != want {
		t.Fatalf("scrollback = %q, want %q", r.Scrollback, want)
	}
}

func TestCapture_QuestionSkipsScrollback(t *testing.T) {
	fm := &fakeMux{screen: "$ ls\nfile\n$ "}
	r := Capture(Options{
		Mux:           fm,
		Atuin:         fakeAtuin{lc: LastCommand{Command: "ls", Exit: "0", Dir: "/work/proj"}},
		GitToplevelFn: noGit,
	})
	if r.Kind != "question" {
		t.Fatalf("kind = %q, want question", r.Kind)
	}
	if r.Scrollback != "" {
		t.Fatalf("question must not capture scrollback, got %q", r.Scrollback)
	}
}

func TestParseAtuinRows(t *testing.T) {
	out := "old cmd\t0\t/a\t10\n" +
		"make build\t2\t/work\t1500\n"
	lc := parseAtuinRows(out)
	if lc.Command != "make build" || lc.Exit != "2" || lc.Dir != "/work" || lc.Duration != "1500" {
		t.Fatalf("parsed wrong: %+v", lc)
	}
}

func TestParseAtuinRows_SkipsOwnTrigger(t *testing.T) {
	// The most recent entries are ai-playbook's own invocations; capture must look
	// back past them to the command the user actually ran.
	out := "gg build\t1\t/work\t37\n" +
		"ai-playbook troubleshoot\t-1\t/work\t5\n" +
		"/Users/x/.local/share/go/bin/ai-playbook session\t0\t/work\t2\n"
	lc := parseAtuinRows(out)
	if lc.Command != "gg build" || lc.Exit != "1" {
		t.Fatalf("expected the pre-trigger command, got: %+v", lc)
	}
}

func TestParseAtuinRows_SingleFieldGuard(t *testing.T) {
	// A row with only a command (no tabs): command == exit slot → exit cleared.
	lc := parseAtuinRows("just-a-command\n")
	if lc.Command != "just-a-command" || lc.Exit != "" {
		t.Fatalf("single-field guard failed: %+v", lc)
	}
}

// ── errMux ───────────────────────────────────────────────────────────────────

// errMux is a fake Mux whose DumpScreen always returns an error.
type errMux struct{}

func (e *errMux) DumpScreen(string) (string, error)      { return "", errors.New("dump failed") }
func (e *errMux) SpawnFloat(mux.SpawnOptions) error      { return mux.ErrNotImplemented }
func (e *errMux) SpawnInputFloat(mux.SpawnOptions) error { return mux.ErrNotImplemented }
func (e *errMux) SpawnPane(mux.SpawnOptions) error       { return mux.ErrNotImplemented }
func (e *errMux) SpawnDocked(mux.SpawnOptions) error     { return mux.ErrNotImplemented }
func (e *errMux) TypeInto(string, string) error          { return mux.ErrNotImplemented }

// ── ExitInt ──────────────────────────────────────────────────────────────────

func TestExitInt(t *testing.T) {
	tests := []struct {
		exit string
		want int
		ok   bool
	}{
		{"0", 0, true},
		{"1", 1, true},
		{"42", 42, true},
		{"-1", -1, true},
		{"", 0, false},
		{"abc", 0, false},
		{"2.5", 0, false},
	}
	for _, tc := range tests {
		r := Request{Exit: tc.exit}
		n, ok := r.ExitInt()
		if ok != tc.ok {
			t.Errorf("Exit=%q: ok=%v, want %v", tc.exit, ok, tc.ok)
			continue
		}
		if ok && n != tc.want {
			t.Errorf("Exit=%q: n=%d, want %d", tc.exit, n, tc.want)
		}
	}
}

// ── NewAtuin ─────────────────────────────────────────────────────────────────

func TestNewAtuin_DefaultBin(t *testing.T) {
	t.Setenv("ATUIN_BIN", "")
	a := NewAtuin()
	if a.Bin != "atuin" {
		t.Fatalf("want atuin, got %q", a.Bin)
	}
}

func TestNewAtuin_EnvBin(t *testing.T) {
	t.Setenv("ATUIN_BIN", "/custom/atuin")
	a := NewAtuin()
	if a.Bin != "/custom/atuin" {
		t.Fatalf("want /custom/atuin, got %q", a.Bin)
	}
}

// ── parseAtuinRows extras ────────────────────────────────────────────────────

func TestParseAtuinRows_AllSkipped(t *testing.T) {
	// Every row is an own trigger → loop exhausted → empty LastCommand.
	out := "ai-playbook troubleshoot\t0\t/work\t5\n" +
		"/usr/local/bin/ai-playbook session\t0\t/work\t2\n"
	lc := parseAtuinRows(out)
	if lc.Command != "" {
		t.Fatalf("expected empty LastCommand, got: %+v", lc)
	}
}

func TestParseAtuinRows_CommandEqualsExitGuard(t *testing.T) {
	// When the command text appears in the exit field (atuin oddity),
	// parseAtuinRows should clear the exit.
	lc := parseAtuinRows("make\tmake\t/dir\t10\n")
	if lc.Command != "make" || lc.Exit != "" {
		t.Fatalf("command-equals-exit guard failed: %+v", lc)
	}
}

// ── isOwnTrigger ─────────────────────────────────────────────────────────────

func TestIsOwnTrigger(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want bool
	}{
		{"bare ai-playbook", "ai-playbook troubleshoot", true},
		{"path ai-playbook", "/usr/local/bin/ai-playbook session", true},
		{"bare apb", "apb assist \"why did this fail\"", true},
		{"path apb", "/usr/local/bin/apb create foo", true},
		{"apb suffix but not own trigger", "apbx foo", false},
		{"apb suffix in longer word", "some-apb foo", false},
		{"unrelated command", "make build", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isOwnTrigger(tc.cmd); got != tc.want {
				t.Errorf("isOwnTrigger(%q) = %v, want %v", tc.cmd, got, tc.want)
			}
		})
	}
}

// ── Capture extras ───────────────────────────────────────────────────────────

func TestCapture_NilMuxNoScrollback(t *testing.T) {
	// Nil mux with a failed command: scrollback must stay empty.
	r := Capture(Options{
		Mux:           nil,
		Atuin:         fakeAtuin{lc: LastCommand{Command: "make", Exit: "1", Dir: "/work/proj"}},
		GitToplevelFn: noGit,
	})
	if r.Kind != "error" {
		t.Fatalf("kind = %q, want error", r.Kind)
	}
	if r.Scrollback != "" {
		t.Fatalf("nil mux must not produce scrollback, got %q", r.Scrollback)
	}
}

func TestCapture_DumpScreenError(t *testing.T) {
	// DumpScreen returning an error must leave Scrollback empty.
	r := Capture(Options{
		Mux:           &errMux{},
		Atuin:         fakeAtuin{lc: LastCommand{Command: "make", Exit: "2", Dir: "/work/proj"}},
		GitToplevelFn: noGit,
	})
	if r.Scrollback != "" {
		t.Fatalf("DumpScreen error must not set scrollback, got %q", r.Scrollback)
	}
}

func TestCapture_CWDFallback(t *testing.T) {
	// When the atuin record has no Dir, Capture falls back to os.Getwd().
	r := Capture(Options{
		Atuin:         fakeAtuin{lc: LastCommand{Command: "ls", Exit: "0", Dir: ""}},
		GitToplevelFn: noGit,
	})
	wd, err := os.Getwd()
	if err != nil {
		t.Skip("cannot determine cwd")
	}
	if r.CWD != wd {
		t.Fatalf("CWD = %q, want %q (os.Getwd)", r.CWD, wd)
	}
}

func TestCapture_ProjectRootFromGit(t *testing.T) {
	// When GitToplevelFn succeeds, ProjectRoot must be the returned root.
	r := Capture(Options{
		Atuin:         fakeAtuin{lc: LastCommand{Command: "ls", Exit: "0", Dir: "/work/proj"}},
		GitToplevelFn: func(string) (string, bool) { return "/work", true },
	})
	if r.ProjectRoot != "/work" {
		t.Fatalf("ProjectRoot = %q, want /work", r.ProjectRoot)
	}
}

func TestCapture_EmptyCommandNoScrollback(t *testing.T) {
	// An empty Command must skip scrollback even for an error exit.
	fm := &fakeMux{screen: "some output\n"}
	r := Capture(Options{
		Mux:           fm,
		Atuin:         fakeAtuin{lc: LastCommand{Command: "", Exit: "1", Dir: "/work/proj"}},
		GitToplevelFn: noGit,
	})
	if r.Scrollback != "" {
		t.Fatalf("empty command must not produce scrollback, got %q", r.Scrollback)
	}
}

func TestCapture_NilGitToplevelFnFallsBack(t *testing.T) {
	// Nil GitToplevelFn triggers the live gitToplevel shim.  With a
	// non-existent directory git fails, so ProjectRoot must equal CWD.
	const fakeDir = "/nonexistent/path/zzz_capture_test"
	r := Capture(Options{
		Atuin: fakeAtuin{lc: LastCommand{Command: "ls", Exit: "0", Dir: fakeDir}},
		// GitToplevelFn intentionally nil
	})
	if r.ProjectRoot != fakeDir {
		t.Fatalf("ProjectRoot = %q, want CWD fallback %q", r.ProjectRoot, fakeDir)
	}
}

func TestCapture_ExplicitScrollbackMax(t *testing.T) {
	// When ScrollbackMax > 0 it must be honoured (the cap branch is taken).
	// dump has anchor at line 1, 4 lines of content before prompt → exceeds max=2.
	dump := "$ make\nout1\nout2\nout3\n"
	fm := &fakeMux{screen: dump}
	r := Capture(Options{
		Mux:           fm,
		Atuin:         fakeAtuin{lc: LastCommand{Command: "make", Exit: "1", Dir: "/work/proj"}},
		GitToplevelFn: noGit,
		ScrollbackMax: 2,
	})
	// anchor=1, end=3 (nr-1=4-1), range 3 > 2 → start=3-2+1=2 → lines[1..2]="out1","out2"
	want := "out1\nout2"
	if r.Scrollback != want {
		t.Fatalf("scrollback = %q, want %q", r.Scrollback, want)
	}
}

// ── SliceScrollback extras ───────────────────────────────────────────────────

func TestSliceScrollback_AnchorWithCap(t *testing.T) {
	// Anchor found but range exceeds max → start is advanced to honour the cap.
	dump := "$ make\nout1\nout2\nout3\n"
	// lines: ["$ make","out1","out2","out3"], nr=4
	// anchor=1, end=3, range=3 > max=2 → start=3-2+1=2 → lines[1..2]
	want := "out1\nout2"
	if got := SliceScrollback(dump, "make", 2); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// ── live-shim smoke tests (skipped when outside a git repo) ─────────────────

func TestGitToplevel_NonexistentDir(t *testing.T) {
	_, ok := gitToplevel("/nonexistent/zzz_gitToplevel_test")
	if ok {
		t.Fatal("expected failure for nonexistent dir")
	}
}

func TestGitToplevel_RealRepo(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Skip("cannot determine cwd")
	}
	root, ok := gitToplevel(cwd)
	if !ok {
		t.Skip("test dir is not inside a git repo")
	}
	if root == "" {
		t.Fatal("gitToplevel returned empty root")
	}
}

func TestGitBranch_RealRepo(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Skip("cannot determine cwd")
	}
	br, ok := gitBranch(cwd)
	if !ok {
		t.Skip("gitBranch failed (detached HEAD or no git repo)")
	}
	if br == "" {
		t.Fatal("gitBranch returned empty branch")
	}
}

// ── ProjectRoot ──────────────────────────────────────────────────────────────

// AI_PLAYBOOK_PROJECT_ROOT env var takes priority over git detection and cwd.
func TestProjectRoot_EnvWins(t *testing.T) {
	t.Setenv("AI_PLAYBOOK_PROJECT_ROOT", "/from/env")
	got := ProjectRoot()
	if got != "/from/env" {
		t.Fatalf("ProjectRoot = %q, want /from/env", got)
	}
}
