# Viewer-UX-polish Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Four presentation-only render refinements — a shared theme palette, unified dialog colors, a bordered callout frame, and list hanging-indents.

**Architecture:** Introduce one `internal/theme` palette package (dedup the duplicated `internal/ui` consts and `internal/input` `Theme`), then make three render-layer changes that draw from it: unify dialog bg/border, reframe callouts with a 5-glyph border, and hang-indent wrapped list items. No behavior change.

**Tech Stack:** Go; bubbletea v2 (`charm.land/bubbletea/v2`) / lipgloss v2 (`charm.land/lipgloss/v2`); the viewer renderer (`internal/ui/render.go`) + dialog widgets (`internal/input`).

## Global Constraints

- Module `github.com/Townk/ai-playbook`. gpg-signed Conventional Commits; NO `Co-Authored-By`; `git add` explicit paths; verify signing `git log -1 --format=%G?` == `G`.
- Presentation-only — no run/author/save behavior changes.
- Canonical colors (Catppuccin Mocha): blue `#89b4fa`, green `#a6e3a1`, mauve `#cba6f7`, peach `#fab387`, red `#f38ba8`, overlay0 `#6c7086`, mantle `#181825`, surface1 `#45475a`, surface0 `#313244`, base `#1e1e2e`, text `#cdd6f4`, codeBg `#282C41`.
- Unified dialog look: **bg = `#181825` (mantle)**, **border = `#89b4fa` (blue) `lipgloss.RoundedBorder()`**.
- Callout frame glyphs: TL `🬞` U+1FB1E, TB `🬭` U+1FB2D, CL `▐` U+2590, BL `🬁` U+1FB01, BB `🬂` U+1FB02. Corners + left bar = admonition **accent**; top/bottom sextants = **callout-bg tone** (`darken(accent, 0.20)`); frame cells on **document bg**; content keeps the darkened-accent bg; text 1 leading space after `▐`; **no right border**.
- List continuation indent = `indent + 2 + lipgloss.Width(marker)` (marker `• ` unordered, `N. ` ordered).
- `gofmt -l` clean; `go vet` clean; touched packages pass `go test`.

---

### Task 1: Shared `internal/theme` palette (dedup)

**Files:**
- Create: `internal/theme/theme.go`, `internal/theme/theme_test.go`
- Modify: `internal/ui/theme.go` (alias to `internal/theme`)
- Modify: `internal/input/theme.go` (`defaultTheme` references `internal/theme`)

**Interfaces:**
- Produces: `theme.Blue/Green/Mauve/Peach/Red/Overlay0/Mantle/Surface1/Surface0/Base/Text/CodeBg` (untyped string consts) + `theme.Darken(hex string, f float64) string`, `theme.BgANSI(hex string) string`, `theme.ParseHex(hex string) (r,g,b int)`. Consumed by Tasks 2-4 + the two packages.

**Context:** The palette is duplicated — `internal/ui/theme.go` (`const colBlue = "#89b4fa"` … + `darken`/`bgANSI`/`parseHex`) and `internal/input/theme.go` (`defaultTheme()` with hand-typed hex). This task makes `internal/theme` the single source; the two packages reference it with NO value change (renders stay byte-identical).

- [ ] **Step 1: Write the failing test** (`internal/theme/theme_test.go`)

```go
package theme

import "testing"

func TestPaletteValues(t *testing.T) {
	cases := map[string]string{
		Blue: "#89b4fa", Green: "#a6e3a1", Mauve: "#cba6f7", Peach: "#fab387",
		Red: "#f38ba8", Overlay0: "#6c7086", Mantle: "#181825", Surface1: "#45475a",
		Base: "#1e1e2e", Text: "#cdd6f4",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("palette const = %q, want %q", got, want)
		}
	}
}

func TestDarken(t *testing.T) {
	// darken scales toward black; 0 = unchanged, 1 = black.
	if got := Darken("#ffffff", 0.0); got != "#ffffff" {
		t.Errorf("Darken 0 = %q", got)
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/theme/`
Expected: FAIL — package/symbols don't exist.

- [ ] **Step 3: Implement `internal/theme/theme.go`**

