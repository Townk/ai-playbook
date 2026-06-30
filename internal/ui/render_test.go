package ui

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// fakeAnswerRegen wires a no-op cached-answer regenerate seam so canRegenerate is
// true (the badge button + reload glyph render) without standing up an orchestrator.
// Used by the cachedBadge render/registration tests, which only care that the reload
// control renders — not which regenerate path runs on click.
func fakeAnswerRegen() func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("")), nil }
}

func joinText(lines []Line) string {
	parts := make([]string, len(lines))
	for i, l := range lines {
		parts[i] = strip(l.Text)
	}
	return strings.Join(parts, "\n")
}

func TestRenderHeadingHasBlockPrefix(t *testing.T) {
	lines, _, _ := Render("# Title", 40, nil, "")
	if !strings.Contains(joinText(lines), "▓▓▓ Title") {
		t.Fatalf("heading missing ▓▓▓ prefix:\n%s", joinText(lines))
	}
	for _, l := range lines {
		if l.Wide {
			t.Fatalf("heading line should not be Wide")
		}
	}
}

func TestRenderParagraphWraps(t *testing.T) {
	md := "alpha beta gamma delta epsilon zeta eta theta iota kappa"
	lines, _, _ := Render(md, 20, nil, "")
	for _, l := range lines {
		if l.Wide {
			t.Fatalf("paragraph line should not be Wide")
		}
		if w := len(strip(l.Text)); w > 20 {
			t.Fatalf("paragraph line %q exceeds width 20 (got %d)", strip(l.Text), w)
		}
	}
	if len(lines) < 2 {
		t.Fatalf("expected the paragraph to wrap to multiple lines, got %d", len(lines))
	}
}

func TestRenderListItems(t *testing.T) {
	lines, _, _ := Render("- one\n- two", 40, nil, "")
	got := joinText(lines)
	if !strings.Contains(got, "one") || !strings.Contains(got, "two") {
		t.Fatalf("list items missing:\n%s", got)
	}
	if !strings.Contains(got, "• one") {
		t.Fatalf("bullet marker missing for first item:\n%s", got)
	}
}

func TestRenderOrderedList(t *testing.T) {
	lines, _, _ := Render("1. first\n2. second", 40, nil, "")
	got := joinText(lines)
	if !strings.Contains(got, "1. first") {
		t.Fatalf("ordered list item 1 missing:\n%s", got)
	}
	if !strings.Contains(got, "2. second") {
		t.Fatalf("ordered list item 2 missing:\n%s", got)
	}
}

func TestRenderNestedList(t *testing.T) {
	lines, _, _ := Render("- a\n    - b", 40, nil, "")
	got := joinText(lines)
	if !strings.Contains(got, "a") || !strings.Contains(got, "b") {
		t.Fatalf("nested list items missing:\n%s", got)
	}
	// Find the indentation of the line containing "a" and "b" and assert that
	// the nested item "b" is more indented than the parent item "a".
	indentOf := func(needle string) int {
		for _, l := range lines {
			plain := strip(l.Text)
			if strings.Contains(plain, needle) {
				return len(plain) - len(strings.TrimLeft(plain, " "))
			}
		}
		return -1
	}
	indentA := indentOf("a")
	indentB := indentOf("b")
	if indentA < 0 || indentB < 0 {
		t.Fatalf("could not locate 'a' or 'b' in output:\n%s", got)
	}
	if indentB <= indentA {
		t.Fatalf("nested item 'b' (indent %d) is not more indented than 'a' (indent %d):\n%s", indentB, indentA, got)
	}
}

func TestRenderInlineStrongText(t *testing.T) {
	// The bold word's text survives (styling is stripped in the assertion).
	lines, _, _ := Render("a **bold** word", 40, nil, "")
	got := joinText(lines)
	if !strings.Contains(got, "bold") {
		t.Fatalf("strong text missing:\n%s", got)
	}
}

func TestRenderCodeBlockIsWideAndUnwrapped(t *testing.T) {
	long := "x := aaaaaaaaaa + bbbbbbbbbb + cccccccccc + dddddddddd // long line"
	md := "```go\n" + long + "\n```"
	lines, _, _ := Render(md, 20, nil, "") // pane narrower than the code line
	var codeLine *Line
	for i := range lines {
		if lines[i].Wide {
			codeLine = &lines[i]
			break
		}
	}
	if codeLine == nil {
		t.Fatalf("expected a Wide code line, got none:\n%s", joinText(lines))
	}
	if w := len(strip(codeLine.Text)); w <= 20 {
		t.Fatalf("code line was wrapped/truncated to width (len=%d); it must keep natural width", w)
	}
	if !strings.Contains(codeLine.Text, "\x1b[") {
		t.Fatalf("code line is not styled (no ANSI): %q", codeLine.Text)
	}
}

func TestRenderBlockQuote(t *testing.T) {
	lines, _, _ := Render("> hello quote", 40, nil, "")
	got := joinText(lines)
	if !strings.Contains(got, "hello quote") {
		t.Fatalf("quote text missing:\n%s", got)
	}
	// bordered frame: content rows use ▐ as the left bar
	if !strings.Contains(got, "▐") {
		t.Fatalf("quote left-bar glyph missing:\n%s", got)
	}
}

func TestRenderQuoteDefaultAdmonition(t *testing.T) {
	lines, _, _ := Render("> hello there friend", 40, nil, "")
	// bordered frame: lines[0] is the top border (🬞🬭…), lines[1] is the body row.
	first := strip(lines[0].Text)
	if !strings.HasPrefix(first, "🬞") {
		t.Fatalf("first line should be the top border starting with '🬞', got: %q", first)
	}
	// lines[1] is the body row: starts with ▐ (left bar) + space + body text.
	body := strip(lines[1].Text)
	if !strings.HasPrefix(body, "▐ ") {
		t.Fatalf("second line should be a body line starting with '▐ ', got: %q", body)
	}
	if !strings.Contains(body, "hello") {
		t.Fatalf("body line should contain the body text, got: %q", body)
	}
	// no line should contain the word "Quote" (no title header for bare quote)
	for _, l := range lines {
		if strings.Contains(strip(l.Text), "Quote") {
			t.Fatalf("bare quote should have no 'Quote' title header, but found it in: %q", strip(l.Text))
		}
		if l.Wide {
			t.Fatalf("quote line should not be Wide")
		}
	}
}

func TestRenderQuoteAdmonitionType(t *testing.T) {
	lines, _, _ := Render("> [!note]\n> be careful here", 40, nil, "")
	// bordered frame: lines[0] is the top border; lines[1] is the header row.
	topBorder := strip(lines[0].Text)
	if !strings.HasPrefix(topBorder, "🬞") {
		t.Fatalf("expected top border starting with '🬞', got %q", topBorder)
	}
	hdr := strip(lines[1].Text)
	if !strings.Contains(hdr, "Note") {
		t.Fatalf("expected Note header at lines[1], got %q", hdr)
	}
	body := joinText(lines)
	if strings.Contains(body, "[!note]") {
		t.Fatalf("admonition marker leaked into the body:\n%s", body)
	}
	if !strings.Contains(body, "be careful here") {
		t.Fatalf("body text missing:\n%s", body)
	}
}

func TestRenderQuoteExplicitQuoteType(t *testing.T) {
	lines, _, _ := Render("> [!quote]\n> some quoted body", 40, nil, "")
	// bordered frame: lines[0] is the top border; lines[1] is the header row.
	topBorder := strip(lines[0].Text)
	if !strings.HasPrefix(topBorder, "🬞") {
		t.Fatalf("[!quote] expected top border starting with '🬞', got %q", topBorder)
	}
	hdr := strip(lines[1].Text)
	if !strings.Contains(hdr, "Quote") {
		t.Fatalf("[!quote] header missing 'Quote' title: %q", hdr)
	}
	if !strings.Contains(hdr, "󱆨") {
		t.Fatalf("[!quote] header missing 󱆨 icon: %q", hdr)
	}
	// body present
	body := joinText(lines)
	if !strings.Contains(body, "some quoted body") {
		t.Fatalf("[!quote] body text missing:\n%s", body)
	}
}

func TestDarken(t *testing.T) {
	got := darken("#FFFFFF", 0.20)
	if got != "#333333" {
		t.Fatalf("darken(#FFFFFF, 0.20) = %q, want #333333", got)
	}
	// darken #89b4fa by 0.20 — all components must be less than original
	origR, origG, origB := parseHex("#89b4fa")
	dr, dg, db := parseHex(darken("#89b4fa", 0.20))
	if dr >= origR || dg >= origG || db >= origB {
		t.Fatalf("darken(#89b4fa, 0.20) = %q; expected all components < originals (%d,%d,%d)", darken("#89b4fa", 0.20), origR, origG, origB)
	}
}

func TestBandFillsWidthWithBg(t *testing.T) {
	bg := "\x1b[48;2;1;1;1m"
	result := band("x", bg, 10)
	if !strings.HasPrefix(result, bg) {
		t.Fatalf("band result does not start with bg sequence: %q", result)
	}
	if !strings.HasSuffix(result, "\x1b[0m") {
		t.Fatalf("band result does not end with reset: %q", result)
	}
	if w := lipgloss.Width(result); w != 10 {
		t.Fatalf("band width = %d, want 10", w)
	}
}

