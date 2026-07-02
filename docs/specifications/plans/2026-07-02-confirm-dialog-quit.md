# Confirm-dialog — button-gap fill, Quit button, ESC-quits (Implementation Plan)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Three fixes to the variable-confirmation gate dialog: (1) paint the inter-button gap on the dialog background (no bleed), (2) add an optional third `[ Quit ]` button, and (3) make ESC and Quit end the run — while ESC inside a Customize var-edit steps back to the confirm dialog.

**Architecture:** `internal/input`'s `confirmField` gains an *optional* tertiary button (empty label ⇒ unchanged two-button behavior for every existing caller). A new `(*Ask).WithTertiaryButton` opt-in wires it from the gate. `internal/ui`'s `advanceGate` routes the tertiary `"quit"` value and confirm-phase ESC to `tea.Quit`, and edit-phase ESC back to the group's confirm dialog.

**Tech Stack:** Go; `charm.land/bubbletea/v2`; `charm.land/lipgloss/v2`; `internal/theme` (`theme.Mantle`).

## Global Constraints

- Module `github.com/Townk/ai-playbook`. Repo at `~/Projects/langs/go/ai-playbook` (the Bash cwd is elsewhere — always `cd ~/Projects/langs/go/ai-playbook` / `git -C …`).
- gpg-signed Conventional Commits (NEVER `--no-gpg-sign`; if signing times out STOP and report BLOCKED — the user re-unlocks with `! echo x | gpg --clearsign`); verify `git log -1 --format=%G?` == `G`. NO `Co-Authored-By` / AI-attribution trailers. `git add` explicit paths only. Commit only (do not push).
- Branch `feat/confirm-dialog-polish` (already checked out; Tasks 1–2 of the prior plan are merged into it).
- Presentation/behavior only — do NOT change dialog geometry (`FloatWidthDefault = 57`) or the value-wrap / bg-fill code already on this branch.
- `NewAsk`'s signature stays as-is (every existing confirm dialog stays two-button and unchanged). The tertiary button is opt-in via `WithTertiaryButton`.
- ESC / Quit / Ctrl-C end the run with the model's default exit code (0), matching assisted's Quit button — no new exit-code taxonomy.
- Gates: `gofmt -l`, `go build ./...`, `go vet ./...`, `ineffassign@v0.2.0` (clean), `go test` on touched packages.

---

### Task 1: confirm field — Mantle-painted gaps + optional third button

**Files:**
- Modify: `internal/input/field_confirm.go` (struct, `handle`, `view`, `value`, `hint`; new `buttonCount`/`focusValue` helpers)
- Modify: `internal/input/confirm_keys.go` (`actTertiary`, split Tab/Shift-Tab, `resolveConfirmKey` signature, `deriveTertiaryKey`)
- Modify: `internal/input/ask.go` (new `WithTertiaryButton`)
- Test: `internal/input/field_confirm_test.go`, `internal/input/confirm_keys_test.go`

**Interfaces:**
- Produces (used by Task 2): `func (a *Ask) WithTertiaryButton(label string) *Ask` — chainable; when the underlying field is a confirm field it adds a third button whose selection makes `value()` return `"quit"`.
- Internal: `confirmField.value()` returns `"yes"` | `"no"` | `"quit"`.

**Context — the bleed:** `field_confirm.go:111` currently joins the two buttons with a plain `"    "`; after each button's own background + SGR reset, those cells fall back to the terminal default. Paint the gap with the same `theme.Mantle` fill `promptStyle` uses (`internal/input/theme.go:106`). `field_confirm.go` must add the import `"github.com/Townk/ai-playbook/internal/theme"`. (The `theme Theme` *parameter* of `newConfirmField` shadows the package name only inside that function; `view`/`hint` reference the package `theme.Mantle` freely.)

