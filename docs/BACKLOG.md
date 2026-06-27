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

## Ideas

- [ ] Cross-block output piping (runme parity; minor) (2026-06-26)
- [ ] Optional rich output via the kitty graphics protocol — images/charts in the pager (2026-06-26)
- [ ] A structured / JUnit-style report for `run --auto` (CI) (2026-06-26)
