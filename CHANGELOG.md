# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- Assist summoned via `apb` no longer captures its own invocation as the last
  command.
- Paste now works in form fields and the choose dialog's "other" entry.
- `diff` parsing no longer mistakes a deleted/added line whose own content
  starts with `-- `/`++ ` (e.g. an SQL comment) for a new file header, which
  previously truncated or misattributed the rest of the hunk.
- Tab-indented diffs no longer overflow their side-by-side cell or drift the
  `│` divider off-column; tabs are expanded to spaces before any width
  calculation, in both the `diff` CLI view and the in-UI diff overlay.
- Drift conflict markup no longer silently drops a hunk whose leading context
  also occurs earlier in the file above a prior hunk's region — each hunk now
  anchors in file order instead of always searching from the top.
- A submitted code block whose payload contains its own run of 3+ backticks
  (e.g. an embedded markdown/shell example) no longer closes the rendered
  fence early — the fence now widens to stay longer than the longest
  backtick run in the payload.
- Submit-time playbook validation now catches a dangling `needs=`/`rollback=`
  reference, a `needs=` cycle, and an id or `file=` value containing
  whitespace/`{`/`}`/`=` (any of which would corrupt the rendered fence tag)
  — previously these only surfaced later, on the post-hoc `validate` pass.
- Env-var references written as parameter expansions (`${FOO:-default}`,
  `${BAZ%.*}`, `${BAZ#prefix}`, `${VAR/a/b}`) are now captured into the
  saved playbook's `env:` front matter; previously only the bare `${VAR}`
  form was recognized and these were silently omitted.

## [0.6.1] - 2026-07-03

### Fixed

- `go install`-ed binaries now report their real module version (read from the
  embedded build info) instead of `dev`. Release-archive builds are unchanged —
  they still carry the version injected at build time.

## [0.6.0] - 2026-07-03

### Added

- Comprehensive `--help`: a grouped top-level overview and real per-command
  help via `ai-playbook <command> --help` / `help <command>`, generated man
  pages, and a zsh completion script with dynamic completion of saved
  playbook slugs — all packaged in the release archives.
- `apb`, a short-name binary built from the same code as `ai-playbook`:
  install it directly (`go install .../cmd/apb`) or grab it from any release
  archive, where it now ships alongside `ai-playbook`. Both binaries behave
  identically; `--help`/`help` and `--version` are name-aware (`apb --help`
  reads "apb").

### Changed

- The confirm dialog's button row is now horizontally centered within the pane
  (previously left-aligned).

## [0.5.0] - 2026-07-03

### Added

- Live playbook store (Phase 1): saved playbooks are now a browsable, searchable,
  editable, re-runnable library. New commands — `list`/`search`
  (`--format human|fuzzy-data-source|json`), `show`, `edit`, and `create` (author a
  playbook directly). `troubleshoot` is renamed to `assist` (the old name still works).
  A global store plus a project-local store (`.ai-playbook/playbooks/`, `proj:`-prefixed
  slugs); both directories are configurable via `[store]`. `run <slug>` adapts a stored
  playbook to the current project (with an "adapted from" banner and a `d` diff view);
  `run --file <path>` runs a file directly. Playbooks gain a `workdir` front-matter field.
- Runs without a terminal multiplexer (ADR-0006 Stage 1): off-mux, the input box
  renders inline below the shell prompt, and the agent's `ask` dialog renders as an
  in-viewer overlay (all types: text/line/confirm/choose/free). With a multiplexer
  present, the floating-pane experience is unchanged.
- Configurable shell (ADR-0006 Stage 2): `[driver] shell` selects the executing
  shell — `zsh`, `bash`, or POSIX `sh`. bash and sh are supported with per-shell
  value-passing that round-trips special characters; zsh gives full fidelity
  (aliases/functions/rc). The default honors `$SHELL` (see *Changed*).
