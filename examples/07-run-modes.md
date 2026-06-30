---
name: Run modes
description: Run a playbook three ways — step through manually with the run subcommand, use --assisted to confirm each block, or go fully hands-off with --auto.
category: tutorial
tags: [tutorial, run-modes]
created: 2026-06-30
---

# Run modes

Every chapter up to this point has opened in the interactive viewer where you click ▶ Run on each block. That works well for exploration, but ai-playbook also ships a `run` subcommand that drives the same blocks from your terminal — useful for shell scripts, automation, and CI pipelines.

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

ai-playbook renders each block header and command in your terminal, then waits for you to press **Enter** before executing it. You see the output stream in place, the block turns green on exit 0, and ai-playbook moves to the next block. Press **q** at any prompt to quit early.

This mode is ideal when you want to step through an unfamiliar playbook, read each command before it runs, and keep one hand on the keyboard.

## Assisted run

<!-- ⏳ needs assisted/auto run modes (not yet built) -->

The `--assisted` flag adds an explicit **[y/n/q]** prompt before each block:

```bash {static}
ai-playbook run --assisted --file examples/07-run-modes.md
```

At each step you see the block header and command followed by a confirmation line:

```text {static}
[build] bash projects/tidy-shop/build.sh
Run this block? [y/n/q]
```

Press `y` to run, `n` to skip, or `q` to quit immediately. Skipped blocks are recorded in the run summary at the end. Any block whose `needs=` dependency was skipped is automatically skipped too — ai-playbook never runs a block against an unmet dependency.

Assisted mode is useful when you are running a playbook written by someone else and want fine-grained control over which steps execute, without fully committing to running everything.

## Auto run

<!-- ⏳ needs assisted/auto run modes (not yet built) -->

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
> In project-bound playbooks (see Chapter 06) auto mode skips the interactive env-variable confirmation gate if all required variables are already present in the environment. Export them before calling ai-playbook and the gate is satisfied silently.

## The stop button

In the viewer, the ▶ Run button becomes **■ Stop** the moment a block starts executing. Click Stop any time to send SIGTERM to the running process; the block is marked as cancelled and no downstream `needs=` blocks will run.

In `--assisted` and `--auto` modes, press **Ctrl-C** in the terminal to abort the current block. ai-playbook catches the interrupt, prints a cancellation summary, and exits non-zero so the calling script or CI step detects the failure.

> [!NOTE]
> **Mux mode:** With a terminal multiplexer active, **■ Stop** appears in the playbook pane while the block's output streams in an adjacent split. Without a mux, Stop and the live output share the same inline area below the block.

## What's next

Chapter 08 introduces the store — a searchable library of saved playbooks you can browse, preview, and launch without specifying a file path every time.