Move the palette + helpers verbatim from `internal/ui/theme.go` (preserve exact behavior). Skeleton:
```go
// Package theme is the single Catppuccin Mocha palette shared by the viewer
// (internal/ui) and the dialog widgets (internal/input).
package theme

const (
	Blue     = "#89b4fa"
	Green    = "#a6e3a1"
	Mauve    = "#cba6f7"
	Peach    = "#fab387"
	Red      = "#f38ba8"
	Overlay0 = "#6c7086"
	Mantle   = "#181825"
	Surface1 = "#45475a"
	Surface0 = "#313244"
	Base     = "#1e1e2e"
	Text     = "#cdd6f4"
	CodeBg   = "#282C41"
)

// ParseHex, Darken, BgANSI — moved verbatim from internal/ui/theme.go.
func ParseHex(hex string) (r, g, b int) { /* existing parseHex body */ }
func Darken(hex string, f float64) string { /* existing darken body */ }
func BgANSI(hex string) string { /* existing bgANSI body */ }
```
(Copy the EXACT existing bodies of `parseHex`/`darken`/`bgANSI` from `internal/ui/theme.go` so behavior is identical.)

- [ ] **Step 4: Dedup the two consumers**

In `internal/ui/theme.go`, replace the moved definitions with aliases (keeps every `colXxx`/`darken(...)` call site unchanged):
```go
import "github.com/Townk/ai-playbook/internal/theme"

const (
	colBlue     = theme.Blue
	colGreen    = theme.Green
	colMauve    = theme.Mauve
	colPeach    = theme.Peach
	colRed      = theme.Red
	colOverlay0 = theme.Overlay0
	colMantle   = theme.Mantle
	colSurface1 = theme.Surface1
	colSurface0 = theme.Surface0
	colBase     = theme.Base
	colText     = theme.Text
	colCodeBg   = theme.CodeBg
)

var (
	parseHex = theme.ParseHex
	darken   = theme.Darken
	bgANSI   = theme.BgANSI
)
```
(Keep any ui-only consts/SGR like `codeBgANSI`/`codeFgANSI`/`mantleBg` as they are — only the shared palette + the 3 helpers move.)
In `internal/input/theme.go`, change `defaultTheme()` to reference `theme.*` for its mirrored values (e.g. `Border: theme.Blue`, `Base: theme.Base`, `Text: theme.Text`, `Muted: theme.Overlay0`, `Rule: theme.Surface0`) instead of hand-typed hex. Leave input-only fields (`FieldBorder`, `ButtonBg`, `Key`, etc.) as-is.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/theme/ ./internal/ui/ ./internal/input/`
Expected: PASS (no value change → existing renders identical).

- [ ] **Step 6: Commit**

```bash
git add internal/theme/theme.go internal/theme/theme_test.go internal/ui/theme.go internal/input/theme.go
git commit -m "refactor(theme): shared internal/theme palette (dedup ui + input)"
```

---

### Task 2: Unified dialog colors

**Files:**
- Modify: `internal/input/frame.go` (`renderFrame` — add the mantle background)
- Modify: `internal/ui/model.go` (help modal border → blue)
- Test: `internal/input/frame_test.go`, `internal/ui/model_test.go` (or the existing help/ask tests)

**Interfaces:**
- Consumes: `theme.Mantle`, `theme.Blue` (Task 1) — referenced via the packages' aliases (`colMantle`, `Theme.Border`).

**Context:** The `internal/input` `renderFrame` (used by the ask overlay, the spawned float, B2b/B3 confirms, the processing float) has the blue rounded border but NO background. The help modal (`internal/ui/model.go`) has the mantle bg but a grey `colSurface1` border. Unify both on mantle bg + blue rounded border.

- [ ] **Step 1: Write the failing tests**

`internal/input/frame_test.go`:
```go
func TestRenderFrame_HasMantleBackground(t *testing.T) {
	out := renderFrame(defaultTheme(), "default", "body", 40)
	// mantle bg SGR = \x1b[48;2;24;24;37m
	if !strings.Contains(out, "\x1b[48;2;24;24;37m") {
		t.Fatalf("renderFrame missing mantle background:\n%q", out)
	}
}
```
(Match `renderFrame`'s real signature — read `internal/input/frame.go`; adjust the call.)
`internal/ui/model_test.go` (help modal border):
```go
func TestHelpModal_BlueBorder(t *testing.T) {
	m := newHelpTestModel(t) // or the existing help-render helper
	v := m.helpView()        // the help modal render fn — match the real name
	if !strings.Contains(v, "#89b4fa") && !strings.Contains(v, "137;180;250") {
		t.Fatalf("help modal border not blue:\n%q", v)
	}
}
```
(Adapt to how the help modal is rendered + how lipgloss emits the border color — assert the blue value in whatever form the render uses; read the help block first.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/input/ -run TestRenderFrame_HasMantleBackground; go test ./internal/ui/ -run TestHelpModal_BlueBorder`
Expected: FAIL.