**Context — backward compatibility:** `newConfirmField`'s signature is unchanged; the new `tertiary`/`terKey` fields default to their zero values (`""`/`0`), so `buttonCount()` returns 2 and the field behaves exactly as today until `WithTertiaryButton` sets a label. `resolveConfirmKey` gains a `terKey rune` parameter — update its sole non-test caller (`field_confirm.go` `handle`) and every call in `confirm_keys_test.go` (pass `0` where no tertiary applies). `actToggle` is replaced by `actToggleNext`/`actTogglePrev`; update any reference in `confirm_keys_test.go`.

- [ ] **Step 1: Write the failing tests**

In `internal/input/field_confirm_test.go` add:

```go
func TestConfirmField_GapPaintedOnMantle(t *testing.T) {
	f := newConfirmField(defaultTheme(), "default", "Confirm", "Customize", false)
	out := f.view(51, true)
	// The 4-space inter-button gap must carry the Mantle background SGR, not reset
	// to the terminal default. Mantle's truecolor bg sequence:
	mantle := lipgloss.NewStyle().Background(lipgloss.Color(theme.Mantle)).Render("    ")
	if !strings.Contains(out, mantle) {
		t.Fatalf("button gap is not painted on the Mantle background:\n%q", out)
	}
}

func TestConfirmField_TertiaryButtonAndValue(t *testing.T) {
	f := newConfirmField(defaultTheme(), "default", "Confirm", "Customize", false)
	// Two-button by default:
	if got := f.buttonCount(); got != 2 {
		t.Fatalf("buttonCount default = %d, want 2", got)
	}
	// Opt in to the third button via the Ask wrapper (mirrors how the gate wires it):
	a := &Ask{m: model{fld: f}}
	a.WithTertiaryButton("Quit")
	cf := a.m.fld.(*confirmField)
	if got := cf.buttonCount(); got != 3 {
		t.Fatalf("buttonCount with tertiary = %d, want 3", got)
	}
	if !strings.Contains(cf.view(51, true), "Quit") {
		t.Fatal("three-button view must render the Quit label")
	}
	// The 'q' accelerator selects Quit:
	nf, act, _ := cf.handle(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if act != fieldDone {
		t.Fatalf("q accelerator action = %v, want fieldDone", act)
	}
	if got := nf.value(); got != "quit" {
		t.Fatalf("value after Quit = %q, want \"quit\"", got)
	}
}

func TestConfirmField_FocusCyclesThroughThree(t *testing.T) {
	f := newConfirmField(defaultTheme(), "default", "Confirm", "Customize", false)
	a := &Ask{m: model{fld: f}}
	a.WithTertiaryButton("Quit")
	cf := a.m.fld.(*confirmField)
	// Tab cycles 0 -> 1 -> 2 -> 0.
	step := func(fld field) field {
		nf, _, _ := fld.handle(tea.KeyPressMsg{Code: tea.KeyTab})
		return nf
	}
	var cur field = cf
	for want := 1; want <= 3; want++ {
		cur = step(cur)
		got := cur.(*confirmField).focus
		if got != want%3 {
			t.Fatalf("after %d tabs focus = %d, want %d", want, got, want%3)
		}
	}
	// Right arrow clamps at the last button (does not wrap).
	cur.(*confirmField).focus = 2
	nf, _, _ := cur.handle(tea.KeyPressMsg{Code: tea.KeyRight})
	if got := nf.(*confirmField).focus; got != 2 {
		t.Fatalf("right arrow at end focus = %d, want 2 (clamped)", got)
	}
}
```

In `internal/input/confirm_keys_test.go` add:

