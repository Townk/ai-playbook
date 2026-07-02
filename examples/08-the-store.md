---
name: The store
description: Browse, search, and open saved playbooks from the store; point $AI_PLAYBOOK_DATA_DIR at a local fixture set to explore it without a live backend.
category: tutorial
tags: [tutorial, store]
created: 2026-06-30
---

# The store

A store is a directory — or a remote index — of saved playbooks. Instead of remembering file paths, you browse with `ai-playbook list`, narrow results with `ai-playbook search`, and open an entry with `ai-playbook show`. Any playbook that lives in a file-backed store also gets an **[edit]** button in the viewer: save the file and the viewer reloads automatically.

This chapter uses a small fixture store that ships with the tutorial so you can try everything without a live backend.

## Point $AI_PLAYBOOK_DATA\_DIR at the fixture store

The environment variable `$AI_PLAYBOOK_DATA_DIR` tells ai-playbook where to look for store entries. Export it before running any commands in this chapter:

```bash {static}
export AI_PLAYBOOK_DATA_DIR="$PWD/examples/store"
```

Run that in the shell where you will follow this walkthrough. Every `ai-playbook` command you issue in that shell now reads from `examples/store/`.

> [!TIP]
> Set `AI_PLAYBOOK_DATA_DIR` in your shell profile or in a `.envrc` managed by direnv to make the store permanent for a project directory.

## List all entries

```bash {static}
ai-playbook list
```

Expected output:

```text {static}
  SLUG               CATEGORY      TAGS
  note-rotate-logs   operations    logs, rotation, cleanup
  tidy-checkup       maintenance   build, test, health
```

The fixture store contains two entries:

- **tidy-checkup** — a quick project health check that runs a build and a test suite (category: maintenance; tags: build, test, health).
- **note-rotate-logs** — a log-rotation playbook that compresses yesterday's log file and removes files older than seven days (category: operations; tags: logs, rotation, cleanup).

## Search by tag

```bash {static}
ai-playbook search logs
```

Expected output:

```text {static}
  SLUG               CATEGORY      TAGS
  note-rotate-logs   operations    logs, rotation, cleanup
```

`search` matches against tags, slug, and description substring. Try `ai-playbook search build` to find `tidy-checkup`, or `ai-playbook search health` to get the same result via a different tag.

## Open a playbook from the store

```bash {static}
ai-playbook show tidy-checkup
```

This opens `tidy-checkup` in the viewer exactly as if you had run `ai-playbook run --file` on the file directly. The two runnable blocks — **Build** and **Test** — are available immediately; **Test** carries `needs=build` so it stays locked until **Build** succeeds.

### The [edit] button

Because `tidy-checkup` is file-backed (stored at `examples/store/tidy-checkup.md`), the viewer header shows an **[edit]** button alongside the playbook title.

> [!NOTE]
> **Without a mux** the **[edit]** button suspends the viewer and opens the file in your `$EDITOR` using `tea.ExecProcess`. When you save and quit the editor the viewer resumes and reloads the updated content automatically.
>
> **With a mux** (tmux, zellij, or Wezterm) the **[edit]** button opens the file in a new tab or split pane. The viewer stays open in its own pane and polls the file's modification time; as soon as you save in the editor pane the viewer refreshes without you switching back.

Try it: click **[edit]**, update the `description` line in the front matter, save, then switch back to the viewer. The new description appears in the header without closing and reopening the file.

> [!TIP]
> `ai-playbook show note-rotate-logs` opens the log-rotation playbook the same way. Its **compress** and **cleanup** blocks have a `needs=compress` dependency — a good example to browse before writing your own chained playbook.

## Validate a playbook

`ai-playbook validate` checks a playbook file for structural correctness and, when a model is configured, requests an AI review of the prose:

```bash {static}
ai-playbook validate --file examples/01-hello-run.md
```

The structural pass verifies:

- The front matter is valid YAML and contains the required keys (`name`, `description`, `category`, `created`).
- Every `needs=` reference points to a block `id` that exists in the same file.
- All fenced blocks are balanced (no unclosed fences or mismatched attributes).

The AI review pass runs when the Claude CLI backend is available and is skipped with a note otherwise (it never fails the check). Set `AI_PLAYBOOK_MODEL` (or `ASSIST_MODEL`) to choose the model; pass `--no-ai` to skip it for a purely deterministic or CI run.

> [!TIP]
> Run `ai-playbook validate --file examples/07-run-modes.md` to check the previous chapter's playbook blocks — a good habit before sharing any file with your team.

## What's next

That covers the core ai-playbook tutorial. From here, explore the reference documentation for the full list of front-matter keys, block attributes, and CLI flags — or open any playbook in `examples/store/` and start editing.
