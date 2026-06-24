// Package driver drives the user's real interactive zsh under a pty, with the
// environment 100% unaltered — no config edits, no env overrides, no prompt/hook
// tampering, no framework names. See docs: the own-sentinel + idle/probe-readiness
// + main-context + foreground-pgrp approach validated by the PoC.
package driver

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

const sentinel = "__AAPB__" // wraps the exit code: __AAPB__<rc>__AAPB__

// Result is one command's outcome.
type Result struct {
	Out      string
	Err      string
	Exit     int  // -1 if never observed
	TimedOut bool // killed by us (timeout or stop)
}

// Options configures a Driver. Env defaults to os.Environ() (the real shell);
// tests pass a controlled ZDOTDIR via Env. Cwd, if set, is entered after spawn.
type Options struct {
	Env []string
	Cwd string
}

// Driver is a live, drivable interactive shell session.
type Driver struct {
	ptmx     *os.File
	cmd      *exec.Cmd
	re       *regexp.Regexp
	shellPid int

	cwd string // the cwd entered at Open (Options.Cwd), for callers that need it

	mu       sync.Mutex
	buf      []byte
	lastSeen time.Time

	runMu sync.Mutex // serializes Run (one foreground command at a time)
}

// Cwd returns the working directory the driver was opened in (Options.Cwd),
// empty if none was given. Note: this is the INITIAL cwd; a subsequent `cd` in
// the session is not tracked here.
func (d *Driver) Cwd() string { return d.cwd }

// Open spawns `zsh -il` under a pty and drives it ready.
func Open(opts Options) (*Driver, error) {
	env := opts.Env
	if env == nil {
		env = os.Environ()
	}
	c := exec.Command("zsh", "-il")
	c.Env = env
	ptmx, err := pty.Start(c)
	if err != nil {
		return nil, err
	}
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 50, Cols: 200})
	d := &Driver{
		ptmx:     ptmx,
		cmd:      c,
		re:       regexp.MustCompile(sentinel + `(-?\d+)` + sentinel),
		shellPid: c.Process.Pid,
		lastSeen: time.Now(),
	}
	go d.read()
	if err := d.ready(); err != nil {
		d.Close()
		return nil, err
	}
	d.run("stty -echo 2>/dev/null", 5*time.Second) // cosmetic: trim echo noise
	if opts.Cwd != "" {
		d.cwd = opts.Cwd
		d.run("builtin cd -- "+shquote(opts.Cwd)+" 2>/dev/null", 10*time.Second)
	}
	return d, nil
}

// Run executes cmd in the shell's MAIN context (cd fires chpwd/precmd → auto-env),
// captures stdout/stderr/exit, and on timeout kills the running command's process
// group. Safe to call serially. Equivalent to RunID("", cmd, timeout).
func (d *Driver) Run(cmd string, timeout time.Duration) Result {
	return d.RunID("", cmd, timeout)
}

// RunID is Run with value-passing. In the hosted shell's main context — AFTER the
// command's exit code is captured and BEFORE the sentinel is printed — it exports
// LAST_EXCODE / LAST_STDOUT / LAST_STDERR (and, when id != "", AAS_OUT_<key> /
// AAS_ERR_<key> / AAS_EXIT_<key>, key = id with [^A-Za-z0-9_]→_) so a later block
// can reference the prior block's output. Because the job is sourced in the main
// context (not a subshell), these exports persist across Runs.
func (d *Driver) RunID(id, cmd string, timeout time.Duration) Result {
	d.runMu.Lock()
	defer d.runMu.Unlock()
	return d.runID(id, cmd, timeout)
}

// Stop kills the currently-running command's foreground process group (TERM then
// KILL). Safe to call from another goroutine while a Run is in flight: it only
// signals — the in-flight Run observes the sentinel/EOF and returns on its own.
// A no-op when the shell is idle (foreground pgrp == the shell itself).
func (d *Driver) Stop() {
	pg := d.Pgrp()
	if pg > 0 && pg != d.shellPid {
		_ = unix.Kill(-pg, unix.SIGTERM)
		time.Sleep(300 * time.Millisecond)
		_ = unix.Kill(-pg, unix.SIGKILL)
	}
}

// Pgrp returns the pty's foreground process group — the running command's group
// (a real, monitorable, killable handle), or the shell's pgrp when idle.
func (d *Driver) Pgrp() int {
	p, err := unix.IoctlGetInt(int(d.ptmx.Fd()), unix.TIOCGPGRP)
	if err != nil {
		return -1
	}
	return p
}

// Close tears down the session.
func (d *Driver) Close() error {
	if d.ptmx != nil {
		_ = d.ptmx.Close()
	}
	if d.cmd != nil && d.cmd.Process != nil {
		_ = d.cmd.Process.Kill()
	}
	return nil
}

// ---- internals ----