func TestQuote_BorderedFrame(t *testing.T) {
	lines, _, _ := Render("> [!NOTE]\n> Hello world\n", 30, nil, "")
	joined := joinText(lines)
	for _, want := range []string{"🬞", "🬭", "▐", "🬁", "🬂"} {
		if !strings.Contains(joined, want) {
			t.Errorf("callout missing frame glyph %q:\n%s", want, joined)
		}
	}
	// content text sits 1 space after the left bar
	if !strings.Contains(joined, "▐ ") {
		t.Errorf("content not 1 space off the left bar:\n%s", joined)
	}
	// callout must have no right border glyph
	if strings.ContainsAny(joined, "▌▕") {
		t.Errorf("callout must have no right border: %q", joined)
	}
}

func TestQuote_BareBlockquoteFallback(t *testing.T) {
	lines, _, _ := Render("> just a quote\n", 30, nil, "")
	joined := joinText(lines)
	// bare blockquote is framed with the fallback accent — ▐ left bar present, no header title
	if !strings.Contains(joined, "▐") {
		t.Errorf("bare blockquote not framed:\n%s", joined)
	}
	// bare blockquote must also have the top/bottom border glyphs
	if !strings.Contains(joined, "🬞") {
		t.Errorf("bare blockquote missing top-left corner 🬞:\n%s", joined)
	}
	if !strings.Contains(joined, "🬁") {
		t.Errorf("bare blockquote missing bottom-left corner 🬁:\n%s", joined)
	}
}

func TestRenderQuoteReflowsToWidth(t *testing.T) {
	long := "> " + strings.Repeat("word ", 40)
	reflowed, _, _ := Render(long, 30, nil, "")
	for _, l := range reflowed {
		if w := lipgloss.Width(l.Text); w > 30 {
			t.Fatalf("quote line exceeds content width 30 (got %d): %q", w, strip(l.Text))
		}
	}
}

func TestRenderCodeBlockNamedLanguageHighlights(t *testing.T) {
	md := "```go\npackage main\n```"
	lines, _, _ := Render(md, 80, nil, "")
	var codeLine *Line
	for i := range lines {
		if lines[i].Wide {
			codeLine = &lines[i]
			break
		}
	}
	if codeLine == nil {
		t.Fatalf("expected a Wide code line, got none:\n%s", joinText(lines))
	}
	if !strings.Contains(codeLine.Text, "\x1b[") {
		t.Fatalf("named language 'go' was not highlighted (no ANSI escape in output): %q", codeLine.Text)
	}
}

func TestRenderCodeBlockUnknownLanguageNoPanic(t *testing.T) {
	md := "```unknown_xyz\nhello world\n```"
	lines, _, _ := Render(md, 80, nil, "")
	var codeLine *Line
	for i := range lines {
		if lines[i].Wide {
			codeLine = &lines[i]
			break
		}
	}
	if codeLine == nil {
		t.Fatalf("expected a Wide code line, got none:\n%s", joinText(lines))
	}
	if !strings.Contains(strip(codeLine.Text), "hello world") {
		t.Fatalf("code text missing from unknown-language block:\n%s", joinText(lines))
	}
}

func TestRenderTableIsWide(t *testing.T) {
	md := "| Col A | Col B |\n|---|---|\n| one | two |\n| three | four |"
	lines, _, _ := Render(md, 12, nil, "")
	wide := false
	for _, l := range lines {
		if l.Wide {
			wide = true
		}
	}
	if !wide {
		t.Fatalf("table produced no Wide lines:\n%s", joinText(lines))
	}
	if !strings.Contains(joinText(lines), "Col A") || !strings.Contains(joinText(lines), "four") {
		t.Fatalf("table cells missing:\n%s", joinText(lines))
	}
	if strings.Contains(joinText(lines), "---") {
		t.Fatalf("table separator row leaked into output:\n%s", joinText(lines))
	}
}

func TestStripRemovesFullSGR(t *testing.T) {
	in := "a\x1b[1mbold\x1b[0m b\x1b[38;2;1;2;3mc\x1b[0m"
	if got := strip(in); got != "abold bc" {
		t.Fatalf("strip = %q, want %q", got, "abold bc")
	}
}

func TestRenderCodeBlockBackgroundStretchesAndSurvives(t *testing.T) {
	lines, _, _ := Render("```go\nx := 1\n```", 40, nil, "")
	var code *Line
	for i := range lines {
		if lines[i].Wide {
			code = &lines[i]
			break
		}
	}
	if code == nil {
		t.Fatal("no wide code line")
	}
	// bg is now carried in the Bg field, not baked into Text
	if code.Bg != codeBgANSI {
		t.Fatalf("code line Bg = %q, want codeBgANSI", code.Bg)
	}
	// Text must NOT already contain a background sequence (it's fg-only)
	if strings.Contains(code.Text, "48;2") {
		t.Fatalf("code line Text contains a background sequence; it should be fg-only: %q", code.Text)
	}
	// render through the viewport: backdrop must fill the full viewport width
	out := Window([]Line{*code}, 0, 0, 40, 1)[0]
	if !strings.HasPrefix(out, codeBgANSI) {
		t.Fatalf("viewport output does not open with bg sequence")
	}
	if w := lipgloss.Width(out); w != 40 {
		t.Fatalf("viewport output width = %d, want 40", w)
	}
	// every "\x1b[0m" inside (except the trailing one) must be followed by the bg re-apply
	inner := strings.TrimSuffix(out, "\x1b[0m")
	if strings.Contains(inner, "\x1b[0m") && !strings.Contains(inner, "\x1b[0m"+codeBgANSI) {
		t.Fatalf("a reset is not followed by a bg re-apply (bg would drop): %q", out)
	}
}

func TestRenderCodeBlockLanguageLabel(t *testing.T) {
	// Use a language not in the icon map — the default devicon (U+F07E2) will appear
	// alongside the label to exercise the fallback path.
	lines, _, _ := Render("```brainfuck\nx := 1\n```", 40, nil, "")
	// first line is the top-label bar, Wide=false.
	if lines[0].Wide {
		t.Fatal("label line should not be Wide")
	}
	got := strip(lines[0].Text)
	// The language name must appear in the label region.
	if !strings.Contains(got, "brainfuck") {
		t.Fatalf("label line %q should contain the language name %q", got, "brainfuck")
	}
	// Left fill contains the ▂ bar character.
	if !strings.Contains(got, "▂") {
		t.Fatalf("label line %q should contain '▂' fill", got)
	}
	// Total display width must be exactly the content width (40).
	if w := lipgloss.Width(lines[0].Text); w != 40 {
		t.Fatalf("label line display width = %d, want 40", w)
	}
}

func TestRenderCodeBlockIconLabel(t *testing.T) {
	// Use 'go' which has an icon — verify the tab shows the glyph and ▂ fill.
	lines, _, _ := Render("```go\nx := 1\n```", 40, nil, "")
	if lines[0].Wide {
		t.Fatal("icon label line should not be Wide")
	}
	got := strip(lines[0].Text)
	// The icon glyph for go must appear in the label.
	goGlyph := langIcons["go"].glyph
	if !strings.Contains(got, goGlyph) {
		t.Fatalf("icon label line %q should contain go glyph %q", got, goGlyph)
	}
	// Left fill still has ▂.
	if !strings.Contains(got, "▂") {
		t.Fatalf("icon label line %q should contain '▂' fill", got)
	}
	// Total display width must be exactly the content width (40).
	if w := lipgloss.Width(lines[0].Text); w != 40 {
		t.Fatalf("icon label line display width = %d, want 40", w)
	}
}

func TestRenderCodeTabIconAndLabel(t *testing.T) {
	lines, _, _ := Render("```rust\nx\n```", 60, nil, "")
	var tab string
	for _, l := range lines {
		if strings.Contains(strip(l.Text), "❘") { // the tab line has the separator
			tab = strip(l.Text)
			break
		}
	}
	if tab == "" {
		t.Fatal("no tab line found")
	}
	if !strings.Contains(tab, "rust") {
		t.Fatalf("tab should show the language label; got %q", tab)
	}
	rustGlyph := langIcons["rust"].glyph
	if !strings.Contains(tab, rustGlyph) {
		t.Fatalf("tab should still show the rust icon glyph; got %q", tab)
	}
}

// tabLine returns the stripped text of the first non-Wide Code line in lines
// (the decorative tab/label line of a code block).
func tabLine(lines []Line) string {
	for _, l := range lines {
		if l.Code && !l.Wide && l.HBar == 0 {
			return strip(l.Text)
		}
	}
	return ""
}

func TestRenderCodeTabDiffGlyph(t *testing.T) {
	// diff block: tab must contain the mini.icons diff glyph.
	lines, _, _ := Render("```diff\n--- a\n+++ b\n@@ -1 +1 @@\n-old\n+new\n```", 80, nil, "")
	tab := tabLine(lines)
	if tab == "" {
		t.Fatal("no tab line found for diff block")
	}
	wantGlyph := langIcons["diff"].glyph
	if !strings.Contains(tab, wantGlyph) {
		t.Fatalf("diff tab %q should contain diff glyph %q", tab, wantGlyph)
	}
}

