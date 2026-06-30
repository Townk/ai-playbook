package ui

import "github.com/Townk/ai-playbook/internal/orchestrator"

// driftMsg carries the result of an async drift check for a diff block. The
// handler sets blockRunState.Drifted when the verdict is DriftDrifted (the
// patch no longer applies cleanly) and clears it on DriftClean / DriftApplied.
type driftMsg struct {
	ID      string
	Verdict orchestrator.DriftVerdict
}

type resultMsg struct {
	ID      string
	Exit    int
	Logpath string
}

type blockRunState struct {
	Status    string // "running" | "ok" | "failed" | "stopped"
	Action    string // "apply" | "undo" | "run" — which action the in-flight result belongs to
	Exit      int
	Logpath   string
	Expanded  bool
	SpinFrame int
	Stopped   bool // user clicked stop on this block; suppress auto-followup when its result arrives

	// FollowupExhausted is set on the "verify" block once the auto-follow-up cap is
	// reached: the verify block normally hides the manual "try another fix" button
	// (it auto-fires), but past the cap auto-firing stops and the button is shown so
	// the user can keep iterating by hand. See render.go's failed-block button gate.
	FollowupExhausted bool

	// Drifted is set when an async drift check (driftCheckCmds) returns DriftDrifted:
	// the patch can no longer be applied or reversed — the target changed incompatibly.
	// Cleared when DriftClean (forward applies) or DriftApplied (already applied).
	// Tasks 3-4 read this to surface a warning badge on the diff block's render.
	Drifted bool
}
