# `ask` — the standalone dialog binary

_Status: approved 2026-07-04 (design settled in review with the project owner;
recorded as ADR-0009's interaction-toolkit surface, migration step 3)._

## Problem

The themed dialog widgets (`confirm`/`line`/`text`/`choose`/`form` in
`internal/input`) are already consumed by external scripts — the owner's
chezmoi config wraps them in `input::*` zsh shims — but the only entry point is
`ai-playbook input`, which is marked **Internal** in climeta: no help, no man
page, no completion, and a single flag-bag surface that mixes public knobs
(title/prompt/variant/labels) with ai-playbook's private float plumbing
(`--out`, FIFOs, `--thinking`, `--history`, wave-demo). Each shim re-implements
argument parsing, theme injection, and binary resolution.

History note: a standalone `ai-assist-input` binary existed and was deliberately
retired into this module at v0.6.0. This spec re-offers the standalone
*surface* — from this repo, the `apb` pattern — without re-fragmenting the code.

## Decisions (made with the owner, 2026-07-04)

1. **Binary name: `ask`** — third GoReleaser build from this repo (`cmd/ask`).
2. **Subcommand per widget** — `ask confirm|line|text|choose|form`, each with
   focused flags, help, man section, completion.
3. **Pure I/O public contract** — stdout + exit codes only. The float/FIFO/
   thinking/history plumbing stays on the hidden `ai-playbook input`, which
   ai-playbook itself continues to use unchanged.
4. **CLI-only for now** — the widgets' Go API joins the single `pkg/` promotion
   (ADR-0009 step 5); no importable API before that.

## Command surface (the public contract)

Exit codes across all subcommands: **0** submit/affirmative, **1** negative
(confirm's "No"), **130** cancel (ESC/Ctrl-C). Values on stdout; nothing else
on stdout, diagnostics on stderr.

- `ask confirm "Proceed?" [--danger|--warning] [--affirmative <label>]
  [--negative <label>] [--default affirmative|negative] [--title <t>]
  [--print]` — the exit code IS the answer (`if ask confirm "Delete?"`);
  `--print` additionally emits `yes`/`no` (shim-compat). Danger forces
  default=negative (existing widget behavior).
- `ask line "Prompt" [--value <v>] [--placeholder <p>] [--title <t>]
  [--icon <glyph>]` — one-line input; submitted value on stdout.
- `ask text "Prompt" [--value <v>] [--height <rows>] [--title <t>]
  [--icon <glyph>]` — multi-line editor; submitted value on stdout.
- `ask choose "Pick one" <item>... [--multi] [--other <label>] [--title <t>]`
  — selection on stdout; `--multi` prints one per line; `--other` enables the
  free-text entry row (its value prints like any selection).
- `ask form [--spec <file>]` — spec from stdin when `--spec` is omitted.
  **Public spec format: JSON** (array of field objects: `type`
  (`line|text|confirm|choose`), `key`, `prompt`, plus the per-type options
  above). Output: `key=value` lines (values shell-quoted); `--json` emits one
  JSON object instead. The internal US/RS encoding remains supported by
  `ai-playbook input` for ai-playbook's own callers; `ask` speaks JSON only.
- Cross-cutting flags on every subcommand: `--width <cols>`, `--padding`,
  `--inset`, `--measure` (print rendered height and exit — the zellij float
  sizing protocol, deliberately public), `--title`.
- `ask --help` / `ask <sub> --help`, `ask --version`/`-v` (version from build
  info, same resolution as ai-playbook/apb).

## Theming

Theme remains flag-configurable (the existing `registerThemeFlags` set), and
every theme flag gains an **`ASK_<FLAG>` environment fallback** (flag wins over
env; env wins over the built-in default) so scripts export the palette once
instead of passing per-call flags. The precedence and variable names are
documented in the man page.

## Internals

- `cmd/ask/main.go` — thin: `os.Exit(askcli.Run(os.Args))`.
- **New `internal/askcli`** — owns the subcommand dispatch, per-subcommand
  flag sets, env-fallback resolution, JSON form-spec parsing, and the
  help/version text. Maps onto the existing `internal/input` widgets — the
  widgets remain the single implementation shared with ai-playbook; `askcli`
  adds NO widget behavior.
- `ai-playbook input` is untouched (flags, US/RS spec, plumbing, Internal
  marking all stay).
- **Docs pipeline**: climeta/docgen extended to emit `docs/man/ask.1` and
  `completions/_ask`, wired into `make docs`; the existing docs-drift CI gate
  covers them automatically. How climeta models a second command tree is an
  implementation-plan decision (a parallel registry is acceptable; do not
  contort the ai-playbook registry).
- **Release**: third GoReleaser build + archives include `ask` alongside
  `ai-playbook`/`apb`; man page + completion bundled like the others.

## Error handling

- Unknown subcommand/flag → usage to stderr, exit 2.
- Malformed JSON form spec → one-line parse error to stderr (with position if
  available), exit 2.
- No TTY → exit 2 with a clear message (the widgets require a terminal; scripts
  redirecting stdin still work because the TUI drives /dev/tty via bubbletea's
  standard behavior — verify and document the actual constraint at
  implementation time).

## Out of scope (recorded, not built)

- Go API (`pkg/`) — ADR-0009 step 5.
- The float/FIFO/thinking/history plumbing — stays internal.
- Migrating the owner's chezmoi shims — a follow-up in that repo; the spec's
  acceptance includes the shims *becoming trivially collapsible*, not
  collapsed.
- Additional widget types (spinner/progress/pick-list) — future candidates
  once the surface proves itself.

## Testing

- `internal/askcli` table tests per subcommand: args → widget-option mapping,
  exit codes (0/1/130/2), stdout shapes (confirm silent-by-default/`--print`,
  choose multi one-per-line, form key=value and `--json`), env-fallback
  precedence (flag > env > default), JSON spec round-trip + malformed-spec
  errors, `--measure` parity with `ai-playbook input --measure` for identical
  dialogs.
- Docs: `make docs` idempotent with the new outputs; docs-check green.
- Release: goreleaser config check (snapshot build ships three binaries).
