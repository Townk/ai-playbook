package askcli

import (
	"fmt"

	"github.com/Townk/ai-playbook/internal/input"
)

func runConfirmCmd(args []string) int {
	fs := newFlagSet("confirm")
	c := registerCommon(fs)
	var danger, warning, doPrint bool
	var affirmative, negative, defaultSide string
	fs.BoolVar(&danger, "danger", false, "danger variant (forces default=negative)")
	fs.BoolVar(&warning, "warning", false, "warning variant")
	fs.StringVar(&affirmative, "affirmative", "Yes", "affirmative button label")
	fs.StringVar(&negative, "negative", "No", "negative button label")
	fs.StringVar(&defaultSide, "default", "affirmative", "default focus: affirmative|negative")
	fs.BoolVar(&doPrint, "print", false, "also print yes/no to stdout")
	pos, code, done := parse(fs, args)
	if done {
		return code
	}

	variant := "default"
	if warning {
		variant = "warning"
	}
	if danger {
		variant = "danger"
	}
	defaultNegative := defaultSide == "negative"
	if variant == "danger" {
		defaultNegative = true // never default to a destructive action
	}

	o := input.ConfirmOptions{
		Theme:           *c.theme,
		Variant:         variant,
		Title:           c.title,
		Prompt:          firstArg(pos),
		Affirmative:     affirmative,
		Negative:        negative,
		DefaultNegative: defaultNegative,
		Padding:         c.padding,
		Inset:           c.inset,
		Width:           c.width,
	}

	if c.measure {
		fmt.Println(input.MeasureConfirm(o))
		return exitOK
	}
	if !hasTTY() {
		return noTTY("confirm")
	}
	out, err := runWidget(widgetInvocation{kind: "confirm", confirm: o})
	if err != nil {
		return widgetErr("confirm", err)
	}
	if out.cancelled {
		return exitCancel
	}
	if out.confirm == "yes" {
		if doPrint {
			fmt.Println("yes")
		}
		return exitOK
	}
	if doPrint {
		fmt.Println("no")
	}
	return exitNegative
}