func TestRenderCodeTabPythonGlyph(t *testing.T) {
	// python block: tab must contain the mini.icons python glyph.
	lines, _, _ := Render("```python\nprint(1)\n```", 80, nil, "")
	tab := tabLine(lines)
	if tab == "" {
		t.Fatal("no tab line found for python block")
	}
	wantGlyph := langIcons["python"].glyph
	if !strings.Contains(tab, wantGlyph) {
		t.Fatalf("python tab %q should contain python glyph %q", tab, wantGlyph)
	}
}

func TestRenderCodeTabUnknownLangNoGlyph(t *testing.T) {
	// Unknown lang: tab must NOT contain the null/default glyph (U+F07E2); only
	// the label should appear.
	const nullGlyph = "\U000F07E2"
	lines, _, _ := Render("```brainfuck\n++++\n```", 80, nil, "")
	tab := tabLine(lines)
	if tab == "" {
		t.Fatal("no tab line found for brainfuck block")
	}
	if strings.Contains(tab, nullGlyph) {
		t.Fatalf("unknown-lang tab must NOT contain default glyph U+F07E2, got %q", tab)
	}
	// The language label should still appear.
	plain := strip(tab)
	if !strings.Contains(plain, "brainfuck") {
		t.Fatalf("unknown-lang tab should still show the lang label; plain = %q", plain)
	}
}

func TestRenderCodeBlockBottomBar(t *testing.T) {
	lines, _, _ := Render("```go\nx := 1\n```", 40, nil, "")
	// last line is the bottom edge bar, Wide=false, filled with 🮂, width == 40.
	last := lines[len(lines)-1]
	if last.Wide {
		t.Fatal("bottom bar line should not be Wide")
	}
	got := strip(last.Text)
	if !strings.Contains(got, "🮂") {
		t.Fatalf("bottom bar line %q should contain '🮂'", got)
	}
	if w := lipgloss.Width(last.Text); w != 40 {
		t.Fatalf("bottom bar line display width = %d, want 40", w)
	}
}

func TestCodeBlockButtonsShell(t *testing.T) {
	_, btns, _ := Render("```sh\nmake all\n```", 40, nil, "")
	var runB, play, copyB *Button
	for i := range btns {
		switch btns[i].Kind {
		case "run":
			runB = &btns[i]
		case "play":
			play = &btns[i]
		case "copy":
			copyB = &btns[i]
		}
	}
	if runB == nil || play == nil || copyB == nil {
		t.Fatalf("shell block must yield run+play+copy, got %+v", btns)
	}
	if runB.Payload != "make all" || play.Payload != "make all" || copyB.Payload != "make all" {
		t.Fatalf("payload = %q/%q/%q, want raw source", runB.Payload, play.Payload, copyB.Payload)
	}
	if runB.Width != 2 || play.Width != 2 || copyB.Width != 2 {
		t.Fatalf("button width must be 2 (glyph+trailing space)")
	}
	if runB.Col >= play.Col || play.Col >= copyB.Col {
		t.Fatalf("run must be left of play left of copy: %d vs %d vs %d", runB.Col, play.Col, copyB.Col)
	}
	if runB.Line != play.Line || play.Line != copyB.Line {
		t.Fatalf("all buttons must live on the same tab line")
	}
}

func TestCodeBlockButtonsNonShell(t *testing.T) {
	_, btns, _ := Render("```python\nx=1\n```", 40, nil, "")
	kinds := map[string]int{}
	for _, b := range btns {
		kinds[b.Kind]++
	}
	// python (Type=="run") now gets run+copy but still NO play
	if kinds["copy"] != 1 || kinds["play"] != 0 || kinds["run"] != 1 {
		t.Fatalf("python block: want exactly 1 run + 0 play + 1 copy, got %v", kinds)
	}
}

func TestRunButtonShellAndScript(t *testing.T) {
	_, b1, _ := Render("```bash {id=a}\nls\n```\n", 80, nil, "")
	_, b2, _ := Render("```python {id=p}\nprint(1)\n```\n", 80, nil, "")
	if buttonForBlock(b1, "a", "run") == nil || buttonForBlock(b1, "a", "play") == nil {
		t.Fatal("shell needs run+play")
	}
	if buttonForBlock(b2, "p", "run") == nil {
		t.Fatal("python needs a run button")
	}
	if buttonForBlock(b2, "p", "play") != nil {
		t.Fatal("python must NOT get play")
	}
}

func TestRunPayloadWrapsInterpreter(t *testing.T) {
	got := runPayload(Block{Type: "run", Lang: "python", Payload: "print(1)"})
	if !strings.Contains(got, "python3 <<'__APB_RUN__'") || !strings.Contains(got, "print(1)") {
		t.Fatalf("python run payload not wrapped: %q", got)
	}
	if runPayload(Block{Type: "shell", Lang: "bash", Payload: "ls"}) != "ls" {
		t.Fatalf("shell payload must stay raw")
	}
}

func TestCodeBlockLineMarkers(t *testing.T) {
	// Narrow content: no horizontal overflow → no HBar row.
	lines, _, _ := Render("```go\nx := 1\n```", 40, nil, "")
	codeCount, hbar := 0, 0
	for _, l := range lines {
		if l.Code {
			codeCount++
		}
		if l.HBar > 0 {
			hbar++
		}
	}
	if codeCount < 3 { // tab + ≥1 body + bottom bar
		t.Fatalf("expected ≥3 Code-tagged lines, got %d", codeCount)
	}
	if hbar != 0 {
		t.Fatalf("narrow block must not emit an HBar row, got %d", hbar)
	}
	// A non-overflowing block keeps its 🮂 bottom bar.
	hasBottom := false
	for _, l := range lines {
		if strings.Contains(l.Text, "🮂") {
			hasBottom = true
		}
	}
	if !hasBottom {
		t.Fatal("non-overflowing block must keep the 🮂 bottom bar")
	}
}

func TestStaticBlockHasNoPlay(t *testing.T) {
	_, buttons, _ := Render("```console {static}\nboom: error\n```\n", 80, nil, "")
	for _, b := range buttons {
		if b.Kind == "play" {
			t.Fatalf("static block must not get a play button: %+v", b)
		}
	}
}

func TestShellBlockKeepsPlayAndCarriesID(t *testing.T) {
	_, buttons, blocks := Render("```bash {id=fix}\nls\n```\n", 80, nil, "")
	var sawPlay bool
	for _, b := range buttons {
		if b.Kind == "play" {
			sawPlay = true
			if b.BlockID != "fix" {
				t.Fatalf("play button missing BlockID: %+v", b)
			}
		}
	}
	if !sawPlay {
		t.Fatalf("shell block must keep play")
	}
	if len(blocks) != 1 || blocks[0].Type != "shell" || blocks[0].ID != "fix" {
		t.Fatalf("blocks wrong: %+v", blocks)
	}
}

func TestCodeBlockHBarOnOverflow(t *testing.T) {
	long := "xy " + strings.Repeat("z", 200)
	lines, _, _ := Render("```go\n"+long+"\n```", 40, nil, "")
	hbarIdx := -1
	for i, l := range lines {
		if l.HBar > 0 {
			if hbarIdx != -1 {
				t.Fatal("expected exactly one HBar row")
			}
			hbarIdx = i
			if !l.Code {
				t.Fatal("HBar row must be Code-tagged")
			}
			if l.HBar <= 40 {
				t.Fatalf("HBar width should be the block width (>40), got %d", l.HBar)
			}
		}
	}
	if hbarIdx == -1 {
		t.Fatal("overflowing block must emit an HBar row")
	}
	if lines[hbarIdx].Wide {
		t.Fatal("HBar row must be a non-Wide row")
	}
	// An overflowing block drops the 🮂 bottom bar — the scrollbar caps it.
	for _, l := range lines {
		if strings.Contains(l.Text, "🮂") {
			t.Fatal("overflowing block must NOT emit the 🮂 bottom bar")
		}
	}
}

// linesContain returns true if any line's stripped text contains sub.
func linesContain(lines []Line, sub string) bool {
	for _, l := range lines {
		if strings.Contains(strip(l.Text), sub) {
			return true
		}
	}
	return false
}

// linesContainAny returns true if any line's stripped text contains any of the candidates.
func linesContainAny(lines []Line, candidates []string) bool {
	for _, c := range candidates {
		if linesContain(lines, c) {
			return true
		}
	}
	return false
}

// spinnerFramesStrings returns spinnerFrames as a slice of strings.
func spinnerFramesStrings() []string {
	out := make([]string, len(spinnerFrames))
	for i, r := range spinnerFrames {
		out[i] = string(r)
	}
	return out
}

func TestRunRegionRunningShowsSpinner(t *testing.T) {
	st := map[string]blockRunState{"a": {Status: "running", SpinFrame: 0}}
	lines, _, _ := Render("```bash {id=a}\nls\n```\n", 80, st, "")
	if !linesContain(lines, "ls") {
		t.Fatal("code still rendered")
	}
	if !linesContainAny(lines, spinnerFramesStrings()) {
		t.Fatalf("running block must show a spinner")
	}
}

