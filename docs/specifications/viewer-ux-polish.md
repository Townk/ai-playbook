# Viewer UX polish (backlog → own spec)

Cross-cutting viewer-rendering refinements, independent of the structured-playbook
feature (they improve EVERY playbook render, not just `create`'s). To be turned
into a focused spec + plan. Captured verbatim from the user's requirements.

## 1. Consistent floating-pane / modal-dialog color scheme

All floating panes / modal dialogs must share one color scheme (a careful refactor
across every ask dialog in the app):

- Dialog **background** = the same background the **help dialog** uses.
- **Rounded border** = the same **blue border** the **ask** dialogs use.

## 2. Callout (admonition) render — padding + bordered frame

The callout needs a padding area around it. Five areas with bg/fg rules; the text
always starts **1 leading space** from the border in the content area:

| Area | Glyph | Codepoint | bg | fg |
|------|-------|-----------|----|----|
| TL — top-left corner    | `🬞` | `\u{1FB1E}` | document background | admonition accent color |
| TB — top border         | `🬭` | `\u{1FB2D}` | document background | callout background |
| CL — content left border| `▐` | `\u{2590}`  | document background | admonition accent color |
| BL — bottom-left corner | `🬁` | `\u{1FB01}` | document background | admonition accent color |
| BB — bottom border      | `🬂` | `\u{1FB02}` | document background | callout background |

(Builds on the typed-callout admonitions already shipped: note/tip/important/
warning/caution, each with its accent color.)

## 3. Unordered list — wrapped-text alignment

Wrapped text aligns indented with the **first text character** of the previous line
(i.e. the first char after the `- ` marker) — a hanging indent.

## 4. Numbered list — wrapped-text alignment

Same hanging indent for numbered lists: wrapped text aligns with the first text
character after the `1. ` / `1) ` marker.

---

These are render-layer changes in `internal/ui` (render.go for callouts/lists; the
`internal/input` ask/dialog theming for the color scheme). None depend on the
structured-playbook feature; sequence as a standalone polish pass.
