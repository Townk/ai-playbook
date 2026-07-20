package ui

import (
	"strings"

	"github.com/clipperhouse/displaywidth"

	"charm.land/lipgloss/v2"
)

// spinnerFrames are the braille dots used by the "working…" indicator — the same
// frames the shell launcher used before the pager owned the spinner.
var spinnerFrames = []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}

// workingPhrases is the ordered progression of "working…" labels shown over a
// long authoring/follow-up wait, easing anxiety as the wait grows. The list is
// advanced one step every workingStepSec seconds and HOLDS the last phrase
// thereafter (authoring has no hard timeout — it runs until claude returns), so
// the user always sees a live, escalating-then-steady reassurance. Tweak the list
// or the cadence here.
var workingPhrases = []string{
	"Working…",
	"Still working…",
	"Thinking this through…",
	"Working through the details…",
	"Still on it…",
	"This needs a closer look…",
	"Getting there…",
	"Taking a bit longer than expected…",
	"Still digging in — hang tight…",
	"Trickier than it looked…",
	"Appreciate your patience…",
	"Still working on it…",
	"This is a stubborn one…",
	"Almost there, I think…",
	"Thanks for waiting…",
	"Still going — nearly there…",
}

// workingStepSec is the cadence (in seconds) at which workingLabel advances to the
// next phrase. Every workingStepSec the label steps forward one entry, capped at
// the last (held) phrase.
const workingStepSec = 15

// workingLabel returns the working-progression phrase for an elapsed wait of
// elapsedSec seconds: index = min(elapsedSec/workingStepSec, len-1) into
// workingPhrases — it escalates one step per workingStepSec then HOLDS the tail.
// Negative elapsed clamps to the first phrase.
func workingLabel(elapsedSec int) string {
	if elapsedSec < 0 {
		elapsedSec = 0
	}
	i := elapsedSec / workingStepSec
	if i >= len(workingPhrases) {
		i = len(workingPhrases) - 1
	}
	return workingPhrases[i]
}

// spinnerLine renders one indicator row: "<frame> <label> <seconds>s", the frame
// and seconds in colBlue, the label in colSubtext. frame is taken modulo the
// frame count (tolerates negative/large values).
func spinnerLine(frame int, label string, seconds int) string {
	n := len(spinnerFrames)
	g := spinnerFrames[((frame%n)+n)%n]
	dot := lipgloss.NewStyle().Foreground(lipgloss.Color(colBlue))
	lab := lipgloss.NewStyle().Foreground(lipgloss.Color(colSubtext))
	return dot.Render(string(g)) + " " + lab.Render(label) + " " + dot.Render(itoa(seconds)+"s")
}

// activityGlyph is the spinning-arrow marker shown before a live agent tool-call
// summary on the activity line (distinct from the braille "Working…" frame).
const activityGlyph = "⟳"

// activityLineMax bounds a collapsed activity summary as a safety net when the
// render width isn't known yet — the model reasoning can be much longer than a
// tool summary, so cap it to ~80 cols before the width-aware render truncates it.
const activityLineMax = 80

// collapseLine flattens summary to a single trimmed line (newlines/whitespace runs
// → single spaces) and caps it to activityLineMax runes (ellipsis when truncated),
// so a long, multi-line model-reasoning chunk renders as one legible row.
func collapseLine(summary string) string {
	s := strings.TrimSpace(strings.Join(strings.Fields(summary), " "))
	r := []rune(s)
	if len(r) > activityLineMax {
		return string(r[:activityLineMax]) + "…"
	}
	return s
}

// activityLineStr renders the agent's live tool-call summary row: "⟳ <summary>"
// in a dim subtext colour, truncated to width. Empty when summary is empty.
func activityLineStr(summary string, width int) string {
	if summary == "" {
		return ""
	}
	s := activityGlyph + " " + summary
	if width > 0 && displaywidth.String(s) > width {
		s = displaywidth.TruncateString(s, width, "…")
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(colOverlay0)).Render(s)
}