func TestRunRegionRunningShowsElapsedSeconds(t *testing.T) {
	st := map[string]blockRunState{"a": {Status: "running", SpinFrame: 30}}
	lines, _, _ := Render("```bash {id=a}\nls\n```\n", 80, st, "")
	if !linesContain(lines, "3s") {
		t.Fatalf("running block with SpinFrame=30 must show '3s' (30 ticks / 10 = 3 seconds)")
	}
}

// buttonForBlock returns the first button for the given block id and kind, or nil.
func buttonForBlock(btns []Button, id, kind string) *Button {
	for i := range btns {
		if btns[i].BlockID == id && btns[i].Kind == kind {
			return &btns[i]
		}
	}
	return nil
}

func TestNeedsGatingHidesRunUntilDepOk(t *testing.T) {
	md := "```bash {id=diag}\nls\n```\n\n```bash {id=fix needs=diag}\nrm x\n```\n"
	// diag not yet run → fix is blocked: no run/play for fix; blocked indicator shown.
	_, btns, _ := Render(md, 80, map[string]blockRunState{}, "")
	if buttonForBlock(btns, "fix", "run") != nil || buttonForBlock(btns, "fix", "play") != nil {
		t.Fatalf("blocked block must have no run/play")
	}
	lines, _, _ := Render(md, 80, map[string]blockRunState{}, "")
	if !linesContain(lines, "needs: diag") {
		t.Fatalf("blocked block must show a needs indicator")
	}
	// diag ok → fix is unblocked.
	_, btns2, _ := Render(md, 80, map[string]blockRunState{"diag": {Status: "ok"}}, "")
	if buttonForBlock(btns2, "fix", "run") == nil || buttonForBlock(btns2, "fix", "play") == nil {
		t.Fatalf("satisfied block must regain run+play")
	}
}

func TestDiffBlockButtons(t *testing.T) {
	_, b, _ := Render("```diff {id=d}\n--- a\n+++ b\n```\n", 80, nil, "")
	if buttonForBlock(b, "d", "diff") == nil {
		t.Fatal("diff block needs a single 'diff' button")
	}
	if buttonForBlock(b, "d", "view-diff") != nil {
		t.Fatal("view-diff button must not be emitted (folded into 'diff')")
	}
	if buttonForBlock(b, "d", "review-diff") != nil {
		t.Fatal("review-diff button must not be emitted (folded into 'diff')")
	}
	if buttonForBlock(b, "d", "apply-diff") == nil {
		t.Fatal("diff needs apply-diff")
	}
	if buttonForBlock(b, "d", "copy") == nil {
		t.Fatal("diff needs copy")
	}
	if buttonForBlock(b, "d", "run") != nil || buttonForBlock(b, "d", "play") != nil {
		t.Fatal("diff must not get run/play")
	}
}

func TestDiffApplyGatedByNeeds(t *testing.T) {
	md := "```bash {id=p}\nls\n```\n\n```diff {id=d needs=p}\n--- a\n+++ b\n```\n"
	_, b, _ := Render(md, 80, map[string]blockRunState{}, "") // p not ok
	if buttonForBlock(b, "d", "apply-diff") != nil {
		t.Fatal("apply-diff must be gated")
	}
	if buttonForBlock(b, "d", "diff") == nil {
		t.Fatal("diff button is never gated")
	}
}

func TestRunRegionCompletedShowsSummaryAndTail(t *testing.T) {
	dir := t.TempDir()
	lp := dir + "/log"
	if err := os.WriteFile(lp, []byte("line1\nlANCHOR\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := map[string]blockRunState{"a": {Status: "ok", Exit: 0, Logpath: lp, Expanded: true}}
	lines, _, _ := Render("```bash {id=a}\nls\n```\n", 80, st, "")
	if !linesContain(lines, "exit 0") {
		t.Fatalf("summary line missing")
	}
	if !linesContain(lines, "lANCHOR") {
		t.Fatalf("expanded region must show the log tail")
	}
}

func TestRunRegionOutputTailLinesAreWide(t *testing.T) {
	dir := t.TempDir()
	lp := dir + "/log"
	if err := os.WriteFile(lp, []byte("output line one\noutput line two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := map[string]blockRunState{"a": {Status: "ok", Exit: 0, Logpath: lp, Expanded: true}}
	lines, _, _ := Render("```bash {id=a}\nls\n```\n", 80, st, "")
	// The summary line (✓ ran … ▸/▾) must be Wide=false (carries the toggle button).
	// The output/tail lines must be Wide=true (side-scrollable like a code block body).
	summaryFound := false
	tailWide := false
	for _, l := range lines {
		plain := strip(l.Text)
		if strings.Contains(plain, "exit 0") {
			summaryFound = true
			if l.Wide {
				t.Fatal("summary line must be Wide=false (it carries the clickable toggle)")
			}
		}
		if strings.Contains(plain, "output line") {
			if l.Wide {
				tailWide = true
			} else {
				t.Fatalf("output tail line must be Wide=true for side-scrolling, got Wide=false: %q", plain)
			}
		}
	}
	if !summaryFound {
		t.Fatal("summary line not found")
	}
	if !tailWide {
		t.Fatal("no Wide tail line found")
	}
}

// TestDiffLineStyle verifies the per-line classifier used for hunk-style rendering.
func TestDiffLineStyle(t *testing.T) {
	cases := []struct {
		line   string
		wantFg string
		wantBg string
	}{
		{"+added line", colGreen, diffAddBgANSI},
		{"-removed line", colRed, diffDelBgANSI},
		{"+++ b/file.go", colSubtext0, codeBgANSI},
		{"--- a/file.go", colSubtext0, codeBgANSI},
		{"@@ -1,3 +1,4 @@", colSky, codeBgANSI},
		{" context line", colText, codeBgANSI},
		{"diff --git a/x b/x", colSubtext0, codeBgANSI},
		{"index abc..def 100644", colSubtext0, codeBgANSI},
	}
	for _, tc := range cases {
		fg, bg := diffLineStyle(tc.line)
		if fg != tc.wantFg {
			t.Errorf("diffLineStyle(%q) fg = %q, want %q", tc.line, fg, tc.wantFg)
		}
		if bg != tc.wantBg {
			t.Errorf("diffLineStyle(%q) bg = %q, want %q", tc.line, bg, tc.wantBg)
		}
	}
}

// TestDiffBlockBodyLinesHaveHunkColors verifies that a rendered diff block emits
// Wide body lines colored by their diff role (add/del/context/hunk-header).
func TestDiffBlockBodyLinesHaveHunkColors(t *testing.T) {
	md := "```diff\n--- a/foo.go\n+++ b/foo.go\n@@ -1,2 +1,2 @@\n-old\n+new\n context\n```"
	lines, _, _ := Render(md, 80, nil, "")

	// Collect wide body lines (the diff content lines) by their stripped text.
	byContent := map[string]Line{}
	for _, l := range lines {
		if l.Wide && l.Code {
			byContent[strings.TrimSpace(strip(l.Text))] = l
		}
	}

	// --- line is a file header → codeBgANSI
	if l, ok := byContent["--- a/foo.go"]; !ok {
		t.Error("missing '--- a/foo.go' wide line")
	} else if l.Bg != codeBgANSI {
		t.Errorf("'--- a/foo.go' Bg = %q, want codeBgANSI", l.Bg)
	}

	// +++ line is a file header → codeBgANSI
	if l, ok := byContent["+++ b/foo.go"]; !ok {
		t.Error("missing '+++ b/foo.go' wide line")
	} else if l.Bg != codeBgANSI {
		t.Errorf("'+++ b/foo.go' Bg = %q, want codeBgANSI", l.Bg)
	}

	// @@ line is hunk header → codeBgANSI
	if l, ok := byContent["@@ -1,2 +1,2 @@"]; !ok {
		t.Error("missing '@@ -1,2 +1,2 @@' wide line")
	} else if l.Bg != codeBgANSI {
		t.Errorf("'@@ -1,2 +1,2 @@' Bg = %q, want codeBgANSI", l.Bg)
	}

	// -old is deletion → diffDelBgANSI
	if l, ok := byContent["-old"]; !ok {
		t.Error("missing '-old' wide line")
	} else if l.Bg != diffDelBgANSI {
		t.Errorf("'-old' Bg = %q, want diffDelBgANSI", l.Bg)
	}

	// +new is addition → diffAddBgANSI
	if l, ok := byContent["+new"]; !ok {
		t.Error("missing '+new' wide line")
	} else if l.Bg != diffAddBgANSI {
		t.Errorf("'+new' Bg = %q, want diffAddBgANSI", l.Bg)
	}

	// context line → codeBgANSI
	if l, ok := byContent["context"]; !ok {
		t.Error("missing 'context' wide line")
	} else if l.Bg != codeBgANSI {
		t.Errorf("'context' Bg = %q, want codeBgANSI", l.Bg)
	}
}

// TestRunButtonBecomesStopWhileRunning verifies that a block whose state is
// "running" emits a "stop" button (not "run"), and that a non-running runnable
// block emits "run" (not "stop").
func TestRunButtonBecomesStopWhileRunning(t *testing.T) {
	md := "```bash {id=blk}\nls\n```\n"

	// Non-running → run button present, stop absent.
	_, btns, _ := Render(md, 80, map[string]blockRunState{}, "")
	if buttonForBlock(btns, "blk", "run") == nil {
		t.Fatal("non-running block must have a run button")
	}
	if buttonForBlock(btns, "blk", "stop") != nil {
		t.Fatal("non-running block must NOT have a stop button")
	}

	// Running → stop button present, run absent.
	st := map[string]blockRunState{"blk": {Status: "running"}}
	_, btnsRunning, _ := Render(md, 80, st, "")
	if buttonForBlock(btnsRunning, "blk", "stop") == nil {
		t.Fatal("running block must have a stop button")
	}
	if buttonForBlock(btnsRunning, "blk", "run") != nil {
		t.Fatal("running block must NOT have a run button")
	}
}

// TestSynthesizedIDForBlockWithNoExplicitID verifies that a fenced block with
// no {id=…} attribute receives a stable, non-empty synthesised id so that
// downstream consumers (buttons, states lookup, flash key) always see a real id.
func TestSynthesizedIDForBlockWithNoExplicitID(t *testing.T) {
	// Single block with no id: must get a non-empty synthesised id.
	_, _, blocks := Render("```bash\nls\n```\n", 80, nil, "")
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0].ID == "" {
		t.Fatal("block with no explicit id must get a synthesised id, got empty string")
	}

	// Two id-less blocks: must get distinct synthesised ids.
	_, _, blocks2 := Render("```bash\nls\n```\n\n```bash\npwd\n```\n", 80, nil, "")
	if len(blocks2) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(blocks2))
	}
	if blocks2[0].ID == "" || blocks2[1].ID == "" {
		t.Fatalf("both blocks must get non-empty ids, got %q and %q", blocks2[0].ID, blocks2[1].ID)
	}
	if blocks2[0].ID == blocks2[1].ID {
		t.Fatalf("two id-less blocks must get distinct ids, both got %q", blocks2[0].ID)
	}

	// Explicit {id=x} is preserved unchanged.
	_, _, blocks3 := Render("```bash {id=x}\nls\n```\n", 80, nil, "")
	if len(blocks3) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks3))
	}
	if blocks3[0].ID != "x" {
		t.Fatalf("explicit id must be preserved, got %q want %q", blocks3[0].ID, "x")
	}
}

