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

- [ ] **Make the multiplexer optional (ADR-0006 Stage 1) — NOT DONE.** Shipped: null mux + detection + `internal/launcher` extraction + CLI/stdin request + stdout answers. **BLOCKING remainder: the inline agent `ask` UI** — off-mux the agent currently can't ask the user (it's "unavailable"), so the assist flow is gutted and we are NOT truly mux-optional. Approach: an in-TUI ask modal. **Lock the UI/UX with the user before implementing.** (2026-06-27)
- [ ] internal/ui test suite is slow on CI (~10min+ under -race on 2-core runners) — parallelize / reduce per-test zsh-driver spawns (2026-06-27)
- [ ] Decouple the shell (ADR-0006 Stage 2): spawn `$SHELL` (zsh as a fidelity *plus*, support bash/sh) instead of hardcoding zsh — today `internal/driver` requires zsh (2026-06-27)
- [ ] Coverage pass toward ~90% — unit-testable packages first: mcpserver 42%, input 66%, capture 70%, triage 73%, tools/floatinput 77%; launcher/cmd orchestration needs integration tests (harder) (2026-06-27)
- [ ] Migrate golangci-lint v1→v2 (modernize; v1 is EOL) — `checkout@v5`/`setup-go@v6` already bumped (2026-06-27)
- [ ] view-diff in a null-mux inline TUI shows a raw "mux: no multiplexer available" — thread the selected mux into `internal/ui` + soften the message (2026-06-27)
- [ ] Default to NO mux: ship with the mux integration OFF by default (user opts in via `mux = "zellij"`); docs show how to integrate. Flip the default only AFTER mux-optional (incl. the inline ask) lands (2026-06-27)
- [ ] 2-tier integration config for mux + shell + AI: a named preset works out of the box (e.g. `mux = "zellij"` picks sensible default commands), with optional per-command overrides for fine-grained control. Apply the SAME config style uniformly across all three integrations (2026-06-27)

## Ideas

- [ ] Portability / progressive enhancement: the driver needs a Unix PTY + signals (`x/sys/unix`), so it's Linux/macOS-only. Evaluate a degraded no-PTY "plain exec" mode for a portable core, and a ConPTY-based Windows driver (large) (2026-06-27)
- [ ] Cross-block output piping (runme parity; minor) (2026-06-26)
- [ ] Optional rich output via the kitty graphics protocol — images/charts in the pager (2026-06-26)
- [ ] A structured / JUnit-style report for `run --auto` (CI) (2026-06-26)
