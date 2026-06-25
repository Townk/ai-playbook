package driver

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// A controlled rc (no p10k/mise needed): a test function, alias, and a real chpwd
// hook that exports a var when entering a dir — exactly the mechanism mise/direnv
// use, so it proves auto-env-on-cd generically and deterministically.
func newTestDriver(t *testing.T) *Driver {
	t.Helper()
	zdot := t.TempDir()
	rc := "" +
		"tfn() { print -r -- FN_OK }\n" +
		"alias talias='print -r -- ALIAS_OK'\n" +
		"autoload -Uz add-zsh-hook\n" +
		"_tenv_hook() { [[ -r .tenv ]] && export TENV=\"$(<.tenv)\" }\n" +
		"add-zsh-hook chpwd _tenv_hook\n"
	if err := os.WriteFile(filepath.Join(zdot, ".zshrc"), []byte(rc), 0644); err != nil {
		t.Fatal(err)
	}
	d, err := Open(Options{Env: append(os.Environ(), "ZDOTDIR="+zdot)})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestResolvesFunctionAndAlias(t *testing.T) {
	d := newTestDriver(t)
	if r := d.Run("tfn", 10*time.Second); r.Out != "FN_OK" || r.Exit != 0 {
		t.Errorf("tfn → %+v", r)
	}
	if r := d.Run("talias", 10*time.Second); r.Out != "ALIAS_OK" || r.Exit != 0 {
		t.Errorf("talias → %+v", r)
	}
}

func TestCapturesStreamsAndExit(t *testing.T) {
	d := newTestDriver(t)
	r := d.Run("print -r -- to-out; print -ru2 -- to-err; (exit 7)", 10*time.Second)
	if r.Out != "to-out" {
		t.Errorf("stdout → %q", r.Out)
	}
	if r.Err != "to-err" {
		t.Errorf("stderr → %q", r.Err)
	}
	if r.Exit != 7 {
		t.Errorf("exit → %d", r.Exit)
	}
}

// The load-bearing one: a chpwd hook (mise/direnv-style) fires on the driver's cd.
func TestAutoEnvOnCd(t *testing.T) {
	d := newTestDriver(t)
	envdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(envdir, ".tenv"), []byte("hooked"), 0644); err != nil {
		t.Fatal(err)
	}
	d.Run("builtin cd -- "+shquote(envdir), 10*time.Second)
	if r := d.Run("print -r -- ${TENV:-MISSING}", 10*time.Second); r.Out != "hooked" {
		t.Errorf("auto-env on cd → %q (want hooked)", r.Out)
	}
}

func TestCdPersists(t *testing.T) {
	d := newTestDriver(t)
	d.Run("builtin cd -- /tmp", 5*time.Second)
	if r := d.Run("pwd", 5*time.Second); r.Out != "/tmp" {
		t.Errorf("pwd → %q", r.Out)
	}
}

func TestTimeoutKillsAndSurvives(t *testing.T) {
	d := newTestDriver(t)
	if r := d.Run("sleep 30", 2*time.Second); !r.TimedOut {
		t.Errorf("sleep 30 should time out → %+v", r)
	}
	if r := d.Run("echo alive", 5*time.Second); r.Out != "alive" {
		t.Errorf("shell should survive → %+v", r)
	}
}

// The load-bearing Stop test: a long in-flight RunID is interrupted by a
// concurrent Stop() and returns promptly (well under its own timeout). This
// proves Stop delivers a real interrupt to the foreground command.
func TestStopInterruptsInflightRun(t *testing.T) {
	d := newTestDriver(t)
	done := make(chan Result, 1)
	go func() {
		// A generous timeout: if Stop did NOT interrupt, the run would block here
		// for the full 30s and the select below would time out first.
		done <- d.RunID("", "sleep 30", 30*time.Second)
	}()
	// Wait until the command is actually running (a foreground pgrp appears, or
	// at least the run has had time to start).
	for i := 0; i < 150; i++ {
		time.Sleep(40 * time.Millisecond)
		if d.Pgrp() > 0 {
			break
		}
	}
	d.Stop()
	select {
	case r := <-done:
		// A Ctrl-C'd sleep exits non-zero (SIGINT → 130) — never a clean 0.
		if r.Exit == 0 && !r.TimedOut {
			t.Errorf("interrupted run should not report clean success → %+v", r)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Stop did not interrupt the in-flight run promptly")
	}
	// The shell must survive the interrupt and remain drivable.
	if r := d.Run("print -r -- alive", 5*time.Second); r.Out != "alive" {
		t.Errorf("shell should survive Stop → %+v", r)
	}
}

func TestForegroundPgrpIsRealPID(t *testing.T) {
	d := newTestDriver(t)
	done := make(chan struct{}, 1)
	go func() { d.Run("sleep 30", 35*time.Second); done <- struct{}{} }()
	pg := 0
	for i := 0; i < 120 && pg == 0; i++ {
		time.Sleep(40 * time.Millisecond)
		if p := d.Pgrp(); p > 0 && p != d.shellPid {
			pg = p
		}
	}
	if pg == 0 {
		t.Fatal("never observed a running command's foreground pgrp")
	}
	if unix.Kill(pg, 0) != nil {
		t.Errorf("foreground pgrp %d is not a live process", pg)
	}
	_ = unix.Kill(-pg, unix.SIGTERM) // targeted kill of that command's whole group
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("killing the command by pgrp did not end the run")
	}
}
