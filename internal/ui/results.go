package ui

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
}
