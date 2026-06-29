# Viewer UX polish — focused spec

Status: agreed (2026-06-29). First of three post-Phase-B follow-ups (viewer-UX-polish →
new-file block → file-change watcher). Cross-cutting render-layer refinements in
`internal/ui` + `internal/input`, independent of the structured-playbook feature — they
improve every playbook render. Supersedes the backlog capture (this file's prior form).

## Goal

Four render-layer refinements: (1) one shared color palette + a unified dialog color
scheme; (2) a bordered, padded callout frame; (3)+(4) hanging indents for wrapped list
items. All are presentation-only — no behavior change.

## 1. Shared theme package + unified dialog colors

Today the palette is **duplicated**: `internal/ui/theme.go` (plain `const`s — Catppuccin
Mocha + `darken`/`bgANSI`/`parseHex`) and `internal/input/theme.go` (a `Theme` struct
with `--theme-*` flag overrides). The same hex appears by hand in both (e.g. `#89b4fa` as
both `colBlue` and `Theme.Border`).

**Shared palette.** Introduce a minimal package `internal/theme` holding the single
source of truth: the Catppuccin Mocha color constants + the helpers `darken`, `bgANSI`,
`parseHex`. Keep it a *palette*, not a theming framework. Then:
- `internal/ui` references `internal/theme` constants instead of its local copies.
- `internal/input`'s `Theme` keeps its flag-override mechanism but its **defaults** come
  from `internal/theme` (no hand-duplicated hex).

**Unified dialog scheme.** Every floating-pane / modal / ask dialog shares one look:
- **background** = `colMantle` (`#181825`) — the help dialog's background.
- **border** = the blue `#89b4fa`, `lipgloss.RoundedBorder()` — the ask dialog's border.

Surfaces to unify (from the grounding):
- The `internal/input` `renderFrame` family (ask overlay, spawned `floatinput` float, the
  B2b/B3 confirm + per-var dialogs, the processing/thinking float) — today blue rounded
  border but **no background**. Add the `colMantle` background (+ `BorderBackground`). The
  single chokepoint is `renderFrame` (`internal/input/frame.go`).
- The **help modal** (`internal/ui/model.go`) — today `colMantle` bg but a grey
  `colSurface1` border. Switch its border to the blue `#89b4fa`.

Net: input dialogs gain the mantle background; the help modal gains the blue border; both
end on mantle-bg + blue-rounded-border. (The `fzf` store picker is external — not a
lipgloss surface — out of scope. The inner field box `FieldBorder` and the in-pane
reengage confirm rows are not framed modals — leave them.)

## 2. Callout (admonition) bordered frame

Today a callout (`render.go` `quote()`) is a single left bar `▋` (accent foreground) over
a darkened-accent background band (`band(…, darken(accent, 0.20))`). Replace the left-bar
look with a padded frame around the **same darkened-accent content background**, using the
exact glyphs below. The frame reads as part of the darkened background — only the corners
+ left bar carry the accent.

| Area | Glyph | Codepoint | fg | bg |
|------|-------|-----------|----|----|
| TL — top-left corner     | `🬞` | `U+1FB1E` | admonition accent   | document bg |
| TB — top border          | `🬭` | `U+1FB2D` | callout bg (dark accent) | document bg |
| CL — content left border | `▐` | `U+2590`  | admonition accent   | document bg |
| BL — bottom-left corner  | `🬁` | `U+1FB01` | admonition accent   | document bg |
| BB — bottom border       | `🬂` | `U+1FB02` | callout bg (dark accent) | document bg |

Per-row composition (no right border):
```
🬞🬭🬭🬭🬭🬭🬭🬭🬭🬭         top    = TL + TB×(w−1)            on document bg
▐ 󰋽 Note                  header = CL + " " + icon+title    content on callout bg
▐ Body text wraps here…   content= CL + " " + text          content on callout bg
🬁🬂🬂🬂🬂🬂🬂🬂🬂🬂         bottom = BL + BB×(w−1)            on document bg
```
- **Content** (header + body rows) keeps the existing darkened-accent background; text
  starts **1 leading space** after the `▐` left bar (already true today — preserve it).
- **Top/bottom borders**: the sextant glyphs painted in the **callout-bg tone** over the
  document bg, so the dark content appears to soften into a top/bottom edge.
- **Corners + left bar**: the **admonition accent** color.
- No right border — content rows pad to width with the callout bg (as today).
- The header row (icon + title, recognized `[!TYPE]` only) sits inside the frame as the
  first content row; bare blockquotes (no `[!TYPE]`) get the frame with the `colOverlay0`
  fallback accent and no header row.

Contained to `render.go` `quote()` (the per-row segment assembly — each frame row mixes
fg/bg per cell, so it can't use a single `band()` call; assemble corner/border segments
with per-cell styles, matching the existing `codeFgANSI` edge-bar pattern) + a few glyph
constants. Builds on the shipped typed callouts (note/tip/important/warning/caution +
accents).

## 3 & 4. List wrapped-text hanging indent

Today `list()` (`render.go`) hands `marker+itemText` as one blob to `emitProse`
(`render.go`), which wraps then pads **every** wrapped line by the same indent — so
continuation lines align under the marker (the bullet/number), not after it. Add a
**hanging indent**: wrapped continuation lines align with the first text character after
the marker — continuation indent = `indent + 2 + lipgloss.Width(marker)` (marker = `• `
for unordered, `N. ` for ordered). One change at the `emitProse`/`list` chokepoint
(a hanging-indent-aware emit), covering both unordered and numbered lists.

## Components (decomposition)

- **`internal/theme`** — new shared palette (constants + `darken`/`bgANSI`/`parseHex`);
  `internal/ui` + `internal/input` reference it (dedup).
- **Unified dialog colors** — `internal/input` `renderFrame` gains the mantle bg; the
  `internal/ui` help modal gains the blue border.
- **Callout frame** — `render.go` `quote()` rework + glyph constants.
- **List hanging indent** — `render.go` `emitProse`/`list` hanging-indent emit.

## Testing

- **Theme package:** the constants/helpers move with identical values (a render of a known
  element is byte-identical before/after the dedup); `internal/input` defaults match.
- **Dialog colors:** a rendered ask/confirm/help frame contains the mantle bg + the blue
  rounded border (assert the SGR/border bytes).
- **Callout frame:** a rendered callout contains the top row (`🬞`+`🬭`), the `▐` left bar
  with text 1 space off it, the bottom row (`🬁`+`🬂`); corners/left in accent, top/bottom
  glyphs in callout-bg tone; content on the darkened-accent bg; no right border; a bare
  blockquote uses the fallback accent + no header.
- **List hanging indent:** a long unordered item wraps with continuation aligned after
  `• `; a long ordered item aligns after `N. ` (assert the continuation-line leading
  spaces == `indent + 2 + width(marker)`).

## Out of scope

The new-file block + file-change watcher (the next two follow-ups); the assisted-run
feature (ROADMAP Phase 2). Presentation-only — no run/author/save behavior changes.
