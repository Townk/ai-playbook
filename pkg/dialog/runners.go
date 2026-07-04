package dialog

import (
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/mattn/go-runewidth"
)

// This file is the public options-struct surface consumed by the standalone
// `ask` binary (internal/askcli). Each Run* function wraps the SAME unexported
// widget machinery that `ai-playbook input` drives, but returns a typed outcome
// instead of calling os.Exit / writing to stdout — so a second front-end can own
// the exit-code and stdout contract. The widgets stay the single implementation;
// these runners add no widget behavior. `ai-playbook input`'s own paths
// (input.Main and the run* helpers) are untouched.

// NarrowRuneWidth pins ambiguous-width + nerd-font glyphs to one cell so
// lipgloss widths match the terminal. It mirrors the first two lines of
// input.Main and MUST run before any lipgloss call (render/measure). Exported
// so `ask` applies the identical accounting `ai-playbook input` relies on.
func NarrowRuneWidth() {
	os.Setenv("RUNEWIDTH_EASTASIAN", "0")
	runewidth.DefaultCondition.EastAsianWidth = false
}

// DefaultTheme returns the built-in palette (Catppuccin Mocha). Exported so a
// second front-end can seed env-fallback flag defaults from the same source.
func DefaultTheme() Theme { return defaultTheme() }

// runProgram runs a tea model with the same options the internal run* helpers
// use (render to stderr, TrueColor) and returns the final model. The error is
// bubbletea's — notably a TTY-open failure when neither stdin nor /dev/tty is a
// terminal, which the caller maps to the no-TTY exit code.
func runProgram(m tea.Model) (tea.Model, error) {
	return tea.NewProgram(
		m,
		tea.WithOutput(os.Stderr),
		tea.WithColorProfile(colorprofile.TrueColor),
	).Run()
}

// --- confirm -----------------------------------------------------------------

// ConfirmOptions configures a confirm dialog. Variant is "default"|"danger"|
// "warning"; DefaultNegative starts focus on the negative button.
type ConfirmOptions struct {
	Theme           Theme
	Variant         string
	Title           string
	Prompt          string
	Affirmative     string
	Negative        string
	DefaultNegative bool
	Padding         int
	Inset           int
	Width           int
}

// ConfirmResult is a confirm outcome: Cancelled (ESC/Ctrl-C) or an answer of
// "yes"/"no" in Value.
type ConfirmResult struct {
	Cancelled bool
	Value     string
}

// RunConfirm runs the confirm dialog and returns its outcome. It never exits the
// process or writes to stdout.
func RunConfirm(o ConfirmOptions) (ConfirmResult, error) {
	fm, err := runProgram(newConfirmModel(o.Theme, o.Variant, o.Title, o.Prompt, o.Affirmative, o.Negative, o.DefaultNegative, o.Padding, o.Inset))
	if err != nil {
		return ConfirmResult{}, err
	}
	res := fm.(model)
	if res.quitting || !res.submitted {
		return ConfirmResult{Cancelled: true}, nil
	}
	return ConfirmResult{Value: res.fld.value()}, nil
}

// MeasureConfirm returns the rendered height (lines) of the confirm dialog at
// o.Width — identical to `ai-playbook input --type confirm --measure`.
func MeasureConfirm(o ConfirmOptions) int {
	m := newConfirmModel(o.Theme, o.Variant, o.Title, o.Prompt, o.Affirmative, o.Negative, o.DefaultNegative, o.Padding, o.Inset)
	m.width = o.Width
	return measureHeight(m.render())
}

// --- line / text -------------------------------------------------------------

// LineOptions configures a one-line input dialog.
type LineOptions struct {
	Theme       Theme
	Variant     string
	Title       string
	Prompt      string
	Value       string
	Placeholder string
	Icon        string
	Padding     int
	Inset       int
	Width       int
}

// TextOptions configures a multi-line editor dialog. Height is the viewport rows.
type TextOptions struct {
	Theme   Theme
	Variant string
	Title   string
	Prompt  string
	Value   string
	Icon    string
	Height  int
	Padding int
	Inset   int
	Width   int
}

// ValueResult is a line/text/choose outcome: Cancelled or a submitted Value.
type ValueResult struct {
	Cancelled bool
	Value     string
}

// RunLine runs the one-line input dialog and returns its outcome.
func RunLine(o LineOptions) (ValueResult, error) {
	m := newInputModel(o.Theme, o.Variant, o.Title, o.Prompt, o.Value, o.Placeholder, 1, o.Padding, o.Inset, true, o.Icon)
	return runValueModel(m)
}

// RunText runs the multi-line editor dialog and returns its outcome.
func RunText(o TextOptions) (ValueResult, error) {
	m := newInputModel(o.Theme, o.Variant, o.Title, o.Prompt, o.Value, "", o.Height, o.Padding, o.Inset, false, o.Icon)
	return runValueModel(m)
}

func runValueModel(m model) (ValueResult, error) {
	fm, err := runProgram(m)
	if err != nil {
		return ValueResult{}, err
	}
	res := fm.(model)
	if res.submitted && !res.quitting {
		return ValueResult{Value: res.fld.value()}, nil
	}
	return ValueResult{Cancelled: true}, nil
}

// MeasureLine returns the rendered height (lines) of the one-line dialog at
// o.Width — identical to `ai-playbook input --type line --measure`.
func MeasureLine(o LineOptions) int {
	m := newInputModel(o.Theme, o.Variant, o.Title, o.Prompt, o.Value, o.Placeholder, 1, o.Padding, o.Inset, true, o.Icon)
	m.width = o.Width
	m.resize()
	return measureHeight(m.render())
}

