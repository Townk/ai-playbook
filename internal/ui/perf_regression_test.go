package ui

import "testing"

// TestRenderSameDocTwiceIdentical pins the memoization contract for B1a/B1b: the
// hoisted goldmark instance and the highlight cache must leave render output
// byte-for-byte identical across repeated renders of the same document. Passes
// before and after the optimization.
func TestRenderSameDocTwiceIdentical(t *testing.T) {
	md := "# Title\n\nSome prose that wraps a little.\n\n" +
		"```go {id=a}\nfunc main() { println(\"a fairly wide line of code goes here\") }\n```\n\n" +
		"```python {id=b}\nprint('hello world')\n```\n\n" +
		"```\nplain fenced block, no language\n```\n"
	l1, b1, blk1 := Render(md, 80, RenderOpts{})
	l2, b2, blk2 := Render(md, 80, RenderOpts{})
	if len(l1) != len(l2) {
		t.Fatalf("line count differs across renders: %d vs %d", len(l1), len(l2))
	}
	for i := range l1 {
		if l1[i].Text != l2[i].Text || l1[i].Wide != l2[i].Wide || l1[i].Bg != l2[i].Bg {
			t.Fatalf("line %d differs across renders:\n a=%q\n b=%q", i, l1[i].Text, l2[i].Text)
		}
	}
	if len(b1) != len(b2) || len(blk1) != len(blk2) {
		t.Fatalf("button/block counts differ: buttons %d vs %d, blocks %d vs %d",
			len(b1), len(b2), len(blk1), len(blk2))
	}
}

// TestReflowCachesMaxWide pins the B2 cache-coherence contract: after a reflow,
// m.maxWide must equal a fresh MaxWideWidth(m.lines) walk, so clampScroll and the
// $/end handlers can read the cached value instead of re-walking every line.
func TestReflowCachesMaxWide(t *testing.T) {
	md := "# Title\n\n```go {id=a}\nfunc main() { println(\"a deliberately wide line of code to force horizontal overflow past the pane\") }\n```\n"
	m := newModel("T", md)
	m.width, m.height = 80, 24
	m.reflow()
	if want := MaxWideWidth(m.lines); m.maxWide != want {
		t.Fatalf("m.maxWide = %d, want MaxWideWidth(m.lines) = %d", m.maxWide, want)
	}
}
