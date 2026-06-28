package ui

// ProgressWidget is the reusable authoring-progress render shared by every host
// (the inline no-mux/arg progress and the in-viewer "thinking" block). It owns the
// spinner frame, the elapsed-time ticks (100ms each), and the latest activity
// summary, and renders the one canonical progress block (spinner + the escalating
// "Working…" phrase + elapsed, with the activity line below). Hosts drive it via
// Tick()/SetActivity() on their existing tick/activity messages and call Render in
// their View, so any change to the progress look propagates everywhere.
type ProgressWidget struct {
	frame    int    // spinner frame (advances each Tick)
	ticks    int    // 100ms ticks; elapsed seconds = ticks / 10
	activity string // latest collapsed activity summary ("" → no activity line)
}

// Tick advances the spinner frame and the elapsed-time counter by one 100ms tick.
func (w *ProgressWidget) Tick() { w.frame++; w.ticks++ }

// SetActivity replaces the activity summary shown below the spinner (collapsed to a
// single legible line). Pass "" to clear it.
func (w *ProgressWidget) SetActivity(summary string) {
	if summary == "" {
		w.activity = ""
		return
	}
	w.activity = collapseLine(summary)
}

// Reset returns the widget to its initial state (frame 0, elapsed 0, no activity) —
// used when a host starts a fresh authoring/thinking phase.
func (w *ProgressWidget) Reset() { w.frame = 0; w.ticks = 0; w.activity = "" }

// Elapsed returns the elapsed whole seconds (ticks/10).
func (w *ProgressWidget) Elapsed() int { return w.ticks / 10 }

// Render returns the progress block: "<spinner> <phrase> <Ns>" and, when an activity
// summary is set, the activity line below it, truncated to width.
func (w *ProgressWidget) Render(width int) string {
	line := spinnerLine(w.frame, workingLabel(w.Elapsed()), w.Elapsed())
	if w.activity == "" {
		return line
	}
	return line + "\n" + activityLineStr(w.activity, width)
}
