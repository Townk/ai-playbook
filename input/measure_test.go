package input

import (
	"strings"
	"testing"
)

func TestMeasureHeight(t *testing.T) {
	cases := []struct {
		name string
		view string
		want int
	}{
		{"single line", "hello", 1},
		{"two lines", "hello\nworld", 2},
		{"three lines", "a\nb\nc", 3},
	}
	for _, c := range cases {
		if got := measureHeight(c.view); got != c.want {
			t.Fatalf("%s: measureHeight=%d want %d", c.name, got, c.want)
		}
	}
}

func TestMeasureMatchesRenderedLines(t *testing.T) {
	th := defaultTheme()
	cases := []struct {
		name string
		view string
	}{
		{
			"confirm",
			func() string {
				m := newConfirmModel(th, "default", "Title", "Prompt?", "Yes", "No", false, 1, 1)
				m.width = 50
				return m.render()
			}(),
		},
		{
			"choose",
			func() string {
				m := newChooseModel(th, "default", "Title", "Pick", []string{"a", "b", "c"}, false, "", 1, 1)
				m.width = 50
				return m.render()
			}(),
		},
	}
	for _, c := range cases {
		want := len(strings.Split(strip(c.view), "\n"))
		if got := measureHeight(strip(c.view)); got != want {
			t.Fatalf("%s: measureHeight=%d want %d", c.name, got, want)
		}
	}
}
