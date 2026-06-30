---
name: Create a File
description: Use a file= block to materialise a config file on disk, then undo it — your first look at the create and undo affordance.
category: tutorial
tags: [tutorial, file, create, undo]
created: 2026-06-30
---

# Create a File

Chapters 01 and 02 ran commands and chained steps. This chapter introduces a different kind of block: `file=`. Instead of executing code, a `file=` block writes its body directly to disk. Click **Create** and the file appears; click **Undo** and it disappears. No shell required.

The project for this chapter is `projects/half-baked` — a partially-complete application that is missing a few local-override files the real app needs at runtime. You will create one of them now.

## The file= block

The `local` block below targets `projects/half-baked/config.local.yml`. Its body is the complete content that will be written to disk when you click **Create**.

```yaml {id=local file=projects/half-baked/config.local.yml}
# Local overrides — not committed to version control.
# Values here take precedence over config.yml at runtime.
app:
  debug: true
  log_level: debug

server:
  port: 9090
```

Click **Create** now. The file appears at `projects/half-baked/config.local.yml`. The button label flips to **Undo**, ready for you to revert whenever you like.

> [!TIP]
> **Why `file=` instead of `bash`?** A `file=` block is declarative: the body *is* the content, byte for byte. A `bash` block running `cat > file` would also write the file, but ai-playbook would have no record of what changed and could not undo it automatically. With `file=`, the create/undo pair is built in.

## Verify

Run the block below to confirm the file exists:

```bash {id=verify}
test -f projects/half-baked/config.local.yml && echo created
```

If you see `created`, the create worked. Now click **Undo** on the `local` block above and run **Verify** again — this time the test exits non-zero and prints nothing, confirming the file is gone.

> [!NOTE]
> Undo deletes the file entirely. If you click **Create** a second time, ai-playbook overwrites any existing file with the block's body. This makes `file=` blocks idempotent: applying them twice leaves the same result as applying them once.

---

Chapter 04 moves from creating files to editing existing ones — using a `diff` block to apply a precise, reviewable change to `config.yml`.
