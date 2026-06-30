# Sandbox tutorial — the `examples/` project garden + guided tour

Status: agreed (2026-06-30). A hands-on learning surface for ai-playbook: a set of fake
projects at varying completeness + a collection of demo playbooks that exercise EVERY
user-facing feature, plus a guided tutorial document. Pre-release artifact — it is written
as if the FULL feature set ships (incl. `--assisted`/`--auto`/`validate`, not yet built);
not released until all features land. Grounding: the code + `docs/specifications/*` (the
guides under `docs/guides/` are stubs; this fills the tutorial one).

## Goal & audience

Let a new user *learn ai-playbook by doing* — open a playbook, click the buttons, watch
each feature fire — without needing a real project or a configured model backend for most
of it. A reader who finishes the tour has touched every viewer affordance and authored one
playbook from a broken project.

## Decisions (locked)

- **Location:** in-repo, top-level `examples/` (versioned → doubles as manual-test
  fixtures). The guide lives at `docs/guides/tutorial.md` (fills the existing stub).
- **Two playbook flavors:** ① **ready-to-run viewer tours** — pre-written `.md`, run via
  `ai-playbook run --file <path>`, deterministic + offline + no harness; cover every viewer
  feature. ② **the fix-it chapter** — ONE deliberately-broken project authored live via
  `ai-playbook create`/`assist` (needs a model backend).
- **mux:** write for the **default no-mux** UX; add short *"with zellij/mux on, this opens
  as a float/tab"* callouts where behavior differs (view-diff, `[edit]`, assist `ask`).
- **Feature completeness:** the tour covers the COMPLETE intended feature set as if shipped,
  including `--assisted`/`--auto` run modes and `validate`. Scenarios depending on
  **not-yet-built** features are marked in the coverage matrix below (⏳) so they're verified
  as those features land.

## Layout

```
examples/
  README.md                     entry: start-here, prerequisites, the safety note, the chapter index
  01-hello-run.md               run/play/stop/copy · static blocks · callouts
  02-needs-verify-rollback.md   needs→blocked notice · verify block · rollback
  03-create-a-file.md           file= create/undo                      → projects/half-baked/
  04-edit-with-diff.md          diff view-diff/apply/undo              → projects/half-baked/
  05-diff-drift.md              drift region · [resolve manually] · [regenerate]  → projects/drifted/
  06-portable-and-env.md        project_bound · $PROJECT_ROOT · the env confirm gate → projects/portable/
  07-run-modes.md               --assisted (confirm-each-step) · --auto · stop  ⏳
  08-the-store.md               list/search/show · [edit]+reload · validate  ⏳(validate)
  09-fix-it.md                  the authoring chapter (points at projects/broken-build/)
  projects/
    tidy-shop/      healthy/complete   — happy-path run/verify (ch.01-02)
    half-baked/     partially complete — a missing file + a stale config (ch.03-04)
    drifted/        a shipped patch that no longer applies (ch.05, pre-drifted, git-tracked)
    portable/       project_bound, declares env: vars, blocks use $PROJECT_ROOT (ch.06)
    broken-build/   deliberately broken — the fix-it target (ch.09)
  store/            2 ready playbooks + a note to point $AI_PLAYBOOK_DATA_DIR here (ch.08)
docs/guides/tutorial.md          the ordered walkthrough (links each chapter playbook)
```

**Path discipline (the cwd rule):** `run --file` runs blocks in the **`.md` file's
directory** = `examples/`. So top-level chapter playbooks reference their project with
relative paths (`projects/half-baked/…`): run blocks `cat projects/half-baked/config.yml`,
`file=projects/half-baked/<new>`, diff patches targeting `projects/drifted/<file>`. The ONE
exception is **ch.06**, which is `project_bound: true` (`project_root: examples/projects/portable`)
so its cwd becomes the project root, `$PROJECT_ROOT` is exported, and the env gate fires.

## Feature coverage matrix (every user-facing feature → a chapter)

| Feature | Chapter | Trigger / setup |
|---|---|---|
| run ▶ / play / stop / copy | 01 | shell+run blocks; a `sleep` block to show stop; copy on any |
| static blocks (illustrative) | 01 | `{static}` / lang text·console·output·log·json |
| callouts (note/tip/important/warning/caution) | 01 | `> [!WARNING]` blockquotes |
| needs → blocked notice | 02 | `needs=<id>` on an unmet dependency |
| verify block | 02 | a top-level `verify` / `{id=verify}` |
| rollback | 02 | `rollback=<id>`; a failing later step triggers reverse rollback |
| file= create / undo | 03 | `{id=… file=projects/half-baked/<new>}`, body = file content |
| diff view-diff / apply / undo | 04 | `diff` block patching `projects/half-baked/config.yml` |
| drift region · resolve manually · regenerate · "didn't resolve" note | 05 | a `diff` block whose target in `projects/drifted/` was shipped pre-edited so `git apply --check` fails |
| project_bound · $PROJECT_ROOT | 06 | `project_bound: true`; blocks use `$PROJECT_ROOT/…` |
| env declaration · confirmation gate (grouped Confirm/Customize) | 06 | front-matter `env:` map (e.g. PROJECT_ROOT + one more) → first-run grouped gate |
| run modes: --assisted (confirm-each-step) · --auto | 07 ⏳ | `ai-playbook run --assisted --file …` / `--auto` |
| store: list · search · show | 08 | install 2 playbooks (via `$AI_PLAYBOOK_DATA_DIR=examples/store`) |
| [edit] source button + reload | 08 | `ai-playbook show <slug>` (file-backed → `[edit]`); edit + save → reload |
| validate | 08 ⏳ | `ai-playbook validate --file …` (structural + AI review) |
| authoring: create · assist (triage: command/answer/escalate) · escalate→submit_playbook | 09 | `ai-playbook create "fix the build"` in `broken-build/`; `assist` after a failing command |
| re-engagement: regenerate · followup ("try another fix") · cached badge | 09 | a failed step → "try another fix"; whole-playbook regenerate; re-run a cached result → badge |
| mux vs no-mux | callouts | every float/tab/overlay difference flagged inline |