func (d *Driver) read() {
	b := make([]byte, 4096)
	for {
		n, err := d.ptmx.Read(b)
		if n > 0 {
			d.mu.Lock()
			d.buf = append(d.buf, b[:n]...)
			d.lastSeen = time.Now()
			d.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (d *Driver) send(s string)   { _, _ = d.ptmx.Write([]byte(s + "\r")) }
func (d *Driver) clearBuf()       { d.mu.Lock(); d.buf = d.buf[:0]; d.mu.Unlock() }
func (d *Driver) idleFor() time.Duration {
	d.mu.Lock()
	defer d.mu.Unlock()
	return time.Since(d.lastSeen)
}

// waitSentinel scans the pty for the next __AAPB__<digits>__AAPB__; returns the
// submatch, or nil on timeout.
func (d *Driver) waitSentinel(timeout time.Duration) []string {
	dl := time.Now().Add(timeout)
	for time.Now().Before(dl) {
		d.mu.Lock()
		m := d.re.FindStringSubmatch(string(d.buf))
		d.mu.Unlock()
		if m != nil {
			return m
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nil
}

// ready waits for the shell to go idle (past startup/instant-prompt) then confirms
// with a no-op probe, retrying if a buffered-during-init probe is swallowed.
func (d *Driver) ready() error {
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		for d.idleFor() < 1200*time.Millisecond && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
		}
		if r := d.run(":", 3*time.Second); !r.TimedOut && r.Exit == 0 {
			return nil
		}
	}
	return fmt.Errorf("driver: shell never became drivable")
}

// run executes cmdline with no value-passing (id=""). Used by ready/stty and as
// the Run path; routes through runID with an empty id.
func (d *Driver) run(cmdline string, timeout time.Duration) Result {
	return d.runID("", cmdline, timeout)
}

func (d *Driver) runID(id, cmdline string, timeout time.Duration) Result {
	dir, err := os.MkdirTemp("", "aapb")
	if err != nil {
		return Result{Exit: -1}
	}
	defer os.RemoveAll(dir)
	o := filepath.Join(dir, "o")
	e := filepath.Join(dir, "e")
	job := filepath.Join(dir, "job.zsh")
	// Main context (`{ }`, sourced — not a subshell): cd/exports persist, hooks
	// fire. Own sentinel carries $? (the group's exit). stdin /dev/null. We
	// capture $? immediately, map SIGPIPE (141)→0 (a producer killed by a
	// downstream `| head`/`| grep -q` is not a failure), then — BEFORE the
	// sentinel print — export the value-passing vars read from the per-job out/err
	// files (still present here; the driver removes the temp dir only after the
	// sentinel returns). ${(q)…} keeps multi-line values intact.
	qo := shquote(o)
	qe := shquote(e)
	vp := "" +
		"export LAST_EXCODE=${(q)__aapb_rc}\n" +
		"export LAST_STDOUT=${(q)\"$(<" + qo + ")\"}\n" +
		"export LAST_STDERR=${(q)\"$(<" + qe + ")\"}\n"
	if id != "" {
		key := sanitizeKey(id)
		vp += "" +
			"export AAS_OUT_" + key + "=${(q)\"$(<" + qo + ")\"}\n" +
			"export AAS_ERR_" + key + "=${(q)\"$(<" + qe + ")\"}\n" +
			"export AAS_EXIT_" + key + "=${(q)__aapb_rc}\n"
	}
	_ = os.WriteFile(job, []byte(
		"{ "+cmdline+" } </dev/null >"+o+" 2>"+e+"\n"+
			"__aapb_rc=$?\n"+
			"[[ $__aapb_rc -eq 141 ]] && __aapb_rc=0\n"+
			vp+
			"print -r -- "+sentinel+"${__aapb_rc}"+sentinel+"\n"), 0644)
	d.clearBuf()
	d.send("source " + job)

	res := Result{Exit: -1}
	m := d.waitSentinel(timeout)
	if m == nil {
		// Stop the running command's group by PID (TERM then KILL) — not ^C.
		if pg := d.Pgrp(); pg > 0 && pg != d.shellPid {
			_ = unix.Kill(-pg, unix.SIGTERM)
			time.Sleep(300 * time.Millisecond)
			_ = unix.Kill(-pg, unix.SIGKILL)
		}
		m = d.waitSentinel(5 * time.Second)
		res.TimedOut = true
	}
	ob, _ := os.ReadFile(o)
	eb, _ := os.ReadFile(e)
	res.Out = strings.TrimRight(string(ob), "\n")
	res.Err = strings.TrimRight(string(eb), "\n")
	if m != nil {
		fmt.Sscanf(m[1], "%d", &res.Exit)
	}
	return res
}

func shquote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// sanitizeKey mirrors the broker convention: id with [^A-Za-z0-9_] → _.
func sanitizeKey(id string) string {
	b := []byte(id)
	for i, c := range b {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			b[i] = '_'
		}
	}
	return string(b)
}
