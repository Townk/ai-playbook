package askcli

import (
	"fmt"

	"github.com/Townk/ai-playbook/pkg/dialog"
)

func runLineCmd(args []string) int {
	fs := newFlagSet("line")
	c := registerCommon(fs)
	var value, placeholder, icon string
	fs.StringVar(&value, "value", "", "initial value")
	fs.StringVar(&placeholder, "placeholder", "", "placeholder text")
	fs.StringVar(&icon, "icon", "", "prompt-column glyph override")
	pos, code, done := parse(fs, args)
	if done {
		return code
	}

	o := dialog.LineOptions{
		Theme:       *c.theme,
		Variant:     "default",
		Title:       c.title,
		Prompt:      firstArg(pos),
		Value:       value,
		Placeholder: placeholder,
		Icon:        icon,
		Padding:     c.padding,
		Inset:       c.inset,
		Width:       c.width,
	}

	if c.measure {
		fmt.Println(dialog.MeasureLine(o))
		return exitOK
	}
	if !hasTTY() {
		return noTTY("line")
	}
	out, err := runWidget(widgetInvocation{kind: "line", line: o})
	if err != nil {
		return widgetErr("line", err)
	}
	if out.cancelled {
		return exitCancel
	}
	fmt.Println(out.value)
	return exitOK
}
