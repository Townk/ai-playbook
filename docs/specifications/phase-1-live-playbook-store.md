# Live Playbook Store — Phase 1 (design spec)

**Status:** draft for review
**Date:** 2026-06-26
**Scope:** the store + its commands, the `assist`/`create` split, adapt-on-run. Viewer
affordances (edit button + file-watching, assisted-run + rollback) are **Phase 2**.

## Goal

Turn the accumulating saved playbooks into a usable, reusable library. Today every
finalized playbook is saved to `~/.local/share/ai-playbook/playbooks/<slug>.md` with
rich front matter (name, description, category, tags, env) but is only reachable via
an **exact** context+request cache hit — so the library is effectively write-only.

Phase 1 makes the library:
- **Browsable / searchable** via the CLI as a data source for an external FZF pick
  (no in-app browser TUI, no new ai-playbook ZLE widgets).
- **Re-runnable** with **adaptation to the current project** (`run <slug>`).
- **Editable** (`edit <slug>` → `$EDITOR`).
- Cleanly separated at the entry verbs: **`assist`** (triage) vs **`create`** (direct
  authoring).

## Background / current state

- `CommitPlaybook` → `~/.local/share/ai-playbook/playbooks/<slug>.md`, YAML front
  matter assembled by us (name/slug/env/provenance) + model (description/category/
  tags). `finalize` backfills front matter onto older playbooks.
- `troubleshoot` (the ZLE trigger) = the triage entry: float → classify (command /
  answer / escalate) → route; escalate serves from cache (with the cached/regenerate
  badge) or authors.
- `run <file>` is the in-process viewer entrypoint; `serveCachedPlaybook` and
  `answer` reshape `os.Args` to `run <tmpfile>`.

## Command surface (Phase 1)

```
assist [<prompt>]                              triage workflow (the ONLY triage entry)
create <prompt>                                author a playbook directly (always fresh)
list   [--format human|fuzzy-data-source|json] list the store
search <query> [--format ...]                  filter the store
show   <slug>                                  render a store playbook (read-only)
run    [<slug> | --playbook <slug> | --file <path>]  adapt-on-run + drive
edit   <slug>                                  open the playbook in $EDITOR
  (internal/aux unchanged: session · answer · finalize · mcp · input · selftest)
```

### `assist` (renamed from `troubleshoot`)
Behaviorally identical to today's `troubleshoot`: the request float, the cheap
classify, the command/answer/escalate routing, the cache serve, and the
cached/regenerate badge. It is the **only** path that reaches triage. The chezmoi ZLE
trigger is repointed `troubleshoot` → `assist`.

### `create <prompt>`
Direct playbook authoring — **no triage, no cache serve, no cache badge**. It:
1. Force-authors a playbook from `<prompt>` (reuses the authoring path:
   `openSession` + `authorPlaybook`, bypassing the classify and the cache-hit serve),
   streaming into the invoking pane's pager.
2. On finish, persists via `CommitPlaybook` to **both the store and the cache** (so a
   later `assist` for the same context can hit it) — but `create` itself never *serves* a
   cache hit; it always does the work.
3. Surfaces a one-line **"similar playbooks already exist: X, Y"** banner when a store
   search on the prompt finds matches — informational only; authoring proceeds.
- `--template <t>` is **reserved** (a future phase for manual templates); not
  implemented in Phase 1.

### `run [<slug> | --playbook <slug> | --file <path>]`
- A bare positional is implied `--playbook <slug>`. Exactly one of {positional,
  `--playbook`, `--file`}; error on conflict or none.
- `--playbook <slug>` / bare slug → store playbook → **adapt-on-run** (below).
- `--file <path>` → a raw markdown file. Internal callers (`serveCachedPlaybook`,
  `answer`) switch to `run --file <tmpfile>`. A file **with** front matter (a
  `workdir`) adapts; a raw file **without** front matter runs **as-is**.

### `edit <slug>`
Resolve the slug → spawn `$EDITOR <path>` (no viewer). `$EDITOR` unset → error.

## The `store` package

```go
type Meta struct {
    Slug, Name, Description, Category string
    Tags        []string
    Env         []EnvVar   // from front matter
    Workdir     string     // target dir (see below); "" if unknown
    Path        string     // absolute file path
    Project     bool       // true → found under PROJECT_ROOT/.ai-playbook (slug is `proj:`-prefixed)
    Created     time.Time
}
func Index() ([]Meta, error)              // scan BOTH stores, parse front matter, sort newest-first
func Load(slug string) (Meta, body string, error)
func Search(query string) ([]Meta, error)// substring across name/description/category/tags
func PathFor(slug string) (string, bool)
```
**Two stores, scanned together:**
- **Global:** `${XDG_DATA_HOME}/ai-playbook/playbooks/*.md` (the `AI_PLAYBOOK_DATA_DIR`
  root). Slug = filename stem.