// TestSynthesizedIDStableAcrossRerenders verifies that re-rendering the same
// document yields the same synthesised ids (deterministic, position-based).
func TestSynthesizedIDStableAcrossRerenders(t *testing.T) {
	md := "```bash\nls\n```\n\n```diff\n--- a\n+++ b\n```\n"
	_, _, blocks1 := Render(md, 80, nil, "")
	_, _, blocks2 := Render(md, 80, nil, "")
	if len(blocks1) != 2 || len(blocks2) != 2 {
		t.Fatalf("expected 2 blocks per render, got %d and %d", len(blocks1), len(blocks2))
	}
	if blocks1[0].ID != blocks2[0].ID || blocks1[1].ID != blocks2[1].ID {
		t.Fatalf("synthesised ids must be stable: first render %q/%q, second %q/%q",
			blocks1[0].ID, blocks1[1].ID, blocks2[0].ID, blocks2[1].ID)
	}
}

// TestSynthesizedIDButtonsHaveNonEmptyBlockID verifies that buttons for
// id-less blocks carry the synthesised id (not an empty string).
func TestSynthesizedIDButtonsHaveNonEmptyBlockID(t *testing.T) {
	_, btns, _ := Render("```bash\nls\n```\n", 80, nil, "")
	for _, b := range btns {
		if b.BlockID == "" {
			t.Fatalf("button %q for id-less block has empty BlockID", b.Kind)
		}
	}
}

// TestCachedBadgePillRow verifies that when isCached=true the cached-pill row
// (the row BELOW the title) contains "cached ·", an age token, and the pill
// glyphs — and that the title line itself no longer carries the pill.
func TestCachedBadgePillRow(t *testing.T) {
	m := newModel("agent", "hello world")
	m.width = 120
	m.isCached = true
	m.cachedAt = time.Now().Add(-3 * time.Minute) // 3 minutes ago
	m.answerRegen = fakeAnswerRegen()             // wire a regenerate path so the reload renders

	row := m.cachedBadgeRow()
	plain := strip(row)
	if !strings.Contains(plain, "cached ·") {
		t.Fatalf("pill row missing 'cached ·' badge: %q", plain)
	}
	if !strings.Contains(plain, "m ago") {
		t.Fatalf("pill row missing age token (expected '...m ago'): %q", plain)
	}
	if !strings.Contains(row, "\U0000E0B6") {
		t.Fatalf("pill row missing powerline left-cap (U+E0B6): %q", row)
	}
	if !strings.Contains(row, "\U0010F1DA") {
		t.Fatalf("pill row missing reload icon (U+10F1DA): %q", row)
	}
	if strings.Contains(m.titleLine(m.width), "\U0000E0B6") {
		t.Fatalf("title line should no longer carry the pill")
	}
}

// TestCachedBadgeInNormalLines verifies that normalLines() places the pill with
// a blank line above and below it.
// Layout: index 0=leading blank, 1=title, 2=blank-above-pill, 3=pill, 4=blank-below-pill, 5+=body.
func TestCachedBadgeInNormalLines(t *testing.T) {
	m := newModel("agent", "hello")
	m.width = 120
	m.height = 24
	m.isCached = true
	m.cachedAt = time.Now().Add(-7 * time.Minute)
	m.answerRegen = fakeAnswerRegen() // wire a regenerate path so the reload renders
	m.reflow()

	lines := m.normalLines()
	if len(lines) < 5 {
		t.Fatal("normalLines returned fewer than 5 lines")
	}

	// Row 2: blank above the pill.
	if got := strings.TrimSpace(strip(lines[2])); got != "" {
		t.Fatalf("row 2 (blank above pill) must be empty, got: %q", got)
	}

	// Row 3: the pill row.
	raw := lines[3]
	plain := strip(raw)
	if !strings.Contains(plain, "cached ·") {
		t.Fatalf("pill row (index 3) missing cached badge: %q", plain)
	}
	if !strings.Contains(plain, "m ago") {
		t.Fatalf("pill row (index 3) missing age in cached badge: %q", plain)
	}
	if !strings.Contains(raw, "\U0000E0B6") {
		t.Fatalf("pill row (index 3) missing powerline left-cap (U+E0B6): %q", raw)
	}
	if !strings.Contains(raw, "\U0010F1DA") {
		t.Fatalf("pill row (index 3) missing reload icon (U+10F1DA): %q", raw)
	}

	// Row 4: blank below the pill.
	if got := strings.TrimSpace(strip(lines[4])); got != "" {
		t.Fatalf("row 4 (blank below pill) must be empty, got: %q", got)
	}
}

// TestBodyTopAndBodyHeightCached verifies the bodyTop/body height arithmetic
// for both cached and non-cached models.
func TestBodyTopAndBodyHeightCached(t *testing.T) {
	const h = 24

	// Non-cached: bodyTop == 3, body == h-5.
	mn := newModel("agent", "")
	mn.height = h
	mn.isCached = false
	if got := mn.bodyTop(); got != 3 {
		t.Errorf("non-cached bodyTop = %d, want 3", got)
	}
	if got := mn.body(); got != h-5 {
		t.Errorf("non-cached body = %d, want %d", got, h-5)
	}

	// Cached: bodyTop == 5, body == h-7.
	mc := newModel("agent", "")
	mc.height = h
	mc.isCached = true
	if got := mc.bodyTop(); got != 5 {
		t.Errorf("cached bodyTop = %d, want 5", got)
	}
	if got := mc.body(); got != h-7 {
		t.Errorf("cached body = %d, want %d", got, h-7)
	}
}

// TestButtonHitTestCachedLayout verifies that with isCached=true a button's
// known screen-Y maps back through buttonAt to the same button, proving that
// the 2-row layout delta is threaded consistently through bodyTop and the
// buttonAt call sites.
func TestButtonHitTestCachedLayout(t *testing.T) {
	const h = 24
	// Render a shell block so we have at least one button.
	m := newModel("agent", "```bash {id=blk}\nls\n```\n")
	m.width = 80
	m.height = h
	m.isCached = true
	m.reflow()

	if len(m.buttons) == 0 {
		t.Fatal("expected at least one button from the shell block")
	}
	b := m.buttons[0]

	// Screen Y for this button: button.Line is the content-line index (0-based
	// in m.lines). With yOff=0, screen Y = b.Line + m.bodyTop().
	screenY := b.Line + m.bodyTop()
	// Screen X: 2-col left margin + b.Col (the button glyph column).
	screenX := 2 + b.Col

	got, ok := buttonAt(m.buttons, screenX, screenY, m.yOff, m.bodyTop())
	if !ok {
		t.Fatalf("buttonAt(%d,%d) returned no button; bodyTop=%d, button.Line=%d, button.Col=%d",
			screenX, screenY, m.bodyTop(), b.Line, b.Col)
	}
	if got.Kind != b.Kind || got.BlockID != b.BlockID {
		t.Fatalf("buttonAt returned wrong button: got %+v, want %+v", got, b)
	}
}

