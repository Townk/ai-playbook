package ui

import "testing"

func TestLineZeroValue(t *testing.T) {
	l := Line{Text: "hello", Wide: true}
	if l.Text != "hello" || !l.Wide {
		t.Fatalf("Line fields not set: %+v", l)
	}
}
