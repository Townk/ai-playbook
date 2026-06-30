---
name: Diff Drift
description: See what happens when a diff was authored against an older version of the file — and learn how to resolve or regenerate it.
category: tutorial
tags: [tutorial, diff, drift, resolve, regenerate]
created: 2026-06-30
---

# Diff Drift

A diff is a promise: "the file looks like this, so apply that change." When the file has moved on since the patch was written, the promise is broken. ai-playbook calls this **drift** — the patch's context no longer matches the file on disk.

This chapter uses `projects/drifted/settings.conf`. The patch below was authored when the file still had `timeout = 30`. Someone later edited the file and changed that value to `99`. The patch still tries to match `timeout = 30` — and fails.

## Triggering drift

The `settimeout` block targets `settings.conf`. Click **Apply** and watch what happens:

```diff {id=settimeout}
--- a/projects/drifted/settings.conf
+++ b/projects/drifted/settings.conf
@@ -2,7 +2,7 @@
 host = localhost
 port = 8080
-timeout = 30
+timeout = 60
 max_connections = 100
 
 [logging]
```

Instead of applying cleanly, ai-playbook highlights a **drift region** — the lines where the patch's expected context (`timeout = 30`) does not match what the file actually contains (`timeout = 99`). The **Apply** button is replaced by two options:

- **Resolve manually** — opens the file in your editor with conflict markers, letting you merge the change by hand. Use this when the correct new value depends on business logic you need to reason about — in this case, deciding whether `60` or some other value is right given that someone deliberately set it to `99`.
- **Regenerate** — sends the current file content back to the AI that authored the patch and asks it to rewrite the diff against the real file. Use this when the patch intent is clear but the context simply needs refreshing.

Neither option is automatically the right one. If you know *why* the file drifted you should resolve manually; if the change is mechanical and the AI context is still accurate, regenerate is faster.

> [!NOTE]
> **Viewing the diff when drift occurs:**
>
> - *Without a terminal multiplexer:* ai-playbook displays the conflict inline below the block in the same pane. Scroll down past the drift region to see the full context of what the patch expected versus what the file contains.
> - *With a multiplexer active (tmux, Zellij, WezTerm):* the diff opens in a floating overlay so you can compare patch and file side by side without leaving your current pane. Close the overlay to return to the playbook.

> [!WARNING]
> Drift is not an error in the patch format — the diff itself is syntactically valid. It is a semantic mismatch between the patch's assumptions and the file's current state. Running `git apply --check` on a drifted patch exits non-zero, which is exactly how ai-playbook detects it before attempting to modify the file.

---

Chapter 06 introduces **portability** and the **env confirmation gate** — how `project_bound` anchors a playbook to a specific project root, and how the `env:` map lets you (and collaborators) confirm or override environment variables before any block runs.
