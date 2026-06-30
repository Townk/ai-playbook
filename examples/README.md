# ai-playbook examples

A hands-on learning surface for ai-playbook. Five fake projects at varying completeness,
two demo store fixtures, and nine chapter playbooks that exercise every user-facing feature.

Work through the chapters in order. Each one is self-contained — you can stop and resume at
any point. Complete the full tour to have touched every viewer affordance and authored one
playbook from a broken project.

## Prerequisites

- `ai-playbook` installed and on your `$PATH`
- (ch.09 only) a configured model backend — ensure `$AI_PLAYBOOK_MODEL` is set (`echo "$AI_PLAYBOOK_MODEL"` to verify)

## Safety note

**Nothing runs until you click.** Playbooks are view-only until you press the run/apply/create
button on a block. However:

- **Shell blocks** run real commands in the project directory. The examples are idempotent and
  touch nothing outside `examples/`.
- **Apply / create / shell demos** (ch.02/03/06 create new files; ch.04/05 modify tracked files)
  write to `examples/projects/`. Undo via the **Undo** button on the block, or reset both
  tracked edits and created files with:
  ```
  git restore examples/ && git clean -fd examples/
  ```
- The store chapter (ch.08) reads from `examples/store/` — point `$AI_PLAYBOOK_DATA_DIR`
  there to try it live.

## Chapter index

| Chapter | File | Feature(s) demonstrated |
|---------|------|------------------------|
| 01 | [01-hello-run.md](01-hello-run.md) | run ▶ / play / stop / copy · static blocks · callouts |
| 02 | [02-needs-verify-rollback.md](02-needs-verify-rollback.md) | needs → blocked notice · verify block · rollback |
| 03 | [03-create-a-file.md](03-create-a-file.md) | file= create / undo |
| 04 | [04-edit-with-diff.md](04-edit-with-diff.md) | diff view-diff / apply / undo |
| 05 | [05-diff-drift.md](05-diff-drift.md) | drift region · resolve manually · regenerate |
| 06 | [06-portable-and-env.md](06-portable-and-env.md) | project_bound · $PROJECT_ROOT · env confirm gate |
| 07 | [07-run-modes.md](07-run-modes.md) | --assisted (confirm-each-step) · --auto · stop |
| 08 | [08-the-store.md](08-the-store.md) | list / search / show · [edit]+reload · validate |
| 09 | [09-fix-it.md](09-fix-it.md) | authoring: create · assist · escalate · followup · regenerate · cached |

Full walkthrough: [docs/guides/tutorial.md](../docs/guides/tutorial.md)

## Project garden

| Project | State | Used in |
|---------|-------|---------|
| `projects/tidy-shop/` | Healthy, complete | ch.01–02 |
| `projects/half-baked/` | Missing file + stale config | ch.03–04 |
| `projects/drifted/` | Pre-drifted (patch won't apply) | ch.05 |
| `projects/portable/` | project_bound with env vars | ch.06 |
| `projects/broken-build/` | Broken build script | ch.09 |
