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
- [ ] Coverage pass toward ~90% — unit-testable packages first: mcpserver 42%, input 66%, capture 70%, triage 73%, tools/floatinput 77%; launcher/cmd orchestration needs integration tests (harder) (2026-06-27)
- [ ] 2-tier integration config — residual: the named-preset selectors are DONE and uniform (`[mux] backend`, `[driver] shell`, `[agent] harness`) and mux has per-command overrides; consider whether shell/AI want per-command/per-aspect overrides too (likely not needed — revisit if a use case appears) (2026-06-27)
- [ ] A5b-strict: the claude stream adapter (internal/agentstream) still returns nil on a truncated/malformed stream with exit 0 — only read failures surface through the new parse-error join (internal/author/events.go); needs adapter-side strictness (stream-contract violation → error) (2026-07-03)
- [ ] A5a-full: interactive/streaming AI calls (agentstream fan-out, DriftRegen) still have no cancellation/timeout plumbing — only classify/metadata are bounded (60s, internal/author/events.go:28) (2026-07-03)
- [ ] B11 residual: `run <slug>` still parses the playbook twice (loadParent + runFile); EnvMain/ValidateMain each double-load — thread the parsed node through dispatch (2026-07-03)
- [ ] Consolidate the five fake-harness script writers in internal/author tests (writeFakeHarness/fakeStreamHarness/fakeMetadataHarness/writeStalledHarness/fakeArgvHarness) into one parameterized helper (2026-07-03)

## Ideas

- [ ] (low priority) E2E/integration tests for the integration entry points (`launcher` entry points, `cmd` `selftest`/`mcpMain`) — spawn the real binary + drive a TUI/PTY. These render via live mux/model/TUI/driver so they're not unit-testable; coverage there is intentionally low. Would push total coverage 80%→~90% (2026-06-27)
- [ ] `inlineInput` (internal/launcher) opens `/dev/tty` unconditionally before the `inlineRunFn` seam, so the `assist` classify→route/cancel flow can't be exercised headless (its tests `t.Skip` without a TTY). Seam the TTY-open so the classify/cancel/route path gets real CI coverage (2026-07-02)
- [ ] Portability / progressive enhancement: the driver needs a Unix PTY + signals (`x/sys/unix`), so it's Linux/macOS-only. Evaluate a degraded no-PTY "plain exec" mode for a portable core, and a ConPTY-based Windows driver (large) (2026-06-27)
- [ ] `create`'s similar-playbooks banner uses a whole-string substring search (`store.Search(prompt)`), so multi-word prompts rarely match — make it per-word/token (2026-06-27)
- [ ] adapt-on-run leaves two temp files per run (`writeTempMarkdown` render+orig in /tmp, never reaped; orig written even when junk-guarded) — defer-cleanup after `ui.Main` returns (2026-06-27)
- [ ] Optional rich output via the kitty graphics protocol — images/charts in the pager (2026-06-26)
- [ ] A JUnit/XML-style report for `run --auto` (CI ingestion) — a plain-text run summary + a JSON per-run log under `${data}/ai-playbook/runs/` shipped 2026-07-01; a JUnit/XML format for CI test-reporters is still open (2026-06-26)
- [ ] Revisit the cwd rule for non-project_bound STORED playbooks: `run <slug>` now opens in the store content dir (runFile F4 rule, one-code-path fix 2026-07-03); decide whether stored playbooks deserve a stored-vs-file distinction (invocation cwd) or whether `workdir:` front matter suffices (2026-07-03)