## The fake projects (completeness + required ship-state)

- **tidy-shop/** — a small, *complete & healthy* fake project (e.g. a tiny shell-script
  "shop" CLI with a build/test script that succeeds). Ch.01-02 run its commands (idempotent,
  relative-path) to show the happy path + verify + a deliberately failing step for rollback.
- **half-baked/** — *partially complete*: ships **missing** a file (ch.03 `file=` creates it)
  and with a **stale config** value (ch.04 `diff` patches it). After the demos it's "more
  complete."
- **drifted/** — ships a config file whose content **does not match** the patch embedded in
  ch.05's `diff` block (so `git apply --check` fails on open → the drift region). Must be
  git-tracked (the drift check shells `git apply --check`). Ch.05 then demos resolve-manually
  + regenerate.
- **portable/** — `project_bound`; declares `env:` (PROJECT_ROOT + e.g. a fake `DATA_DIR`);
  its blocks reference `$PROJECT_ROOT/…`. Ch.06 shows the export + the confirmation gate.
- **broken-build/** — a *deliberately broken* fake project (e.g. a build script that fails
  on an obvious, fixable cause). Ships a README: "ask ai-playbook to fix me". Ch.09's
  authoring target.

## Playbook `.md` format (author to the CODE, not the stale schema doc)

Front matter is a YAML map (`frontmatter` pkg): `name`, `description`, `category`, `tags`,
`env` (a **map** `NAME: {value, why}`), `project_root`, `project_bound`, `created`. Body =
`# <Title>` H1 + `## <Section>` headings + fenced blocks. Fence grammar:
` ```<lang> {id=<id> needs=<a,b> rollback=<id> file=<path>}` or ` ```<lang> {static}`.
Block kinds (`classifyType`): bash/sh/zsh → shell; diff/patch → diff; `file=` → create;
other langs → run; text/console/output/log/json/empty or `{static}` → static. Callouts =
`> [!TYPE]` (note|tip|important|warning|caution). A `file=` body is the full new-file
content; a `diff` body is a unified patch (diff edits, file= creates). Minimum valid: an H1
+ ≥1 fenced block. (The verbatim per-block templates from the grounding go into the plan.)

## The tutorial document (`docs/guides/tutorial.md`)

An **ordered walkthrough**, one section per chapter, each step shaped:
> **Run:** `ai-playbook run --file examples/03-create-a-file.md`
> **You'll see:** … **Click:** the `create` button on the `file=` block. **Notice:** the
> button flips to `undo`; the file now exists. **Undo:** click `undo` to remove it.

Sequenced so features build (basics → file changes → drift → portability → run modes →
store → author-your-own). Opens with prerequisites (install, optional model backend for
ch.09, the no-mux/mux note) + the **safety note** (blocks run real shell in the project dir;
nothing runs until you click; apply/create demos modify tracked example files — undo via the
button or `git restore`). mux callouts inline where behavior differs. Ends with ch.09:
point `ai-playbook create` at `broken-build/` and watch a playbook get authored.

## Safety & determinism

- Every run block is **idempotent, relative-path, in-project-dir**; nothing touches `$HOME`
  or system state. The env confirm gate's exports are scoped to the run.
- Viewer-only is safe (buttons are click-triggered). The tour leads with this.
- apply-diff / file=-create / drift-regenerate **modify tracked `examples/` files** → the
  guide tells the reader to `undo` (button) or `git restore examples/`. (Acceptable for a
  hands-on tutorial; the projects are fixtures.)
- ch.05 drift requires the `drifted/` file to ship in a state where the patch fails —
  verified at build time (`git apply --check` against the shipped file fails).

## Out of scope

- Changing any product behavior (this is content only; if a demo reveals a real bug, file it
  separately).
- A runner/harness for the tutorial (it's run by hand via the CLI).
- CI that executes the playbooks (a possible later enhancement; for now the playbooks are
  manually verified, and the ⏳ chapters are verified as their features land).

## Decomposition (for the plan)

Build in chunks, each independently reviewable: (1) the `examples/` scaffold + README +
the fake projects in their required ship-states; (2) the deterministic viewer-tour playbooks
01–06 (+ verify each renders/runs); (3) the run-modes (07) + store (08) chapters incl. the
store fixtures; (4) the fix-it project + ch.09; (5) `docs/guides/tutorial.md` tying it all
together. The ⏳ (assisted/auto/validate) scenarios are written complete but flagged for
post-feature verification.