- **Project-local:** `${PROJECT_ROOT}/.ai-playbook/playbooks/*.md` (PROJECT_ROOT from the
  existing capture / `AI_PLAYBOOK_PROJECT_ROOT`, git-root fallback). Slug = **`proj:`** +
  filename stem, so project playbooks are referenced unambiguously by `dependencies`
  (Phase 3) and CI. This is what makes playbooks docs-as-code: commit
  `.ai-playbook/playbooks/` in the repo, share with the team.

Resolution: a bare slug → the **global** store; `proj:<stem>` → the **project** store
(explicit, no shadowing surprises). On-demand directory scan, no DB. A malformed file is
skipped (logged), never fatal.

## Front matter: add `workdir`

Add a `workdir` field = the target directory the playbook applies to. Populated at
author/commit time from the authoring project root (provenance). `finalize` backfills
it onto existing playbooks where provenance is available; otherwise it stays empty and
`run` asks for it. Home paths normalize to `~` for portability (as env values already
do).

## `--format` outputs (`list` / `search`)

- **`human`** (default): aligned columns — name, description, category, age — for
  terminal reading.
- **`fuzzy-data-source`**: one record per line, `\x1f` (US)-delimited:
  `<display>\x1f<slug>\x1f<path>`
  - `display` (field 1) = `<name> — <description> · <category> · <tags>`; FZF shows +
    searches this via `--with-nth 1`.
  - `slug` (field 2) → `ENTER` binding runs `ai-playbook run {2}`.
  - `path` (field 3) → `ALT+ENTER` binding runs `ai-playbook edit {2}` (slug) — path
    provided so the pick needn't re-resolve.
- **`json`**: array of `Meta` for scripting.

## Adapt-on-run

`run --playbook <slug>`:
1. `store.Load(slug)` → playbook + `workdir`.
2. **Resolve target dir:** default = `workdir`. If empty or the dir doesn't exist →
   `ask` the user (the input float) for the target directory ("stale workdir"
   confirmation when it exists but you want to override is a nicety, optional).
3. **Adapt:** one **authoring-model** call (default thinking) rewrites the playbook for
   the target context — paths, versions, project-specifics — producing the adapted
   playbook.
4. **Show + drive:** render the adapted playbook in the pager with a banner
   *"adapted from `<slug>`"* and a `d` keybind to view the original→adapted diff; the
   user drives the run-blocks as normal.
5. **Junk-guard:** if the adaptation isn't a valid playbook (no H1 / no runnable
   blocks), fall back to the original (reuse the existing `isValidPlaybook` /
   replace-protection guard).

`run --file` without front matter → skip steps 1–5's adapt, render + drive as-is.

## Cache-badge gating

The pager's cached/regenerate badge (`isCached` + the regenerate pill) renders **only**
for an `assist` cache-hit serve. `create` and `run` (adapt) never show it. (The
`canRegenerate`/`appendCachedButton` gating is already in place; ensure `create`/`run`
paths leave `isCached` false.)

## Error handling

- Empty store → friendly "no saved playbooks yet."
- Unknown slug → clear error (and the FZF pick only emits real slugs).
- Adapt failure / junk → original (no block).
- `$EDITOR` unset → error.

## Testing

- `store` Index/Load/Search: front-matter parse, newest-first sort, substring search,
  malformed-file skip.
- `--format` outputs: the `fuzzy-data-source` field layout (3 `\x1f` fields, display
  composition), human columns, json shape.
- `run` arg resolution: bare positional ⇒ `--playbook`; `--playbook`/`--file`
  exclusivity; error on none/both.
- Adapt-on-run: workdir resolve, the ask-for-target path, the adapt prompt, the
  junk→original fallback; `--file` no-front-matter as-is.
- `create`: force-author (no classify/cache-serve), store+cache write, `isCached`
  false, the similar-playbooks banner from a seeded store.
- `assist`: the rename keeps the triage + cache badge intact.
- `edit`: `$EDITOR` spawn argv.

## chezmoi pairing (dotfiles repo — separate commits, `feat/ai-playbook-phase-b`)

- **FZF pick** (a ZLE widget): `ai-playbook list --format fuzzy-data-source` piped to
  `fzf --delimiter $'\x1f' --with-nth 1 --bind 'enter:execute(ai-playbook run {2})'
  --bind 'alt-enter:execute(ai-playbook edit {2})'` (exact binding finalized at impl).
- **Trigger rename:** the assist ZLE trigger → `ai-playbook assist`.

## Out of scope (Phase 2)

- The **edit** tag-button + viewer **file-watching** (re-render on save).
- The **assisted-run** button + per-step confirm + **execution log**.
- The **rollback-block** schema (`{rollback=<id>}`), the authoring-prompt rollback
  emission, and the assisted-run rollback flow (reverse-order rollback of completed
  steps on failure).
- `create --template` (manual playbook templates).