- **Run modes for `run`** — `--assisted` (guided: a "ready" cursor auto-scrolls
  each next step into view; a focusable `[ Run ][ Skip ][ Quit ]` footer confirms
  each step; on failure it switches to `[ Roll back ][ Leave as-is ][ Quit ]`) and
  `--auto` (headless: runs every block in `needs=` order, stops on the first
  failure with a non-zero exit and a summary; renders inline in the terminal /
  CI-friendly). `--auto` rolls back completed steps in reverse order on failure by
  default (via `{rollback=<id>}` blocks); `--no-auto-rollback` opts out. Each run
  writes a structured JSON log under `${XDG_DATA_HOME}/ai-playbook/runs/`.
- **`validate [<slug> | --file <path>]`** — deterministic structural checks
  (front-matter required keys, `needs=` existence and cycles, duplicate ids, fence
  balance; plus no-runnable / missing-language warnings) and an advisory AI prose
  review, with live progress (a spinner on a TTY, a dot heartbeat in CI) and
  `--no-ai` / `--plain` / `--quiet`. Exits non-zero on structural errors only.
- **Viewer affordances** — an `[edit]` tag-button opens `$EDITOR` on a file-backed
  playbook and the viewer reloads on save (1s mtime watch); a pure-Go side-by-side,
  syntax-highlighted diff view (ADR-0008) backs both the `diff`-block "view diff"
  button and the adapt-on-run `d` overlay, mux-aware (a floating pane with a
  multiplexer, a modal overlay without).
- **`run --auto --with-env <JSON | file>`** — supply a project-bound playbook's
  declared `env:` values on the CLI as an inline JSON object or a path to a JSON
  file, instead of exporting them. Values take precedence over the environment;
  undeclared keys are ignored with a warning. Valid only with `--auto`.
- **`env [<slug> | --file <path>]`** — print a playbook's declared `env:` as a
  `--with-env`-compatible JSON object, each value resolved from the current
  environment (sensitive values — token/key/secret/password-like names or
  high-entropy values — are emitted empty and listed on stderr). Scaffolds the
  round-trip `env > env.json` → edit → `run --auto --with-env env.json`.
- **`depends_on: [slug, …]`** front-matter field — a playbook can declare other
  store slugs it needs run first. `run <slug>` resolves the transitive
  dependency graph and runs each dependency headless, in topological order,
  before the parent; the first failure aborts the whole chain with a non-zero
  exit. A dependency cycle or a dangling (unresolvable) slug is a hard error
  (exit 2); `validate` flags the same issues as structural errors. `--with-env`
  and `env <slug>` both span the entire chain — the union of every variable
  declared anywhere in the graph.

### Changed

- `create <prompt>` now shows **inline progress** while authoring — the spinner +
  `Waiting…` + elapsed + model-activity line render below the shell prompt (not the
  fullscreen viewer) — and only then opens the viewer with the **complete** playbook
  (no live-stream takeover). The flow is identical with or without a multiplexer, and
  the authoring agent's `ask` is supported throughout (float with a mux; an inline ask
  box, paused/resumed around the progress line, without one).
- Authored playbooks now target the configured shell: `sh` runs receive POSIX-only
  guidance (no `[[ ]]`, arrays, or bash/zsh extensions); `bash` and `zsh` runs are
  identified explicitly. The effective shell is resolved from `[driver] shell` (or
  `$SHELL` when unset) and injected into the authoring prompt.
- The multiplexer integration is now **OFF by default** (was: auto-enabled inside
  zellij). Opt in with `[mux] backend = "zellij"`. The `$ZELLIJ`-presence
  auto-enable is removed; per-command `[mux]` template overrides remain as tier-2.
  **Behavior change** (ADR-0007): pre-existing users who relied on auto-zellij must
  add `[mux] backend = "zellij"`.
