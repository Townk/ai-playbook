// Package orchestrator is the in-process, typed replacement for the shell
// ai-assist-action-broker. The broker read <kind>␟<id>␟<payload>␞ records off a
// fifo and performed them; here the pager calls typed Go methods directly — no
// fifos, no text framing. Stage 2 wired the run/stop path to the driver (with
// value-passing across blocks) plus copy/play via the Mux. Stage 4c-i wires the
// diff kinds in-process: apply-diff / undo-diff git-apply the patch via the
// driver, and view-diff opens a floating diff viewer via the Float mux. The
// regenerate / followup / wrapup kinds remain modeled but deferred.
package orchestrator

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"ai-playbook/driver"
	"ai-playbook/mux"
)

// defaultTimeout bounds a single run block (matches the broker's
// AI_ASSIST_RUN_TIMEOUT default of 120s).
const defaultTimeout = 120 * time.Second

// ErrNotImplemented marks an action kind that is modeled but deferred to a later
// migration stage.
var ErrNotImplemented = errors.New("orchestrator: action kind not implemented yet")

// Mux is the terminal-multiplexer surface the orchestrator needs. Stage 2 needs
// only clipboard + type-into-origin-pane; diff-float et al. come with later
// stages, so they are not on the interface yet.
type Mux interface {
	// Copy places text on the clipboard (or OSC 52 over SSH).
	Copy(text string) error
	// Play types cmd into the user's origin pane and submits it.
	Play(cmd string) error
}

// Kind enumerates every action the broker handled, typed.
type Kind int

const (
	KindCopy Kind = iota
	KindPlay
	KindRun
	KindStop
	KindViewDiff
	KindApplyDiff
	KindUndoDiff
	KindRegenerate
	KindFollowup
	KindWrapup
)

// String renders a Kind as its broker record name.
func (k Kind) String() string {
	switch k {
	case KindCopy:
		return "copy"
	case KindPlay:
		return "play"
	case KindRun:
		return "run"
	case KindStop:
		return "stop"
	case KindViewDiff:
		return "view-diff"
	case KindApplyDiff:
		return "apply-diff"
	case KindUndoDiff:
		return "undo-diff"
	case KindRegenerate:
		return "regenerate"
	case KindFollowup:
		return "followup"
	case KindWrapup:
		return "wrapup"
	default:
		return "unknown"
	}
}

// Action is the typed form of the broker's 3-field record (kind␟id␟payload).
type Action struct {
	Kind    Kind
	ID      string
	Payload string
}

// Orchestrator performs actions against a live shell Driver and a Mux.
//
// Mux is the small clipboard/play surface (copy/play). Float is the richer
// terminal-multiplexer surface used to open the view-diff float; it is optional —
// when nil, view-diff is a no-op success (the float just doesn't open) rather than
// an error, so a non-zellij environment degrades gracefully.
type Orchestrator struct {
	Drv   *driver.Driver
	Mux   Mux
	Float mux.Mux
}

// New builds an Orchestrator over the given driver and mux. The Float mux (for
// view-diff) is set separately via WithFloat so existing two-arg callers/tests
// keep compiling.
func New(d *driver.Driver, m Mux) *Orchestrator {
	return &Orchestrator{Drv: d, Mux: m}
}

// WithFloat sets the terminal-multiplexer surface used to open the view-diff
// floating pane and returns the orchestrator (chainable). Optional — leaving it
// nil makes view-diff a graceful no-op.
func (o *Orchestrator) WithFloat(f mux.Mux) *Orchestrator {
	o.Float = f
	return o
}

// Do performs one action. For KindRun it returns the command Result; for every
// other kind the Result is zero. A deferred kind returns ErrNotImplemented.
func (o *Orchestrator) Do(a Action) (driver.Result, error) {
	switch a.Kind {
	case KindRun:
		// Execute the block in the shell, value-passing AAS_OUT_<id>/LAST_* so a
		// later block can reference this one's output.
		return o.Drv.RunID(a.ID, a.Payload, defaultTimeout), nil
	case KindStop:
		// Interrupt the running block by killing its foreground process group.
		o.Drv.Stop()
		return driver.Result{}, nil
	case KindCopy:
		// Clipboard (or OSC 52 over SSH).
		return driver.Result{}, o.Mux.Copy(a.Payload)
	case KindPlay:
		// Type the block into the user's origin pane and run it.
		return driver.Result{}, o.Mux.Play(a.Payload)

	case KindViewDiff:
		// Open the patch side-by-side in a floating diff pane (fire-and-forget).
		return driver.Result{}, o.viewDiff(a.ID, a.Payload)
	case KindApplyDiff:
		// git-apply the patch in the session shell; Exit 0 → applied.
		return o.applyDiff(a.Payload, false), nil
	case KindUndoDiff:
		// git-apply --reverse the patch (apply⇄undo toggle); Exit 0 → reverted.
		return o.applyDiff(a.Payload, true), nil

	// ---- modeled but deferred to later stages ----
	case KindRegenerate:
		// Will re-run the original request and stream a fresh result into the pane.
		return driver.Result{}, ErrNotImplemented
	case KindFollowup:
		// Will re-engage the agent with a "previous fix did not work" prompt.
		return driver.Result{}, ErrNotImplemented
	case KindWrapup:
		// Will build a session summary and stream it into the pane.
		return driver.Result{}, ErrNotImplemented
	default:
		return driver.Result{}, ErrNotImplemented
	}
}

