package orchestrator

// coverage_extras_test.go — targeted tests for the executor core's edge branches:
//
//   • Kind.String default case
//   • Do default kind (ErrNotImplemented)
//   • projectRoot nil-driver path
//   • writePatch trailing-newline addition
//   • viewDiff SpawnFloat error propagation
//
// The re-engagement coverage tests moved to internal/reengage with the ADR-0009
// step-2 split.

import (
	"errors"
	"os"
	"testing"
)

// ── Kind.String default case ─────────────────────────────────────────────────

func TestKindString_Unknown(t *testing.T) {
	if got := Kind(999).String(); got != "unknown" {
		t.Errorf("Kind(999).String() = %q, want unknown", got)
	}
}

// ── Do default case ──────────────────────────────────────────────────────────

func TestDo_DefaultKindNotImplemented(t *testing.T) {
	// An unrecognized Kind that falls through to the default: branch in Do.
	o := &Orchestrator{Mux: &recMux{}}
	_, err := o.Do(Action{Kind: Kind(999)})
	if !errors.Is(err, ErrNotImplemented) {
		t.Errorf("Do(Kind(999)) err = %v, want ErrNotImplemented", err)
	}
}

// ── projectRoot ───────────────────────────────────────────────────────────────

func TestProjectRoot_NilDriverEnvVar(t *testing.T) {
	// nil Drv → fall through to AI_PLAYBOOK_PROJECT_ROOT.
	t.Setenv("AI_PLAYBOOK_PROJECT_ROOT", "/from/env")
	o := &Orchestrator{}
	if got := o.projectRoot(); got != "/from/env" {
		t.Errorf("projectRoot (nil driver, env set) = %q, want /from/env", got)
	}
}

func TestProjectRoot_NilDriverNoEnv(t *testing.T) {
	// nil Drv and unset env → empty string.
	t.Setenv("AI_PLAYBOOK_PROJECT_ROOT", "")
	o := &Orchestrator{}
	if got := o.projectRoot(); got != "" {
		t.Errorf("projectRoot (nil driver, no env) = %q, want empty", got)
	}
}

// ── writePatch ────────────────────────────────────────────────────────────────

func TestWritePatch_AddsTrailingNewline(t *testing.T) {
	// A diff without a trailing newline must get one appended.
	p, err := writePatch("no-trailing-newline")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(p)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "no-trailing-newline\n" {
		t.Errorf("writePatch added-newline content = %q, want trailing \\n", b)
	}
}

func TestWritePatch_EmptyDiff(t *testing.T) {
	// An empty diff results in a file containing just a newline.
	p, err := writePatch("")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(p)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "\n" {
		t.Errorf("writePatch empty diff content = %q, want \\n", b)
	}
}

// ── ViewDiff with SpawnFloat error ────────────────────────────────────────────

func TestViewDiff_SpawnFloatError(t *testing.T) {
	// When SpawnFloat fails, viewDiff should propagate the error.
	rf := &recFloat{err: errors.New("mux: pane limit exceeded")}
	o := &Orchestrator{Mux: &recMux{}, Float: rf}
	_, err := o.Do(Action{Kind: KindViewDiff, ID: "x", Payload: "diff --git a/f b/f\n"})
	if err == nil {
		t.Error("viewDiff with SpawnFloat error should return error, got nil")
	}
}