- [ ] **Step 3: Implement**

In `internal/input/frame.go` `renderFrame`, add the background to the frame style (alongside the existing `RoundedBorder`/`BorderForeground(t.variantColor(...))`):
```go
	style = style.
		Background(lipgloss.Color(theme.Mantle)).
		BorderBackground(lipgloss.Color(theme.Mantle))
```
(Import `internal/theme`; apply to the same style that sets the border. The interior content already renders over it.)
In `internal/ui/model.go`, the help modal block — change its `BorderForeground(colSurface1)` to `BorderForeground(lipgloss.Color(colBlue))` (keep `Background(colMantle)`).

- [ ] **Step 4: Run tests**

Run: `go test ./internal/input/ ./internal/ui/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/input/frame.go internal/ui/model.go internal/input/frame_test.go internal/ui/model_test.go
git commit -m "feat(ui): unify dialog colors — mantle bg + blue rounded border"
```

---

### Task 3: Callout bordered frame

**Files:**
- Modify: `internal/ui/render.go` (`quote()` + glyph constants)
- Test: `internal/ui/render_test.go`

**Interfaces:**
- Consumes: `admonitions` map (accent per type), `darken`/`bgANSI`/`band` (existing), `colOverlay0` fallback.

**Context:** `quote()` (`render.go` ~945-1003) renders a callout as a left bar `▋` (accent fg) over a `band(…, darken(accent,0.20))` background, with an optional header line (icon+title) and wrapped body lines. Replace the left-bar look with the 5-glyph frame: a top border row, content rows with the `▐` left bar, a bottom border row — corners+left in the accent, top/bottom sextants in the callout-bg tone, frame cells on the document bg, content on the darkened-accent bg, no right border.

- [ ] **Step 1: Write the failing test**

