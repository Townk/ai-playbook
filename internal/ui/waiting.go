package ui

// WaitingLine renders the viewer-style classify-progress block for the explicit
// no-mux path: the tiered "Working…" spinner row (the same workingPhrases the
// authoring viewer uses) plus, when activity != "", the model-activity line
// below it. frame advances the braille spinner; elapsedSec drives the tiered
// phrase; width truncates the activity line.
func WaitingLine(frame, elapsedSec int, activity string, width int) string {
	line := spinnerLine(frame, workingLabel(elapsedSec), elapsedSec)
	if activity == "" {
		return line
	}
	return line + "\n" + activityLineStr(activity, width)
}
