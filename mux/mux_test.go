package mux

import (
	"errors"
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

func TestSpawnDeferred(t *testing.T) {
	z := &Zellij{Bin: "zellij"}
	if !errors.Is(z.SpawnFloat(SpawnOptions{}), ErrNotImplemented) {
		t.Fatal("SpawnFloat should be ErrNotImplemented")
	}
	if !errors.Is(z.SpawnPane(SpawnOptions{}), ErrNotImplemented) {
		t.Fatal("SpawnPane should be ErrNotImplemented")
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
