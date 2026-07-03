---
name: Fix it — authoring a playbook
description: Point ai-playbook at a broken project, watch the model triage the failure, and author a playbook that fixes the build — your first look at the create and assist commands.
category: tutorial
tags: [tutorial, authoring, create, assist]
created: 2026-06-30
---

# Fix it — authoring a playbook

> [!IMPORTANT]
> **This chapter requires a configured model backend.** Chapters 01–08 are fully offline — no model needed. Chapter 09 calls a language model to author, triage, and regenerate. Ensure the Claude CLI is installed and authenticated before continuing (`claude --version` to verify); optionally pick a model via `[agent] model` in the config file.

Chapters 01–08 worked with pre-authored playbooks. This chapter flips the authoring seat to you — but you do not write the playbook by hand. Instead, you point ai-playbook at a broken project, let it read the failure, and watch it draft the fix.

The target is `examples/projects/broken-build`. Its `build.sh` reads from `version.txt`, but the repo ships `VERSION`. The mismatch is obvious; the fix is mechanical. That makes it a perfect first authoring exercise: the answer is unambiguous, so you can judge whether the model got it right at a glance.

## Observe the failure

Open a terminal, change into the broken project, and run the build script:

```bash {static}
cd examples/projects/broken-build
bash build.sh
```

You will see something like:

```text {static}
build.sh: line 3: version.txt: No such file or directory
```

The script exits non-zero. That failure — the last command, its exit code, the current working directory, and the scrollback in your terminal — is exactly what ai-playbook reads when you invoke either authoring command. Leave the terminal open; you will use it in the next two sections.

## What ai-playbook captures

Both authoring commands (`create` and `assist`) start by reading the shell context:

- **last command** — the command you just ran (`bash build.sh`)
- **exit code** — the non-zero result that signals failure
- **cwd** — the directory where the command was run (`…/broken-build`)
- **scrollback** — the visible terminal output captured from the most recent prompt

This context is what the model reasons over. The richer the scrollback, the better the diagnosis — which is why triggering the failure first, rather than asking the model to guess, produces sharper results.

## Path 1: force-author with create

`ai-playbook create` bypasses triage and goes straight to authoring. Use it when you already know you want a playbook and just need the model to draft one:

```bash {static}
ai-playbook create "fix the build"
```

ai-playbook sends your request, the captured shell context, and the contents of any files it can read in the cwd to the model. The model responds with a structured playbook. ai-playbook writes it to a temporary file and opens it in the viewer automatically.

The generated playbook will contain one or more steps to fix the mismatched filename — for example a `file=` block to create `version.txt` from the content of `VERSION`, or a `diff` block patching `build.sh` to read `VERSION` directly.

## Path 2: triage with assist

`ai-playbook assist` adds a triage step before authoring. Use it when you want the model to decide whether the failure is worth a full playbook or just needs a quick command or explanation:

```bash {static}
ai-playbook assist
```

The model reads the same captured context as `create` and chooses one of three responses:

- **command** — the fix is a one-liner. ai-playbook prints a suggested shell command you can copy and run directly. No playbook is authored.
- **answer** — the failure is informational. ai-playbook prints an explanation ("your build.sh reads `version.txt`; the file is actually called `VERSION`") and leaves it at that.
- **escalate** — the fix requires multiple coordinated steps. ai-playbook escalates to full playbook authoring (the same path as `create`) and opens the result in the viewer.

For `broken-build` the model should escalate — there are at least two ways to fix the mismatch and a playbook lets the reader choose. But `assist` is not deterministic: with a simpler failure you may get a command or an answer instead.

> [!NOTE]
> **Mux mode and the assist ask:**
>
> After choosing **escalate**, the model may ask a followup question before drafting — for example, "do you want to rename the file or patch the script?". Without a terminal multiplexer, this question appears **inline** in the same terminal pane where you ran `ai-playbook assist`. With a mux active (tmux, Zellij, WezTerm), the question opens in a **floating overlay** so it does not scroll away your failure context. Answer in either case and the draft continues.

## The generated playbook

Whichever path you took, the viewer now shows a freshly authored playbook. It contains:

- A front-matter block with a generated `name`, `description`, `category`, and `created` date.
- One or more sections with blocks that implement the fix — a `file=` block, a `diff` block, or a shell command to rename `VERSION` to `version.txt`.
- Possibly a `## Verify` section that re-runs `bash build.sh` to confirm the fix worked.

Read through the playbook before running anything. The model's draft is a starting point — you own the blocks and should satisfy yourself that each one does what you expect before clicking **Run**.

## Re-engage: try another fix (followup)

Run the first fix block. If the build still fails after the step — for example the model patched the script but got the path wrong — you will see a **Try another fix** button below the failed block.

Clicking **Try another fix** sends the block's output and exit code back to the model as a followup and asks it to propose an alternative. The model authors a replacement block (or a corrected diff), which appears inline below the original. You can compare the two and decide which to run next.

> [!TIP]
> **Try another fix** is a targeted followup: it replaces the specific block that failed, not the whole playbook. Use it when one step is wrong but the rest of the draft is sound.

## Re-engage: regenerate the whole playbook

If the draft is fundamentally off — wrong approach, wrong file, wrong scope — use the **Regenerate** button in the viewer header:

```text {static}
[Regenerate ↺]
```

Regenerate discards the current draft entirely, feeds the model the same original context plus any new scrollback from your attempts, and produces a fresh playbook from scratch. The new draft opens in the same viewer window, replacing the previous one.

## The cached badge

Once the fix is confirmed (the verify step exits 0, or the build runs cleanly), re-running `ai-playbook assist` from the same directory will show a **cached** badge in the viewer header:

```text {static}
Fix it — authoring a playbook   [cached]
```

The badge means ai-playbook found a previously-authored playbook for the same context in the same project and is showing you that result instead of calling the model again. Repeated invocations are fast and free. To force a fresh draft, set `AI_PLAYBOOK_NO_CACHE=1` before running `ai-playbook assist`.

---

That covers every authoring surface: capturing context, force-authoring with `create`, triaging with `assist`, re-engaging with a followup or a full regenerate, and reading the cached badge on repeat runs. Combined with the previous eight chapters you have now touched every viewer affordance and authored one playbook from a broken project.
