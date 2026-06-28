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

const sentinel = "__APB__" // wraps the exit code: __APB__<rc>__APB__

// Result is one command's outcome.
type Result struct {
	Out      string
	Err      string
	Exit     int  // -1 if never observed
	TimedOut bool // killed by us (timeout or stop)
}

// Options configures a Driver. Env defaults to os.Environ() (the real shell);
// tests pass a controlled ZDOTDIR via Env. Cwd, if set, is entered after spawn.
// Shell selects the executing shell: "" | "auto" | "zsh" | "bash" | "sh"; ""
// behaves like "auto" (zsh if present, else $SHELL fallback, else error). Shell
// is resolved by resolveShell; unsupported names return an error from Open.
type Options struct {
	Env   []string
	Cwd   string
	Shell string // "" | "auto" | "zsh" | "bash" | "sh"; "" behaves like "auto"
}

// Driver is a live, drivable interactive shell session.
type Driver struct {
	ptmx     *os.File
	cmd      *exec.Cmd
	re       *regexp.Regexp
	shellPid int
	a        shellAdapter // shell-specific spawn/job/source/cd tokens

	cwd string // the cwd entered at Open (Options.Cwd), for callers that need it

	mu       sync.Mutex
	buf      []byte
	lastSeen time.Time
	stopped  bool // set by Stop, observed by waitSentinel to return promptly

	runMu sync.Mutex // serializes Run (one foreground command at a time)
}

// Cwd returns the working directory the driver was opened in (Options.Cwd),
// empty if none was given. Note: this is the INITIAL cwd; a subsequent `cd` in
// the session is not tracked here.
func (d *Driver) Cwd() string { return d.cwd }

