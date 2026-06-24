// Package orchestrator is the in-process, typed replacement for the shell
// ai-assist-action-broker. The broker read <kind>␟<id>␟<payload>␞ records off a
// fifo and performed them; here the pager calls typed Go methods directly — no
// fifos, no text framing. Stage 2 wires the run/stop path to the driver (with
// value-passing across blocks) plus copy/play via the Mux; the diff / regenerate
// / followup / wrapup kinds are modeled but deferred to later stages.
package orchestrator

import (
	"errors"
	"time"

	"ai-playbook/driver"
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
type Orchestrator struct {
	Drv *driver.Driver
	Mux Mux
}

// New builds an Orchestrator over the given driver and mux.
func New(d *driver.Driver, m Mux) *Orchestrator {
	return &Orchestrator{Drv: d, Mux: m}
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

	// ---- modeled but deferred to later stages ----
	case KindViewDiff:
		// Will open the patch side-by-side in a floating diff pane.
		return driver.Result{}, ErrNotImplemented
	case KindApplyDiff:
		// Will git-apply the patch in the shell and re-gate dependents.
		return driver.Result{}, ErrNotImplemented
	case KindUndoDiff:
		// Will git-apply --reverse the patch (apply⇄undo toggle).
		return driver.Result{}, ErrNotImplemented
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
