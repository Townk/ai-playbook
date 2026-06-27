# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Runs without a terminal multiplexer (ADR-0006 Stage 1): off-mux, the input box
  renders inline below the shell prompt, and the agent's `ask` dialog renders as an
  in-viewer overlay (all types: text/line/confirm/choose/free). With a multiplexer
  present, the floating-pane experience is unchanged.
- Configurable shell (ADR-0006 Stage 2): `[driver] shell` selects the executing
  shell — `zsh` (default), `bash`, or POSIX `sh` — falling back to `$SHELL` when
  unset. zsh remains the default for full fidelity (aliases/functions/rc); bash and
  sh are supported with per-shell value-passing that round-trips special characters.

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
