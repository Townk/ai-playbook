package input

import tea "charm.land/bubbletea/v2"

// Ask is the embeddable ask dialog — the SAME desc/box/hint frame the float used,
// re-hosted in the no-mux viewer as an overlay. It supports every ask type the
// float renders: text|line|free (text entry), confirm (yes/no), and choose
// (pick-from-options). It wraps the standalone input `model` and delegates all
// rendering + key handling to the underlying `field`, so each type behaves exactly
// as it does in the float — no special-casing here.
type Ask struct {
	m model
}

// FloatWidthDefault matches the float's 57-col geometry, so the overlay box is the
// same width the user sees in the mux-present ask. Exported so the ui overlay
// (internal/ui/ask_overlay.go) can share the single source of truth.
const FloatWidthDefault = 57

// NewAsk builds an ask dialog for typ:
//   - "line"            → single-line text entry
//   - "text"/"free"/""  → multi-line text entry
//   - "confirm"         → a yes/no button row
//   - "choose"          → a single-select option list built from choices
//
// value prefills the text variants; choices supplies the options for "choose".
// The field constructors are exactly the ones the standalone `input` command uses
// for each type, so the rendering and key handling are identical.
func NewAsk(title, prompt, value, typ string, choices []string) *Ask {
	theme := defaultTheme()
	const variant = "default"

	switch typ {
	case "confirm":
		m := newInputModel(theme, variant, title, prompt, "", "", 1, 1, 1, false, "")
		m.fld = newConfirmField(theme, variant, "Yes", "No", false)
		m.width = FloatWidthDefault
		return &Ask{m: m}
	case "choose":
		m := newInputModel(theme, variant, title, prompt, "", "", 1, 1, 1, false, "")
		m.fld = newChooseField(theme, variant, choices, false, "")
		m.width = FloatWidthDefault
		return &Ask{m: m}
	default: // text | free | line | ""
		singleLine := typ == "line"
		height := 3
		if singleLine {
			height = 1
		}
		m := newInputModel(theme, variant, title, prompt, value, "", height, 1, 1, singleLine, "")
		m.width = FloatWidthDefault
		m.resize()
		return &Ask{m: m}
	}
}

func (a *Ask) Init() tea.Cmd { return a.m.fld.initCmd() }

// Update steps the dialog by delegating to the underlying field. done is true when
// the user submitted or cancelled; submitted distinguishes the two; value is the
// field's produced answer (the typed text, "yes"/"no", or the chosen option).
// Window resize is handled by the embedding ui model before messages reach here;
// passing WindowSizeMsg is unnecessary and the case is intentionally absent.
func (a *Ask) Update(msg tea.Msg) (cmd tea.Cmd, done, submitted bool, value string) {
	f, act, c := a.m.fld.handle(msg)
	a.m.fld = f
	switch act {
	case fieldDone:
		return c, true, true, a.m.fld.value()
	case fieldCancel:
		return c, true, false, ""
	}
	return c, false, false, ""
}

// View renders the framed dialog at width (the overlay composites this centered).
func (a *Ask) View(width int) string {
	a.m.width = width
	a.m.resize()
	return a.m.render()
}