// MeasureText returns the rendered height (lines) of the multi-line dialog at
// o.Width — identical to `ai-playbook input --type text --measure`.
func MeasureText(o TextOptions) int {
	m := newInputModel(o.Theme, o.Variant, o.Title, o.Prompt, o.Value, "", o.Height, o.Padding, o.Inset, false, o.Icon)
	m.width = o.Width
	m.resize()
	return measureHeight(m.render())
}

// --- choose ------------------------------------------------------------------

// ChooseOptions configures a selection dialog. Multi allows multiple selections;
// Other, when non-empty, adds a free-text entry row.
type ChooseOptions struct {
	Theme   Theme
	Variant string
	Title   string
	Prompt  string
	Options []string
	Multi   bool
	Other   string
	Padding int
	Inset   int
	Width   int
}

// RunChoose runs the selection dialog and returns its outcome. For a multi
// selection Value is newline-joined (one selection per line).
func RunChoose(o ChooseOptions) (ValueResult, error) {
	fm, err := runProgram(newChooseModel(o.Theme, o.Variant, o.Title, o.Prompt, o.Options, o.Multi, o.Other, o.Padding, o.Inset))
	if err != nil {
		return ValueResult{}, err
	}
	res := fm.(model)
	if res.quitting || !res.submitted {
		return ValueResult{Cancelled: true}, nil
	}
	val := res.fld.value()
	if val == "" {
		return ValueResult{Cancelled: true}, nil
	}
	return ValueResult{Value: val}, nil
}

// MeasureChoose returns the rendered height (lines) of the selection dialog at
// o.Width — identical to `ai-playbook input --type choose --measure`.
func MeasureChoose(o ChooseOptions) int {
	m := newChooseModel(o.Theme, o.Variant, o.Title, o.Prompt, o.Options, o.Multi, o.Other, o.Padding, o.Inset)
	m.width = o.Width
	return measureHeight(m.render())
}

// --- form --------------------------------------------------------------------

// FormFieldSpec is one field of a form. Type is "line"|"text"|"confirm"|
// "choose"; unset per-type options fall back to the widget defaults.
type FormFieldSpec struct {
	Key    string
	Type   string
	Prompt string
	// line / text
	Value       string
	Placeholder string
	Height      int
	// choose
	Options []string
	Multi   bool
	Other   string
	// confirm
	Affirmative     string
	Negative        string
	DefaultNegative bool
}

// FormOptions configures a multi-field form.
type FormOptions struct {
	Theme   Theme
	Title   string
	Fields  []FormFieldSpec
	Padding int
	Inset   int
	Width   int
}

// FormPair is one submitted key/value of a form, in field order.
type FormPair struct {
	Key   string
	Value string
}

// FormResult is a form outcome: Cancelled or the submitted Pairs (field order).
type FormResult struct {
	Cancelled bool
	Pairs     []FormPair
}

// buildFormModelFromSpecs constructs a formModel directly from typed specs,
// reusing the widget constructors. It parallels newFormModel but supports the
// public "confirm" field type and typed per-field options (rather than the
// internal US/RS param string), and never touches the internal form path.
func buildFormModelFromSpecs(o FormOptions) formModel {
	specs := make([]formField, len(o.Fields))
	fields := make([]field, len(o.Fields))
	for i, fs := range o.Fields {
		label := fs.Prompt
		if label == "" {
			label = fs.Key
		}
		specs[i] = formField{name: fs.Key, ftype: fs.Type, label: label}
		fields[i] = buildFieldFromSpec(o.Theme, fs)
	}
	return formModel{
		theme:   o.Theme,
		title:   o.Title,
		specs:   specs,
		fields:  fields,
		focus:   0,
		width:   64,
		padding: o.Padding,
		inset:   o.Inset,
	}
}

func buildFieldFromSpec(t Theme, fs FormFieldSpec) field {
	switch fs.Type {
	case "text":
		h := fs.Height
		if h <= 0 {
			h = 4
		}
		return newTextField(t, fs.Value, fs.Placeholder, h, false)
	case "choose":
		return newChooseField(t, "default", fs.Options, fs.Multi, fs.Other)
	case "confirm":
		aff := fs.Affirmative
		if aff == "" {
			aff = "Yes"
		}
		neg := fs.Negative
		if neg == "" {
			neg = "No"
		}
		return newConfirmField(t, "default", aff, neg, fs.DefaultNegative)
	default: // "line" and any unknown fallback
		return newTextField(t, fs.Value, fs.Placeholder, 1, true)
	}
}

// RunForm runs the multi-field form and returns its outcome. It never exits the
// process or writes to stdout.
func RunForm(o FormOptions) (FormResult, error) {
	fm, err := runProgram(buildFormModelFromSpecs(o))
	if err != nil {
		return FormResult{}, err
	}
	res := fm.(formModel)
	if res.cancelled || !res.submitted {
		return FormResult{Cancelled: true}, nil
	}
	pairs := make([]FormPair, len(res.specs))
	for i, ff := range res.specs {
		pairs[i] = FormPair{Key: ff.name, Value: res.fields[i].value()}
	}
	return FormResult{Pairs: pairs}, nil
}

// MeasureForm returns the worst-case rendered height (lines) of the form across
// focus states at o.Width — identical to `ai-playbook input --type form --measure`.
func MeasureForm(o FormOptions) int {
	return buildFormModelFromSpecs(o).maxHeight(o.Width)
}
