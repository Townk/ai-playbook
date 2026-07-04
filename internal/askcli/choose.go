package askcli

import (
	"fmt"

	"github.com/Townk/ai-playbook/internal/input"
)

func runChooseCmd(args []string) int {
	fs := newFlagSet("choose")
	c := registerCommon(fs)
	var multi bool
	var other string
	fs.BoolVar(&multi, "multi", false, "allow multiple selections")
	fs.StringVar(&other, "other", "", "enable a free-text entry row with this label")
	pos, code, done := parse(fs, args)
	if done {
		return code
	}

	// First positional is the prompt; the rest are the selectable items.
	var prompt string
	var items []string
	if len(pos) > 0 {
		prompt = pos[0]
		items = pos[1:]
	}

	o := input.ChooseOptions{
		Theme:   *c.theme,
		Variant: "default",
		Title:   c.title,
		Prompt:  prompt,
		Options: items,
		Multi:   multi,
		Other:   other,
		Padding: c.padding,
		Inset:   c.inset,
		Width:   c.width,
	}

	if c.measure {
		fmt.Println(input.MeasureChoose(o))
		return exitOK
	}
	if !hasTTY() {
		return noTTY("choose")
	}
	out, err := runWidget(widgetInvocation{kind: "choose", choose: o})
	if err != nil {
		return widgetErr("choose", err)
	}
	if out.cancelled {
		return exitCancel
	}
	// A multi selection arrives newline-joined, so Println yields one per line;
	// a single selection is one line.
	fmt.Println(out.value)
	return exitOK
}
