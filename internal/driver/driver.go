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

// maxSentinelLen bounds a full sentinel token (__APB__<rc>__APB__) for the
// scan-offset rewind in waitSentinel: the two 7-byte markers plus a generous
// allowance for the signed exit-code digits. The scan rewinds by this much so a
// sentinel split across two pty read chunks is never missed.
const maxSentinelLen = 2*len(sentinel) + 24

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
	ptmxFd   int // ptmx's raw fd, captured once at Open; used for TIOCGPGRP so Pgrp never calls os.File.Fd() (which would race the reader goroutine's fd teardown)
	cmd      *exec.Cmd
	re       *regexp.Regexp
	shellPid int
	a        shellAdapter // shell-specific spawn/job/source/cd tokens

	shimDir string // temp ZDOTDIR shim dir (zsh history-leak fix); "" if no shim

	mu       sync.Mutex
	cwd      string // the session's LIVE cwd: Options.Cwd at Open, then updated after every block from the job's cwd temp-file (guarded by mu)
	buf      []byte
	scanOff  int // waitSentinel's scan cursor into buf (already-scanned prefix); reset with buf
	lastSeen time.Time
	stopped  bool // set by Stop, observed by waitSentinel to return promptly

	runMu sync.Mutex // serializes Run (one foreground command at a time)
}

// Cwd returns the session's LIVE working directory: Options.Cwd at Open, then
// the block's final directory after each Run (the job's EXIT-trap records it and
// the main shell re-applies it, so a `cd` inside a block is tracked). Empty if no
// Cwd was given and no block has cd'd yet.
func (d *Driver) Cwd() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cwd
}

