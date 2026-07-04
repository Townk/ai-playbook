// Package orchestrator is the in-process, typed replacement for the shell
// ai-assist-action-broker. The broker read <kind>␟<id>␟<payload>␞ records off a
// fifo and performed them; here the pager calls typed Go methods directly — no
// fifos, no text framing. It wires the run/stop path to the driver (with
// value-passing across blocks), copy/play via the Mux, and the diff kinds
// in-process: apply-diff / undo-diff git-apply the patch via the driver, and
// view-diff spawns the in-process diff renderer (`ai-playbook diff`) in a Float mux
// pane, plus file create/undo and drift checking.
//
// This is the executor CORE (ADR-0009 step 2): it is AI-free. The re-engagement
// surface (regenerate / followup / finalplaybook / commit) lives in
// internal/reengage; the ui holds that engine as a second handle. The regenerate /
// followup button kinds still exist in the action vocabulary so the ui can name
// them, but they never route through Do — reaching Do is a wiring bug (ErrMisrouted).
package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Townk/ai-playbook/internal/diff"
	"github.com/Townk/ai-playbook/internal/mux"
	"github.com/Townk/ai-playbook/pkg/driver"
)

// defaultTimeout bounds a single run block (matches the broker's
// AI_PLAYBOOK_RUN_TIMEOUT default of 120s).
const defaultTimeout = 120 * time.Second

// ErrNotImplemented marks an action kind that is modeled but deferred to a later
// migration stage.
var ErrNotImplemented = errors.New("orchestrator: action kind not implemented yet")

// ErrMisrouted marks a re-engagement kind (regenerate / followup) that reached the
// executor's Do. Those kinds re-author and yield a NEW stream that must SWAP the
// ui's rendered playbook — that does not fit Do's (Result, error) shape, so the ui
// drives them through the internal/reengage engine instead. Reaching Do means the
// caller used the wrong seam; a distinct error (not ErrNotImplemented) keeps such a
// wiring bug from masquerading as "not available in in-process mode yet".
var ErrMisrouted = errors.New("orchestrator: re-engagement kind reached the executor — route via the reengage engine")

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
	KindCreateFile
	KindUndoCreate
	KindRegenerate
	KindFollowup
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
	case KindCreateFile:
		return "create"
	case KindUndoCreate:
		return "undo-create"
	case KindRegenerate:
		return "regenerate"
	case KindFollowup:
		return "followup"
	default:
		return "unknown"
	}
}

// ParseKind maps a UI button-kind string to its typed Kind — the single inverse of
// Kind.String, so the ui does not hand-maintain a duplicate switch. It accepts the
// canonical names Kind.String emits plus the "diff" alias for view-diff (the button
// kind the renderer uses). The second result is false for strings that name no
// executor action (e.g. pager-local "toggle"), so a caller can degrade cleanly.
func ParseKind(s string) (Kind, bool) {
	switch s {
	case "copy":
		return KindCopy, true
	case "play":
		return KindPlay, true
	case "run":
		return KindRun, true
	case "stop":
		return KindStop, true
	case "diff", "view-diff":
		return KindViewDiff, true
	case "apply-diff":
		return KindApplyDiff, true
	case "undo-diff":
		return KindUndoDiff, true
	case "create":
		return KindCreateFile, true
	case "undo-create":
		return KindUndoCreate, true
	case "regenerate":
		return KindRegenerate, true
	case "followup":
		return KindFollowup, true
	default:
		return 0, false
	}
}