```go
func TestResolveConfirmKey_Tertiary(t *testing.T) {
	// terKey 'q' resolves to actTertiary; Tab/Shift-Tab map to the directional actions.
	if got := resolveConfirmKey("q", 'y', 'n', 'q'); got != actTertiary {
		t.Errorf("q -> %v, want actTertiary", got)
	}
	if got := resolveConfirmKey("tab", 'y', 'n', 'q'); got != actToggleNext {
		t.Errorf("tab -> %v, want actToggleNext", got)
	}
	if got := resolveConfirmKey("shift+tab", 'y', 'n', 'q'); got != actTogglePrev {
		t.Errorf("shift+tab -> %v, want actTogglePrev", got)
	}
	// With no tertiary (terKey 0), 'q' is inert.
	if got := resolveConfirmKey("q", 'y', 'n', 0); got != actNone {
		t.Errorf("q with no tertiary -> %v, want actNone", got)
	}
}
```

- [ ] **Step 2: Run to verify they fail** — `cd ~/Projects/langs/go/ai-playbook && go test ./internal/input/ -run 'ConfirmField_Gap|ConfirmField_Tertiary|ConfirmField_FocusCycles|ResolveConfirmKey_Tertiary'` → FAIL / build error (new symbols undefined).

- [ ] **Step 3: Implement**

`internal/input/confirm_keys.go` — replace the action constants and `resolveConfirmKey`, add `deriveTertiaryKey`:

```go
const (
	actNone confirmAction = iota
	actAffirm
	actNegate
	actTertiary
	actSubmit
	actFocusLeft
	actFocusRight
	actToggleNext
	actTogglePrev
	actCancel
)
```

```go
// deriveTertiaryKey returns the label's lowercased first rune as an accelerator,
// unless it is empty or collides with either resolved primary accelerator, in
// which case it returns 0 (no accelerator — the button stays reachable via
// arrows/Tab/mouse).
func deriveTertiaryKey(label string, affKey, negKey rune) rune {
	k := firstLower(label)
	if k == 0 || k == affKey || k == negKey {
		return 0
	}
	return k
}
```

```go
func resolveConfirmKey(key string, affKey, negKey, terKey rune) confirmAction {
	switch key {
	case "enter":
		return actSubmit
	case "esc", "ctrl+c":
		return actCancel
	case "tab":
		return actToggleNext
	case "shift+tab":
		return actTogglePrev
	case "left":
		return actFocusLeft
	case "right":
		return actFocusRight
	}
	r := []rune(key)
	if len(r) != 1 {
		return actNone
	}
	c := unicode.ToLower(r[0])
	switch {
	case c == affKey:
		return actAffirm
	case c == negKey:
		return actNegate
	case terKey != 0 && c == terKey:
		return actTertiary
	case c == 'y' && negKey != 'y':
		return actAffirm
	case c == 'n' && affKey != 'n':
		return actNegate
	}
	return actNone
}
```

`internal/input/field_confirm.go` — add the import, struct fields, helpers, and rewrite `handle`/`view`/`value`/`hint`:

```go
import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Townk/ai-playbook/internal/theme"
)
```

Struct — add `tertiary string`, `terKey rune` (and widen the `focus`/`accepted_v` doc comments to include the tertiary):

```go
type confirmField struct {
	theme       Theme
	variant     string
	affirmative string
	negative    string
	tertiary    string // "" = two-button (default); non-empty adds a third button
	affKey      rune
	negKey      rune
	terKey      rune
	focus       int    // 0 = affirmative, 1 = negative, 2 = tertiary
	accepted    bool
	accepted_v  string // "yes" | "no" | "quit"
}
```

Helpers:

```go
// buttonCount is 2 by default, 3 when a tertiary label is set.
func (f *confirmField) buttonCount() int {
	if f.tertiary != "" {
		return 3
	}
	return 2
}

// focusValue maps a focus index to its submit value.
func focusValue(focus int) string {
	switch focus {
	case 0:
		return "yes"
	case 1:
		return "no"
	default:
		return "quit"
	}
}
```

`handle`:

