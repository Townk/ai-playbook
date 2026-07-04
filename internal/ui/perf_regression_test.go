package ui

import (
	"reflect"
	"strings"
	"testing"
)

// spinnerRow returns the first View line containing the run-spinner phrase, or "".
func spinnerRow(view string) string {
	for _, ln := range strings.Split(view, "\n") {
		if strings.Contains(strip(ln), "running…") {
			return ln
		}
	}
	return ""
}

// TestSpinTickDoesNotReflow pins the three B1c guarantees: a spinTick with a running
// block (1) causes ZERO Render calls, (2) still advances the spinner glyph at View
// time, and (3) leaves every button hitbox (Line/Col/Width/Kind/BlockID) unchanged.
func TestSpinTickDoesNotReflow(t *testing.T) {
	m := newModel("T", "```bash {id=a}\ntrue\n```\n")
	m.width, m.height = 80, 24
	m.blockStates = map[string]blockRunState{"a": {Status: "running"}}
	m.reflow() // emits + tags the spinner line (SpinID=="a")

	// The spinner line must be tagged so the View can regenerate it without a reflow.
	tagged := false
	for _, ln := range m.lines {
		if ln.SpinID == "a" && ln.SpinLabel == "running…" {
			tagged = true
		}
	}
	if !tagged {
		t.Fatal("no run-region spinner line tagged with SpinID=\"a\"")
	}

	view0 := m.View().Content
	row0 := spinnerRow(view0)
	if row0 == "" {
		t.Fatal("spinner row not present in View before tick")
	}
	btnsBefore := append([]Button(nil), m.buttons...)

	// (1) zero Render calls across the spin tick.
	before := renderCalls.Load()
	m = mustModel(m.Update(spinTickMsg{gen: m.tickGen}))
	if got := renderCalls.Load(); got != before {
		t.Errorf("spin tick triggered %d Render call(s); want 0", got-before)
	}

	// (2) the glyph advanced: SpinFrame bumped and the rendered spinner row differs.
	if m.blockStates["a"].SpinFrame != 1 {
		t.Errorf("SpinFrame=%d after one tick, want 1", m.blockStates["a"].SpinFrame)
	}
	row1 := spinnerRow(m.View().Content)
	if row1 == row0 {
		t.Errorf("spinner row did not advance across a tick:\n%q", row0)
	}
	if !strings.ContainsRune(strip(row1), spinnerFrames[1]) {
		t.Errorf("frame-1 spinner glyph %q missing from row: %q", string(spinnerFrames[1]), strip(row1))
	}

	// (3) button hitboxes unchanged (no reflow → geometry cannot shift).
	if !reflect.DeepEqual(btnsBefore, m.buttons) {
		t.Errorf("button hitboxes changed across a spin tick:\n before=%+v\n after =%+v", btnsBefore, m.buttons)
	}
}

// TestCountBlocksMatchesRender pins the B12 single-source contract: countBlocks
// (cheap goldmark parse, no styling) must return the SAME number as the length of
// Render's Block list for every document — that count is what isValidPlaybook keys
// on, so the two must never disagree. The corpus exercises the classification
// branches (shell/run/diff/create/static), the normalizeFences repair path, indented
// code blocks, and code nested in a list/quote (which Render does NOT enumerate, so
// countBlocks must not either).
func TestCountBlocksMatchesRender(t *testing.T) {
	corpus := []string{
		"",
		"# Title\n\nJust prose, no blocks.\n",
		"# Title\n\n```go {id=a}\nfunc main() {}\n```\n\n```python {id=b}\nprint('x')\n```\n\n```\nplain\n```\n",
		"# T\n\n```bash {id=s}\nls\n```\n\n```diff {id=d}\n- a\n+ b\n```\n\n```text {id=c file=x.txt}\nhi\n```\n",
		"# T\n\n```json {static}\n{\"k\":1}\n```\n\n```\nno lang\n```\n",
		// Malformed closing fence — normalizeFences repairs it; both paths must agree.
		"# T\n\n```go {id=a}\nfmt.Println(1)\n```trailing text on the closer\n\nmore prose\n",
		// Indented (4-space) code block → a top-level *ast.CodeBlock.
		"# T\n\n    indented code block\n    second line\n\nprose\n",
		// Code fenced INSIDE a list item and a blockquote — Render walks these for
		// prose only, so neither becomes a Block.
		"# T\n\n- item with code:\n\n  ```go\n  nested()\n  ```\n\n> ```go\n> quoted()\n> ```\n",
	}
	for i, md := range corpus {
		_, _, blocks := Render(md, 80, RenderOpts{})
		if got := countBlocks(md); got != len(blocks) {
			t.Errorf("corpus[%d]: countBlocks=%d, want len(Render blocks)=%d\n---\n%s", i, got, len(blocks), md)
		}
	}
}

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
