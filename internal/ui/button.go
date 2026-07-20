package ui

// Button is one activatable control on a code-block tab. Line indexes into the
// []Line returned by Render; Col/Width are the glyph+trailing-space click target
// within that line's content (before the model's 2-col left margin). Kind is
// "play" or "copy"; Payload is the code block's raw source. BlockID is the
// block's assigned id (auto-assigned if not specified in the info string).
//
// When Screen is true, Line is an absolute screen row (0-based) rather than a
// content-line index. Screen buttons are hit-tested directly by screen Y
// (msg.Y == Line) without any yOff/bodyTop adjustment. This is used for
// fixed-header controls like the reload icon on the cached pill.
type Button struct {
	Line    int
	Col     int
	Width   int
	Kind    string
	Payload string
	BlockID string
	Screen  bool
	// Pill marks a powerline-pill button (cap + body + cap, actionPill). Hint
	// mode renders its whole span with the inverted greyed fill (hintCodeRow
	// pillSpans) instead of the dark-red glyph cell, and anchors the hint label
	// over the glyph just after the left cap (Col+1).
	Pill bool
}

// hintAlphabet is the ordered set of single-char labels used by assignHintLabels.
const hintAlphabet = "asdfghjklqwertyuiop"

// buttonAt maps a mouse click at screen position (x, y) to a Button.
// bodyTop is the screen row of the first body line; yOff is the viewport offset.
// The content column is x-2 (2-col left margin); the line index is yOff+(y-bodyTop).
//
// For Screen buttons (b.Screen == true), Line is an absolute screen row and the
// match is y == b.Line; the 2-col left margin is still applied to x.
// For body buttons (b.Screen == false), Line is a content-line index and the
// match is yOff+(y-bodyTop) == b.Line with y >= bodyTop required.
func buttonAt(buttons []Button, x, y, yOff, bodyTop int) (Button, bool) {
	col := x - 2 // 2-col left margin (applied consistently for all buttons)
	for _, b := range buttons {
		if b.Screen {
			// Screen-fixed button: match absolute screen row directly.
			if y == b.Line && col >= b.Col && col < b.Col+b.Width {
				return b, true
			}
			continue
		}
		// Body button: require y >= bodyTop and map to content-line index.
		if y < bodyTop {
			continue
		}
		line := yOff + (y - bodyTop)
		if b.Line == line && col >= b.Col && col < b.Col+b.Width {
			return b, true
		}
	}
	return Button{}, false
}

// assignHintLabels assigns distinct single-char labels from hintAlphabet to
// each visible button in order, returning a map from label to Button.
func assignHintLabels(visible []Button) map[string]Button {
	out := make(map[string]Button, len(visible))
	for i, b := range visible {
		if i >= len(hintAlphabet) {
			break
		}
		out[string(hintAlphabet[i])] = b
	}
	return out
}