```go
func (f *confirmField) handle(msg tea.Msg) (field, fieldAction, tea.Cmd) {
	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return f, fieldNone, nil
	}
	n := f.buttonCount()
	switch resolveConfirmKey(confirmKeyString(kp), f.affKey, f.negKey, f.terKey) {
	case actAffirm:
		c := *f
		c.accepted = true
		c.accepted_v = "yes"
		return &c, fieldDone, nil
	case actNegate:
		c := *f
		c.accepted = true
		c.accepted_v = "no"
		return &c, fieldDone, nil
	case actTertiary:
		if f.tertiary == "" {
			return f, fieldNone, nil
		}
		c := *f
		c.accepted = true
		c.accepted_v = "quit"
		return &c, fieldDone, nil
	case actSubmit:
		c := *f
		c.accepted = true
		c.accepted_v = focusValue(f.focus)
		return &c, fieldDone, nil
	case actFocusLeft:
		c := *f
		if c.focus > 0 {
			c.focus--
		}
		return &c, fieldNone, nil
	case actFocusRight:
		c := *f
		if c.focus < n-1 {
			c.focus++
		}
		return &c, fieldNone, nil
	case actToggleNext:
		c := *f
		c.focus = (f.focus + 1) % n
		return &c, fieldNone, nil
	case actTogglePrev:
		c := *f
		c.focus = (f.focus - 1 + n) % n
		return &c, fieldNone, nil
	case actCancel:
		return f, fieldCancel, nil
	}
	return f, fieldNone, nil
}
```

`view` (Mantle-painted gap; render the third button only when set):

```go
func (f *confirmField) view(innerW int, focused bool) string {
	gap := lipgloss.NewStyle().Background(lipgloss.Color(theme.Mantle)).Render("    ")
	btns := []string{
		f.button(f.affirmative, focused && f.focus == 0),
		f.button(f.negative, focused && f.focus == 1),
	}
	if f.tertiary != "" {
		btns = append(btns, f.button(f.tertiary, focused && f.focus == 2))
	}
	return strings.Join(btns, gap)
}
```

`value`:

```go
func (f *confirmField) value() string {
	if f.accepted {
		return f.accepted_v
	}
	return focusValue(f.focus)
}
```

`hint` (append the tertiary segment when present and it has an accelerator):

```go
func (f *confirmField) hint() string {
	key := lipgloss.NewStyle().Foreground(lipgloss.Color(f.theme.Key))
	word := lipgloss.NewStyle().Foreground(lipgloss.Color(f.theme.Muted))
	seg := func(k, w string) string { return key.Render(k) + word.Render(" "+w) }
	sep := word.Render(" · ")
	segs := []string{
		seg("󱊷", "dismiss"),
		seg(string(f.affKey), strings.ToLower(f.affirmative)),
		seg(string(f.negKey), strings.ToLower(f.negative)),
	}
	if f.tertiary != "" && f.terKey != 0 {
		segs = append(segs, seg(string(f.terKey), strings.ToLower(f.tertiary)))
	}
	return strings.Join(segs, sep)
}
```

`internal/input/ask.go` — add the chainable setter (after `NewAsk`):

```go
// WithTertiaryButton adds a third button (e.g. "Quit") to a confirm dialog.
// Selecting it makes the dialog's value() return "quit". It is a no-op for
// non-confirm dialogs. Chainable so callers can write
// NewAsk(...).WithTertiaryButton("Quit").
func (a *Ask) WithTertiaryButton(label string) *Ask {
	if cf, ok := a.m.fld.(*confirmField); ok {
		cf.tertiary = label
		cf.terKey = deriveTertiaryKey(label, cf.affKey, cf.negKey)
	}
	return a
}
```

Then fix the compile breaks: update `field_confirm.go`'s `handle` call to `resolveConfirmKey` (done above, 4 args) and every `resolveConfirmKey(...)` call plus any `actToggle` reference in `confirm_keys_test.go` (pass `0` for `terKey`; `actToggle` → `actToggleNext`/`actTogglePrev` as the case intends).