// Action is the typed form of the broker's 3-field record (kind␟id␟payload).
type Action struct {
	Kind    Kind
	ID      string
	Payload string
	// StdinPath, for a KindRun action, is the filesystem path fed to the block's
	// stdin — the retained stdout capture of its from= producer (ADR-0010). Empty
	// keeps the block's stdin at </dev/null exactly as before; the caller resolves
	// it (producer id → driver.CapturePath → STAT) so the executor never re-derives
	// the retention layout. Ignored by every non-run kind.
	StdinPath string
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

	// createBackups records the prior content of files touched by createFile so
	// undoCreate can restore them symmetrically. A nil *[]byte means the file was
	// new (undo deletes it); a non-nil *[]byte holds the overwritten content.
	//
	// backupMu guards createBackups only — not the file I/O and not the rest of
	// Do. Do is invoked from concurrent Bubble Tea tea.Cmd goroutines, so two
	// quick create/undo clicks race on this map; a scoped mutex prevents the
	// concurrent-map panic without needlessly serializing unrelated actions
	// (Run/Stop/Copy) that touch neither the map nor each other's state.
	backupMu      sync.Mutex
	createBackups map[string]*[]byte
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
		// Execute the block in the shell, value-passing APB_OUT_<id>/LAST_* so a
		// later block can reference this one's output. StdinPath (when set) pipes a
		// prior block's retained capture into this block's stdin (from= piping).
		return o.Drv.RunID(a.ID, a.Payload, a.StdinPath, defaultTimeout), nil
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

	case KindCreateFile:
		// Write a new (or overwrite an existing) file; backs up prior content.
		return o.createFile(a.Payload), nil
	case KindUndoCreate:
		// Restore the backed-up content (or delete the file if it was new).
		return o.undoCreate(a.Payload), nil

	// ---- re-engagement kinds ----
	// These re-invoke the author and yield a NEW stream that must SWAP the ui's
	// rendered playbook — that doesn't fit Do's (Result, error) shape, so the ui
	// drives them through the internal/reengage engine instead of Do. Reaching them
	// here means the caller used the wrong seam; surface the distinct ErrMisrouted so
	// the wiring bug doesn't render as "not available in in-process mode yet".
	case KindRegenerate, KindFollowup:
		return driver.Result{}, ErrMisrouted
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
	cmd += "-- " + driver.Shquote(patch)
	return o.Drv.Run(cmd, applyTimeout)
}

// EditSource opens the playbook source in a docked editor pane (mux only).
// Guard: if Float is nil (no mux wired) this is a no-op — the no-mux path uses
// tea.ExecProcess instead (model.go).
func (o *Orchestrator) EditSource(editor, path string) error {
	if o.Float == nil {
		return nil
	}
	parts := append(strings.Fields(editor), path)
	return o.Float.SpawnDocked(mux.SpawnOptions{Cmd: parts, Cwd: o.projectRoot(), Name: "edit", Floating: false})
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
	selfExe, err := os.Executable()
	if err != nil {
		return err
	}
	name := "diff:" + id
	cwd := o.projectRoot()
	return o.Float.SpawnFloat(mux.SpawnOptions{
		Cmd:      []string{selfExe, "diff", patch},
		Cwd:      cwd,
		Name:     name,
		Floating: true,
		Width:    90,
		Height:   90,
	})
}

