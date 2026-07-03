# Backlog

A lean tactical tracker. Division of labor:

- **ROADMAP** ([`ROADMAP.md`](ROADMAP.md)) — strategy and phases.
- **BACKLOG** (this file) — tactical bugs, tasks, and ideas.
- **CHANGELOG** ([`../CHANGELOG.md`](../CHANGELOG.md)) — user-facing changes.
- **git** — the full record.

Items are one-liners: `- [ ] <one-liner> (YYYY-MM-DD)`. Keep it lean — prune
done/stale entries. Phase work lives in the roadmap, not here.

## Bugs

_(none — the stored-parent `fm.Env` drop was fixed 2026-07-02 with the depends_on work.)_

## Tasks

- [ ] Extend the shared-test-driver speedup to `internal/{orchestrator,driver,tools,launcher}` — each still spawns a real zsh per test (the ~1200ms `driver.Open` idle floor), so the `-race` lane is still ~3–5min on CI (orchestrator ~68s, driver ~38s, tools ~33s locally). The same `TestMain` shared-driver pattern used for `internal/ui` (577a0ed) applies; once done, `-race` can move back onto the fast per-push lane and `race.yml` can be retired (2026-07-03)
- [ ] ESC-audit (broader sweep): the KNOWN classify-wave case is FIXED — ESC during the `assist` thinking wave now cancels instead of routing (2026-07-02). Remaining: sweep the pager's own `esc` cases in `internal/ui/model.go` (~lines 1345/1351/1386/1423/1674) for consistent cancel/dismiss (never exit the app; Ctrl+C exits). The `--assisted`/`run` variable-confirm gate stays a deliberate exception (ESC ends the run) (2026-06-27)
- [ ] Prompt/hint-line bleed tests are non-discriminating: the SGR-containment tests for the confirm/choose prompt and hint lines pass even with the `promptStyle`/`hintKW` fix reverted — `renderFrame`'s per-line `Background(Mantle)` wrap carries the bg through a foreground-only span. Devise a discriminating assertion (the text-box-interior bg tests are the working model) (2026-07-02)
- [ ] internal/ui test suite is slow on CI (~10min+ under -race on 2-core runners) — parallelize / reduce per-test zsh-driver spawns (2026-06-27)
- [ ] Coverage pass toward ~90% — unit-testable packages first: mcpserver 42%, input 66%, capture 70%, triage 73%, tools/floatinput 77%; launcher/cmd orchestration needs integration tests (harder) (2026-06-27)
- [ ] 2-tier integration config — residual: the named-preset selectors are DONE and uniform (`[mux] backend`, `[driver] shell`, `[agent] harness`) and mux has per-command overrides; consider whether shell/AI want per-command/per-aspect overrides too (likely not needed — revisit if a use case appears) (2026-06-27)

## Ideas

- [ ] (low priority) E2E/integration tests for the integration entry points (`launcher` entry points, `cmd` `selftest`/`mcpMain`) — spawn the real binary + drive a TUI/PTY. These render via live mux/model/TUI/driver so they're not unit-testable; coverage there is intentionally low. Would push total coverage 80%→~90% (2026-06-27)
- [ ] (small, cheap) Make `cmd/ai-playbook` `main` dispatch unit-testable: extract `run(args []string, deps) int` (keep the lone `os.Exit` in `main`), inject the subcommand funcs behind a seam so dispatch can be spied. Also trivially testable today: `atomicWrite`/`dirExists`/`head`. Distinct from the integration-glue item above — this part is a fixable structural gap, not inherent (2026-06-27)
- [ ] `inlineInput` (internal/launcher) opens `/dev/tty` unconditionally before the `inlineRunFn` seam, so the `assist` classify→route/cancel flow can't be exercised headless (its tests `t.Skip` without a TTY). Seam the TTY-open so the classify/cancel/route path gets real CI coverage (2026-07-02)
- [ ] Portability / progressive enhancement: the driver needs a Unix PTY + signals (`x/sys/unix`), so it's Linux/macOS-only. Evaluate a degraded no-PTY "plain exec" mode for a portable core, and a ConPTY-based Windows driver (large) (2026-06-27)
- [ ] `create`'s similar-playbooks banner uses a whole-string substring search (`store.Search(prompt)`), so multi-word prompts rarely match — make it per-word/token (2026-06-27)
- [ ] adapt-on-run leaves two temp files per run (`writeTempMarkdown` render+orig in /tmp, never reaped; orig written even when junk-guarded) — defer-cleanup after `ui.Main` returns (2026-06-27)
- [ ] Optional rich output via the kitty graphics protocol — images/charts in the pager (2026-06-26)
- [ ] A JUnit/XML-style report for `run --auto` (CI ingestion) — a plain-text run summary + a JSON per-run log under `${data}/ai-playbook/runs/` shipped 2026-07-01; a JUnit/XML format for CI test-reporters is still open (2026-06-26)