```go
func TestQuote_BorderedFrame(t *testing.T) {
	lines := renderMarkdownLines(t, "> [!NOTE]\n> Hello world\n", 30) // helper: render md → []Line
	joined := strings.Join(lineTexts(lines), "\n")
	for _, want := range []string{"🬞", "🬭", "▐", "🬁", "🬂"} {
		if !strings.Contains(joined, want) {
			t.Errorf("callout missing frame glyph %q:\n%s", want, joined)
		}
	}
	// content text sits 1 space after the left bar
	if !strings.Contains(joined, "▐ ") {
		t.Errorf("content not 1 space off the left bar:\n%s", joined)
	}
	// NO right-border glyph (the left-bar set has no mirror on the right)
}

func TestQuote_BareBlockquoteFallback(t *testing.T) {
	lines := renderMarkdownLines(t, "> just a quote\n", 30)
	joined := strings.Join(lineTexts(lines), "\n")
	if !strings.Contains(joined, "▐") { // framed with the fallback accent, no header
		t.Errorf("bare blockquote not framed:\n%s", joined)
	}
}
```
(Use the existing render-test helper that turns markdown into `[]Line` — read `render_test.go` for the real helper; adapt `renderMarkdownLines`/`lineTexts`.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ui/ -run TestQuote_`
Expected: FAIL — no frame glyphs.

- [ ] **Step 3: Implement**

Add glyph constants near the existing render glyphs:
```go
const (
	calloutTL = "\U0001FB1E" // 🬞 top-left corner
	calloutTB = "\U0001FB2D" // 🬭 top border
	calloutCL = "▐"     // ▐ content left border
	calloutBL = "\U0001FB01" // 🬁 bottom-left corner
	calloutBB = "\U0001FB02" // 🬂 bottom border
)
```
Rework `quote()`: keep Steps 1-3 (collect body, detect `[!type]`, pick `color` accent + `bg := bgANSI(darken(color,0.20))`). Then:
- **Top row**: `calloutTL` styled accent-fg (document bg) + `calloutTB` styled in the callout-bg tone (fg = `darken(color,0.20)`, document bg) repeated to `r.width-1`. Append as a `Line`.
- **Content rows** (header if `a != nil`, then each wrapped body line): build `leftBar := <calloutCL accent-fg>` then `band(" "+content, bg, r.width-1)` so the left cell is on document bg (accent) and the rest is the callout bg with text 1 space in. Append each.
- **Bottom row**: `calloutBL` accent-fg + `calloutBB` callout-bg-tone fg repeated to `r.width-1`. Append.
- No right-border glyph; content rows pad to width with the callout bg (the existing `band` width-pad).
Assemble each frame cell with `lipgloss.NewStyle().Foreground(...).Background(...)` (the document bg = terminal default / `colBase`; the callout-bg tone fg = `darken(color,0.20)`), NOT a single `band()` for the border rows (their fg/bg differ per cell). Reuse `band` only for the content portion.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/ui/ -run TestQuote_`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/render.go internal/ui/render_test.go
git commit -m "feat(ui): bordered callout frame (top/left/bottom glyphs, accent corners)"
```

---

### Task 4: List wrapped-text hanging indent

**Files:**
- Modify: `internal/ui/render.go` (`list()` + a hanging-indent emit)
- Test: `internal/ui/render_test.go`

**Interfaces:**
- Consumes: `emitProse` (existing wrap), `lipgloss.Width`.

**Context:** `list()` (`render.go` ~298) passes `marker+itemText` as one blob to `emitProse` (~393), which wraps then pads EVERY line by the same `indent` — so continuations align under the marker, not after it. Add a hanging indent so wrapped lines align with the first text char (continuation indent = `indent + 2 + lipgloss.Width(marker)`).

- [ ] **Step 1: Write the failing test**

```go
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

func TestList_OrderedHangingIndent(t *testing.T) {
	lines := renderMarkdownLines(t, "1. "+strings.Repeat("word ", 12)+"\n", 24)
	txts := lineTexts(lines)
	cont := txts[1]
	lead := len(cont) - len(strings.TrimLeft(cont, " "))
	if lead != 5 { // indent 2 + width("1. ")=3
		t.Errorf("ordered continuation indent = %d, want 5 (after '1. ')", lead)
	}
}
```
(Adapt the base `indent` to what `list()` actually passes — read `list()`; the test asserts continuation == base + marker width, whatever the base is. Fix the expected numbers to the real base indent.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ui/ -run TestList_`
Expected: FAIL — continuation aligns under the marker (lead == base indent, not base+marker).

- [ ] **Step 3: Implement**

Add a hanging-indent emit + use it from `list()`:
```go
// emitHanging wraps s so the FIRST line gets firstIndent and every wrapped
// continuation gets hangIndent (a hanging indent for list items).
func (r *renderer) emitHanging(s string, firstIndent, hangIndent int) {
	w := r.width - firstIndent
	if w < 1 {
		w = 1
	}
	wrapped := lipgloss.NewStyle().Width(w).Render(s)
	for i, ln := range strings.Split(wrapped, "\n") {
		pad := firstIndent
		if i > 0 {
			pad = hangIndent
		}
		r.lines = append(r.lines, Line{Text: strings.Repeat(" ", pad) + ln, Wide: false})
	}
}
```
In `list()`, replace the `r.emitProse(marker+itemText, indent+2)` call with:
```go
	r.emitHanging(marker+itemText, indent+2, indent+2+lipgloss.Width(marker))
```
(Keep the nested-list recursion below it unchanged.)

- [ ] **Step 4: Run tests**

Run: `go test ./internal/ui/ -run TestList_`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/render.go internal/ui/render_test.go
git commit -m "feat(ui): hanging indent for wrapped list items"
```

---

## Self-Review

**Spec coverage:** shared theme + dedup (T1) ✓; unified dialog colors (T2) ✓; callout frame (T3) ✓; list hanging indents — unordered + ordered (T4) ✓.

**Type consistency:** `theme.*` consts/helpers (T1) ↔ the `colXxx`/`darken`/`bgANSI` aliases used by T2-T4; `renderFrame` mantle bg (T2) ↔ `theme.Mantle`; the callout glyph consts (T3) match the spec codepoints; `emitHanging(s, firstIndent, hangIndent)` (T4) ↔ its `list()` call.

**Deferred (NOT this plan):** the new-file block + file-change watcher (the next two follow-ups); assisted-run (ROADMAP Phase 2).

**Open items the implementer must confirm against real code (flagged, not placeheld):**
- T1: copy the EXACT existing `parseHex`/`darken`/`bgANSI` bodies; keep ui-only `codeBgANSI`/`codeFgANSI`/`mantleBg` in `internal/ui`.
- T2: `renderFrame`'s real signature + the help modal's real render fn/block; assert the blue value in whatever form lipgloss emits.
- T3: the real render-test helper (`renderMarkdownLines`/`lineTexts` are sketches); the per-cell border assembly + the document-bg choice (terminal default vs `colBase`).
- T4: the real base `indent` `list()` passes (fix the test's expected leading-space numbers to match); `renderMarkdownLines` helper.