- [ ] **Step 4: Run to verify they pass** — `go test ./internal/input/ -run 'ConfirmField_Gap|ConfirmField_Tertiary|ConfirmField_FocusCycles|ResolveConfirmKey_Tertiary'` → PASS; then the full package: `go test ./internal/input/` → PASS; `go build ./...`; `go vet ./...`.

- [ ] **Step 5: Commit**

```bash
cd ~/Projects/langs/go/ai-playbook && git add internal/input/field_confirm.go internal/input/confirm_keys.go internal/input/ask.go internal/input/field_confirm_test.go internal/input/confirm_keys_test.go && git commit -m "feat(input): optional third confirm button + Mantle-painted button gap"
```

Verify: `git log -1 --format=%G?` == `G`.

---

### Task 2: gate wiring — ESC / Quit end the run

**Files:**
- Modify: `internal/ui/confirm_gate.go` (`raiseGroupConfirm`, `advanceGate`)
- Test: `internal/ui/confirm_gate_test.go`

**Interfaces:**
- Consumes: `input.NewAsk(...).WithTertiaryButton("Quit")` (Task 1); the confirm dialog now yields `value == "quit"` when Quit is chosen.

**Context:** `m.gate` is a pointer (`g := m.gate` aliases it; `g.gi++`/`g.ci++` mutations persist into the returned model). `advanceGate` (`confirm_gate.go:301`) is entered for BOTH the confirm dialog and the per-var edit; `g.customizing` distinguishes them. The quit path mirrors assisted's Quit button (`internal/ui/assisted.go:366` returns `tea.Quit`) — clear the gate and return `tea.Quit` (default exit code 0). `tea` is already imported.

**Existing tests to update:** `confirm_gate_test.go` has ESC-path assertions of the OLD dismiss behavior at ~line 178 and ~line 251 (`m.advanceGate("", false)`). Read them and update to the new semantics: confirm-phase ESC now returns a `tea.Quit` command and clears the gate; if either existing case is actually an edit-phase ESC, update it to expect a re-raised confirm dialog (gate intact). Do not weaken unrelated assertions.

