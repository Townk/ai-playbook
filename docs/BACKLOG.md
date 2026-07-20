# Backlog

A lean tactical tracker. Division of labor:

- **ROADMAP** ([`ROADMAP.md`](ROADMAP.md)) — strategy and phases.
- **BACKLOG** (this file) — tactical bugs, tasks, and ideas.
- **CHANGELOG** ([`../CHANGELOG.md`](../CHANGELOG.md)) — user-facing changes.
- **git** — the full record.

Items are one-liners: `- [ ] <one-liner> (YYYY-MM-DD)`. Keep it lean — prune
done/stale entries. Phase work lives in the roadmap, not here.

## Bugs

## Tasks

- [ ] cursor FULL builtin-containment depends on cursor honoring `failClosed:true` preToolUse hooks (fail-closed on crash/garbage), proven live against cursor-agent 2026.07.01-777f564 — RE-PROVE on a cursor-agent upgrade (run `go test ./internal/author -run TestCursorLive_ToolHookBlocksBuiltins`); if a future version regresses failClosed, the `cmd.Dir=<ToolDir>` scratch cwd is the structural backstop (2026-07-06)
- [ ] launcher `session.toolTransport` gates on `selfExe != ""` although pi's transport doesn't need SelfExe — a failed `os.Executable` silently drops pi's tools even though its transport could be wired; decouple the gate per-harness or document the conservatism (2026-07-06)
- [ ] Extend the shared-test-driver speedup to `internal/{orchestrator,driver,tools,launcher}` — each still spawns a real zsh per test (the ~1200ms `driver.Open` idle floor), so the `-race` lane is still ~3–5min on CI (orchestrator ~68s, driver ~38s, tools ~33s locally). The same `TestMain` shared-driver pattern used for `internal/ui` (577a0ed) applies; once done, `-race` can move back onto the fast per-push lane and `race.yml` can be retired (2026-07-03)
- [ ] ESC-audit (broader sweep): the KNOWN classify-wave case is FIXED — ESC during the `assist` thinking wave now cancels instead of routing (2026-07-02). Remaining: sweep the pager's own `esc` cases in `internal/ui/model.go` (~lines 1345/1351/1386/1423/1674) for consistent cancel/dismiss (never exit the app; Ctrl+C exits). The `--assisted`/`run` variable-confirm gate stays a deliberate exception (ESC ends the run) (2026-06-27)
- [ ] Coverage pass toward ~90% — unit-testable packages first: mcpserver 42%, input 66%, capture 70%, triage 73%, tools/floatinput 77%; launcher/cmd orchestration needs integration tests (harder) (2026-06-27)
- [ ] 2-tier integration config — residual: the named-preset selectors are DONE and uniform (`[mux] backend`, `[driver] shell`, `[agent] harness`) and mux has per-command overrides; consider whether shell/AI want per-command/per-aspect overrides too (likely not needed — revisit if a use case appears) (2026-06-27)
- [ ] A5a-full: interactive/streaming AI calls (agentstream fan-out, DriftRegen) still have no cancellation/timeout plumbing — only classify/metadata are bounded (60s, internal/author/events.go:28); the same plumbing should surface stream truncation on the authoring paths, where FanOut discards closeFn's error so A5b-strict only protects triage (2026-07-03)
- [ ] B11 residual: `run <slug>` still parses the playbook twice (loadParent + runFile); EnvMain/ValidateMain each double-load — thread the parsed node through dispatch (2026-07-03)
- [ ] Consolidate the five fake-harness script writers in internal/author tests (writeFakeHarness/fakeStreamHarness/fakeMetadataHarness/writeStalledHarness/fakeArgvHarness) into one parameterized helper (2026-07-03)
- [ ] `spinRow` (ui/model.go) hand-duplicates `runRegion`'s spinner-row construction (indent + `frame/10` seconds rule) — share a helper or add a frame-0 equality assertion so a format change can't desync the tick-regenerated row from the reflow-baked one (2026-07-04)
- [ ] Refuse-solution: the degraded TEXT fallbacks (orchestrator Regenerate/FinalPlaybook/Followup when the events producer fails to start) re-engage without constraints — thread them through author.Author/FinalPlaybookText/Followup, or keep the spec's degraded-mode exemption (2026-07-04)
- [ ] `assignIDs` in internal/playbook advertises a phantom `b<N>` auto-id scheme that can never fire (buildBlock assigns `auto-<n>` first) — wire it as the single id rule or drop it and its tests (2026-07-04)
- [ ] retry: consider demoting/blocking rollback of pre-seeded blocks whose undo payloads reference `$APB_` vars — a retry-session rollback runs the undo with the rolled-back run's value-passing vars empty (spec Semantics notes the edge; R2 review finding 4) (2026-07-05)
- [ ] climeta guardrails: make the flag drift guard two-way and cover `create --template`; replace the finalize magic-string with an explicit Documented field so man and zsh generators agree (2026-07-04)
- [ ] CI hardening batch: per-job cache keys + golangci-lint via go.mod `tool` directive; `concurrency` groups; dependabot (gomod+actions); `go mod tidy -diff` in CI (drop the GoReleaser hook); release-notes empty-file guard; a cheap macos-latest build+vet job (darwin-first releases, ubuntu-only CI) (2026-07-04)
- [ ] Unify the two cell-width engines: app code measures with mattn/go-runewidth while the charm v2 stack renders with clipperhouse/displaywidth — EA/emoji width disagreements can misalign frames (2026-07-04)
- [ ] Test hygiene: retire coverage_boost_test.go's symbol-pinning tests into behavioral per-file tests; add factories for the 124 copy-pasted `defaultTheme(), "default"` constructor calls; share the duplicated collectMsgs helper (2026-07-04)
- [ ] BASIC-tier residual (ADR-0012): FollowupPrompt's BASE text teaches the `run` tool unconditionally — under a BASIC harness the sentence should gate on tools wiring (left byte-identical in H1; only the folds gate today) (2026-07-06)

