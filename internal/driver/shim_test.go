package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// envWithoutZDOTDIR returns os.Environ() with any pre-existing ZDOTDIR removed, so
// the appended test ZDOTDIR is the one getenvFn resolves (getenvFn returns the
// FIRST prefix match — a stray ambient ZDOTDIR would otherwise shadow the fixture).
func envWithoutZDOTDIR() []string {
	src := os.Environ()
	out := make([]string, 0, len(src))
	for _, e := range src {
		if !strings.HasPrefix(e, "ZDOTDIR=") {
			out = append(out, e)
		}
	}
	return out
}

// TestZshShim_ChainsRealRcAndDisablesHistory is the regression guard for the
// history-leak fix. It builds a SYNTHETIC "real" ZDOTDIR (so the test is
// deterministic — no dependency on the user's live rc/atuin), opens a zsh driver
// pointed at it, and proves both halves of the contract:
//
//   - the shim chained the user's real .zshenv AND .zshrc (env var, rc var, a
//     function, and an alias all survive) — i.e. we did NOT silently drop the
//     user's environment; and
//   - recording was disabled AT INIT: HISTFILE=/dev/null and SAVEHIST=0 even
//     though the real .zshrc explicitly set them to a real file / 99999.
func TestZshShim_ChainsRealRcAndDisablesHistory(t *testing.T) {
	realDir := t.TempDir()
	// .zshenv: exported early, before .zshrc — proves the .zshenv chain fired.
	if err := os.WriteFile(filepath.Join(realDir, ".zshenv"),
		[]byte("export APB_TEST_ENV=envval\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// .zshrc: alias + function + exported var + history settings the shim must
	// override AFTER sourcing (proving the init-time disable wins over the rc).
	rc := "" +
		"alias apbtestalias='echo A'\n" +
		"apbtestfn(){ echo F }\n" +
		"export APB_TEST_RC=rcval\n" +
		"HISTFILE=$HOME/realhist\n" +
		"SAVEHIST=99999\n"
	if err := os.WriteFile(filepath.Join(realDir, ".zshrc"), []byte(rc), 0o644); err != nil {
		t.Fatal(err)
	}

	d, err := Open(Options{Shell: "zsh", Env: append(envWithoutZDOTDIR(), "ZDOTDIR="+realDir)})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	// Shim must be active: a temp dir with all four startup files present.
	if d.shimDir == "" {
		t.Fatal("shimDir is empty — shim was not created for zsh")
	}
	for _, name := range []string{".zshenv", ".zprofile", ".zshrc", ".zlogin"} {
		if _, statErr := os.Stat(filepath.Join(d.shimDir, name)); statErr != nil {
			t.Errorf("shim file %s missing after Open: %v", name, statErr)
		}
	}

	// --- env preserved (did we silently drop the user's env?) ---
	if r := d.Run("print -r -- $APB_TEST_ENV", 5*time.Second); r.Out != "envval" {
		t.Errorf("APB_TEST_ENV = %q, want envval (real .zshenv not chained)", r.Out)
	}
	if r := d.Run("print -r -- $APB_TEST_RC", 5*time.Second); r.Out != "rcval" {
		t.Errorf("APB_TEST_RC = %q, want rcval (real .zshrc not chained)", r.Out)
	}
	if r := d.Run("print -r -- ${functions[apbtestfn]:+function}", 5*time.Second); r.Out != "function" {
		t.Errorf("apbtestfn not defined as a function (real .zshrc not chained), got %q", r.Out)
	}
	if r := d.Run("alias apbtestalias", 5*time.Second); !strings.Contains(r.Out, "apbtestalias=") {
		t.Errorf("apbtestalias not defined (real .zshrc not chained), got %q", r.Out)
	}

	// --- recording disabled at INIT, overriding the rc's history settings ---
	if r := d.Run("print -r -- $HISTFILE", 5*time.Second); r.Out != "/dev/null" {
		t.Errorf("HISTFILE = %q, want /dev/null (init-time disable did not override rc)", r.Out)
	}
	if r := d.Run("print -r -- $SAVEHIST", 5*time.Second); r.Out != "0" {
		t.Errorf("SAVEHIST = %q, want 0 (init-time disable did not override rc)", r.Out)
	}

	// Shim dir is cleaned up on Close.
	dir := d.shimDir
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Errorf("shim dir %s not removed after Close (stat err: %v)", dir, statErr)
	}
}

// TestHistoryShimFiles_PerAdapter locks the interface contract: zsh returns the
// four shim files, bash/sh return nil (they keep the runtime historyOff path).
func TestHistoryShimFiles_PerAdapter(t *testing.T) {
	z := zshAdapter{}.historyShimFiles()
	if z == nil {
		t.Fatal("zsh historyShimFiles() = nil, want the four startup files")
	}
	for _, name := range []string{".zshenv", ".zprofile", ".zshrc", ".zlogin"} {
		if _, ok := z[name]; !ok {
			t.Errorf("zsh shim missing %s", name)
		}
	}
	if got := (bashAdapter{}).historyShimFiles(); got != nil {
		t.Errorf("bash historyShimFiles() = %v, want nil", got)
	}
	if got := (shAdapter{}).historyShimFiles(); got != nil {
		t.Errorf("sh historyShimFiles() = %v, want nil", got)
	}
}

// TestNonShimShell_UsesRuntimePath proves bash (no shim) leaves shimDir empty and
// still gets history disabled via the runtime historyOff path at Open.
func TestNonShimShell_UsesRuntimePath(t *testing.T) {
	d, err := Open(Options{Shell: "bash"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	if d.shimDir != "" {
		t.Errorf("bash shimDir = %q, want empty (no shim for bash)", d.shimDir)
	}
	if r := d.Run("printf '%s' \"$HISTFILE\"", 5*time.Second); r.Out != "/dev/null" {
		t.Errorf("bash HISTFILE = %q, want /dev/null (runtime historyOff path)", r.Out)
	}
}