// TestCachedBadgeAbsentWhenNotCachedNoPill verifies that with isCached=false the
// pill row is empty and the title line carries no pill glyphs.
func TestCachedBadgeAbsentWhenNotCachedNoPill(t *testing.T) {
	m := newModel("agent", "hello world")
	m.width = 120
	m.isCached = false

	if m.cachedBadgeRow() != "" {
		t.Fatalf("cachedBadgeRow must be empty when isCached=false: %q", m.cachedBadgeRow())
	}
	if strings.Contains(m.titleLine(m.width), "\U0000E0B6") {
		t.Fatalf("title line must not contain the pill when isCached=false")
	}
}

// TestCachedReloadButtonRegistered verifies that when isCached=true and reflow
// is called, a Screen-fixed "regenerate" button is present at the pill row
// (bodyTop()-2) with a sane column, Width=2, BlockID="cached".
func TestCachedReloadButtonRegistered(t *testing.T) {
	m := newModel("agent", "hello")
	m.width = 120
	m.height = 24
	m.isCached = true
	m.cachedAt = time.Now().Add(-5 * time.Minute)
	m.answerRegen = fakeAnswerRegen() // wire a regenerate path so the reload renders
	m.reflow()

	var regenBtn *Button
	for i := range m.buttons {
		if m.buttons[i].Kind == "regenerate" {
			regenBtn = &m.buttons[i]
			break
		}
	}
	if regenBtn == nil {
		t.Fatal("no regenerate button found after reflow with isCached=true")
	}
	if !regenBtn.Screen {
		t.Error("regenerate button must have Screen=true")
	}
	wantLine := m.bodyTop() - 2
	if regenBtn.Line != wantLine {
		t.Errorf("regenerate button Line = %d, want %d (bodyTop()-2)", regenBtn.Line, wantLine)
	}
	if regenBtn.BlockID != "cached" {
		t.Errorf("regenerate button BlockID = %q, want %q", regenBtn.BlockID, "cached")
	}
	// The ENTIRE pill is the click target: Col 0 (the left cap, after buttonAt
	// strips the 2-col margin) and Width = the pill's visible width sans trailing space.
	if regenBtn.Col != 0 {
		t.Errorf("regenerate button Col = %d, want 0 (whole-pill target starts at the left cap)", regenBtn.Col)
	}
	wantW := lipgloss.Width(m.cachedBadge()) - 1
	if regenBtn.Width != wantW {
		t.Errorf("regenerate button Width = %d, want %d (whole pill, sans trailing space)", regenBtn.Width, wantW)
	}
}

// TestCachedReloadButtonAbsentWhenNotCached verifies that with isCached=false
// no regenerate button is added after reflow.
func TestCachedReloadButtonAbsentWhenNotCached(t *testing.T) {
	m := newModel("agent", "hello")
	m.width = 120
	m.height = 24
	m.isCached = false
	m.reflow()

	for _, b := range m.buttons {
		if b.Kind == "regenerate" {
			t.Fatalf("regenerate button must NOT be present when isCached=false, got %+v", b)
		}
	}
}

// TestCachedReloadButtonHitTest verifies that buttonAt resolves a click at the
// reload icon's screen position to the regenerate button.
func TestCachedReloadButtonHitTest(t *testing.T) {
	m := newModel("agent", "hello")
	m.width = 120
	m.height = 24
	m.isCached = true
	m.cachedAt = time.Now().Add(-2 * time.Minute)
	m.answerRegen = fakeAnswerRegen() // wire a regenerate path so the reload renders
	m.reflow()

	var regenBtn *Button
	for i := range m.buttons {
		if m.buttons[i].Kind == "regenerate" {
			regenBtn = &m.buttons[i]
			break
		}
	}
	if regenBtn == nil {
		t.Fatal("no regenerate button found")
	}

	// Screen-fixed button: click at screen Y = regenBtn.Line, screen X = 2 + regenBtn.Col.
	screenY := regenBtn.Line
	screenX := 2 + regenBtn.Col // 2-col left margin applied by buttonAt

	got, ok := buttonAt(m.buttons, screenX, screenY, m.yOff, m.bodyTop())
	if !ok {
		t.Fatalf("buttonAt(%d,%d) returned no button; regenBtn=%+v", screenX, screenY, *regenBtn)
	}
	if got.Kind != "regenerate" || got.BlockID != "cached" {
		t.Fatalf("buttonAt returned wrong button: got %+v, want Kind=regenerate BlockID=cached", got)
	}
	// The WHOLE pill is clickable: a click at the right end resolves too.
	rightX := 2 + regenBtn.Col + regenBtn.Width - 1
	got2, ok2 := buttonAt(m.buttons, rightX, screenY, m.yOff, m.bodyTop())
	if !ok2 || got2.Kind != "regenerate" {
		t.Fatalf("buttonAt at the pill's right end (%d,%d) did not resolve to regenerate: ok=%v got=%+v", rightX, screenY, ok2, got2)
	}
}

// TestCachedRegenHintLabelRendered verifies that in hint mode the regenerate
// button's hint label is drawn on the blank line ABOVE the cached pill (anchored
// near the reload glyph), not omitted — and that it floats above, not over, the pill.
func TestCachedRegenHintLabelRendered(t *testing.T) {
	m := newModel("agent", "hello")
	m.width = 120
	m.height = 24
	m.isCached = true
	m.cachedAt = time.Now()
	m.answerRegen = fakeAnswerRegen() // wire a regenerate path so the reload renders
	m.reflow()

	var regen Button
	found := false
	for _, b := range m.buttons {
		if b.Kind == "regenerate" {
			regen, found = b, true
		}
	}
	if !found {
		t.Fatal("no regenerate button registered after reflow")
	}
	m.hintMode = true
	m.hintLabels = map[string]Button{"Z": regen}

	lines := strings.Split(strip(m.viewString()), "\n")
	pillIdx := -1
	for i, l := range lines {
		if strings.Contains(l, "cached ·") {
			pillIdx = i
			break
		}
	}
	if pillIdx < 1 {
		t.Fatalf("pill row not found at index >=1: %#v", lines)
	}
	if !strings.Contains(lines[pillIdx-1], "Z") {
		t.Fatalf("regenerate hint label 'Z' not rendered on the line above the pill; above=%q", lines[pillIdx-1])
	}
	if strings.Contains(lines[pillIdx], "Z") {
		t.Errorf("hint label should float above the pill, not over it; pill=%q", lines[pillIdx])
	}
}

// TestCachedReloadButtonMouseClick verifies that clicking the reload icon sets
// m.flashKey="cached:regenerate", returns a non-nil cmd, and triggers the
// in-process regenerate (REPLACE: md cleared, thinking on, no longer cached). A
// cached-answer regenerate seam stands in for a real orchestrator.
func TestCachedReloadButtonMouseClick(t *testing.T) {
	m := newModel("agent", "hello")
	m.width = 120
	m.height = 24
	m.isCached = true
	m.cachedAt = time.Now().Add(-1 * time.Minute)
	m.answerRegen = fakeAnswerRegen()
	m.reflow()

	var regenBtn *Button
	for i := range m.buttons {
		if m.buttons[i].Kind == "regenerate" {
			regenBtn = &m.buttons[i]
			break
		}
	}
	if regenBtn == nil {
		t.Fatal("no regenerate button found")
	}

	// Simulate a left-click at the reload icon's screen position.
	clickX := 2 + regenBtn.Col
	clickY := regenBtn.Line

	msg2, cmd := m.Update(tea.MouseClickMsg{
		Button: tea.MouseLeft,
		X:      clickX,
		Y:      clickY,
	})
	m2 := msg2.(model)

	if m2.flashKey != "cached:regenerate" {
		t.Errorf("flashKey = %q, want %q", m2.flashKey, "cached:regenerate")
	}
	if cmd == nil {
		t.Error("clicking reload icon must return a non-nil cmd")
	}
	if m2.md != "" {
		t.Errorf("regenerate must clear md (REPLACE); got %q", m2.md)
	}
	if m2.isCached {
		t.Error("regenerate must drop the cached state")
	}
	if !m2.thinking {
		t.Error("regenerate must start a thinking session")
	}
}

