package askcli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/Townk/ai-playbook/internal/input"
)

// jsonFormField is the public JSON form-spec field shape (an array of these).
type jsonFormField struct {
	Type   string `json:"type"`
	Key    string `json:"key"`
	Prompt string `json:"prompt"`
	// line / text
	Value       string `json:"value"`
	Placeholder string `json:"placeholder"`
	Height      int    `json:"height"`
	// choose
	Options []string `json:"options"`
	Multi   bool     `json:"multi"`
	Other   string   `json:"other"`
	// confirm
	Affirmative string `json:"affirmative"`
	Negative    string `json:"negative"`
	Default     string `json:"default"` // affirmative|negative
}

// parseJSONFormSpec parses the public JSON form spec into typed field specs.
// It is strict — unknown field keys, trailing data after the array, and an
// unrecognized "default" value are all errors (cheap to reject before ship,
// breaking to start rejecting after). Errors are single-line (with a byte
// position for malformed JSON).
func parseJSONFormSpec(raw []byte) ([]input.FormFieldSpec, error) {
	var fields []jsonFormField
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&fields); err != nil {
		if se, ok := err.(*json.SyntaxError); ok {
			return nil, fmt.Errorf("invalid JSON spec at byte %d: %v", se.Offset, err)
		}
		return nil, fmt.Errorf("invalid JSON spec: %v", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("unexpected data after the JSON spec array")
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("spec has no fields")
	}
	out := make([]input.FormFieldSpec, len(fields))
	for i, f := range fields {
		if f.Key == "" {
			return nil, fmt.Errorf("field %d: missing \"key\"", i)
		}
		switch f.Type {
		case "line", "text", "confirm", "choose":
		case "":
			return nil, fmt.Errorf("field %q: missing \"type\"", f.Key)
		default:
			return nil, fmt.Errorf("field %q: unsupported type %q (use line, text, confirm, or choose)", f.Key, f.Type)
		}
		switch f.Default {
		case "", "affirmative", "negative":
		default:
			return nil, fmt.Errorf("field %q: invalid default %q (use affirmative or negative)", f.Key, f.Default)
		}
		out[i] = input.FormFieldSpec{
			Key:             f.Key,
			Type:            f.Type,
			Prompt:          f.Prompt,
			Value:           f.Value,
			Placeholder:     f.Placeholder,
			Height:          f.Height,
			Options:         f.Options,
			Multi:           f.Multi,
			Other:           f.Other,
			Affirmative:     f.Affirmative,
			Negative:        f.Negative,
			DefaultNegative: f.Default == "negative",
		}
	}
	return out, nil
}

// readFormSpec returns the raw spec bytes from the --spec file, or from stdin
// when path is empty.
func readFormSpec(path string) ([]byte, error) {
	if path != "" {
		return os.ReadFile(path)
	}
	return io.ReadAll(os.Stdin)
}

func runFormCmd(args []string) int {
	fs := newFlagSet("form")
	c := registerCommon(fs)
	var spec string
	var asJSON bool
	fs.StringVar(&spec, "spec", "", "JSON spec file; omit to read stdin")
	fs.BoolVar(&asJSON, "json", false, "emit one JSON object instead of key=value lines")
	if _, code, done := parse(fs, args); done {
		return code
	}

	raw, err := readFormSpec(spec)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ask form: %v\n", err)
		return exitUsage
	}
	fields, err := parseJSONFormSpec(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ask form: %v\n", err)
		return exitUsage
	}

	o := input.FormOptions{
		Theme:   *c.theme,
		Title:   c.title,
		Fields:  fields,
		Padding: c.padding,
		Inset:   c.inset,
		Width:   c.width,
	}

	if c.measure {
		fmt.Println(input.MeasureForm(o))
		return exitOK
	}
	if !hasTTY() {
		return noTTY("form")
	}
	out, err := runWidget(widgetInvocation{kind: "form", form: o})
	if err != nil {
		return widgetErr("form", err)
	}
	if out.cancelled {
		return exitCancel
	}

	if asJSON {
		obj := make(map[string]string, len(out.formPairs))
		for _, p := range out.formPairs {
			obj[p.Key] = p.Value
		}
		b, err := json.Marshal(obj)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ask form: %v\n", err)
			return exitNegative
		}
		fmt.Println(string(b))
		return exitOK
	}
	for _, p := range out.formPairs {
		fmt.Printf("%s=%s\n", p.Key, shellQuote(p.Value))
	}
	return exitOK
}
