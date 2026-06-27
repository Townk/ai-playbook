# Backlog

A lean tactical tracker. Division of labor:

- **ROADMAP** ([`ROADMAP.md`](ROADMAP.md)) — strategy and phases.
- **BACKLOG** (this file) — tactical bugs, tasks, and ideas.
- **CHANGELOG** ([`../CHANGELOG.md`](../CHANGELOG.md)) — user-facing changes.
- **git** — the full record.

Items are one-liners: `- [ ] <one-liner> (YYYY-MM-DD)`. Keep it lean — prune
done/stale entries. Phase work lives in the roadmap, not here.

## Bugs

_(none — phase work lives in the roadmap)_

## Tasks

- [ ] internal/ui test suite is slow on CI (~10min+ under -race on 2-core runners) — parallelize / reduce per-test zsh-driver spawns (2026-06-27)
- [ ] Decouple the shell: spawn `$SHELL` (zsh as a fidelity *plus*, support bash/sh) instead of hardcoding zsh — today `internal/driver` requires zsh (2026-06-27)
- [ ] Make the multiplexer optional: detect zellij/tmux for the docked-pane + new-tab niceties, fall back to an inline full-screen TUI when no mux is present — today a mux is assumed (2026-06-27)
- [ ] Bump CI/release actions to Node-24 (`checkout@v5`, `setup-go@v6`) + migrate golangci-lint v1→v2 — clears the remaining Node-20 deprecation warnings (2026-06-27)

## Ideas

- [ ] Portability / progressive enhancement: the driver needs a Unix PTY + signals (`x/sys/unix`), so it's Linux/macOS-only. Evaluate a degraded no-PTY "plain exec" mode for a portable core, and a ConPTY-based Windows driver (large) (2026-06-27)
- [ ] Cross-block output piping (runme parity; minor) (2026-06-26)
- [ ] Optional rich output via the kitty graphics protocol — images/charts in the pager (2026-06-26)
- [ ] A structured / JUnit-style report for `run --auto` (CI) (2026-06-26)