// setCwd updates the live session cwd under mu.
func (d *Driver) setCwd(p string) {
	d.mu.Lock()
	d.cwd = p
	d.mu.Unlock()
}

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
	// ZDOTDIR shim (zsh): disable history recording at shell INIT — before atuin's
	// preexec/precmd hooks are ever armed — by pointing zsh at a temp ZDOTDIR whose
	// startup files source the user's real ones and then hard-disable recording.
	// This closes the two leaks the runtime historyOff() could not: (1) historyOff
	// itself being recorded by atuin's preexec before it can unhook, and (2) the
	// instant-prompt race that let subsequent source-lines be recorded. Falls back
	// gracefully to the runtime path if the shim can't be created; nil = no shim
	// (bash/sh) → keep the runtime historyOff call below.
	shimDir := ""
	if files := a.historyShimFiles(); files != nil {
		realZ := getenvFn("ZDOTDIR")
		if realZ == "" {
			realZ = getenvFn("HOME")
		}
		if dir, mkErr := os.MkdirTemp("", "apb-zdotdir-"); mkErr == nil {
			ok := true
			for name, content := range files {
				if wErr := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); wErr != nil {
					ok = false
					break
				}
			}
			if ok {
				shimDir = dir
				env = withEnv(env, "ZDOTDIR", dir)
				env = append(env, "APB_REAL_ZDOTDIR="+realZ)
			} else {
				_ = os.RemoveAll(dir) // partial write → clean up, fall back to runtime path
			}
		}
	}
	c := exec.Command(bin, a.spawnArgs()...)
	c.Env = env
	ptmx, err := pty.Start(c)
	if err != nil {
		if shimDir != "" {
			_ = os.RemoveAll(shimDir)
		}
		return nil, err
	}
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 50, Cols: 200})
	d := &Driver{
		ptmx:     ptmx,
		ptmxFd:   int(ptmx.Fd()), // captured once, before the reader goroutine starts
		cmd:      c,
		re:       regexp.MustCompile(sentinel + `(-?\d+)` + sentinel),
		shellPid: c.Process.Pid,
		lastSeen: time.Now(),
		a:        a,
		shimDir:  shimDir,
	}
	go d.read()
	if err := d.ready(); err != nil {
		d.Close()
		return nil, err
	}
	// Stop the driver's own commands (`source <job>` lines) from polluting the
	// user's shell/atuin history — run ONCE in the main context before anything else.
	// When a ZDOTDIR shim is active it already disabled recording at INIT, so skip
	// the runtime path (which is now only the fallback for the non-shim shells or a
	// failed shim setup).
	if d.shimDir == "" {
		d.runMain(d.a.historyOff(), 5*time.Second)
	}
	d.run("stty -echo 2>/dev/null", 5*time.Second) // cosmetic: trim echo noise
	if opts.Cwd != "" {
		d.setCwd(opts.Cwd)
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

// RunMain runs cmd in the driver's MAIN shell context (not the errexit subshell that
// Run uses), so side effects like `export` persist for subsequent Run calls. It is the
// exported counterpart to the internal runMain used at Open. Unlike the Open-internal
// runMain call (which runs before any concurrent caller exists), RunMain may be called
// concurrently with Run/RunID, so it acquires runMu to serialize shell access.
func (d *Driver) RunMain(cmd string, timeout time.Duration) {
	d.runMu.Lock()
	defer d.runMu.Unlock()
	d.runMain(cmd, timeout)
}

// runMain executes cmd in the shell's MAIN context — NOT inside the errexit
// subshell that runID wraps the user command in — and waits for completion. This
// is for session setup / env injection whose effect must persist on the main shell
// (historyOff at Open; export at gate-confirm via RunMain). cmd and a
// main-context sentinel echo are sent as one raw line; the result is discarded
// (best-effort). Callers that may run concurrently (RunMain) must hold runMu
// themselves; the Open-time call happens before any concurrent caller exists and
// therefore skips the lock.
func (d *Driver) runMain(cmd string, timeout time.Duration) {
	if cmd == "" {
		return
	}
	d.clearBuf()
	d.setStopped(false)
	d.send(cmd + "; " + d.a.sentinelEcho())
	d.waitSentinel(timeout)
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
	// Uses the raw fd captured at Open (not d.ptmx.Fd()): calling os.File.Fd()
	// concurrently with the reader goroutine's Read/close would be a data race,
	// whereas a bare TIOCGPGRP ioctl on the fd number touches no os.File state.
	p, err := unix.IoctlGetInt(d.ptmxFd, unix.TIOCGPGRP)
	if err != nil {
		return -1
	}
	return p
}

// closeGrace is the pause between SIGTERM and SIGKILL when tearing a group down
// in Close — long enough for a well-behaved process to exit on TERM, short enough
// to keep quit responsive.
const closeGrace = 150 * time.Millisecond

// Close tears down the session. It (1) sets stopped so any in-flight Run's
// waitSentinel returns promptly instead of blocking for its full timeout (the
// killed shell will never print its sentinel); (2) TERM→grace→KILLs the running
// block's foreground process group — a block runs in its OWN pgrp under job
// control, so the shell-group signal below does not reach it — then the shell's
// own process group, so no orphaned children survive; and (3) reaps the shell
// with Wait so it does not linger as a zombie. Safe to call more than once (the
// second call's signals/Wait are harmless no-ops).
func (d *Driver) Close() error {
	// Unblock any in-flight waitSentinel first.
	d.setStopped(true)
	// Tear down the running block's foreground group (distinct from the shell's),
	// then the shell's own group.
	if d.cmd != nil && d.cmd.Process != nil {
		if pg := d.Pgrp(); pg > 0 && pg != d.shellPid {
			killGroup(pg)
		}
		killGroup(d.shellPid)
	}
	if d.ptmx != nil {
		_ = d.ptmx.Close()
	}
	if d.cmd != nil && d.cmd.Process != nil {
		_ = d.cmd.Wait() // reap the shell (no zombie)
	}
	if d.shimDir != "" {
		_ = os.RemoveAll(d.shimDir)
	}
	return nil
}

// killGroup escalates a process group to termination: SIGTERM, a brief grace,
// then SIGKILL — the same escalation Stop uses for a distinct foreground group.
func killGroup(pgid int) {
	_ = unix.Kill(-pgid, unix.SIGTERM)
	time.Sleep(closeGrace)
	_ = unix.Kill(-pgid, unix.SIGKILL)
}

// withEnv returns env with any existing `key=` entry removed and `key=val`
// appended, so the spawned shell sees exactly one (overriding) value.
func withEnv(env []string, key, val string) []string {
	prefix := key + "="
	out := env[:0:0] // fresh backing array; never mutate the caller's slice
	for _, e := range env {
		if !strings.HasPrefix(e, prefix) {
			out = append(out, e)
		}
	}
	return append(out, key+"="+val)
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
func (d *Driver) clearBuf()     { d.mu.Lock(); d.buf = d.buf[:0]; d.scanOff = 0; d.mu.Unlock() }
func (d *Driver) idleFor() time.Duration {
	d.mu.Lock()
	defer d.mu.Unlock()
	return time.Since(d.lastSeen)
}

// waitSentinel scans the pty for the next __APB__<digits>__APB__; returns the
// regexp submatch (as []byte slices into buf, valid until the next clearBuf), or
// nil on timeout. It also returns nil promptly when Stop has been called: a
// ^C-interrupted job may never print its sentinel, so the stop flag
// short-circuits the wait instead of blocking for the full timeout.
func (d *Driver) waitSentinel(timeout time.Duration) [][]byte {
	dl := time.Now().Add(timeout)
	for time.Now().Before(dl) {
		if m := d.scanNext(); m != nil {
			return m
		}
		d.mu.Lock()
		stopped := d.stopped
		d.mu.Unlock()
		if stopped {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nil
}

// scanNext does one incremental sentinel scan of buf and advances the scan
// cursor. It searches d.buf directly (no full-buffer string copy) starting a
// little before the already-scanned prefix — rewound by maxSentinelLen so a
// sentinel spanning the previous read-chunk boundary is still matched — then
// advances scanOff past the current buffer length. Returns the submatch or nil.
// Cheap by design: the poll only rescans bytes near the tail, so it does not
// starve the reader goroutine under heavy tty output.
func (d *Driver) scanNext() [][]byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	start := d.scanOff - maxSentinelLen
	if start < 0 {
		start = 0
	}
	m := d.re.FindSubmatch(d.buf[start:])
	if n := len(d.buf); n > d.scanOff {
		d.scanOff = n
	}
	return m
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
		_, _ = fmt.Sscanf(string(m[1]), "%d", &res.Exit)
		// Track the block's final cwd so relative paths / floats resolve against the
		// LIVE session dir. The EXIT-trap wrote it to cwdf and the main shell already
		// re-applied it; an empty file means the block left cwd unchanged.
		if cb, rerr := os.ReadFile(cwdf); rerr == nil {
			if p := strings.TrimRight(string(cb), "\n"); p != "" {
				d.setCwd(p)
			}
		}
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
