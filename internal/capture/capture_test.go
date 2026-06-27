package capture

import (
	"testing"

	"ai-playbook/internal/mux"
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
