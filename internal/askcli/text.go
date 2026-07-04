package askcli

import (
	"fmt"

	"github.com/Townk/ai-playbook/internal/input"
)

func runTextCmd(args []string) int {
	fs := newFlagSet("text")
	c := registerCommon(fs)
	var value, icon string
	var height int
	fs.StringVar(&value, "value", "", "initial value")
	fs.IntVar(&height, "height", 10, "editor viewport rows")
	fs.StringVar(&icon, "icon", "", "prompt-column glyph override")
	pos, code, done := parse(fs, args)
	if done {
		return code
	}

	o := input.TextOptions{
		Theme:   *c.theme,
		Variant: "default",
		Title:   c.title,
		Prompt:  firstArg(pos),
		Value:   value,
		Icon:    icon,
		Height:  height,
		Padding: c.padding,
		Inset:   c.inset,
		Width:   c.width,
	}

	if c.measure {
		fmt.Println(input.MeasureText(o))
		return exitOK
	}
	if !hasTTY() {
		return noTTY("text")
	}
	out, err := runWidget(widgetInvocation{kind: "text", text: o})
	if err != nil {
		return widgetErr("text", err)
	}
	if out.cancelled {
		return exitCancel
	}
	fmt.Println(out.value)
	return exitOK
}
