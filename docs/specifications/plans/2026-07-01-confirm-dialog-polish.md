# Confirm-dialog polish — bg fill + value wrapping (Implementation Plan)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Two fixes to the variable-confirmation dialog (`Confirm / Customize`), exposed by the `--assisted` load-time gate: (1) the dialog body fully paints in the dialog background (no bleed-through), and (2) variables render in an aligned two-column layout with long values wrapping under the value column (hanging indent).

**Diagnosis (grounded):**
- **Bg bleed:** `internal/input/confirm.go:97` renders the prompt (the var list) with a **foreground-only** style — `lipgloss.NewStyle().Foreground(m.theme.Text).Render(m.prompt)` — no `Background`, so its cells reset to the terminal default bg instead of the frame's Mantle (classic lipgloss nested-style bleed). `renderFrame` (`internal/input/frame.go:46-52`) wraps everything in `.Background(theme.Mantle)`, but the inner foreground-only prompt breaks it.
- **No wrap / no alignment:** the dialog is a fixed **57-col** float (`input.FloatWidthDefault`, inner width = `57 − frameBorder(2) − 2·frameHPad(2) = 51`). `confirm.go` renders `m.prompt` **as-is** (no wrap), and `raiseGroupConfirm` (`internal/ui/confirm_gate.go`) builds flat, unaligned lines: `fmt.Fprintf(&b, "  %s = %s\n", v.Name, g.values[v.Name])`. Long values overflow the border.

**Target layout** (your mockup — aligned name column, value wraps with a hanging indent to the value column):
```
DATA_DIR:           $PROJECT_ROOT/data
SOME_LONG_VARIABLE: The value of this variable could
                    span many lines depending on how
                    long it is!
PROJECT_ROOT:       ~/Projects/langs/go/ai-playbook/so
                    me/other/directory
```

**Tech Stack:** Go; `charm.land/lipgloss/v2`; `internal/input` (`confirm.go`, `frame.go`), `internal/ui` (`confirm_gate.go`), `internal/theme`.

## Global Constraints

- Module `github.com/Townk/ai-playbook`. gpg-signed Conventional Commits (NEVER `--no-gpg-sign`; if signing times out STOP + report BLOCKED — user re-unlocks with `! echo x | gpg --clearsign`); verify `%G?`==`G`. NO `Co-Authored-By`/AI-attribution trailers. `git add` explicit paths only.
- **Repo at `~/Projects/langs/go/ai-playbook`**.
- Do not change dialog GEOMETRY (still `FloatWidthDefault` = 57) or the gate's behavior — this is presentation only (the var list's text + the prompt's bg).
- Gates: `gofmt -l`, `go build ./...`, `go vet ./...`, `ineffassign@v0.2.0` (clean), `go test` on touched packages.

---

### Task 1: aligned two-column value wrapping

**Files:**
- Modify: `internal/ui/confirm_gate.go` (`raiseGroupConfirm` builds the aligned/wrapped body via a new pure helper `formatConfirmVars`), `internal/input/frame.go` or `input.go` (expose the dialog inner width)
- Test: `internal/ui/confirm_gate_test.go` (or the existing confirm-gate test file)

**Interfaces:**
- Produces: `func input.AskInnerWidth() int` (= `FloatWidthDefault − frameBorder − 2*frameHPad`, using the unexported consts in `internal/input`); `func formatConfirmVars(names []string, values map[string]string, innerW int) string` (pure; the aligned/wrapped var block).

