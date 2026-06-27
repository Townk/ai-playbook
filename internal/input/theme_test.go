package input

import (
	"flag"
	"testing"
)

func TestThemeDefaults(t *testing.T) {
	d := defaultTheme()
	if d.Border != "#89b4fa" || d.Danger != "#ff5555" || d.Accent != "#cba6f7" {
		t.Fatalf("unexpected defaults: %+v", d)
	}
}

func TestThemeFlagOverride(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	th := registerThemeFlags(fs)
	if err := fs.Parse([]string{"--theme-border", "#abcdef"}); err != nil {
		t.Fatal(err)
	}
	if th.Border != "#abcdef" {
		t.Fatalf("override failed: got %q", th.Border)
	}
	if th.Danger != "#ff5555" {
		t.Fatalf("unset token must keep default, got %q", th.Danger)
	}
}

func TestVariantColors(t *testing.T) {
	d := defaultTheme()
	for v, want := range map[string]string{"default": d.Border, "danger": d.Danger, "warning": d.Warning} {
		if got := d.variantColor(v); got != want {
			t.Fatalf("variantColor(%q)=%q want %q", v, got, want)
		}
	}
	if d.titleColor("default") != d.Accent {
		t.Fatal("default title must be the accent (mauve)")
	}
	if d.titleColor("danger") != d.Danger || d.titleColor("warning") != d.Warning {
		t.Fatal("danger/warning title must follow the variant color")
	}
}
