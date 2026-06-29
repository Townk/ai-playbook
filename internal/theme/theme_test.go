package theme

import "testing"

func TestPaletteValues(t *testing.T) {
	cases := map[string]string{
		Blue: "#89b4fa", Green: "#a6e3a1", Mauve: "#cba6f7", Peach: "#fab387",
		Red: "#f38ba8", Overlay0: "#6c7086", Mantle: "#181825", Surface1: "#45475a",
		Base: "#1e1e2e", Text: "#cdd6f4", Surface0: "#313244", CodeBg: "#282C41",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("palette const = %q, want %q", got, want)
		}
	}
}

func TestDarken(t *testing.T) {
	// Darken scales toward black: f=0 → black, f=1 → unchanged.
	// %02X formatting means the result is upper-case hex.
	if got := Darken("#ffffff", 1.0); got != "#FFFFFF" {
		t.Errorf("Darken 1.0 = %q", got)
	}
	r, g, b := ParseHex(Darken("#a0a0a0", 0.5))
	if r != 0x50 || g != 0x50 || b != 0x50 {
		t.Errorf("Darken 0.5 of a0a0a0 = %02x%02x%02x, want 505050", r, g, b)
	}
}

func TestBgANSI(t *testing.T) {
	if got := BgANSI("#181825"); got != "\x1b[48;2;24;24;37m" {
		t.Errorf("BgANSI = %q", got)
	}
}