// projectRoot anchors the float pane's cwd, mirroring the broker's
// ${AI_PLAYBOOK_PROJECT_ROOT:-$PWD}: the driver's session cwd, else
// $AI_PLAYBOOK_PROJECT_ROOT, else "" (the mux falls back to its own default).
func (o *Orchestrator) projectRoot() string {
	if o.Drv != nil {
		if c := o.Drv.Cwd(); c != "" {
			return c
		}
	}
	return os.Getenv("AI_PLAYBOOK_PROJECT_ROOT")
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

// DriftVerdict classifies the relationship between a unified diff and its target
// as determined by git apply --check (forward then reverse).
type DriftVerdict int

const (
	DriftClean   DriftVerdict = iota // patch applies forward — target is at pre-patch state
	DriftApplied                     // patch reverse-applies — already applied to target
	DriftDrifted                     // neither applies — target changed incompatibly
)

// driftCheckTimeout bounds a single `git apply --check` invocation. The check is
// read-only and near-instant; the timeout only guards against a filesystem stall
// (a hung git) wedging document load, since CheckDrift fires once per diff block.
const driftCheckTimeout = 10 * time.Second

// CheckDrift classifies whether diff still applies to its target. It never mutates
// the working tree (git apply --check, forward then reverse).
//
// It runs git DIRECTLY via exec.Command, not through the session shell: the check
// is read-only, needs no session shell state, and must NOT contend with an
// in-flight Run on the driver's runMu (it fires once per diff block on document
// load). The check is anchored at projectRoot() — the SAME directory
// DriftTargetPath resolves the target file against — so the check and any
// subsequent apply agree on which file they evaluate. The patch reaches git as a
// file path (the same temp-patch mechanism applyDiff uses), not stdin.
//
// State mapping (preserved bit-for-bit from the prior shell path): forward
// `--check` exit 0 → DriftClean; else reverse `--check --reverse` exit 0 →
// DriftApplied; else DriftDrifted (which also absorbs environmental failures —
// git missing, not a repo, target absent — that the viewer treats as "needs
// attention").
func (o *Orchestrator) CheckDrift(diff string) (DriftVerdict, error) {
	patch, err := writePatch(diff)
	if err != nil {
		return DriftDrifted, err
	}
	defer os.Remove(patch)
	if o.gitApplyChecks(patch, false) {
		return DriftClean, nil
	}
	if o.gitApplyChecks(patch, true) {
		return DriftApplied, nil
	}
	return DriftDrifted, nil
}

// gitApplyChecks runs `git apply --check` (adding --reverse when reverse) on the
// temp patch file, anchored at projectRoot(), and reports whether it applied
// cleanly (exit 0). It mirrors the flags the shell path used exactly (--recount
// --ignore-whitespace) and bounds the run with a context timeout so a hung git
// can't wedge document load. A non-zero exit, a launch error (git missing), or a
// timeout all report false — the CheckDrift caller maps a double-false to
// DriftDrifted, matching the prior path's environmental-failure handling.
func (o *Orchestrator) gitApplyChecks(patch string, reverse bool) bool {
	ctx, cancel := context.WithTimeout(context.Background(), driftCheckTimeout)
	defer cancel()
	args := []string{"apply", "--check", "--recount", "--ignore-whitespace"}
	if reverse {
		args = append(args, "--reverse")
	}
	args = append(args, "--", patch)
	cmd := exec.CommandContext(ctx, "git", args...)
	// Anchor at the session root the same way DriftTargetPath does, so the check's
	// resolution of the patch's a/b paths matches the file a later apply targets.
	// An empty projectRoot leaves Dir unset → exec uses the process cwd, which is
	// what the session shell inherited when it too had no tracked cwd.
	cmd.Dir = o.projectRoot()
	return cmd.Run() == nil
}

// fileAction is the JSON payload shared by KindCreateFile / KindUndoCreate.
type fileAction struct {
	Path string `json:"path"`
	Body string `json:"body"`
}

// EncodeFileAction serialises a path + body into the JSON payload expected by
// createFile / undoCreate. Called by the UI button (Task 3) when building the
// Action.Payload for these kinds.
func EncodeFileAction(path, body string) string {
	b, _ := json.Marshal(fileAction{Path: path, Body: body})
	return string(b)
}

// decodeFileAction deserialises a KindCreateFile / KindUndoCreate payload.
func decodeFileAction(payload string) (string, string, error) {
	var fa fileAction
	if err := json.Unmarshal([]byte(payload), &fa); err != nil {
		return "", "", err
	}
	return fa.Path, fa.Body, nil
}

// createFile writes body to the path described by payload, anchoring relative
// paths against the driver's session cwd (projectRoot). If the file already
// exists its content is saved in createBackups so undoCreate can restore it;
// a nil entry records that the file was new (undo must delete it).
func (o *Orchestrator) createFile(payload string) driver.Result {
	relPath, body, err := decodeFileAction(payload)
	if err != nil {
		return driver.Result{Exit: -1, Err: err.Error()}
	}
	abs := relPath
	if !filepath.IsAbs(abs) {
		root := o.projectRoot()
		if root == "" {
			return driver.Result{Exit: -1, Err: "createFile: cannot resolve relative path — driver cwd unknown"}
		}
		abs = filepath.Join(root, relPath)
	}
	// Capture prior content if the file exists (a nil entry records a new file).
	// The read is outside the lock; only the map write is guarded.
	var entry *[]byte
	if existing, rerr := os.ReadFile(abs); rerr == nil {
		cp := make([]byte, len(existing))
		copy(cp, existing)
		entry = &cp
	}
	o.backupMu.Lock()
	if o.createBackups == nil {
		o.createBackups = make(map[string]*[]byte)
	}
	o.createBackups[abs] = entry
	o.backupMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return driver.Result{Exit: -1, Err: err.Error()}
	}
	// Ensure a trailing newline: the UI trims the render-trimmed payload, so a
	// body without one would produce a non-POSIX file (e.g. a .go file that fails
	// gofmt). Empty bodies are left empty; already-newline-terminated bodies are
	// unchanged.
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		return driver.Result{Exit: -1, Err: err.Error()}
	}
	return driver.Result{Exit: 0}
}

