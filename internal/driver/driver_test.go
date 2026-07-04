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
	// Pin zsh explicitly: this fixture is zsh-specific (ZDOTDIR rc, the `print`
	// builtin, add-zsh-hook). The default now honors $SHELL — bash on CI — so the
	// ambient default must not be relied on here.
	d, err := Open(Options{Shell: "zsh", Env: append(os.Environ(), "ZDOTDIR="+zdot)})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// TestOpenProbesReadyWithoutIdleFloor pins the per-run-nonce win: because a stale
// sentinel from a swallowed init-time probe can never satisfy a later probe's wait
// (its nonce differs), ready() no longer pays a fixed idle floor before its first
// probe — it probes immediately after a brief courtesy settle. The old floor
// GUARANTEED every Open was >= 1200ms; a generous < 900ms bound proves it is gone.
// RED before the nonce change. Wall-clock bounds against a live `zsh -il` spawn
// are meaningless on shared CI runners (observed: 8.4s on a loaded GitHub runner
// tripped v0.8.0's release gate), so the assertion runs on developer machines
// only — CI still exercises Open itself through every other driver test.
func TestOpenProbesReadyWithoutIdleFloor(t *testing.T) {
	if os.Getenv("CI") != "" {
		t.Skip("wall-clock Open bound is not meaningful on shared CI runners")
	}
	zdot := t.TempDir()
	if err := os.WriteFile(filepath.Join(zdot, ".zshrc"), []byte("\n"), 0644); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	d, err := Open(Options{Shell: "zsh", Env: append(os.Environ(), "ZDOTDIR="+zdot)})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	if el := time.Since(start); el > 900*time.Millisecond {
		t.Errorf("Open took %v; the per-run nonce should let ready() probe immediately (the old fixed idle floor guaranteed >= 1200ms)", el)
	}
}

