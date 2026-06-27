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

- [ ] ESC-audit: ensure ESC consistently *cancels the current operation / dismisses a modal* (never exits the app — that's Ctrl+C). Known case: ESC during the in-box classify-wave currently proceeds/routes instead of cancelling (2026-06-27)
- [ ] Rename the value-passing env prefix `AAS_` → `AAPB_` (`AAS_OUT/ERR/EXIT` are leftover "ai-assist" naming; user-facing in generated playbooks → careful migration, or keep + document the legacy name) (2026-06-27)
- [ ] internal/ui test suite is slow on CI (~10min+ under -race on 2-core runners) — parallelize / reduce per-test zsh-driver spawns (2026-06-27)
- [ ] Coverage pass toward ~90% — unit-testable packages first: mcpserver 42%, input 66%, capture 70%, triage 73%, tools/floatinput 77%; launcher/cmd orchestration needs integration tests (harder) (2026-06-27)
- [ ] Make the author/agent prompt shell-aware: `internal/author/prompt.go:143` hardcodes `set -e` + shell idioms; should adapt to `cfg.Driver.Shell` so a non-zsh shell gets correct guidance (2026-06-27)
- [ ] view-diff in a null-mux inline TUI shows a raw "mux: no multiplexer available" — thread the selected mux into `internal/ui` + soften the message (2026-06-27)
- [ ] 2-tier integration config — residual: the named-preset selectors are DONE and uniform (`[mux] backend`, `[driver] shell`, `[agent] harness`) and mux has per-command overrides; consider whether shell/AI want per-command/per-aspect overrides too (likely not needed — revisit if a use case appears) (2026-06-27)

## Ideas

- [ ] (low priority) E2E/integration tests for the integration entry points (`launcher` entry points, `cmd` `selftest`/`mcpMain`) — spawn the real binary + drive a TUI/PTY. These render via live mux/model/TUI/driver so they're not unit-testable; coverage there is intentionally low. Would push total coverage 80%→~90% (2026-06-27)
- [ ] (small, cheap) Make `cmd/ai-playbook` `main` dispatch unit-testable: extract `run(args []string, deps) int` (keep the lone `os.Exit` in `main`), inject the subcommand funcs behind a seam so dispatch can be spied. Also trivially testable today: `atomicWrite`/`dirExists`/`head`. Distinct from the integration-glue item above — this part is a fixable structural gap, not inherent (2026-06-27)
- [ ] Portability / progressive enhancement: the driver needs a Unix PTY + signals (`x/sys/unix`), so it's Linux/macOS-only. Evaluate a degraded no-PTY "plain exec" mode for a portable core, and a ConPTY-based Windows driver (large) (2026-06-27)
- [ ] `create`'s similar-playbooks banner uses a whole-string substring search (`store.Search(prompt)`), so multi-word prompts rarely match — make it per-word/token (2026-06-27)
- [ ] adapt-on-run leaves two temp files per run (`writeTempMarkdown` render+orig in /tmp, never reaped; orig written even when junk-guarded) — defer-cleanup after `ui.Main` returns (2026-06-27)
- [ ] Cross-block output piping (runme parity; minor) (2026-06-26)
- [ ] Optional rich output via the kitty graphics protocol — images/charts in the pager (2026-06-26)
- [ ] A structured / JUnit-style report for `run --auto` (CI) (2026-06-26)
