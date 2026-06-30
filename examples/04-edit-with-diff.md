---
name: Edit with Diff
description: Apply a unified diff to an existing file, review the change before committing it, and undo it with a single click.
category: tutorial
tags: [tutorial, diff, edit, undo]
created: 2026-06-30
---

# Edit with Diff

Chapter 03 created a file from scratch. This chapter edits one that already exists. `diff` blocks let you ship a precise, reviewable change: you see the old line in red, the new line in green, and you decide whether to apply it. If you change your mind, **Undo** restores the original.

The target file is `projects/half-baked/config.yml`. It currently declares `version: "1.0"` — stale. The diff below bumps it to `"2.0"`.

## View and apply the diff

The `bump` block is a unified diff. Before you apply it, click **View diff** to open the side-by-side comparison. You will see the old value (`"1.0"`) highlighted in red and the replacement (`"2.0"`) in green.

When you are happy with what you see, click **Apply**:

```diff {id=bump}
--- a/projects/half-baked/config.yml
+++ b/projects/half-baked/config.yml
@@ -1,6 +1,6 @@
 app:
   name: half-baked
-  version: "1.0"
+  version: "2.0"
   description: A partially-complete project used for tutorial demos.
 
 server:
```

`config.yml` now reads `version: "2.0"`. The **Apply** button has become **Undo** — click it any time to roll the file back to its original content.

> [!TIP]
> **View diff before every apply.** The side-by-side view makes it obvious if a patch was authored against a slightly different version of the file — mismatched context lines show up immediately as unexpected red regions. Catching that visually is faster than diagnosing a failed `git apply`.

## Verify

Confirm the version field was updated:

```bash {id=verify needs=bump}
grep 'version' projects/half-baked/config.yml
```

You should see `  version: "2.0"`. After you click **Undo** on the diff block above, run **Verify** again — it will print `  version: "1.0"`, confirming the original was restored.

> [!NOTE]
> `diff` blocks support the same `needs=` and `rollback=` attributes as `bash` blocks. If a later step in a sequence fails, any applied diffs in the rollback chain are automatically reverted — restoring the file to its pre-apply state without manual intervention.

---

Chapter 05 shows what happens when a diff cannot apply cleanly because the file has drifted from the version the patch was authored against.