// undoCreate reverses a previous createFile: if the file was overwritten its
// prior content is restored; if the file was new it is deleted. The backup
// entry is removed after use so a second undo is a no-op (no backup → nothing
// to undo).
func (o *Orchestrator) undoCreate(payload string) driver.Result {
	relPath, _, err := decodeFileAction(payload)
	if err != nil {
		return driver.Result{Exit: -1, Err: err.Error()}
	}
	abs := relPath
	if !filepath.IsAbs(abs) {
		root := o.projectRoot()
		if root == "" {
			return driver.Result{Exit: -1, Err: "undoCreate: cannot resolve relative path — driver cwd unknown"}
		}
		abs = filepath.Join(root, relPath)
	}
	o.backupMu.Lock()
	backup, found := o.createBackups[abs]
	o.backupMu.Unlock()
	if !found {
		return driver.Result{Exit: 0} // no backup recorded — nothing to undo
	}
	if backup != nil {
		// File existed before; restore it.
		if err := os.WriteFile(abs, *backup, 0o644); err != nil {
			// Leave the backup entry intact so undo is retryable.
			return driver.Result{Exit: -1, Err: err.Error()}
		}
	} else {
		// File was new; delete it.
		if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
			// Leave the backup entry intact so undo is retryable.
			return driver.Result{Exit: -1, Err: err.Error()}
		}
	}
	// Only remove the backup entry after a successful restore/delete, so a
	// failed undo can be retried (the entry is still present on an error path).
	o.backupMu.Lock()
	delete(o.createBackups, abs)
	o.backupMu.Unlock()
	return driver.Result{Exit: 0}
}

// DriftTargetPath resolves the on-disk absolute path of a patch's target file: the
// parsed new-file path (files[0].NewPath) with a leading a/ or b/ prefix stripped,
// joined against the session root (projectRoot — the driver's cwd) when relative. It
// is the single source of truth for "which file does this patch target", shared by
// DriftRegen and the UI's "resolve manually" editor (F21) so both act on the exact
// file the drift check evaluated. Returns an error when the patch has no target.
func (o *Orchestrator) DriftTargetPath(patch string) (string, error) {
	files := diff.Parse(patch)
	if len(files) == 0 {
		return "", errors.New("could not parse patch target")
	}
	rel := strings.TrimPrefix(strings.TrimPrefix(files[0].NewPath, "b/"), "a/")
	if rel == "" {
		return "", errors.New("could not parse patch target")
	}
	if filepath.IsAbs(rel) {
		return rel, nil
	}
	return filepath.Join(o.projectRoot(), rel), nil
}