// TestCachedBadgeFlashHighlightsWholePill verifies that on click (flashKey ==
// "cached:regenerate") the WHOLE pill highlights: it flips to the flash colour
// (colFlashOn = #ffffff) as the background — so the peach bg is gone — while the
// reload glyph is still present. Without flash, the pill keeps its colPeach bg.
func TestCachedBadgeFlashHighlightsWholePill(t *testing.T) {
	m := newModel("agent", "hello")
	m.isCached = true
	m.cachedAt = time.Now().Add(-3 * time.Minute)
	m.answerRegen = fakeAnswerRegen() // wire a regenerate path so the reload renders

	const whiteBg = "48;2;255;255;255" // colFlashOn (#ffffff) as background
	const peachBg = "48;2;250;179;135" // colPeach  (#fab387) as background

	// Without flash: peach pill, no white flash bg.
	m.flashKey = ""
	normal := m.cachedBadge()
	if strings.Contains(normal, whiteBg) {
		t.Errorf("without flash, pill must NOT contain the white flash bg %q\ngot: %q", whiteBg, normal)
	}
	if !strings.Contains(normal, peachBg) {
		t.Errorf("without flash, pill must have the colPeach bg %q\ngot: %q", peachBg, normal)
	}
	if !strings.Contains(normal, "\U0010F1DA") {
		t.Error("cachedBadge() must contain the reload icon U+10F1DA")
	}

	// With flash: the WHOLE pill highlights — white flash bg present, peach bg gone.
	m.flashKey = "cached:regenerate"
	flash := m.cachedBadge()
	if !strings.Contains(flash, whiteBg) {
		t.Errorf("on click the whole pill must highlight with the white flash bg %q\ngot: %q", whiteBg, flash)
	}
	if strings.Contains(flash, peachBg) {
		t.Errorf("on flash the whole pill flips to the flash colour; colPeach bg must be gone\ngot: %q", flash)
	}
	if !strings.Contains(flash, "\U0010F1DA") {
		t.Error("flashed pill must still contain the reload icon")
	}
}

// TestRelativeAge verifies the relative-age helper boundaries.
func TestRelativeAge(t *testing.T) {
	now := time.Now()
	cases := []struct {
		age  time.Duration
		want string
	}{
		{30 * time.Second, "just now"},
		{3 * time.Minute, "3m ago"},
		{90 * time.Minute, "1h ago"},
		{48 * time.Hour, "2d ago"},
	}
	for _, tc := range cases {
		got := relativeAge(now.Add(-tc.age))
		if got != tc.want {
			t.Errorf("relativeAge(-%v) = %q, want %q", tc.age, got, tc.want)
		}
	}
}

// TestDiffApplyUndoToggleButtons verifies the apply⇄undo button flip:
// - Status "" (not applied) → apply-diff button present, undo-diff absent.
// - Status "ok" (applied)   → undo-diff button present, apply-diff absent.
// - diff (view) button is always present regardless of state.
func TestDiffApplyUndoToggleButtons(t *testing.T) {
	md := "```diff {id=fix}\n--- a/f.go\n+++ b/f.go\n@@ -1 +1 @@\n-old\n+new\n```\n"

	// Not yet applied: apply-diff shown, no undo-diff.
	_, btns, _ := Render(md, 80, map[string]blockRunState{}, "")
	if buttonForBlock(btns, "fix", "apply-diff") == nil {
		t.Fatal("unapplied diff must have apply-diff button")
	}
	if buttonForBlock(btns, "fix", "undo-diff") != nil {
		t.Fatal("unapplied diff must NOT have undo-diff button")
	}
	if buttonForBlock(btns, "fix", "diff") == nil {
		t.Fatal("diff (view) button must always be present")
	}

	// Applied (Status=="ok"): undo-diff shown, no apply-diff.
	st := map[string]blockRunState{"fix": {Status: "ok"}}
	_, btnsOk, _ := Render(md, 80, st, "")
	if buttonForBlock(btnsOk, "fix", "undo-diff") == nil {
		t.Fatal("applied diff must have undo-diff button")
	}
	if buttonForBlock(btnsOk, "fix", "apply-diff") != nil {
		t.Fatal("applied diff must NOT have apply-diff button")
	}
	if buttonForBlock(btnsOk, "fix", "diff") == nil {
		t.Fatal("diff (view) button must always be present when applied")
	}
}

// TestDiffUndoNotNeedsGated verifies that the undo-diff button appears even
// when the diff block has unmet needs (undo is always allowed when applied).
func TestDiffUndoNotNeedsGated(t *testing.T) {
	md := "```bash {id=p}\nls\n```\n\n```diff {id=d needs=p}\n--- a\n+++ b\n```\n"
	// p not ok but d is applied (Status=="ok").
	st := map[string]blockRunState{"d": {Status: "ok"}}
	_, btns, _ := Render(md, 80, st, "")
	if buttonForBlock(btns, "d", "undo-diff") == nil {
		t.Fatal("undo-diff must be present regardless of unmet needs (always allowed when applied)")
	}
	if buttonForBlock(btns, "d", "apply-diff") != nil {
		t.Fatal("apply-diff must be absent when applied (undo shown instead)")
	}
}

// TestDiffNeedsReGatingAfterUndo verifies that a {needs=fix} block is gated
// when fix.Status=="ok" (unlocked) and re-gated when fix.Status=="" (undone).
func TestDiffNeedsReGatingAfterUndo(t *testing.T) {
	md := "```diff {id=fix}\n--- a\n+++ b\n```\n\n```bash {id=cmd needs=fix}\necho done\n```\n"

	// fix applied → cmd is ungated.
	stOk := map[string]blockRunState{"fix": {Status: "ok"}}
	_, btnsOk, _ := Render(md, 80, stOk, "")
	if buttonForBlock(btnsOk, "cmd", "run") == nil {
		t.Fatal("cmd must be ungated (run button present) when fix is applied")
	}
	linesOk, _, _ := Render(md, 80, stOk, "")
	if linesContain(linesOk, "needs: fix") {
		t.Fatal("needs indicator must not appear when fix is applied")
	}

	// fix undone (Status cleared to "") → cmd re-locks.
	stUndone := map[string]blockRunState{"fix": {Status: ""}}
	_, btnsUndone, _ := Render(md, 80, stUndone, "")
	if buttonForBlock(btnsUndone, "cmd", "run") != nil {
		t.Fatal("cmd must be re-gated (no run button) after fix is undone")
	}
	linesUndone, _, _ := Render(md, 80, stUndone, "")
	if !linesContain(linesUndone, "needs: fix") {
		t.Fatal("needs indicator must reappear after fix is undone")
	}
}

// TestFollowupButtonOnFailedRunBlock verifies that a non-verify failed run block
// renders a "↻ try another fix" (Kind "followup") button whose payload is the
// block's raw command text.
func TestFollowupButtonOnFailedRunBlock(t *testing.T) {
	md := "```bash {id=fix}\nmake build\n```\n"
	st := map[string]blockRunState{"fix": {Status: "failed", Exit: 1}}
	lines, btns, _ := Render(md, 80, st, "")
	if !linesContain(lines, "try another fix") {
		t.Fatal("failed run block must show a 'try another fix' label")
	}
	b := buttonForBlock(btns, "fix", "followup")
	if b == nil {
		t.Fatal("failed run block must register a followup button")
	}
	if b.Payload != "make build" {
		t.Errorf("followup button payload = %q, want the block command %q", b.Payload, "make build")
	}
}

// TestNoFollowupButtonOnVerifyBlock verifies the verify block does NOT get a
// followup button (it auto-fires in the model result handler instead).
func TestNoFollowupButtonOnVerifyBlock(t *testing.T) {
	md := "```bash {id=verify}\nmake build\n```\n"
	st := map[string]blockRunState{"verify": {Status: "failed", Exit: 1}}
	_, btns, _ := Render(md, 80, st, "")
	if buttonForBlock(btns, "verify", "followup") != nil {
		t.Fatal("verify block must NOT render a followup button (it auto-fires)")
	}
}

// TestNoFollowupButtonOnOkBlock verifies a successful run block has no followup button.
func TestNoFollowupButtonOnOkBlock(t *testing.T) {
	md := "```bash {id=fix}\nmake build\n```\n"
	st := map[string]blockRunState{"fix": {Status: "ok", Exit: 0}}
	_, btns, _ := Render(md, 80, st, "")
	if buttonForBlock(btns, "fix", "followup") != nil {
		t.Fatal("an ok block must not render a followup button")
	}
}

// TestStoppedBlockNeutralNoFollowupButton verifies a stopped block renders a
// neutral "stopped" run-region (not "failed") and never offers a followup button.
func TestStoppedBlockNeutralNoFollowupButton(t *testing.T) {
	md := "```bash {id=fix}\nmake build\n```\n"
	st := map[string]blockRunState{"fix": {Status: "stopped", Exit: 143}}
	lines, btns, _ := Render(md, 80, st, "")
	if buttonForBlock(btns, "fix", "followup") != nil {
		t.Fatal("a stopped block must not render a followup button")
	}
	if linesContain(lines, "try another fix") {
		t.Fatal("a stopped block must not show the 'try another fix' label")
	}
	if linesContain(lines, "failed") {
		t.Fatal("a stopped block must read as neutral, not 'failed'")
	}
	if !linesContain(lines, "stopped") {
		t.Fatal("a stopped block run-region must read as 'stopped'")
	}
}

// lineForSub returns the first Line whose stripped text contains sub, and true,
// or a zero Line and false if none.
func lineForSub(lines []Line, sub string) (Line, bool) {
	for _, l := range lines {
		if strings.Contains(strip(l.Text), sub) {
			return l, true
		}
	}
	return Line{}, false
}

