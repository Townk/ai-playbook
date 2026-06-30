---
name: Hello, run
description: Your first steps with ai-playbook — run a shell command, meet the play and stop buttons, and learn what static blocks and callouts are for.
category: tutorial
tags: [tutorial, basics]
created: 2026-06-30
---

# Hello, run

Welcome to ai-playbook. This chapter covers the core affordances you will reach for constantly: running a shell block, streaming output with play, stopping a long-running command, reading static reference blocks, and spotting callout hints.

Open this file with `ai-playbook view examples/01-hello-run.md` and work through each section in order. All commands run relative to `examples/` — nothing touches anything outside that directory.

## Run a shell block

Every fenced `bash` block gets a **▶ Run** button and a **⧉ Copy** button. When the block carries an `id=` attribute it also gets a **▷ Play** button. Play streams output continuously (useful for build logs), while Run collects output and displays it when the command exits.

Click **▶ Run** on the block below:

```bash {id=build}
bash projects/tidy-shop/build.sh
```

You should see two lines — `tidy-shop: building…` then `tidy-shop: build OK`. The block turns green when the command exits 0.

> [!TIP]
> **Copy before you run.** The ⧉ Copy button puts the raw command on your clipboard so you can paste it into any terminal and tweak a flag before running. Nothing executes until you actually click Run.

> [!NOTE]
> **Mux mode:** With a terminal multiplexer active, **▷ Play** opens a split pane so you can watch the stream alongside the playbook. Without a mux the output appears inline below the block — identical content, different layout.

## Run a non-shell block

Not every language gets a Play button. For languages like Python, ai-playbook can still execute the block with your local interpreter — but it collects the output rather than streaming it, so there is no Play affordance. The Run button is still there.

```python {id=hello}
print("Hello from tidy-shop!")
```

Click **▶ Run**. The interpreter runs the snippet and displays `Hello from tidy-shop!` when it finishes.

> [!TIP]
> If the Run button is missing for a language, check that the interpreter is on your `$PATH`. Run `which python3` in a terminal to verify.

## Meet the stop button

Some commands take a while. The moment you click **▶ Run** on a long-running block, the button changes to **■ Stop**. Click Stop any time to send SIGTERM and cancel the command cleanly.

Try it: click Run below, then click Stop before the five-second sleep finishes.

```bash {id=wait}
echo "waiting…"
sleep 5
echo "done"
```

If you let it run to completion you will see both `waiting…` and `done`. If you click Stop mid-sleep you will see only `waiting…` and the block will show a cancelled status.

## Static blocks: reference output

A `{static}` block is read-only reference material — it has no Run or Copy button. Use static blocks to show expected output, schema snapshots, or any snippet the reader should read but not execute.

The block below shows the expected output of the build step you ran above:

```text {static}
tidy-shop: building…
tidy-shop: build OK
```

> [!WARNING]
> Static blocks are **display only**. If you paste a `{static}` block into a shell it will run as literal text — strip the fence markers first.

## What's next

Chapter 02 introduces three more features: `needs=` to chain steps in order, a `## Verify` section for an end-to-end health-check, and `rollback=` so ai-playbook can undo changes automatically when a step fails.