**Context:**
- **`input.AskInnerWidth()`** (new exported fn in `internal/input`, e.g. in `frame.go`): `return FloatWidthDefault - frameBorder - 2*frameHPad` (= 51). This lets the `ui` side wrap to the exact dialog inner width without magic numbers.
- **`formatConfirmVars(names, values, innerW)`** (new pure fn in `internal/ui/confirm_gate.go`) — builds the var block:
  - `nameW := max over names of lipgloss.Width(name)`.
  - `valueCol := nameW + 2` (name + `":"` + one space). Values start at `valueCol`.
  - `avail := innerW - valueCol; if avail < 8 { avail = 8 }` (floor so tiny widths still wrap sanely).
  - For each name (in order): the label = `name + ":"` padded with spaces to `valueCol` (so all values align). Wrap `values[name]` to `avail` — break on spaces; **hard-break a token longer than `avail`** (mid-word, as in the `PROJECT_ROOT` path example). The first wrapped line follows the label; each continuation line is `strings.Repeat(" ", valueCol)` + the wrapped chunk.
  - Join lines with `"\n"`; return the block (no trailing newline).
  - (A small local word-wrap-with-hard-break helper is fine; do NOT pull a new dependency — `lipgloss`/`strings` only.)
- **`raiseGroupConfirm`** (`confirm_gate.go`): replace the `for … fmt.Fprintf(&b, "  %s = %s\n", …)` loop with `formatConfirmVars(names, g.values, input.AskInnerWidth())` where `names` is the current group's var names in order. Keep the leading `"Confirm these variables for this run:\n\n"` header, then the formatted block. (The header line is short — it renders fine.)

- [ ] **Step 1: Write the failing tests** (pure `formatConfirmVars`):

```go
func TestFormatConfirmVars_AlignsAndWraps(t *testing.T) {
	names := []string{"DATA_DIR", "SOME_LONG_VARIABLE", "PROJECT_ROOT"}
	vals := map[string]string{
		"DATA_DIR":           "$PROJECT_ROOT/data",
		"SOME_LONG_VARIABLE": "The value of this variable could span many lines depending on how long it is!",
		"PROJECT_ROOT":       "~/Projects/langs/go/ai-playbook/some/other/directory",
	}
	out := formatConfirmVars(names, vals, 51)
	lines := strings.Split(out, "\n")
	// value column = len("SOME_LONG_VARIABLE")=18 + 2 = 20
	// short value fits on the label line, aligned at col 20:
	if !strings.HasPrefix(lines[0], "DATA_DIR:") { t.Fatalf("line0 = %q", lines[0]) }
	if !strings.Contains(lines[0], "$PROJECT_ROOT/data") { t.Fatalf("short value must sit on the label line: %q", lines[0]) }
	// the label is padded so the value starts at col 20 (18 + colon + space):
	if idx := strings.Index(lines[0], "$PROJECT_ROOT/data"); idx != 20 {
		t.Errorf("value column = %d, want 20", idx)
	}
	// the long value wraps: a continuation line is indented to col 20 (all spaces before it):
	var cont string
	for _, l := range lines[1:] {
		if strings.TrimSpace(l) != "" && strings.HasPrefix(l, strings.Repeat(" ", 20)) { cont = l; break }
	}
	if cont == "" { t.Fatal("expected a continuation line indented to the value column") }
	// no rendered line exceeds the inner width:
	for _, l := range lines { if lipgloss.Width(l) > 51 { t.Errorf("line exceeds innerW: %q (%d)", l, lipgloss.Width(l)) } }
}

func TestFormatConfirmVars_HardBreaksLongToken(t *testing.T) {
	// a single unbreakable token longer than the available width must char-break, not overflow.
	out := formatConfirmVars([]string{"P"}, map[string]string{"P": strings.Repeat("x", 200)}, 51)
	for _, l := range strings.Split(out, "\n") {
		if lipgloss.Width(l) > 51 { t.Fatalf("long token must hard-break; line width %d: %q", lipgloss.Width(l), l) }
	}
}
```

- [ ] **Step 2: Run to verify they fail** — `cd ~/Projects/langs/go/ai-playbook && go test ./internal/ui/ -run FormatConfirmVars` → FAIL.
- [ ] **Step 3: Implement** `input.AskInnerWidth`, `formatConfirmVars`, and wire it into `raiseGroupConfirm`.
- [ ] **Step 4: Run to verify they pass** — those tests; `go test ./internal/ui/ -run 'Confirm|Gate|Assisted'` (gate behavior unchanged); `go build ./...`; `go test ./internal/input/`.
- [ ] **Step 5: Commit** — `git add internal/ui/confirm_gate.go internal/input/frame.go internal/ui/confirm_gate_test.go && git commit -m "feat(ui): aligned + wrapped variable list in the confirm dialog"`

