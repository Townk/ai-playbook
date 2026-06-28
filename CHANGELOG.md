# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/Townk/ai-playbook/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/Townk/ai-playbook/releases/tag/v0.3.0
[0.2.0]: https://github.com/Townk/ai-playbook/releases/tag/v0.2.0
[0.1.0]: https://github.com/Townk/ai-playbook/releases/tag/v0.1.0