- [ ] **Step 1: Write the failing tests** — add to `internal/ui/confirm_gate_test.go` (adapt the gate-construction preamble to the file's existing helpers for building a `model` with a live `m.gate` in the confirm phase and, separately, in the customizing phase):

```go
func TestAdvanceGate_QuitButtonQuitsRun(t *testing.T) {
	m := newGateModelInConfirmPhase(t) // existing/local helper: gate live, confirm phase
	m, cmd := m.advanceGate("quit", true)
	if m.gate != nil {
		t.Fatal("Quit must clear the gate")
	}
	if !isQuitCmd(cmd) {
		t.Fatal("Quit must return a tea.Quit command")
	}
}

func TestAdvanceGate_ConfirmEscQuitsRun(t *testing.T) {
	m := newGateModelInConfirmPhase(t)
	m, cmd := m.advanceGate("", false) // ESC on the confirm dialog
	if m.gate != nil {
		t.Fatal("confirm-phase ESC must clear the gate")
	}
	if !isQuitCmd(cmd) {
		t.Fatal("confirm-phase ESC must return a tea.Quit command")
	}
}

func TestAdvanceGate_EditEscReturnsToConfirm(t *testing.T) {
	m := newGateModelInCustomizePhase(t) // existing/local helper: gate live, customizing
	m, cmd := m.advanceGate("", false) // ESC while editing a var
	if m.gate == nil {
		t.Fatal("edit-phase ESC must keep the gate (back to confirm, not quit)")
	}
	if m.gate.customizing {
		t.Fatal("edit-phase ESC must leave the customizing phase")
	}
	if isQuitCmd(cmd) {
		t.Fatal("edit-phase ESC must not quit the run")
	}
	if !m.askMode { // re-raised the confirm dialog
		t.Fatal("edit-phase ESC must re-raise the confirm dialog")
	}
}

// isQuitCmd reports whether cmd, when invoked, yields tea.QuitMsg.
func isQuitCmd(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}
```

(If the test file already provides model-construction helpers, reuse them rather than adding `newGateModelIn*Phase`; the intent is one confirm-phase model and one customizing-phase model. Only add `isQuitCmd` if no equivalent exists.)

- [ ] **Step 2: Run to verify they fail** — `go test ./internal/ui/ -run 'AdvanceGate_QuitButton|AdvanceGate_ConfirmEsc|AdvanceGate_EditEsc'` → FAIL.

- [ ] **Step 3: Implement**

In `raiseGroupConfirm` (`confirm_gate.go`), opt into the Quit button:

```go
	m.ask = input.NewAsk("Variables", b.String(), "", "confirm", nil, "Confirm", "Customize").WithTertiaryButton("Quit")
```

In `advanceGate`, replace the `!submitted` block and add the `"quit"` branch:

```go
	if !submitted {
		if g.customizing { // ESC during a per-var edit → back to the group's confirm
			g.customizing = false
			g.ci = 0
			return m.raiseGroupConfirm()
		}
		m.gate = nil // ESC on the confirm dialog → quit the run
		return m, tea.Quit
	}
	if g.customizing {
		g.values[g.groups[g.gi][g.ci].Name] = value
		g.ci++
		if g.ci < len(g.groups[g.gi]) {
			return m.raiseVarEdit()
		}
		g.customizing = false
		g.ci = 0
		g.gi++
		return m.afterGroup()
	}
	// confirm phase
	if value == "quit" { // Quit button → quit the run
		m.gate = nil
		return m, tea.Quit
	}
	if value == "no" { // Customize → edit this group's vars
		g.customizing = true
		g.ci = 0
		return m.raiseVarEdit()
	}
	g.gi++ // Confirm → next group
	return m.afterGroup()
```

Update the `advanceGate` doc comment to describe the new ESC/Quit semantics.

- [ ] **Step 4: Run to verify they pass** — `go test ./internal/ui/ -run 'AdvanceGate'` → PASS; then `go test ./internal/ui/ -run 'Confirm|Gate|Assisted'` (gate flow intact); `go build ./...`.

- [ ] **Step 5: Commit**

```bash
cd ~/Projects/langs/go/ai-playbook && git add internal/ui/confirm_gate.go internal/ui/confirm_gate_test.go && git commit -m "feat(ui): confirm-gate Quit button and ESC end the run"
```

Verify: `git log -1 --format=%G?` == `G`.

---

## Final verification (after both tasks)

- [ ] `cd ~/Projects/langs/go/ai-playbook && gofmt -l internal/ui internal/input` empty; `go build ./... && go vet ./...` clean; `go run github.com/gordonklaus/ineffassign@v0.2.0 ./...` clean; `go test ./internal/ui/ ./internal/input/` PASS.
- [ ] `go install ./cmd/ai-playbook`, then **live-verify** (TTY): `ai-playbook run --assisted --file examples/06-portable-and-env.md` →
  - the confirm dialog shows **`[ Confirm ] [ Customize ] [ Quit ]`** with the gaps fully painted in the dialog background (no bleed);
  - **←/→** and **Tab** move focus across all three; **`q`** and the **Quit** button both exit the run cleanly; **ESC** also exits the run;
  - choosing **Customize** then pressing **ESC** mid-edit returns to the confirm dialog rather than quitting.

## Self-review notes (coverage vs spec)

- Gap bleed → Task 1 `view` Mantle gap. Third button → Task 1 (`tertiary`/`terKey`, `handle`, `view`, `value`, `hint`, `WithTertiaryButton`). ESC/Quit end the run + edit-ESC back-nav → Task 2 `advanceGate`. Backward compatibility → `newConfirmField` unchanged, tertiary defaults empty, `NewAsk` signature unchanged. `choose.go` / text-box interior bleed remain out of scope (separate surfaces).
