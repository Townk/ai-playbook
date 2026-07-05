package driver

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// openShell opens a driver for one crossShell row with an isolated interactive
// rc, skipping the sub-test when the shell binary is absent. Shared by the
// capture-retention / env-export / stdin matrix tests below.
func openShell(t *testing.T, cs crossShell) *Driver {
	t.Helper()
	if _, err := exec.LookPath(cs.sel); err != nil {
		t.Skipf("%s not found on PATH; skipping", cs.sel)
	}
	dir := t.TempDir()
	for _, f := range cs.rcFiles(dir) {
		if err := os.WriteFile(filepath.Join(dir, f.name), []byte(f.content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	d, err := Open(Options{Shell: cs.sel, Env: envWith(os.Environ(), cs.rcEnv(dir))})
	if err != nil {
		t.Fatalf("Open(%s): %v", cs.sel, err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// TestRetainsCaptureExactBytes: an identified run's stdout is retained at
// res.OutPath byte-for-byte — including multi-line, no-trailing-newline, and
// binary (NUL + control bytes) payloads that env-var round-tripping mangles. The
// retained FILE is the raw redirect target, so it holds exactly what the command
// wrote. Runs across zsh/bash/sh.
func TestRetainsCaptureExactBytes(t *testing.T) {
	const to = 10 * time.Second
	cases := []struct {
		name string
		// fmtArg is a printf FORMAT string (octal escapes in the format are uniform
		// across zsh/bash/sh, unlike %b); bytes are kept <= 0x7f so every shell emits
		// them identically. The NUL (\000) is the binary-hostile byte that env-var
		// round-tripping cannot carry — the retained FILE holds it exactly.
		fmtArg string
		want   []byte
	}{
		{"multiline_trailing_nl", `line1\nline2\n`, []byte("line1\nline2\n")},
		{"no_trailing_nl", `line1\nline2`, []byte("line1\nline2")},
		{"binary_nul_and_controls", `a\001b\000c\177d`, []byte{'a', 0x01, 'b', 0x00, 'c', 0x7F, 'd'}},
	}
	for _, cs := range crossShells {
		cs := cs
		t.Run(cs.sel, func(t *testing.T) {
			d := openShell(t, cs)
			for _, c := range cases {
				res := d.RunID("prod", "printf '"+c.fmtArg+"'", "", to)
				if res.OutPath == "" {
					t.Fatalf("[%s] OutPath empty for an identified run", c.name)
				}
				got, err := os.ReadFile(res.OutPath)
				if err != nil {
					t.Fatalf("[%s] read OutPath: %v", c.name, err)
				}
				if string(got) != string(c.want) {
					t.Errorf("[%s] retained bytes = %q, want %q", c.name, got, c.want)
				}
			}
		})
	}
}

// TestRetainsCaptureOnFailure: a producer that emits partial output then exits
// non-zero still has its (partial) stdout retained — retention is not gated on
// success, matching res.Out today.
func TestRetainsCaptureOnFailure(t *testing.T) {
	const to = 10 * time.Second
	for _, cs := range crossShells {
		cs := cs
		t.Run(cs.sel, func(t *testing.T) {
			d := openShell(t, cs)
			res := d.RunID("failer", "printf 'partial\\n'; exit 3", "", to)
			if res.Exit != 3 {
				t.Fatalf("exit = %d, want 3", res.Exit)
			}
			got, err := os.ReadFile(res.OutPath)
			if err != nil {
				t.Fatalf("read OutPath: %v", err)
			}
			if string(got) != "partial\n" {
				t.Errorf("retained bytes on failure = %q, want %q", got, "partial\n")
			}
		})
	}
}

// openNoclobberShell opens a driver for one crossShell row whose INTERACTIVE rc
// turns noclobber ON in the shell that sources job scripts (zsh `setopt noclobber`,
// bash `set -o noclobber`, dash `set -C`). The retained capture redirect must
// survive it on a re-run — RED against a plain `>`/`2>`, GREEN with `>|`/`2>|`.
func openNoclobberShell(t *testing.T, cs crossShell) *Driver {
	t.Helper()
	if _, err := exec.LookPath(cs.sel); err != nil {
		t.Skipf("%s not found on PATH; skipping", cs.sel)
	}
	dir := t.TempDir()
	var files []rcFileSpec
	switch cs.sel {
	case "zsh":
		files = []rcFileSpec{{".zshrc", "setopt noclobber\n"}}
	case "bash":
		files = []rcFileSpec{
			{".bashrc", "set -o noclobber\n"},
			{".bash_profile", "[ -r ~/.bashrc ] && . ~/.bashrc\n"},
		}
	case "sh":
		files = []rcFileSpec{{"env.sh", "set -C\n"}}
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f.name), []byte(f.content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	d, err := Open(Options{Shell: cs.sel, Env: envWith(os.Environ(), cs.rcEnv(dir))})
	if err != nil {
		t.Fatalf("Open(%s): %v", cs.sel, err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// TestReRunSurvivesNoclobber is the noclobber matrix (zsh/bash/sh). Under the
// user's `noclobber`/`set -C`, the session-dir capture target already EXISTS on a
// re-run, so a plain `>` redirect fails ("file exists", rc=1) and the capture keeps
// the STALE first-run bytes. This asserts the fixed contract — the second run exits
// 0 and the capture holds the SECOND run's bytes — which is RED against the old
// `>`/`2>` and GREEN with `>|`/`2>|`. A portable probe first confirms noclobber is
// actually in effect (otherwise the test would pass vacuously).
func TestReRunSurvivesNoclobber(t *testing.T) {
	const to = 10 * time.Second
	for _, cs := range crossShells {
		cs := cs
		t.Run(cs.sel, func(t *testing.T) {
			d := openNoclobberShell(t, cs)

			// Sanity: noclobber IS active in the sourcing shell. `>|` always
			// truncates; a following plain `>` to that existing file must be blocked
			// (the `if` sees the redirect's non-zero status). Portable to all three.
			probe := d.Run(`t=$(mktemp); printf x >| "$t"; if printf y > "$t" 2>/dev/null; then printf CLOBBER_OK; else printf CLOBBER_BLOCKED; fi; rm -f "$t"`, to)
			if probe.Out != "CLOBBER_BLOCKED" {
				t.Fatalf("[%s] noclobber not in effect (probe=%q) — test would be vacuous", cs.sel, probe.Out)
			}

			first := d.RunID("nc", "printf 'first-run-longer\\n'", "", to)
			if first.Exit != 0 {
				t.Fatalf("[%s] first run exited %d, want 0", cs.sel, first.Exit)
			}
			second := d.RunID("nc", "printf 'second\\n'", "", to)
			if second.Exit != 0 {
				t.Errorf("[%s] re-run under noclobber exited %d, want 0 (capture redirect must use >|)", cs.sel, second.Exit)
			}
			if first.OutPath != second.OutPath {
				t.Fatalf("[%s] re-run OutPath changed: %q vs %q", cs.sel, first.OutPath, second.OutPath)
			}
			got, err := os.ReadFile(second.OutPath)
			if err != nil {
				t.Fatalf("[%s] read OutPath: %v", cs.sel, err)
			}
			if string(got) != "second\n" {
				t.Errorf("[%s] capture after re-run = %q, want %q (stale first-run bytes ⇒ noclobber blocked the clobber)", cs.sel, got, "second\n")
			}
		})
	}
}

// TestReRunOverwritesCapture: re-running the same id overwrites (truncates) its
// retained capture — the second run's bytes fully replace the first's.
func TestReRunOverwritesCapture(t *testing.T) {
	const to = 10 * time.Second
	for _, cs := range crossShells {
		cs := cs
		t.Run(cs.sel, func(t *testing.T) {
			d := openShell(t, cs)
			first := d.RunID("x", "printf 'first-longer-output\\n'", "", to)
			second := d.RunID("x", "printf 'second\\n'", "", to)
			if first.OutPath != second.OutPath {
				t.Fatalf("re-run OutPath changed: %q vs %q (same id must reuse the file)", first.OutPath, second.OutPath)
			}
			got, err := os.ReadFile(second.OutPath)
			if err != nil {
				t.Fatalf("read OutPath: %v", err)
			}
			if string(got) != "second\n" {
				t.Errorf("after re-run retained bytes = %q, want %q (not overwritten)", got, "second\n")
			}
		})
	}
}

// TestCloseRemovesCaptures: Close() removes the retained capture files (and their
// session dir), leaving nothing behind.
func TestCloseRemovesCaptures(t *testing.T) {
	cs := crossShells[0] // zsh — always present
	if _, err := exec.LookPath(cs.sel); err != nil {
		t.Skipf("%s not found on PATH; skipping", cs.sel)
	}
	dir := t.TempDir()
	for _, f := range cs.rcFiles(dir) {
		if err := os.WriteFile(filepath.Join(dir, f.name), []byte(f.content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	d, err := Open(Options{Shell: cs.sel, Env: envWith(os.Environ(), cs.rcEnv(dir))})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	res := d.RunID("y", "printf 'keep\\n'", "", 10*time.Second)
	if res.OutPath == "" {
		t.Fatal("OutPath empty for an identified run")
	}
	if _, err := os.Stat(res.OutPath); err != nil {
		t.Fatalf("capture file should exist before Close: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(res.OutPath); !os.IsNotExist(err) {
		t.Errorf("capture file survived Close (err=%v), want removed", err)
	}
}

// TestUnidentifiedRunRetainsNothing: a run with id=="" retains no capture —
// OutPath/ErrPath stay empty (the ready/stty/cd probes must not litter the
// session dir).
func TestUnidentifiedRunRetainsNothing(t *testing.T) {
	d := newTestDriver(t)
	res := d.Run("printf 'transient\\n'", 10*time.Second)
	if res.OutPath != "" || res.ErrPath != "" {
		t.Errorf("unidentified run retained paths OutPath=%q ErrPath=%q, want both empty", res.OutPath, res.ErrPath)
	}
	if res.Out != "transient" {
		t.Errorf("Out = %q, want transient (capture still works in-memory)", res.Out)
	}
}

// TestCaptureFileEnvExportsReadable: after an identified run the job exports
// APB_OUT_FILE_<key>/APB_ERR_FILE_<key> holding the RAW path to the retained
// capture, so a later block reads the producer's output via `cat "$APB_OUT_FILE_x"`.
// Across zsh/bash/sh.
func TestCaptureFileEnvExportsReadable(t *testing.T) {
	const to = 10 * time.Second
	for _, cs := range crossShells {
		cs := cs
		t.Run(cs.sel, func(t *testing.T) {
			d := openShell(t, cs)
			d.RunID("cap", "printf 'hello-capture\\n'; printf 'err-capture\\n' 1>&2", "", to)
			// The out-file var must point at a readable file whose content matches.
			if r := d.Run(`cat "$APB_OUT_FILE_cap"`, to); r.Out != "hello-capture" {
				t.Errorf("[%s] cat $APB_OUT_FILE_cap = %q, want hello-capture", cs.sel, r.Out)
			}
			if r := d.Run(`cat "$APB_ERR_FILE_cap"`, to); r.Out != "err-capture" {
				t.Errorf("[%s] cat $APB_ERR_FILE_cap = %q, want err-capture", cs.sel, r.Out)
			}
		})
	}
}

// TestStdinWiringDeliversExactBytes: a non-empty stdinPath redirects the block's
// subshell stdin from that file (replacing </dev/null); `cat` echoes the exact
// bytes. An empty stdinPath keeps </dev/null (cat yields nothing). Across
// zsh/bash/sh.
func TestStdinWiringDeliversExactBytes(t *testing.T) {
	const to = 10 * time.Second
	for _, cs := range crossShells {
		cs := cs
		t.Run(cs.sel, func(t *testing.T) {
			d := openShell(t, cs)
			payload := "piped line one\npiped line two\n"
			f := filepath.Join(t.TempDir(), "stdin.dat")
			if err := os.WriteFile(f, []byte(payload), 0644); err != nil {
				t.Fatal(err)
			}
			if r := d.RunID("consumer", "cat", f, to); r.Out != "piped line one\npiped line two" {
				t.Errorf("[%s] stdin-fed cat = %q, want the piped payload", cs.sel, r.Out)
			}
			// Empty stdinPath → /dev/null, so cat sees EOF immediately.
			if r := d.RunID("consumer2", "cat", "", to); r.Out != "" {
				t.Errorf("[%s] empty stdinPath should keep </dev/null (cat empty), got %q", cs.sel, r.Out)
			}
		})
	}
}

// TestStdinFromRetainedCapture: end-to-end for P2 — a producer's retained OutPath
// feeds a consumer's stdin directly (the seam P4 wires via From). Proves the two
// halves compose: retention path in, exact bytes out.
func TestStdinFromRetainedCapture(t *testing.T) {
	const to = 10 * time.Second
	for _, cs := range crossShells {
		cs := cs
		t.Run(cs.sel, func(t *testing.T) {
			d := openShell(t, cs)
			prod := d.RunID("p", "printf 'from-producer\\n'", "", to)
			if prod.OutPath == "" {
				t.Fatal("producer OutPath empty")
			}
			if r := d.RunID("c", "cat", prod.OutPath, to); r.Out != "from-producer" {
				t.Errorf("[%s] consumer fed from producer capture = %q, want from-producer", cs.sel, r.Out)
			}
		})
	}
}
