package mux

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
func (f *fakeMux) SpawnFloat(SpawnOptions) error { return ErrNotImplemented }
func (f *fakeMux) SpawnPane(SpawnOptions) error  { return ErrNotImplemented }

// Compile-time check that fakeMux and Zellij satisfy Mux.
var (
	_ Mux = (*fakeMux)(nil)
	_ Mux = (*Zellij)(nil)
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
	z := &Zellij{Bin: "zellij"}
	if !errors.Is(z.SpawnPane(SpawnOptions{}), ErrNotImplemented) {
		t.Fatal("SpawnPane should be ErrNotImplemented")
	}
}

// floatArgs reproduces the broker's `zellij action new-pane --floating …`
// invocation; assert the vector without a real zellij.
func TestFloatArgs_BrokerShape(t *testing.T) {
	z := &Zellij{Bin: "zellij"}
	got := z.floatArgs(SpawnOptions{
		Cmd:  []string{"delta", "--side-by-side", "/tmp/patch"},
		Cwd:  "/proj/root",
		Name: "diff:fix",
	})
	want := []string{
		"action", "new-pane", "--floating",
		"--width", "90%", "--height", "90%", "--close-on-exit",
		"--cwd", "/proj/root", "--name", "diff:fix",
		"--", "delta", "--side-by-side", "/tmp/patch",
	}
	if len(got) != len(want) {
		t.Fatalf("floatArgs len = %d, want %d\ngot:  %v\nwant: %v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("floatArgs[%d] = %q, want %q\ngot:  %v", i, got[i], want[i], got)
		}
	}
}

// SpawnFloat requires a command (no command → error, never a malformed spawn).
func TestSpawnFloat_NeedsCommand(t *testing.T) {
	z := &Zellij{Bin: "zellij"}
	if err := z.SpawnFloat(SpawnOptions{}); err == nil {
		t.Fatal("SpawnFloat with no Cmd should error")
	}
}

// SpawnFloat actually exec's the resolved binary with the broker-shaped args. A
// stub captures argv to a file (ZELLIJ_BIN points at it) so no real zellij runs.
func TestSpawnFloat_ExecsStub(t *testing.T) {
	dir := t.TempDir()
	argfile := filepath.Join(dir, "args")
	stub := filepath.Join(dir, "zellij")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argfile + "\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	z := &Zellij{Bin: stub}
	if err := z.SpawnFloat(SpawnOptions{
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

func TestResolveZellijBin_EnvOverride(t *testing.T) {
	// A non-executable env value is ignored (falls through to PATH/known dirs).
	t.Setenv("ZELLIJ_BIN", "/definitely/not/a/real/zellij")
	got := resolveZellijBin()
	if got == "/definitely/not/a/real/zellij" {
		t.Fatal("non-executable ZELLIJ_BIN should not be used")
	}
}