// historyOff runs in the MAIN context at Open (via runMain): HISTFILE=/dev/null
// must persist to a later Run — proof the driver's commands won't be saved to the
// user's shell history. (Removing atuin's hooks can't be unit-tested without
// atuin; HISTFILE is the observable main-context effect.)
func TestHistoryOff_DisablesHistfileInMainContext(t *testing.T) {
	d := newTestDriver(t)
	if r := d.Run("print -r -- $HISTFILE", 5*time.Second); r.Out != "/dev/null" {
		t.Errorf("HISTFILE after Open = %q, want /dev/null (historyOff main-context effect)", r.Out)
	}
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
	d.Run("builtin cd -- "+Shquote(envdir), 10*time.Second)
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

// A block using the `set -euo pipefail` safety idiom that then FAILS must NOT kill
// the hosted shell — zsh errexit exits the whole shell on a failing command, so the
// subshell must isolate it. We must still get the real non-zero exit (not a
// timeout), the shell must survive (errexit not leaked), and a cd inside the
// errexit-aborted block must still persist via the EXIT-trap cwd capture.
func TestSetEFailingBlockIsolated(t *testing.T) {
	d := newTestDriver(t)
	r := d.Run("set -euo pipefail\nbuiltin cd -- /tmp\nfalse\nprint should-not-run", 10*time.Second)
	if r.TimedOut {
		t.Fatalf("set -e failing block timed out (errexit not isolated): %+v", r)
	}
	if r.Exit == 0 {
		t.Errorf("want non-zero exit from a failing set -e block, got %+v", r)
	}
	if r.Out == "should-not-run" {
		t.Errorf("errexit should have stopped the block before the final print: %+v", r)
	}
	if r2 := d.Run("false; print -r -- alive", 5*time.Second); r2.Out != "alive" || r2.TimedOut {
		t.Errorf("shell didn't survive / errexit leaked to next run: %+v", r2)
	}
	if r3 := d.Run("pwd", 5*time.Second); r3.Out != "/tmp" {
		t.Errorf("cd inside the set -e block didn't persist: %q", r3.Out)
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

// TestCwdTracksCdAcrossRuns: after a block cds into a subdir, Cwd() must reflect
// the session's live cwd — not the stale Open-time dir. The job's EXIT-trap
// writes the block's final pwd to the cwd temp file and the main shell re-applies
// it, so the driver can read that back after the run. We compare Cwd() to the
// shell's own pwd (robust to any symlink normalization the shell applies).
func TestCwdTracksCdAcrossRuns(t *testing.T) {
	d := newTestDriver(t)
	sub := t.TempDir()
	d.Run("builtin cd -- "+Shquote(sub), 10*time.Second)
	pwd := d.Run("pwd", 5*time.Second).Out
	if pwd == "" {
		t.Fatal("could not determine the shell's cwd after cd")
	}
	if got := d.Cwd(); got != pwd {
		t.Errorf("Cwd() = %q, want it to track the live session cwd %q", got, pwd)
	}
}

// TestCloseTerminatesRunningBlockPromptly: Close while a long block is in flight
// must (a) unblock the in-flight Run promptly — not leave it waiting for the
// sentinel until the run's whole timeout — and (b) tear down the running
// command's process group so no orphan survives. RED today: Close never sets
// stopped, so the in-flight RunID's waitSentinel blocks until its 120s timeout
// (the quit-while-running hang) — the select below times out.
func TestCloseTerminatesRunningBlockPromptly(t *testing.T) {
	zdot := t.TempDir()
	if err := os.WriteFile(filepath.Join(zdot, ".zshrc"), []byte("\n"), 0644); err != nil {
		t.Fatal(err)
	}
	d, err := Open(Options{Shell: "zsh", Env: append(os.Environ(), "ZDOTDIR="+zdot)})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	done := make(chan Result, 1)
	go func() { done <- d.RunID("", "sleep 60", 120*time.Second) }()
	pg := 0
	for i := 0; i < 150 && pg == 0; i++ {
		time.Sleep(40 * time.Millisecond)
		if p := d.Pgrp(); p > 0 && p != d.shellPid {
			pg = p
		}
	}
	if pg == 0 {
		t.Fatal("never observed the running block's foreground pgrp")
	}
	start := time.Now()
	if err := d.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if el := time.Since(start); el > 5*time.Second {
		t.Errorf("Close took %v with a block in flight, want prompt (< 5s)", el)
	}
	select {
	case <-done:
		// The in-flight run returned promptly — no quit-while-running hang.
	case <-time.After(8 * time.Second):
		t.Fatal("in-flight run blocked after Close (quit-while-running hang: stopped not set)")
	}
	// The block's process group must be gone (kill -0 fails once it's reaped).
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if unix.Kill(pg, 0) != nil {
			return // gone
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("running block's pgrp %d survived Close (orphaned children)", pg)
}

// TestClose_DoubleCloseNoOp: Close is documented "safe to call more than once",
// but a second Close used to re-run the whole teardown — re-signalling the
// REAPED shell pid (whose pid/pgid the OS may have already handed to an
// unrelated process) with a TERM→grace→KILL escalation. The second call must
// be a fast no-op: the closeGrace sleep inside killGroup makes the old
// behavior observable as >=150ms, so the timing bound below is discriminating.
func TestClose_DoubleCloseNoOp(t *testing.T) {
	d := newTestDriver(t)
	if err := d.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	start := time.Now()
	if err := d.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if el := time.Since(start); el > 100*time.Millisecond {
		t.Errorf("second Close took %v, want a fast no-op (< 100ms; a re-run teardown pays killGroup's 150ms grace)", el)
	}
}

// TestPgrp_ClosedDriverNeverIoctls: after Close the ptmx fd number may be
// REUSED by an unrelated file — a TIOCGPGRP ioctl on it would read (or error
// on) someone else's fd. Pgrp on a closed driver must return -1 from the
// closed flag alone, never touching the fd. The probe driver borrows a LIVE
// second driver's ptmx fd (standing in for "the fd number was reused"): if
// Pgrp ioctl'd it, it would see that shell's real foreground pgrp (> 0).
func TestPgrp_ClosedDriverNeverIoctls(t *testing.T) {
	live := newTestDriver(t)
	probe := &Driver{ptmxFd: live.ptmxFd, closed: true}
	if pg := probe.Pgrp(); pg != -1 {
		t.Errorf("Pgrp on a closed driver = %d, want -1 without ioctl'ing the (reused) fd", pg)
	}
}

// TestScanNextCatchesSplitSentinel is the B7 boundary case: the incremental scan
// advances a cursor to len(buf) each poll, so a sentinel that arrives split
// across two reads (opening __APB__ in chunk 1, the rest in chunk 2) would be
// missed if the next scan started at the cursor. The maxSentinelLen rewind must
// back the scan start up far enough to re-see the opening marker. No live shell —
// this drives scanNext directly with hand-fed chunks.
func TestScanNextCatchesSplitSentinel(t *testing.T) {
	re := sentinelRE("abcd1234")
	d := &Driver{}
	// Chunk 1 ends mid-sentinel (opening marker + nonce present, exit+closing not).
	d.buf = append(d.buf, []byte("noise output "+sentinel+"abcd1234_")...)
	if m := d.scanNext(re); m != nil {
		t.Fatalf("scanNext matched a partial sentinel: %q", m)
	}
	// The cursor is now at len(buf); the opening marker sits BEFORE it.
	d.buf = append(d.buf, []byte("0"+sentinel+" trailing")...)
	m := d.scanNext(re)
	if m == nil {
		t.Fatal("scanNext missed a sentinel split across the read boundary (rewind too small)")
	}
	if string(m[1]) != "0" {
		t.Errorf("captured exit field = %q, want \"0\"", m[1])
	}
}

// TestScanNextRejectsStaleNonce pins the reason the per-run nonce exists: a
// COMPLETE, well-formed sentinel left in the buffer by a PREVIOUS run — e.g. a
// probe swallowed during shell init that prints its __APB__<nonce>_0__APB__ late —
// must NOT satisfy the current run's wait. Only the token bearing THIS run's own
// nonce is accepted, which is exactly what lets ready() probe immediately with no
// stale-collision idle floor. No live shell — drives scanNext directly.
func TestScanNextRejectsStaleNonce(t *testing.T) {
	const staleNonce = "deadbeef"
	const liveNonce = "0badf00d"
	d := &Driver{}
	// A leftover sentinel from a previous run (its nonce, exit 0) sits in the buffer.
	d.buf = append(d.buf, []byte("leftover "+sentinel+staleNonce+"_0"+sentinel+" ")...)
	if m := d.scanNext(sentinelRE(liveNonce)); m != nil {
		t.Fatalf("current wait accepted a STALE sentinel (nonce collision still possible): %q", m)
	}
	// This run's own sentinel (its nonce, exit 5) then arrives and IS accepted.
	d.buf = append(d.buf, []byte(sentinel+liveNonce+"_5"+sentinel)...)
	m := d.scanNext(sentinelRE(liveNonce))
	if m == nil {
		t.Fatal("current wait missed its OWN sentinel")
	}
	if string(m[1]) != "5" {
		t.Errorf("captured exit field = %q, want \"5\"", m[1])
	}
}

// RunMain exports a var in the main shell context, and the export persists to
// a later Run. This is the use case for B2b confirmation gates that must inject
// env vars into the same driver before a block runs.
func TestRunMain_ExportPersists(t *testing.T) {
	d, err := Open(Options{Shell: "bash"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	d.RunMain("export B2B_TEST=hello", 5*time.Second)
	res := d.Run("printf '%s' \"$B2B_TEST\"", 5*time.Second)
	if res.Out != "hello" {
		t.Fatalf("exported var not visible to later Run: output=%q", res.Out)
	}
}
