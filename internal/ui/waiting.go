package ui

// WaitingLine renders the shared progress block (spinner + escalating phrase +
// elapsed + activity). Thin wrapper over ProgressWidget for callers that hold raw
// frame/elapsed/activity values rather than a widget.
func WaitingLine(frame, elapsedSec int, activity string, width int) string {
	w := ProgressWidget{frame: frame, ticks: elapsedSec * 10, activity: collapseLine(activity)}
	return w.Render(width)
}