// applyTimeout bounds a `git apply` run (small, local — far under the run default).
const applyTimeout = 30 * time.Second

// applyDiff writes the unified diff to a temp patch file and runs `git apply` in
// the session shell (via the driver, so it executes in the session's cwd/env),
// ported from the broker's broker::git_apply. reverse adds --reverse (the undo
// half of the apply⇄undo toggle). The flags mirror the broker exactly:
//
//	--recount          infer hunk line counts from the body (agent-authored diffs
//	                   reliably miscount the @@ headers; the body is correct).
//	--ignore-whitespace forgive context-line whitespace drift.
//
// The returned driver.Result is the verdict: Exit 0 = applied/reverted; a
// non-zero Exit with stderr = failure feedback the ui surfaces. The patch file is
// removed after the run.
func (o *Orchestrator) applyDiff(diff string, reverse bool) driver.Result {
	patch, err := writePatch(diff)
	if err != nil {
		return driver.Result{Exit: -1, Err: err.Error()}
	}
	defer os.Remove(patch)
	cmd := "git apply --recount --ignore-whitespace "
	if reverse {
		cmd += "--reverse "
	}
	cmd += "-- " + shquote(patch)
	return o.Drv.Run(cmd, applyTimeout)
}

// viewDiff writes the patch to a temp file and opens it in a floating viewer pane
// (hunk → delta → less, like the broker's broker::open_diff). Fire-and-forget:
// the float is best-effort, so a nil Float mux or a spawn error is non-fatal.
// The patch file is intentionally NOT removed — the floating viewer reads it
// asynchronously after this returns (the OS reclaims temp files; the broker left
// them too).
func (o *Orchestrator) viewDiff(id, diff string) error {
	if o.Float == nil {
		return nil // no mux wired → graceful no-op (the float just doesn't open)
	}
	patch, err := writePatch(diff)
	if err != nil {
		return err
	}
	name := "diff:" + id
	cwd := o.projectRoot()
	return o.Float.SpawnFloat(mux.SpawnOptions{
		Cmd:      diffViewerCmd(patch),
		Cwd:      cwd,
		Name:     name,
		Floating: true,
		Width:    90,
		Height:   90,
	})
}

// projectRoot anchors the float pane's cwd, mirroring the broker's
// ${AI_ASSIST_PROJECT_ROOT:-$PWD}: the driver's session cwd, else
// $AI_ASSIST_PROJECT_ROOT, else "" (the mux falls back to its own default).
func (o *Orchestrator) projectRoot() string {
	if o.Drv != nil {
		if c := o.Drv.Cwd(); c != "" {
			return c
		}
	}
	return os.Getenv("AI_ASSIST_PROJECT_ROOT")
}

// writePatch writes diff to a temp patch file with a guaranteed trailing newline
// (git apply rejects a patch without one — the broker appends one too) and
// returns its path.
func writePatch(diff string) (string, error) {
	f, err := os.CreateTemp("", "ai-playbook-apply-*.patch")
	if err != nil {
		return "", err
	}
	name := f.Name()
	body := diff
	if len(body) == 0 || body[len(body)-1] != '\n' {
		body += "\n"
	}
	if _, err := f.WriteString(body); err != nil {
		f.Close()
		os.Remove(name)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

// diffViewerCmd resolves the diff viewer command for patch, porting the broker's
// preference: hunk (split mode) → delta (side-by-side) → less. hunk is overridable
// via $AI_ASSIST_HUNK_BIN (for tests, as in the broker).
func diffViewerCmd(patch string) []string {
	if h := hunkBin(); h != "" {
		return []string{h, "patch", "--mode", "split", patch}
	}
	if d := lookViewer("delta"); d != "" {
		return []string{d, "--side-by-side", "--paging=always", patch}
	}
	return []string{"less", patch}
}

// hunkBin resolves the hunk binary: $AI_ASSIST_HUNK_BIN, else hunk on PATH, else
// well-known install dirs, else "" (not installed).
func hunkBin() string {
	if v := os.Getenv("AI_ASSIST_HUNK_BIN"); v != "" {
		return v
	}
	return lookViewer("hunk")
}

// lookViewer resolves name on PATH, else a couple of well-known install dirs,
// returning "" when not found.
func lookViewer(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	for _, cand := range []string{
		filepath.Join("/opt/homebrew/bin", name),
		filepath.Join("/usr/local/bin", name),
	} {
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return cand
		}
	}
	return ""
}

// shquote single-quotes s for safe inclusion in a shell command line (the driver
// runs cmd through zsh). Matches driver.shquote semantics.
func shquote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