// TestMalformedClosingFenceDoesNotNukeRender is the exact Bug A repro: a closing
// ``` immediately followed by prose on the SAME line (no newline). Without the
// fence normalizer goldmark treats "```SDK…" as an opening info-string fence and
// renders the ENTIRE rest of the document as code. The normalizer must close the
// block so the SDK prose and everything after render as text.
func TestMalformedClosingFenceDoesNotNukeRender(t *testing.T) {
	md := "Here is the build:\n\n" +
		"```bash\n" +
		"gg build\n" +
		"```SDK is at `/Users/x/sdk`, but ANDROID_HOME is unset. Set it.\n\n" +
		"## Next steps\n\n" +
		"Run the tool again.\n"

	lines, _, blocks := Render(md, 80, nil, "")

	// Exactly ONE code block (the bash block), carrying only "gg build".
	if len(blocks) != 1 {
		t.Fatalf("want 1 code block, got %d: %+v", len(blocks), blocks)
	}
	if strings.TrimSpace(blocks[0].Payload) != "gg build" {
		t.Fatalf("code block swallowed trailing text; payload = %q", blocks[0].Payload)
	}

	// The SDK prose must render as PROSE (not Code), and survive the render.
	l, ok := lineForSub(lines, "ANDROID_HOME is unset")
	if !ok {
		t.Fatal("SDK prose was dropped from the render entirely")
	}
	if l.Code {
		t.Fatalf("SDK prose rendered as code, not prose: %q", strip(l.Text))
	}

	// The rest of the document must survive too.
	if !linesContain(lines, "Next steps") {
		t.Fatal("heading after the malformed fence was lost")
	}
	if !linesContain(lines, "Run the tool again") {
		t.Fatal("trailing paragraph after the malformed fence was lost")
	}
}

// TestNormalizeFences_WellFormedUntouched: well-formed fences and fence content
// (including a line that merely contains backticks mid-content) are not altered.
func TestNormalizeFences_WellFormedUntouched(t *testing.T) {
	cases := []string{
		"```bash\ngg build\n```\nprose after\n",
		"```\nplain code\n```\n",
		"```python\nx = 1  # uses `backticks` in a comment\n```\n",
		"no fences at all\njust prose\n",
		"~~~\ntilde fence\n~~~\nprose\n",
	}
	for _, in := range cases {
		if got := normalizeFences(in); got != in {
			t.Errorf("normalizeFences altered well-formed input:\n in: %q\nout: %q", in, got)
		}
	}
}

// TestNormalizeFences_Repairs: the malformed-closer cases are split into a clean
// closing fence + trailing prose, with newlines preserved.
func TestNormalizeFences_Repairs(t *testing.T) {
	cases := []struct{ in, want string }{
		{"```bash\ngg build\n```SDK is here.\n", "```bash\ngg build\n```\nSDK is here.\n"},
		{"```\ncode\n``` trailing\n", "```\ncode\n```\ntrailing\n"},
		// Longer opener; closer must be >= opener length.
		{"````\ncode\n````tail\n", "````\ncode\n````\ntail\n"},
		// A "```" run shorter than the opener is content, not a closer.
		{"````\n```\nstill code\n````\nprose\n", "````\n```\nstill code\n````\nprose\n"},
	}
	for _, c := range cases {
		if got := normalizeFences(c.in); got != c.want {
			t.Errorf("normalizeFences(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// renderMarkdownLines renders md at the given width and returns the lines.
func renderMarkdownLines(t *testing.T, md string, width int) []Line {
	t.Helper()
	lines, _, _ := Render(md, width, nil, "")
	return lines
}

// lineTexts strips ANSI from each line and returns a slice of plain strings.
func lineTexts(lines []Line) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = strip(l.Text)
	}
	return out
}

// TestList_HangingIndent verifies that wrapped unordered-list items indent
// continuation lines to align after the "• " marker, not under it.
// Top-level list: base indent = 2 (list() uses indent+2, indent=0).
// "• " is 2 cols wide → hangIndent = 2+2 = 4.
func TestList_HangingIndent(t *testing.T) {
	// a long unordered item that must wrap at width 24
	lines := renderMarkdownLines(t, "- "+strings.Repeat("word ", 12)+"\n", 24)
	txts := lineTexts(lines)
	if len(txts) < 2 {
		t.Fatalf("item did not wrap: %v", txts)
	}
	// continuation aligns after "• " → leading spaces == base indent (2) + width("• ")=2 → 4
	cont := txts[1]
	lead := len(cont) - len(strings.TrimLeft(cont, " "))
	if lead != 4 {
		t.Errorf("unordered continuation indent = %d, want 4 (after '• ')", lead)
	}
}

// TestList_OrderedHangingIndent verifies that wrapped ordered-list items indent
// continuation lines to align after the "N. " marker.
// Top-level list: base indent = 2; "1. " is 3 cols wide → hangIndent = 2+3 = 5.
func TestList_OrderedHangingIndent(t *testing.T) {
	lines := renderMarkdownLines(t, "1. "+strings.Repeat("word ", 12)+"\n", 24)
	txts := lineTexts(lines)
	if len(txts) < 2 {
		t.Fatalf("ordered item did not wrap: %v", txts)
	}
	cont := txts[1]
	lead := len(cont) - len(strings.TrimLeft(cont, " "))
	if lead != 5 { // indent 2 + width("1. ")=3
		t.Errorf("ordered continuation indent = %d, want 5 (after '1. ')", lead)
	}
}

// TestList_HangingIndent_NoOverflow verifies that wrapped list items do NOT
// overflow the render width. With emitHanging budgeting for hangIndent (the
// continuation indent), all lines—including wrapped continuations—must fit
// within the requested width.
func TestList_HangingIndent_NoOverflow(t *testing.T) {
	const width = 24
	lines := renderMarkdownLines(t, "- "+strings.Repeat("word ", 12)+"\n", width)
	for i, ln := range lines {
		w := lipgloss.Width(ln.Text)
		if w > width {
			t.Errorf("line %d exceeds width %d (got %d): %q", i, width, w, strip(ln.Text))
		}
	}
}

func TestCreateBlock_TabAndButton(t *testing.T) {
	_, buttons, _ := Render("```go {id=new file=cmd/x/main.go}\npackage main\n```\n", 100, nil, "")
	var has bool
	for _, b := range buttons {
		if b.BlockID == "new" && b.Kind == "create" {
			has = true
		}
	}
	if !has {
		t.Fatal("create block has no create button")
	}
	// applied → undo button
	_, buttons2, _ := Render("```go {id=new file=cmd/x/main.go}\npackage main\n```\n", 100,
		map[string]blockRunState{"new": {Status: "ok"}}, "")
	var undo bool
	for _, b := range buttons2 {
		if b.BlockID == "new" && b.Kind == "undo-create" {
			undo = true
		}
	}
	if !undo {
		t.Fatal("applied create block must show undo-create")
	}
	// tab must show the file path
	lines, _, _ := Render("```go {id=new file=cmd/x/main.go}\npackage main\n```\n", 100, nil, "")
	text := joinText(lines)
	if !strings.Contains(text, "cmd/x/main.go") {
		t.Fatalf("create tab must contain file path, got:\n%s", text)
	}
}

// TestDriftedDiff_GreysApplyAndViewDiff verifies that buttonGlyph dims the
// diff (view) and apply-diff glyphs to colOverlay0 when the block is Drifted,
// and leaves non-drifted blocks with their normal colors.
func TestDriftedDiff_GreysApplyAndViewDiff(t *testing.T) {
	r := &renderer{states: map[string]blockRunState{"fix": {Drifted: true}}}
	bg := lipgloss.NewStyle()
	wantDim := bg.Foreground(lipgloss.Color(colOverlay0))

	// diff (view) glyph: normal color is colBlue, drifted → colOverlay0
	gotDiff := r.buttonGlyph("fix", "diff", glyphViewDiff, colBlue, bg)
	if gotDiff != wantDim.Render(glyphViewDiff) {
		t.Fatalf("drifted diff button should be dimmed:\n got  %q\n want %q", gotDiff, wantDim.Render(glyphViewDiff))
	}
	// apply-diff glyph: normal color is colGreen, drifted → colOverlay0
	gotApply := r.buttonGlyph("fix", "apply-diff", glyphApply, colGreen, bg)
	if gotApply != wantDim.Render(glyphApply) {
		t.Fatalf("drifted apply-diff button should be dimmed:\n got  %q\n want %q", gotApply, wantDim.Render(glyphApply))
	}
	// non-drifted block: the diff glyph must NOT be dimmed
	rNormal := &renderer{states: map[string]blockRunState{"fix": {Drifted: false}}}
	gotNormal := rNormal.buttonGlyph("fix", "diff", glyphViewDiff, colBlue, bg)
	if gotNormal == wantDim.Render(glyphViewDiff) {
		t.Fatal("non-drifted diff button must NOT be dimmed")
	}
}

func TestDriftedDiff_RegionAndResolveButton(t *testing.T) {
	states := map[string]blockRunState{"fix": {Drifted: true}}
	src := "```diff {id=fix}\n--- a/cmd/x.go\n+++ b/cmd/x.go\n@@ -1 +1 @@\n-a\n+b\n```\n"
	lines, buttons, _ := Render(src, 100, states, "")
	if !strings.Contains(joinText(lines), "no longer applies") {
		t.Fatalf("missing drift message:\n%s", joinText(lines))
	}
	var has bool
	for _, b := range buttons {
		if b.BlockID == "fix" && b.Kind == "drift-resolve" {
			has = true
		}
	}
	if !has {
		t.Fatal("drifted block must have a drift-resolve button")
	}
}
