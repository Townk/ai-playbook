---
name: Needs, Verify, and Rollback
description: Chain steps with needs=, guard your work with a Verify section, and roll back your changes with one click when a step fails.
category: tutorial
tags: [tutorial, needs, rollback, verify]
created: 2026-06-30
---

# Needs, Verify, and Rollback

Chapter 01 showed you how to run individual blocks. This chapter introduces three features that turn a playbook into a reliable sequence: `needs=` for ordering, `## Verify` for end-to-end confirmation, and `rollback=` to undo your changes when a step fails.

All commands run relative to `examples/`. The project used here is the same `tidy-shop` from chapter 01.

## Setup: seeding a workspace

Before a step can read a file, that file must exist. The `prep` block below seeds a tiny workspace inside `tidy-shop`:

```bash {id=prep}
mkdir -p projects/tidy-shop/.work
echo seeded > projects/tidy-shop/.work/seed
```

Run it now. A directory `.work/` and a file `seed` appear inside the project. The block is idempotent — running it again simply overwrites the seed file with the same content.

## Blocking with needs=

The block below reads the file that `prep` created. Notice its fence tag: `{id=use needs=prep}`. The `needs=prep` attribute tells ai-playbook that this block cannot run until `prep` has completed successfully.

```bash {id=use needs=prep}
cat projects/tidy-shop/.work/seed
```

If you click **▶ Run** on `use` before running `prep`, you will see a **blocked** notice instead of output — ai-playbook refuses to start the dependent step until its dependency is green. Run `prep` first and the notice clears automatically.

> [!TIP]
> `needs=` accepts a comma-separated list: `needs=a,b,c`. Every listed block must succeed before the dependent block unlocks.

## Rollback: undo when things go wrong

A rollback block is the cleanup pair for a step that makes a change. You declare the pair with `rollback=<id>` on the step that creates the change:

```bash {id=stage rollback=undo-stage}
mkdir -p projects/tidy-shop/.work
touch projects/tidy-shop/.work/stage-marker
echo "stage marker created"
```

The rollback target tears down what `stage` put in place:

```bash {id=undo-stage}
rm -f projects/tidy-shop/.work/stage-marker
echo "stage marker removed"
```

You would not normally click `undo-stage` by hand. Instead, when a later step fails, the viewer offers a **Rollback playbook** button that runs it for you. The block below is *designed* to fail so you can see that button appear:

```bash {id=boom needs=stage}
exit 1
```

> [!CAUTION]
> The `boom` block is **designed to fail**. After it fails you can click **Rollback playbook** to undo what `stage` put in place. Do not click Rollback in a real workflow where `stage` has made changes you want to keep.

Run `stage` first (it creates the marker), then run `boom`. Because `boom` exits 1, the header shows a **⚠ a step failed** indicator and the failed block grows a **Rollback playbook** button. Click it: ai-playbook runs the rollback chain in reverse — `undo-stage` runs and the stage marker disappears.

> [!WARNING]
> Rollback runs **in reverse registration order**: the most recently applied step is undone first. Design each rollback to be safe even if the forward step only partially completed — for example, use `rm -f` instead of `rm` so a missing file is not itself an error.

## Verify

`## Verify` is a top-level section that ai-playbook recognises by name. The viewer surfaces it as a distinct affordance — a quick health-check you can re-run any time to confirm the project is in a good state, independent of the steps above.

```bash {id=verify}
bash projects/tidy-shop/test.sh
```

Click **▶ Run** in the Verify section. You should see `tidy-shop: tests passed`. If rollback left the workspace in an unexpected state, fix it and run Verify again until it goes green.

---

That covers the three sequencing features. Chapter 03 introduces `file=` blocks for creating new files — and how to undo them.
