# ai-playbook — contributor & agent guide

ai-playbook is a harness-agnostic, terminal-native AI assistant that turns live shell
context into runnable, reusable playbooks. **`docs/ROADMAP.md` is the source of truth**
for the feature roadmap; design lives under `docs/architecture/` and `docs/specifications/`.

## Build / test / format / install

- `go build ./...` · `go vet ./...` · `go test ./...` (the `ui` suite is slow, ~2 min — allow time).
- Format: `gofmt -w <files>`; CI gates on `gofmt -l` being empty.
- Install the binary: `go install ./cmd/ai-playbook` (deploys to `$GOBIN` / `~/.local/share/go/bin`).
- Lint (once CI lands): `golangci-lint run`.

## Commits

- **Conventional Commits** (`feat:` / `fix:` / `refactor:` / `docs:` / `chore:` + scope).
- **gpg-signed** — every commit. If signing stalls (pinentry/agent), **STOP and report**;
  **never** use `--no-gpg-sign`.
- **No** `Co-Authored-By` / AI-attribution trailers — the author owns the work.
- `git add` by **explicit path** (never `git add -A` / `.`).
- Default branch is `master`.

## Conventions

- **Harness-agnostic:** the model harness is pluggable (Claude today). Don't hardcode
  Claude specifics outside the harness/adapter layer.
- **Playbook schema** (the contract — see `docs/specifications/playbook-schema.md`):
  front matter `name/description/category/tags/env/workdir/depends_on`; fenced-block tags
  `{id=<id>}` (runnable; `{id=verify}` is the success check), `{rollback=<id>}`, `{static}`.
- **Layout:** golang-standards/project-layout (`cmd/ai-playbook/`, `internal/`, `pkg/`).
- **Specs are committed** to `docs/specifications/`; drafting happens in scratch and the
  approved version is committed. Architectural decisions get an ADR under
  `docs/architecture/adrs/`.

## Tracking (BACKLOG.md & CHANGELOG.md)

- **BACKLOG (`docs/BACKLOG.md`)**: read it at the **start** of a work session. When
  you find a bug, hit a limitation, or defer a task, **ADD** it under Bugs/Tasks/
  Ideas as `- [ ] <one-liner> (YYYY-MM-DD)`. When you finish a backlog item,
  **REMOVE** it (and add a CHANGELOG entry if it is user-facing). Keep it lean —
  prune done/stale items. It is **TACTICAL only**; strategy/phases live in
  `docs/ROADMAP.md`.
- **CHANGELOG (`CHANGELOG.md`)**: follow [keepachangelog.com](https://keepachangelog.com).
  Every **user-facing** change (feature, fix, breaking change, removal,
  deprecation) gets an entry under `## [Unreleased]` in the correct group
  (Added/Changed/Deprecated/Removed/Fixed/Security) **in the same commit as the
  change**. Internal-only refactors don't need an entry. On a release: rename
  `[Unreleased]` → `[X.Y.Z] - DATE`, add a fresh empty `[Unreleased]`, and tag.

## Documentation

`docs/ROADMAP.md` (roadmap) · `docs/architecture/` (overview + ADRs) ·
`docs/specifications/` (engineering specs) · `docs/guides/` (user guides) ·
`docs/configuration.md` (config reference) · `docs/BACKLOG.md` (tactical tracker) ·
`CHANGELOG.md` (user-facing changes).

## Verify before claiming done

Run `go build/vet/test ./...` green and `gofmt -l` clean before committing; quote the
result, don't assert it.
