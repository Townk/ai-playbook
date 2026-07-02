---
name: Run modes
description: Run a playbook three ways — step through manually with the run subcommand, use --assisted to confirm each block, or go fully hands-off with --auto.
category: tutorial
tags: [tutorial, run-modes]
created: 2026-06-30
---

# Run modes

Every chapter up to this point has opened in the interactive viewer where you click **Run** on each block. That works well for exploration, but ai-playbook also ships a `run` subcommand that drives the same blocks from your terminal — useful for shell scripts, automation, and CI pipelines.

This chapter is both a live playbook and a tutorial about running playbooks. The three blocks below build and verify `tidy-shop`; the prose that follows shows you three different ways to invoke the whole file.

Open this file in the viewer to run the blocks interactively, or keep reading to learn how to drive it from the terminal.

## Build

```bash {id=build}
bash projects/tidy-shop/build.sh
```

## Test

```bash {id=test needs=build}
bash projects/tidy-shop/test.sh
```

## Status

```bash {id=status needs=test}
echo "tidy-shop: all checks passed at $(date +%H:%M:%S)"
```

The three blocks are **idempotent** — running them a second time produces the same exit code. The `needs=` chain enforces order: `test` will not start until `build` succeeds, and `status` will not start until `test` succeeds.

---

## Manual step-through

The simplest way to drive a playbook from the terminal is the `run` subcommand with no extra flags:

```bash {static}
ai-playbook run --file examples/07-run-modes.md
```

This opens an interactive fullscreen viewer (the pager) where you can click or press **Run** to execute each block. You see the output stream in place, the block turns green on exit 0, and ai-playbook displays the next block ready to run. Press **q** to quit and exit the viewer.

This mode is ideal when you want to step through an unfamiliar playbook, read each command before it runs, and keep one hand on the keyboard.

## Assisted run

The `--assisted` flag opens the same fullscreen viewer as manual step-through, but in **guided mode**:

```bash {static}
ai-playbook run --assisted --file examples/07-run-modes.md
```

A **ready** cursor (`▶`) points at the next runnable block, and the viewer auto-scrolls that block into view — about a third of the way down the screen — so you can read its prose before deciding what to do with it. A footer with focusable buttons, **`[ Run ] [ Skip ] [ Quit ]`**, confirms each step: move focus with **←/→** or **Tab**, and select with **Enter**/**Space** or a mouse click. Choosing `Run` executes the ready block and advances the cursor to the next one; `Skip` marks it skipped without running it — and any block whose `needs=` dependency was skipped is automatically skipped too, since ai-playbook never runs a block against an unmet dependency; `Quit` exits the viewer immediately.

If a block fails, the footer switches to **`[ Roll back ] [ Leave as-is ] [ Quit ]`**: `Roll back` undoes the blocks that already completed, in reverse order, and `Leave as-is` stops the run and keeps whatever state the completed blocks produced.

The document stays scrollable the whole time — `j`/`k` and the other viewer navigation keys work even while the footer is showing — and **Ctrl-C** aborts the run at any point.

Assisted mode is useful when you are running a playbook written by someone else and want fine-grained control over which steps execute, without fully committing to running everything. If a playbook declares variables (see Chapter 06), `--assisted` confirms them as soon as the viewer opens, before the first step's `[ Run ]` footer appears.

## Auto run

The `--auto` flag removes all prompts and runs every block in dependency order without pausing:

```bash {static}
ai-playbook run --auto --file examples/07-run-modes.md
```

ai-playbook streams each block's output to stdout, exits `0` when all blocks succeed, and exits non-zero on the first failure — printing a summary that identifies which block failed and at what line.

Auto mode is designed for CI pipelines and shell scripts. A typical pipeline invocation looks like:

```bash {static}
ai-playbook run --auto --file examples/07-run-modes.md 2>&1 | tee run.log
```

> [!TIP]
> In project-bound playbooks (see Chapter 06) auto mode skips the interactive env-variable confirmation gate if all required variables are already present in the environment. Export them before calling ai-playbook and the gate is satisfied silently. Or pass them inline with `--with-env` — a JSON object (`--with-env '{"PROJECT_ROOT":"/path"}'`) or a path to a JSON file. Values given this way take precedence over the environment; undeclared keys are ignored with a warning.

## Rollback in CI

When an `--auto` run hits a failing step it stops immediately and — by default — **undoes the steps that already succeeded**, running each completed block's `rollback=` target in reverse order. That is what makes `--auto` safe to drop into a pipeline: a failed run doesn't leave half-applied state behind, and the non-zero exit still fails the CI step.

Chapter 02's playbook is built to show this — its `stage` block declares a `rollback=` target and a later block fails on purpose:

```bash {static}
ai-playbook run --auto --file examples/02-needs-verify-rollback.md
```

The run stops at the failing block, rolls `stage` back, and prints a summary:

```text {static}
  ✓ ok        prep    (exit 0)
  ✓ ok        use     (exit 0)
  ↺ rolledback stage
  ✗ failed    boom    (exit 1)
  ✓ ok        undo-stage (exit 0)
```

The forward step that was undone reads `↺ rolledback`; the rollback command that ran to undo it reads `✓ ok`. The process exits non-zero (here `1`, the failing block's code) **even though the rollback itself succeeded** — so a CI step that checks the exit status fails the build, while the workspace is left clean.

To stop on failure *without* rolling back — leaving the completed steps in place for post-mortem inspection — add `--no-auto-rollback`:

```bash {static}
ai-playbook run --auto --no-auto-rollback --file examples/02-needs-verify-rollback.md
```

## The stop button

In the viewer, the **Run** button becomes **Stop** the moment a block starts executing. Click **Stop** any time to send SIGTERM to the running process; the block is marked as cancelled and no downstream `needs=` blocks will run.

In `--assisted` and `--auto` modes, press **Ctrl-C** in the terminal to abort the current block. ai-playbook catches the interrupt, prints a cancellation summary, and exits non-zero so the calling script or CI step detects the failure.

> [!NOTE]
> **Mux mode:** With a terminal multiplexer active, **Stop** appears in the playbook pane while the block's output streams in an adjacent split. Without a mux, **Stop** and the live output share the same inline area below the block.

## What's next

Chapter 08 introduces the store — a searchable library of saved playbooks you can browse, preview, and launch without specifying a file path every time.
