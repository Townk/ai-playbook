package mux

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/config"
)

// fakeMux is the injectable test double used by capture tests too.
type fakeMux struct {
	screen   string
	dumpErr  error
	lastPane string
}

func (f *fakeMux) DumpScreen(pane string) (string, error) {
	f.lastPane = pane
	return f.screen, f.dumpErr
}
func (f *fakeMux) SpawnFloat(SpawnOptions) error      { return ErrNotImplemented }
func (f *fakeMux) SpawnInputFloat(SpawnOptions) error { return ErrNotImplemented }
func (f *fakeMux) SpawnPane(SpawnOptions) error       { return ErrNotImplemented }
func (f *fakeMux) SpawnDocked(SpawnOptions) error     { return ErrNotImplemented }
func (f *fakeMux) TypeInto(string, string) error      { return ErrNotImplemented }

// Compile-time check that fakeMux and the templated impl satisfy Mux.
var (
	_ Mux = (*fakeMux)(nil)
	_ Mux = (*templated)(nil)
)

func TestFakeMux_DumpScreen(t *testing.T) {
	f := &fakeMux{screen: "line1\nline2\n"}
	got, err := f.DumpScreen("terminal_3")
	if err != nil {
		t.Fatal(err)
	}
	if got != "line1\nline2\n" {
		t.Fatalf("screen = %q", got)
	}
	if f.lastPane != "terminal_3" {
		t.Fatalf("pane = %q", f.lastPane)
	}
}

func TestSpawnPaneDeferred(t *testing.T) {
	m := FromConfig(config.Default())
	if !errors.Is(m.SpawnPane(SpawnOptions{}), ErrNotImplemented) {
		t.Fatal("SpawnPane should be ErrNotImplemented")
	}
}

// floatArgv asserts the default profile reproduces the broker's
// `zellij action new-pane --floating …` invocation (so view-diff is unchanged).
func TestDefaultProfile_FloatArgv(t *testing.T) {
	tpl := config.Default().Mux
	got := tpl.Substitute(tpl.OpenFloatingPane, config.Subst{
		Cmd:    []string{"delta", "--side-by-side", "/tmp/patch"},
		Cwd:    "/proj/root",
		Name:   "diff:fix",
		Width:  "90%",
		Height: "90%",
	})
	want := []string{
		"zellij", "action", "new-pane", "--floating",
		"--width", "90%", "--height", "90%", "--close-on-exit",
		"--cwd", "/proj/root", "--name", "diff:fix",
		"--", "delta", "--side-by-side", "/tmp/patch",
	}
	assertArgv(t, got, want)
}

// With no cwd/name set the paired flags drop entirely (matching the old code,
// which emitted --cwd/--name only when set).
func TestDefaultProfile_FloatArgv_OmitsEmptyPairs(t *testing.T) {
	tpl := config.Default().Mux
	got := tpl.Substitute(tpl.OpenFloatingPane, config.Subst{
		Cmd:    []string{"less", "/tmp/p"},
		Width:  "90%",
		Height: "90%",
	})
	want := []string{
		"zellij", "action", "new-pane", "--floating",
		"--width", "90%", "--height", "90%", "--close-on-exit",
		"--", "less", "/tmp/p",
	}
	assertArgv(t, got, want)
}

// The default dump-screen profile reproduces `zellij action dump-screen [-p p]`.
func TestDefaultProfile_DumpScreenArgv(t *testing.T) {
	tpl := config.Default().Mux
	withPane := tpl.Substitute(tpl.DumpScreen, config.Subst{Pane: "terminal_3"})
	assertArgv(t, withPane, []string{"zellij", "action", "dump-screen", "-p", "terminal_3"})

	focused := tpl.Substitute(tpl.DumpScreen, config.Subst{})
	assertArgv(t, focused, []string{"zellij", "action", "dump-screen"})
}

func TestDefaultProfile_DockedArgv(t *testing.T) {
	tpl := config.Default().Mux
	got := tpl.Substitute(tpl.OpenDockedPane, config.Subst{
		Cmd:  []string{"ai-playbook", "run", "/tmp/pb.md"},
		Cwd:  "/proj",
		Name: "playbook",
	})
	want := []string{
		"zellij", "action", "new-pane", "--direction", "right", "--close-on-exit",
		"--cwd", "/proj", "--name", "playbook",
		"--", "ai-playbook", "run", "/tmp/pb.md",
	}
	assertArgv(t, got, want)
}

// The input float (open-input-float) is borderless+pinned, has an empty --name,
// and is sized in ABSOLUTE columns/rows (bare integers, not percents) — matching
// ai-assist-summon's `--borderless true --pinned true --name "" --width 57
// --height <measured>`.
func TestDefaultProfile_InputFloatArgv(t *testing.T) {
	tpl := config.Default().Mux
	got := tpl.Substitute(tpl.OpenInputFloat, config.Subst{
		Cmd:    []string{"ai-playbook", "input", "--type", "text"},
		Cwd:    "/proj",
		Width:  "57", // absolute columns (the mux emits a bare int for WidthCols>0)
		Height: "9",  // measured rows
	})
	want := []string{
		"zellij", "action", "new-pane", "--floating", "--close-on-exit",
		"--name", "", "--borderless", "true", "--pinned", "true",
		"--width", "57", "--height", "9",
		"--cwd", "/proj",
		"--", "ai-playbook", "input", "--type", "text",
	}
	assertArgv(t, got, want)
}