---

### Task 2: paint the confirm-dialog body background

**Files:**
- Modify: `internal/input/confirm.go` (the prompt section style) + any sibling section render with the same foreground-only-inside-Mantle-frame bleed on the confirm/customize path
- Test: `internal/input/confirm_test.go` (or add one)

**Interfaces:** none new — a styling fix.

**Context:** `internal/input/confirm.go:97` renders the prompt with `lipgloss.NewStyle().Foreground(m.theme.Text).Render(m.prompt)` — add `.Background(lipgloss.Color(theme.Mantle))` (import `internal/theme` if not already) so the prompt cells carry the dialog bg and don't reset to the terminal default. This fixes the reported bleed for every confirm/customize dialog (the var gate is the "confirm" variant; the per-var edit is the "line"/text variant — CHECK the text/line field's prompt/label render (`field_text.go`/`field.go`) for the same foreground-only pattern inside the Mantle frame and apply the same `.Background(theme.Mantle)` there if it bleeds). Do NOT change `renderFrame` or the geometry; only the section text styles that were missing the bg.

- [ ] **Step 1: Write the failing test** — render a confirm dialog and assert the prompt line carries the Mantle bg SGR:

```go
func TestConfirmDialog_PromptHasDialogBackground(t *testing.T) {
	// build a confirm model with a prompt, render it, and assert the Mantle bg SGR appears
	// on the prompt content (not only on the frame padding). Mantle = theme.Mantle (#181825).
	// Use the existing confirm-model test constructor; render via View()/render(); strip to the
	// prompt line; assert it contains the 48;2;<r>;<g>;<b> background sequence for Mantle
	// (or, more robustly, assert the rendered prompt does NOT contain a bare reset that drops
	// to default bg before end-of-line). Mirror how other input render tests inspect SGR.
}
```
(Adapt to the input package's existing render-test helpers; if asserting raw SGR is brittle, assert the style used for the prompt has a non-empty Background — e.g. factor the prompt style into a small `func promptStyle(t Theme) lipgloss.Style` and test that `promptStyle(...).GetBackground()` is Mantle.)

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/input/ -run PromptHasDialogBackground` → FAIL.
- [ ] **Step 3: Implement** the `.Background(theme.Mantle)` on the confirm prompt (+ the line/text prompt if it bleeds).
- [ ] **Step 4: Run to verify it passes** — that test; `go test ./internal/input/`; `go build ./...`.
- [ ] **Step 5: Commit** — `git add internal/input/confirm.go internal/input/confirm_test.go && git commit -m "fix(input): paint the confirm-dialog prompt on the dialog background (no bleed)"` (add `field_text.go`/`field.go` to the stage if you fixed the line-variant prompt).

---

## Final verification (after all tasks)

- [ ] `cd ~/Projects/langs/go/ai-playbook && gofmt -l internal/ui internal/input` empty; `go build ./... && go vet ./...` clean; `ineffassign@v0.2.0 ./...` clean; `go test ./internal/ui/ ./internal/input/` PASS.
- [ ] `go install ./cmd/ai-playbook`, then **live-verify** (TTY): `ai-playbook run --assisted --file examples/06-portable-and-env.md` → the confirm dialog's body is **fully painted** in the dialog bg (no terminal-bg bleed behind the var list), and the variables are **aligned** with the value column, long values **wrapping** with a hanging indent (matches the target mockup). The Customize per-var edit is likewise fully painted.

## Self-review notes (coverage vs diagnosis)

- Bg bleed (confirm.go:97 foreground-only) → Task 2 (`.Background(theme.Mantle)`, + the line variant). Wrap/align (raiseGroupConfirm flat lines; no wrap in confirm.go) → Task 1 (`formatConfirmVars` at `input.AskInnerWidth()`). Geometry + gate behavior unchanged. General audit of OTHER dialog variants' bg (choose/form) is out of scope — a follow-up if they show the same bleed.
