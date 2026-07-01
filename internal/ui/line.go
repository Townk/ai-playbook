package ui

// Line is one rendered terminal line. Wide marks code-block / table lines that
// keep their natural width (may exceed the pane) and scroll horizontally; prose
// lines are pre-wrapped to the pane width and stay anchored (Wide=false).
type Line struct {
	Text string // styled (ANSI), ready to print
	Wide bool
	Bg   string // optional background ANSI seq; when set on a Wide line, the
	// viewport paints it as a fixed full-width backdrop behind the
	// horizontally-scrolling text (code blocks).
	HBar int // >0 ⇒ render as a horizontal scrollbar for a code block of this
	//      content width; Text is an empty placeholder.
	Code bool // belongs to a code block (tab, body, bottom bar, or HBar row)
	// Callout marks a callout/admonition frame row (top border, left-bar content
	// line, or bottom border). Like Code rows, these carry a colored frame + bg
	// tone; in hint mode they are dimmed while preserving their fill rather than
	// stripped to plain text (which would read as a badly-framed floating line).
	Callout bool
}