// Open resolves the configured shell (Options.Shell; default "auto" → zsh),
// spawns it under a pty, and drives it ready. Returns an error if the shell
// cannot be resolved (e.g. unsupported selector or binary not found).
func Open(opts Options) (*Driver, error) {
	env := opts.Env
	if env == nil {
		env = os.Environ()
	}
	// getenvFn reads from opts.Env when set (so a test's controlled env is
	// honored for SHELL / ZDOTDIR lookups), else falls back to os.Getenv.
	getenvFn := func(key string) string {
		if opts.Env != nil {
			prefix := key + "="
			for _, e := range opts.Env {
				if strings.HasPrefix(e, prefix) {
					return e[len(prefix):]
				}
			}
			return ""
		}
		return os.Getenv(key)
	}
	bin, a, err := resolveShell(opts.Shell, getenvFn, exec.LookPath)
	if err != nil {
		return nil, err
	}
	c := exec.Command(bin, a.spawnArgs()...)
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
		a:        a,
	}
	go d.read()
	if err := d.ready(); err != nil {
		d.Close()
		return nil, err
	}
	d.run("stty -echo 2>/dev/null", 5*time.Second) // cosmetic: trim echo noise
	if opts.Cwd != "" {
		d.cwd = opts.Cwd
		d.run(d.a.cdCmd(opts.Cwd), 10*time.Second)
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
// LAST_EXCODE / LAST_STDOUT / LAST_STDERR (and, when id != "", APB_OUT_<key> /
// APB_ERR_<key> / APB_EXIT_<key>, key = id with [^A-Za-z0-9_]→_) so a later block
// can reference the prior block's output. Because the job is sourced in the main
// context (not a subshell), these exports persist across Runs.
func (d *Driver) RunID(id, cmd string, timeout time.Duration) Result {
	d.runMu.Lock()
	defer d.runMu.Unlock()
	return d.runID(id, cmd, timeout)
}

// Stop interrupts whatever the session is currently running — the robust,
// generic equivalent of a user hitting Ctrl-C. Safe to call from another
// goroutine while a Run is in flight: it only signals; the in-flight Run
// observes the sentinel/EOF and returns on its own.
//
// It works in two stages, mirroring a real terminal interrupt:
//
//  1. Write the interrupt character (^C / 0x03) to the pty master. The tty line
//     discipline delivers SIGINT to the foreground process group — exactly what
//     Ctrl-C does. This interrupts external commands AND shell builtins /
//     functions uniformly, and crucially it covers the case where the running
//     command shares the shell's own process group (where a pgrp-targeted signal
//     would be skipped to avoid killing the shell).
//  2. After a brief grace period, if a DISTINCT foreground process group is
//     still in front (pg > 0 && pg != shellPid), escalate to SIGTERM then
//     SIGKILL on that group. The shell itself is never signalled.
//
// Known limitation: a command that deliberately detaches into its own session
// (e.g. a build tool that spawns a persistent background daemon, like the gradle
// daemon) escapes the controlling tty's foreground group. Stop interrupts the
// invocation we launched here — not an intentional, independently-sessioned
// background daemon, which by design outlives the foreground command.
func (d *Driver) Stop() {
	// Signal any in-flight waitSentinel to stop waiting promptly. A SIGINT
	// delivered via ^C aborts the shell's `source` of the job, so the sentinel
	// may never print — without this flag the Run would block until its timeout.
	d.setStopped(true)

	// Stage 1: deliver SIGINT via the tty, like Ctrl-C. Covers builtins,
	// functions, and commands sharing the shell's pgrp.
	_, _ = d.ptmx.Write([]byte{0x03})

	// Stage 2: escalate to a distinct foreground group if one survives.
	time.Sleep(150 * time.Millisecond)
	pg := d.Pgrp()
	if pg > 0 && pg != d.shellPid {
		_ = unix.Kill(-pg, unix.SIGTERM)
		time.Sleep(300 * time.Millisecond)
		_ = unix.Kill(-pg, unix.SIGKILL)
	}
}

func (d *Driver) setStopped(v bool) {
	d.mu.Lock()
	d.stopped = v
	d.mu.Unlock()
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

func (d *Driver) send(s string) { _, _ = d.ptmx.Write([]byte(s + "\r")) }
func (d *Driver) clearBuf()     { d.mu.Lock(); d.buf = d.buf[:0]; d.mu.Unlock() }
func (d *Driver) idleFor() time.Duration {
	d.mu.Lock()
	defer d.mu.Unlock()
	return time.Since(d.lastSeen)
}

// waitSentinel scans the pty for the next __APB__<digits>__APB__; returns the
// submatch, or nil on timeout. It also returns nil promptly when Stop has been
// called: a ^C-interrupted job may never print its sentinel, so the stop flag
// short-circuits the wait instead of blocking for the full timeout.
func (d *Driver) waitSentinel(timeout time.Duration) []string {
	dl := time.Now().Add(timeout)
	for time.Now().Before(dl) {
		d.mu.Lock()
		m := d.re.FindStringSubmatch(string(d.buf))
		stopped := d.stopped
		d.mu.Unlock()
		if m != nil {
			return m
		}
		if stopped {
			return nil
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
	dir, err := os.MkdirTemp("", "apb")
	if err != nil {
		return Result{Exit: -1}
	}
	defer os.RemoveAll(dir)
	o := filepath.Join(dir, "o")
	e := filepath.Join(dir, "e")
	job := filepath.Join(dir, "job."+d.a.jobExt())
	// The block runs in a SUBSHELL `( … )` — sourced in the main shell, but the
	// subshell isolates a block's `set -e`/`set -u`/`setopt`/`trap`. This is
	// critical: zsh `errexit` exits the WHOLE shell on a failing command regardless
	// of function scope, so a block doing `set -euo pipefail` then a failing command
	// would kill the hosted shell (and the sentinel would never print → the driver
	// waits out its whole timeout). A subshell contains that exit. To keep
	// cd-persistence + auto-env across blocks (the reason we don't just always use a
	// subshell), an EXIT trap captures the block's final cwd — even when errexit
	// aborts the subshell — and the MAIN shell re-applies it afterwards, which fires
	// chpwd/precmd so mise/direnv/nix activate for the next block. Own sentinel
	// carries $? (the block's exit). stdin /dev/null. We capture $? immediately, map
	// SIGPIPE (141)→0 (a producer killed by a downstream `| head`/`| grep -q` is not
	// a failure), re-apply the cwd, then — BEFORE the sentinel print — export the
	// value-passing vars read from the per-job out/err files (still present here; the
	// driver removes the temp dir only after the sentinel returns). ${(q)…} keeps
	// multi-line values intact. Post-block lines use if/fi (not `&&`) defensively.
	// (Trade-off: a block's raw `export FOO=…` no longer persists to later blocks;
	// value-passing across blocks goes through APB_OUT_<id>/LAST_*, which the driver
	// sets in the main context below.)
	cwdf := filepath.Join(dir, "cwd")
	_ = os.WriteFile(job, []byte(d.a.job(jobParams{
		cmdline: cmdline,
		o:       o,
		e:       e,
		cwdf:    cwdf,
		id:      id,
		key:     sanitizeKey(id),
	})), 0644)
	d.clearBuf()
	d.setStopped(false)
	d.send(d.a.sourceCmd(job))

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
		// Best-effort parse of the sentinel's exit field; res.Exit stays 0 on failure.
		_, _ = fmt.Sscanf(m[1], "%d", &res.Exit)
	}
	return res
}

func shquote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// sanitizeKey mirrors the broker convention: id with [^A-Za-z0-9_] → _.
func sanitizeKey(id string) string {
	b := []byte(id)
	for i, c := range b {
		if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
			b[i] = '_'
		}
	}
	return string(b)
}