- The shell driver now **defaults to `$SHELL`** (was: zsh-first). With no
  `[driver] shell` set it honors the login shell when its basename names a supported
  shell (zsh/bash/sh), falling back `zsh` → `bash` → `sh`. Pin a specific shell with
  `[driver] shell`. **Behavior change** (ADR-0007); a zsh user is unaffected.
- **Run-block value-passing env vars renamed** `AAS_*` → `APB_*`: the exported
  variables are now `APB_OUT_<id>`, `APB_ERR_<id>`, and `APB_EXIT_<id>` (were
  `AAS_OUT_<id>`, `AAS_ERR_<id>`, `AAS_EXIT_<id>`). The old prefix was a leftover
  from the retired "ai-assist" shell stack. If you have saved playbooks that reference
  the old names, update them: `s/\$AAS_/\$APB_/g`. The store is days old so few if
  any saved playbooks should be affected.

### Fixed

- Framed dialogs are now fully painted on their background — no bleed-through of
  the terminal's default: the variable-confirmation dialog's prompt body, button
  row (now `[ Confirm ][ Customize ][ Quit ]`), and hint line; the `choose`
  dialog's prompt; and the text-input box interior. The hint line sits flush
  against the bottom border (no trailing blank line), and the confirmation
  dialog's variables render in an aligned two-column layout with long values
  wrapping under the value column.
- In the confirm gate, **ESC** and the new **Quit** button end the run; ESC while
  editing a variable (Customize) steps back to the confirm dialog instead of
  quitting.
- ESC during the `assist` classify wait now cancels the request instead of
  proceeding to route it as if submitted.
- `--assisted` now confirms a project-bound playbook's declared variables at load
  (before the first step), matching the run-modes spec.

## [0.3.0] - 2026-06-26

### Added

- Single Go binary unifying and replacing the retired shell-script stack;
  harness-agnostic design (Claude harness today), invoked directly or bound to a
  shell key.
- `assist` triage (command / answer / escalate) with routing.
- Cache-by-kind: a repeat command/answer/playbook is served without re-classify;
  a cached answer invalidates in place (reload re-runs the cheap classify).
- In-process re-engagement: regenerate / follow-up / wrap-up.
- Auto-follow-up on a failed verify; native verify-success confirm (green
  ask-style buttons, `c` to generate).
- The wave thinking animation.
- Replace-protection: never persist a non-playbook over the resolved troubleshoot.
- Front matter (`name`/`description`/`category`/`tags`/`env`) with `finalize`
  backfill.
- Multi-language run blocks (shell plus python/node/ruby/perl via interpreter
  heredocs).
- MCP tools backend (run / ask / remember) over a unix socket, dialing the shared
  shell driver.

### Changed

- Performance: classify runs thinking-OFF (~2.6s vs ~7–9s); async session open so
  cached playbooks render instantly and shell buttons enable when ready; answers
  skip the driver.
- Rebrand: environment variables renamed to `AI_PLAYBOOK_*`; `ai-playbook` labels
  and cache schema; corrected system-prompt tool references (MCP run/ask/remember).

### Removed

- The retired zsh + `libexec/` shell stack.
- Dead FIFO plumbing, including `--results-fifo` and the broker process.

## [0.2.0] - (historical)

- First all-Go-binary release (replaced the shell stack).

## [0.1.0] - (historical)

- Original zsh shell-script implementation (ai-assist).

[Unreleased]: https://github.com/Townk/ai-playbook/compare/v0.6.1...HEAD
[0.6.1]: https://github.com/Townk/ai-playbook/compare/v0.6.0...v0.6.1
[0.6.0]: https://github.com/Townk/ai-playbook/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/Townk/ai-playbook/compare/v0.3.0...v0.5.0
[0.3.0]: https://github.com/Townk/ai-playbook/releases/tag/v0.3.0
[0.2.0]: https://github.com/Townk/ai-playbook/releases/tag/v0.2.0
[0.1.0]: https://github.com/Townk/ai-playbook/releases/tag/v0.1.0
