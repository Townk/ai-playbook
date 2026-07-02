# Confirm-dialog — button-gap fill, Quit button, ESC-quits (Design)

**Status:** approved (2026-07-02)
**Branch:** `feat/confirm-dialog-polish` (follow-on to the bg-fill + value-wrap work)

## Problem

Live testing of the variable-confirmation gate (`ai-playbook run --assisted --file examples/06-portable-and-env.md`) surfaced three defects in the two-button `[ Confirm ] [ Customize ]` dialog:

1. **Button-gap bleed.** The gap between the buttons renders on the terminal's
   default background, not the dialog's Mantle fill. Root cause: `internal/input/field_confirm.go:111` joins the buttons with a plain, unstyled `"    "` — after each button emits its own background + SGR reset, those gap cells fall back to the default background (the same nested-style bleed Task 2 fixed for the prompt body, here on the button row).

2. **ESC leaves a half-open state.** ESC (and Ctrl-C) on the confirm dialog maps
   to `fieldCancel` → `advanceGate(!submitted)` → the gate is dropped and the
   viewer returns to reading with the gate *unsatisfied*. In `--assisted`, where
   the gate fires at load, that strands the session with no confirmed variables
   and no footer.

3. **No discoverable quit.** The only way out of the gate is an undocumented ESC;
   there is no visible control for "I don't want to run this."

## Goals

- The confirm dialog's button row is fully painted in the dialog background — no
  gap bleed, with two **or** three buttons.
- ESC on the confirm dialog **quits the run** cleanly (the same `tea.Quit` path
  assisted's existing Quit button uses).
- A third **`[ Quit ]`** button makes quitting discoverable; it quits via the
  same path as ESC.
- ESC while *editing* a variable (Customize) steps **back to the confirm dialog**
  (cancels just the edit), not quit.
- Backward-compatible: every other confirm dialog in the app stays two-button and
  unchanged.

## Non-goals

- No change to dialog geometry (`FloatWidthDefault = 57`) or the value-wrap /
  bg-fill work already merged on this branch.
- No new exit-code taxonomy: ESC / Quit / Ctrl-C all end the run with the
  process's existing default (0), matching assisted's Quit button. (A distinct
  non-zero "aborted" code is a possible future refinement, explicitly deferred.)
- `choose.go` and the text-input box interior share the same bleed pattern but
  are out of scope (separate surfaces, noted as follow-ups).

## Design

### Confirm field — Mantle-painted gaps + optional third button

`internal/input/field_confirm.go` (`confirmField`) gains an **optional** tertiary
button. When its label is empty (the default for every existing caller) the field
renders and behaves exactly as today (two buttons). When set, it renders a third
button and participates in focus/selection.

- **Struct:** add `tertiary string` and `terKey rune`.
- **Gaps:** `view` joins buttons with a gap explicitly painted on the dialog
  background (`lipgloss.NewStyle().Background(lipgloss.Color(theme.Mantle))`),
  the same `theme.Mantle` `promptStyle` uses. This fixes the bleed for both the
  two- and three-button layouts.
- **Focus:** `focus` ranges over `0..n-1` (`n` = 2 or 3). ←/→ move within the row
  (clamped at the ends); Tab / Shift-Tab cycle (wrap). For `n == 2` this is
  identical to today's behavior.
- **Selection:** Enter selects the focused button; the tertiary accelerator
  selects Quit directly. `value()` returns `"yes"` (affirmative), `"no"`
  (negative), or `"quit"` (tertiary).
- **Accelerator:** the tertiary key is the label's first letter, lowercased, only
  if it collides with neither resolved accelerator (for `"Quit"` → `q`, which
  does not clash with the `y`/`n` the colliding `Confirm`/`Customize` labels
  already fall back to). On collision → no accelerator (still reachable by
  arrows / Tab / mouse).
- **Hint:** when a tertiary is set, its accelerator + label join the hint row.

`internal/input/confirm_keys.go`: add an `actTertiary` action; `resolveConfirmKey`
takes the tertiary key and returns `actTertiary` for it; Tab and Shift-Tab map to
distinct forward/back cycle actions (previously both mapped to a single toggle —
a 2-state flip, unchanged for two buttons).

`internal/input/ask.go`: a new `(*Ask) WithTertiaryButton(label string) *Ask`
chainable setter type-asserts the underlying field to `*confirmField` and sets its
tertiary label + derived key. `NewAsk`'s signature is unchanged, so no other
caller is touched.

### Gate wiring — ESC / Quit end the run

`internal/ui/confirm_gate.go`:

- `raiseGroupConfirm` builds the confirm dialog with `.WithTertiaryButton("Quit")`.
- `advanceGate`:
  - `submitted && value == "quit"` (Quit button) → clear the gate, `tea.Quit`.
  - `!submitted` while **not** customizing (ESC on the confirm dialog) → clear the
    gate, `tea.Quit`.
  - `!submitted` while **customizing** (ESC on a per-var edit) → re-raise the
    current group's confirm dialog (back-navigation; the gate stays intact).

The quit path matches assisted's Quit button: return `tea.Quit` with the model's
default exit code (0).

## Testing

- `internal/input/field_confirm_test.go`: the inter-button gap carries the Mantle
  background; a three-button row renders when a tertiary is set and stays
  two-button otherwise; `value()` returns `"quit"` on tertiary focus and on the
  `q` accelerator; focus cycles `0→1→2→0` on Tab and clamps on arrows.
- `internal/input/confirm_keys_test.go`: `resolveConfirmKey` maps the tertiary key
  to `actTertiary` and Tab / Shift-Tab to the forward / back cycle actions.
- `internal/ui/confirm_gate_test.go`: `advanceGate("quit", true)` and
  confirm-phase `advanceGate("", false)` each return a `tea.Quit` command and
  clear the gate; edit-phase `advanceGate("", false)` re-raises the confirm dialog
  with the gate intact. Existing ESC-path assertions (old dismiss behavior) are
  updated to the new semantics.

## Files

- `internal/input/field_confirm.go` — struct, `view` (gaps + 3rd button),
  `handle` (focus cycling, tertiary select), `value`, `hint`.
- `internal/input/confirm_keys.go` — `actTertiary`, directional cycle actions,
  `resolveConfirmKey` signature.
- `internal/input/ask.go` — `WithTertiaryButton`.
- `internal/ui/confirm_gate.go` — `raiseGroupConfirm`, `advanceGate`.
- Tests as above.