- [ ] Finish the `pkg/` promotion (ADR-0009 step 5, last piece): `pkg/runner`←orchestrator — the executor holds a `mux.Mux` (edit/float pane spawning; mux→config→cache) and calls `diff.Parse` (diff→theme); needs design (a narrowed executor-owned pane-spawn interface, or a public mux) rather than a mechanical cut. `internal/autorun`→`pkg/runner/auto` waits on the same (imports orchestrator + cache) (2026-07-04); note ui.Options also embeds reengage/askbridge/orchestrator types — the same seam inventory for any future ui/runner promotion

- [ ] `ui.Main` is a tested-but-dead ~50-line shim (zero production callers since ui.Run(Options); its doc claims an argv contract launcher.RunMain now owns) — delete it and its argv-parsing tests, or re-document it as deliberate compat surface (2026-07-04)

- [ ] `draft.Render` silently drops `file=` on a `Static:true` item instead of `draft.Validate` rejecting the contradictory combination (surfaced by the P1 classifier fidelity probe, 2026-07-04) — reject at submit time

- [ ] `sanitizeKey` collides ids `a-b`/`a_b` → same `APB_OUT_a_b` AND (since retention) the same capture file — add a validate warning for id pairs that sanitize identically (2026-07-04)

- [ ] Test the rollback×from-chain interleave (assisted failure footer: start a materialization chain, press Roll back mid-chain) — audited safe (prevAction guard + runMu) but untested (2026-07-04)
- [ ] Parameterize the draft↔file-validator round-trip test across lang/type classes — one fixture guards it today, but the divergence class it protects (draft rules vs `pkg/playbook/validate` agreeing) occurred once; a table over the lang/type classes would catch the next drift (2026-07-04)
- [ ] Cross-equality test pinning `internal/autorun`'s `effectiveNeeds` against `playbook.Block.EffectiveNeeds` — the two carry verbatim-duplicated `needs= ∪ from=` semantics (inherent to the DTO split), so a change to one must not silently diverge from the other (2026-07-04)

- [ ] KB compaction convergence memo: a persistently-over-budget file whose compactions are rejected pays one wasted AI call per refine→w commit — skip when content unchanged since the last rejected attempt (2026-07-05)

- [ ] validate `verify` quality warning is ID-only: a `{static}` or `create` block with `id=verify` silences the warning even though it can't prove the goal state — consider requiring the verify id on a runnable block (2026-07-05)
- [ ] validate `rollback` quality warning: a `rollback=` attr on a `{static}` block suppresses the warning even though a static block never runs — count rollback declarations only on runnable blocks (2026-07-05)

- [ ] validate `env-decl` quality warning false-positives on single-quoted braced literals (`echo '${DOC_VAR}'` warns) — quote-aware scanning or documented as accepted (2026-07-05)

- [ ] The structured draft's top-level verify Step can't declare `timeout=` (per-code-item only) — a long-running verify gets only the 10m default; add the field if a real case appears (2026-07-05)

- [ ] Timed-out failure messages name the ceiling but not the remedy — consider a hint suffix pointing at the `timeout=` fence attr (2026-07-05)

## Ideas

- [ ] (low priority) E2E/integration tests for the integration entry points (`launcher` entry points, `cmd` `selftest`/`mcpMain`) — spawn the real binary + drive a TUI/PTY. These render via live mux/model/TUI/driver so they're not unit-testable; coverage there is intentionally low. Would push total coverage 80%→~90% (2026-06-27)
- [ ] `inlineInput` (internal/launcher) opens `/dev/tty` unconditionally before the `inlineRunFn` seam, so the `assist` classify→route/cancel flow can't be exercised headless (its tests `t.Skip` without a TTY). Seam the TTY-open so the classify/cancel/route path gets real CI coverage (2026-07-02)
- [ ] Portability / progressive enhancement: the driver needs a Unix PTY + signals (`x/sys/unix`), so it's Linux/macOS-only. Evaluate a degraded no-PTY "plain exec" mode for a portable core, and a ConPTY-based Windows driver (large) (2026-06-27)
- [ ] `create`'s similar-playbooks banner uses a whole-string substring search (`store.Search(prompt)`), so multi-word prompts rarely match — make it per-word/token (2026-06-27)
- [ ] adapt-on-run leaves two temp files per run (`writeTempMarkdown` render+orig in /tmp, never reaped; orig written even when junk-guarded) — defer-cleanup after `ui.Run` returns (2026-06-27)
- [ ] Optional rich output via the kitty graphics protocol — images/charts in the pager (2026-06-26)
- [ ] A JUnit/XML-style report for `run --auto` (CI ingestion) — a plain-text run summary + a JSON per-run log under `${data}/ai-playbook/runs/` shipped 2026-07-01; a JUnit/XML format for CI test-reporters is still open (2026-06-26)
- [ ] Revisit the cwd rule for non-project_bound STORED playbooks: `run <slug>` now opens in the store content dir (runFile F4 rule, one-code-path fix 2026-07-03); decide whether stored playbooks deserve a stored-vs-file distinction (invocation cwd) or whether `workdir:` front matter suffices (2026-07-03)
