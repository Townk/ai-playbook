package ui

import "testing"

func TestLangIcon(t *testing.T) {
	// Known canonical language: go.
	t.Run("known_go", func(t *testing.T) {
		glyph, color, ok := langIcon("go")
		if !ok {
			t.Fatal("langIcon(\"go\") ok=false, want true")
		}
		want := langIcons["go"]
		if glyph != want.glyph {
			t.Fatalf("glyph = %q, want %q", glyph, want.glyph)
		}
		if color != want.color {
			t.Fatalf("color = %q, want %q", color, want.color)
		}
	})

	// Alias: py → python.
	t.Run("alias_py", func(t *testing.T) {
		glyph, color, ok := langIcon("py")
		if !ok {
			t.Fatal("langIcon(\"py\") ok=false, want true")
		}
		want := langIcons["python"]
		if glyph != want.glyph {
			t.Fatalf("py glyph = %q, want python glyph %q", glyph, want.glyph)
		}
		if color != want.color {
			t.Fatalf("py color = %q, want python color %q", color, want.color)
		}
	})

	// bash has its own canonical entry (MiniIconsGreen).
	t.Run("bash_own_entry", func(t *testing.T) {
		glyph, color, ok := langIcon("bash")
		if !ok {
			t.Fatal("langIcon(\"bash\") ok=false, want true")
		}
		want := langIcons["bash"]
		if glyph != want.glyph {
			t.Fatalf("bash glyph = %q, want %q", glyph, want.glyph)
		}
		if color != want.color {
			t.Fatalf("bash color = %q, want %q", color, want.color)
		}
	})

	// Unknown language: ok must be false.
	t.Run("unknown_brainfuck", func(t *testing.T) {
		glyph, _, ok := langIcon("brainfuck")
		if ok {
			t.Fatal("langIcon(\"brainfuck\") ok=true, want false")
		}
		if glyph != "" {
			t.Fatalf("unknown glyph = %q, want empty", glyph)
		}
	})

	// Empty string: ok must be false.
	t.Run("empty", func(t *testing.T) {
		glyph, _, ok := langIcon("")
		if ok {
			t.Fatal("langIcon(\"\") ok=true, want false")
		}
		if glyph != "" {
			t.Fatalf("empty glyph = %q, want empty", glyph)
		}
	})

	// Case-insensitive: Go → go icon.
	t.Run("case_Go", func(t *testing.T) {
		glyph, _, ok := langIcon("Go")
		if !ok {
			t.Fatal("langIcon(\"Go\") ok=false, want true")
		}
		if glyph != langIcons["go"].glyph {
			t.Fatalf("Go glyph = %q, want %q", glyph, langIcons["go"].glyph)
		}
	})

	// Case-insensitive: PYTHON → python icon.
	t.Run("case_PYTHON", func(t *testing.T) {
		glyph, _, ok := langIcon("PYTHON")
		if !ok {
			t.Fatal("langIcon(\"PYTHON\") ok=false, want true")
		}
		if glyph != langIcons["python"].glyph {
			t.Fatalf("PYTHON glyph = %q, want %q", glyph, langIcons["python"].glyph)
		}
	})

	// diff has a mini.icons entry; patch is aliased to diff.
	t.Run("diff_entry", func(t *testing.T) {
		glyph, color, ok := langIcon("diff")
		if !ok {
			t.Fatal("langIcon(\"diff\") ok=false, want true")
		}
		want := langIcons["diff"]
		if glyph != want.glyph {
			t.Fatalf("diff glyph = %q, want %q", glyph, want.glyph)
		}
		if color != want.color {
			t.Fatalf("diff color = %q, want %q", color, want.color)
		}
	})

	t.Run("patch_alias_diff", func(t *testing.T) {
		g1, c1, _ := langIcon("diff")
		g2, c2, ok := langIcon("patch")
		if !ok {
			t.Fatal("langIcon(\"patch\") ok=false, want true")
		}
		if g2 != g1 {
			t.Fatalf("patch glyph = %q, want diff glyph %q", g2, g1)
		}
		if c2 != c1 {
			t.Fatalf("patch color = %q, want diff color %q", c2, c1)
		}
	})
}

func TestLangIconOrDefault(t *testing.T) {
	// Known language returns that language's icon.
	t.Run("python", func(t *testing.T) {
		g, c := langIconOrDefault("python")
		want := langIcons["python"]
		if g != want.glyph {
			t.Fatalf("python glyph = %q, want %q", g, want.glyph)
		}
		if c != want.color {
			t.Fatalf("python color = %q, want %q", c, want.color)
		}
	})

	// Unknown language returns an empty glyph (no icon) and colSubtext0.
	t.Run("unknown_returns_empty_glyph", func(t *testing.T) {
		g, c := langIconOrDefault("brainfuck")
		if g != "" {
			t.Fatalf("unknown glyph = %q, want empty string", g)
		}
		if c != colSubtext0 {
			t.Fatalf("unknown color = %q, want %q", c, colSubtext0)
		}
	})
}
