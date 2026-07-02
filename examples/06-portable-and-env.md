---
name: Portable & env
description: Demonstrates $PROJECT_ROOT export and the variable confirmation gate
category: tutorial
tags: [tutorial, portability]
project_bound: true
project_root: examples/projects/portable
env:
  PROJECT_ROOT:
    value: examples/projects/portable
    why: the project this playbook operates in
  DATA_DIR:
    value: $PROJECT_ROOT/data
    why: where the playbook writes its working data
created: 2026-06-30
---

# Portable & env

Every chapter up to this point ran with its current working directory set to `examples/`. This chapter is different: it is **project-bound**. When you open it, the viewer changes directory to the project root (`examples/projects/portable`) and exports `$PROJECT_ROOT` pointing at that same path. All paths in the blocks below are relative to the project, not to the repository root — which is what makes a playbook **portable**.

## How project_bound works

Two front-matter keys drive the behaviour:

- `project_bound: true` — tells the viewer to anchor the playbook to a specific project directory instead of the default `examples/` cwd.
- `project_root: examples/projects/portable` — the path (relative to the repository root) that becomes the working directory and is exported as `$PROJECT_ROOT`.

When you open this playbook the viewer:

1. Resolves `examples/projects/portable` relative to the repository root.
2. Sets the working directory to that path for every shell block.
3. Exports `$PROJECT_ROOT` so blocks can reference it explicitly.

## The env map and the confirmation gate

The front matter also declares an `env:` map — a set of variables with default values and a short explanation of each:

```yaml {static}
env:
  PROJECT_ROOT:
    value: examples/projects/portable
    why: the project this playbook operates in
  DATA_DIR:
    value: $PROJECT_ROOT/data
    why: where the playbook writes its working data
```

On the **first block run** of a session, ai-playbook pauses and shows a grouped **Confirm / Customize** dialog — one dialog per ≤5 variables. The dialog shows each variable's name, current default, and the `why` explanation. You can accept all defaults or override individual values before the block executes. Once confirmed, the values are exported into the shell environment for the rest of the session.

> [!NOTE]
> **Mux mode:** With a terminal multiplexer active, the Confirm / Customize dialog appears inline in the playbook pane. Without a mux it appears as a full-screen overlay — identical content and behaviour, different layout.

This means you can share a playbook with a colleague who has the project checked out at a different path: they simply update `PROJECT_ROOT` in the confirmation dialog, and every downstream reference (`$DATA_DIR` and any others built on top of it) resolves correctly without editing the file.

The confirmation gate also applies when running this playbook non-interactively. `ai-playbook run --assisted --file examples/06-portable-and-env.md` opens the guided viewer and confirms the variables as soon as it loads—before the first step—so you can review and adjust them up front. With `ai-playbook run --auto --file examples/06-portable-and-env.md`, the playbook runs unattended: it requires the variables already set in the environment (export them first) and errors listing any that are missing — or supply them with `--with-env '{"PROJECT_ROOT":"…","DATA_DIR":"…"}'` (or `--with-env env.json`) instead of exporting.

## Writing to $DATA_DIR

The block below seeds a small file inside the project's `data/` directory. Notice it references `$DATA_DIR` — which the confirmation gate resolved for you — rather than any absolute path.

```bash {id=seed}
echo "Project root: $PROJECT_ROOT" && mkdir -p "$DATA_DIR" && echo seeded > "$DATA_DIR/seed.txt" && echo "wrote $DATA_DIR/seed.txt"
```

The block is idempotent: running it a second time overwrites `seed.txt` with the same content and prints the same message.

## Reading back from $DATA_DIR

The block below reads the file that `seed` created. The `needs=seed` attribute ensures it cannot run until `seed` has completed successfully.

```bash {id=show needs=seed}
cat "$DATA_DIR/seed.txt"
```

You should see `seeded`. If you run `show` before `seed`, ai-playbook will show a **blocked** notice and refuse to start — the file would not exist yet.

> [!TIP]
> `$DATA_DIR` is itself defined as `$PROJECT_ROOT/data`, so it inherits any override you made to `$PROJECT_ROOT` in the confirmation gate. Change the root once; every derived variable follows.

## Why portability matters

Hard-coding `/Users/alice/projects/portable` into a shell block means the playbook breaks the moment anyone else opens it. By anchoring all paths to `$PROJECT_ROOT` and declaring that variable in the `env:` map you get:

- **Zero-edit sharing** — recipients confirm (or adjust) a single variable in the gate; the rest of the playbook is untouched.
- **CI compatibility** — a CI runner exports `PROJECT_ROOT` before invoking ai-playbook non-interactively, skipping the interactive gate entirely.
- **Auditability** — the `why:` field in each env entry documents intent right next to the value, in the file, so there is no separate wiki page to keep in sync.

---

That covers project-bound playbooks and the environment confirmation gate. Chapter 07 introduces non-interactive (headless) execution, where the gate is bypassed in favour of environment variables exported by the caller.