// SpawnInputFloat resolves the open-input-float template and emits ABSOLUTE sizes
// (WidthCols/HeightRows → bare integers) into the argv. A stub captures the argv.
func TestSpawnInputFloat_AbsoluteSizesAndFlags(t *testing.T) {
	dir := t.TempDir()
	argfile := filepath.Join(dir, "args")
	stub := filepath.Join(dir, "stubmux")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argfile + "\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Mux.OpenInputFloat = stub + ` new-pane --floating --close-on-exit --name "" --borderless true --pinned true --width {width} --height {height} {cwdarg} -- {cmd}`
	m := FromConfig(cfg)
	if err := m.SpawnInputFloat(SpawnOptions{
		Cmd:        []string{"ai-playbook", "input"},
		Cwd:        "/c",
		WidthCols:  57,
		HeightRows: 11,
	}); err != nil {
		t.Fatalf("SpawnInputFloat: %v", err)
	}
	b, err := os.ReadFile(argfile)
	if err != nil {
		t.Fatalf("stub did not record args: %v", err)
	}
	out := string(b)
	for _, want := range []string{"--floating", "--borderless\ntrue", "--pinned\ntrue", "--width\n57", "--height\n11"} {
		if !strings.Contains(out, want) {
			t.Errorf("input-float argv missing %q\n%s", want, out)
		}
	}
	// Bare integers, NOT percents.
	if strings.Contains(out, "57%") || strings.Contains(out, "11%") {
		t.Errorf("input-float must use absolute (bare int) sizes, not percent\n%s", out)
	}
}

func TestDefaultProfile_TypeIntoArgv(t *testing.T) {
	tpl := config.Default().Mux
	got := tpl.Substitute(tpl.TypeIntoPane, config.Subst{Pane: "terminal_3", Text: "git status"})
	assertArgv(t, got, []string{"zellij", "action", "write-chars", "--pane-id", "terminal_3", "git status"})
}

// With a pane id, TypeInto resolves the targeted argv (--pane-id <pane>) so the
// write is focus-independent. The command route always passes req.PaneID.
func TestTypeInto_TargetsPaneArgv(t *testing.T) {
	tr := FromConfig(config.Default()).(*templated)
	got := tr.typeIntoArgv("terminal_3", "git log")
	assertArgv(t, got, []string{"zellij", "action", "write-chars", "--pane-id", "terminal_3", "git log"})
}

// With an empty pane, TypeInto strips the pane-id flag entirely (no stray empty
// "--pane-id"), falling back to a focused write.
func TestTypeInto_EmptyPaneFallsBackToFocused(t *testing.T) {
	tr := FromConfig(config.Default()).(*templated)
	got := tr.typeIntoArgv("", "git log")
	assertArgv(t, got, []string{"zellij", "action", "write-chars", "git log"})
	for _, a := range got {
		if a == "--pane-id" || a == "" {
			t.Fatalf("empty pane must not leave a dangling --pane-id: %q", got)
		}
	}
}

// SpawnFloat with no command errors (no malformed spawn).
func TestSpawnFloat_NeedsCommand(t *testing.T) {
	m := FromConfig(config.Default())
	if err := m.SpawnFloat(SpawnOptions{}); err == nil {
		t.Fatal("SpawnFloat with no Cmd should error")
	}
}

// SpawnFloat exec's the resolved template argv. A stub captures argv to a file
// (config overrides the template to point at the stub) so no real zellij runs.
func TestSpawnFloat_ExecsStub(t *testing.T) {
	dir := t.TempDir()
	argfile := filepath.Join(dir, "args")
	stub := filepath.Join(dir, "stubmux")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argfile + "\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Mux.OpenFloatingPane = stub + " new-pane --floating --close-on-exit {cwdarg} {namearg} -- {cmd}"
	m := FromConfig(cfg)
	if err := m.SpawnFloat(SpawnOptions{
		Cmd:  []string{"less", "/tmp/p"},
		Cwd:  "/c",
		Name: "diff:x",
	}); err != nil {
		t.Fatalf("SpawnFloat: %v", err)
	}
	b, err := os.ReadFile(argfile)
	if err != nil {
		t.Fatalf("stub did not record args: %v", err)
	}
	out := string(b)
	for _, want := range []string{"new-pane", "--floating", "--close-on-exit", "/c", "diff:x", "less", "/tmp/p"} {
		if !strings.Contains(out, want) {
			t.Errorf("stub argv missing %q\n%s", want, out)
		}
	}
}

func assertArgv(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("argv len = %d, want %d\ngot:  %v\nwant: %v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("argv[%d] = %q, want %q\ngot:  %v", i, got[i], want[i], got)
		}
	}
}
