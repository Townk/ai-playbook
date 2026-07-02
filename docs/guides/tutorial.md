# ai-playbook tutorial

This guide walks you through every user-facing feature of ai-playbook in the order that makes the most sense for a first-timer: basics first, file changes next, portability and automation after that, and finally authoring your own playbook from a broken project.

Work through the chapters in order. Each one builds on what came before. By the time you reach chapter 09 you will have touched every viewer affordance and authored one playbook from scratch.

## Prerequisites

- `ai-playbook` installed and on your `$PATH`
- A local clone of this repository (all examples reference files inside `examples/`)
- **(Chapter 09 only)** a configured model backend — ensure `$AI_PLAYBOOK_MODEL` is set (`echo "$AI_PLAYBOOK_MODEL"` to verify)

No model backend is needed for chapters 01–08. They are fully offline: pre-authored playbooks that exercise the viewer and the CLI without calling any AI service.

## No-mux / mux note

ai-playbook is designed to work with or without a terminal multiplexer (tmux, Zellij, WezTerm). Where the experience differs this guide calls it out inline with a `> [!NOTE]` callout. The short version: without a mux, secondary views (diff overlay, env gate, assist ask) appear inline in the same pane; with a mux they open in a float or tab. Every feature is available in both modes.

## Safety note

**Nothing runs until you click.** Opening a playbook in the viewer is read-only. Shell blocks only execute when you press **▶ Run** or **▷ Play**.

A few things to keep in mind as you work through the chapters:

- **Shell blocks run real commands** in the project directory (usually `examples/`). The examples are idempotent and touch nothing outside `examples/`.
- **Apply / create / shell demos** (chapters 02/03/06 create new files; chapters 04/05 modify tracked files) write to `examples/projects/`. Each block has an **Undo** button. For a clean slate, run `git restore examples/ && git clean -fd examples/` from the repo root.
- **The store chapter** (chapter 08) reads from `examples/store/` — point `$AI_PLAYBOOK_DATA_DIR` there before running the commands in that chapter.
- **Chapter 09** calls a live model and writes a generated playbook to a temporary file. The generated file is not committed to the repo.

---

## Chapter 01 — Hello, run

File: [`examples/01-hello-run.md`](../../examples/01-hello-run.md)

Features: run ▶ / play ▷ / stop ■ / copy ⧉ · static blocks · callouts

**Run:** `ai-playbook run --file examples/01-hello-run.md`

**You'll see:** A playbook titled "Hello, run" with several fenced code blocks and callout boxes. Each `bash` block shows a **▶ Run** button and a **⧉ Copy** button. Blocks with an `id=` attribute also show a **▷ Play** button.

**Click:** **▶ Run** on the `build` block. **Notice:** The block turns green when `bash projects/tidy-shop/build.sh` exits 0. Output appears below the block.

**Click:** **▷ Play** on the same block. **Notice:** Output streams line-by-line (in a mux split pane, or inline below the block without a mux) rather than appearing all at once. Play is useful for long-running commands where you want to watch progress.

**Click:** **▶ Run** on the `wait` block (the five-second sleep), then immediately click **■ Stop**. **Notice:** The block shows a cancelled status.

**Click:** **⧉ Copy** on any block. **Notice:** The raw command is on your clipboard — paste it into a terminal to run it with custom flags, unchanged.

**Read:** The `{static}` block showing expected build output. Static blocks have no run or copy button — they are reference material only.

**Read:** The `> [!TIP]` and `> [!WARNING]` callouts. Notice their distinct styling. ai-playbook supports five callout types: note, tip, important, warning, caution.

---

## Chapter 02 — Needs, Verify, and Rollback

File: [`examples/02-needs-verify-rollback.md`](../../examples/02-needs-verify-rollback.md)

Features: needs= · blocked notice · verify block · rollback

**Run:** `ai-playbook run --file examples/02-needs-verify-rollback.md`

**You'll see:** A playbook with a `prep` block, a `use` block that carries `needs=prep`, a `stage` block paired with `rollback=undo-stage`, a deliberately-failing `boom` block, and a `## Verify` section.

**Click:** **▶ Run** on `use` *without* running `prep` first. **Notice:** The block shows a **blocked** notice — ai-playbook refuses to start a step until all its `needs=` dependencies are green.

**Click:** **▶ Run** on `prep`, then **▶ Run** on `use`. **Notice:** The blocked notice clears automatically once `prep` succeeds, and `use` runs cleanly.

**Click:** **▶ Run** on `stage`, then **▶ Run** on `boom`. **Notice:** `boom` exits 1, which triggers the rollback chain: `undo-stage` runs automatically in reverse order, cleaning up what `stage` put in place.

**Click:** **▶ Run** in the `## Verify` section. **Notice:** The viewer surfaces the Verify section as a distinct affordance — a quick health-check you can re-run any time independently of the steps above.

---

## Chapter 03 — Create a File

File: [`examples/03-create-a-file.md`](../../examples/03-create-a-file.md)

Features: file= create / undo

**Run:** `ai-playbook run --file examples/03-create-a-file.md`

**You'll see:** A playbook targeting `projects/half-baked`. The `local` block carries a `file=projects/half-baked/config.local.yml` attribute — its body is the full content of the file to be written.

**Click:** **Create** on the `local` block. **Notice:** The file appears on disk and the button flips to **Undo**. A `file=` block is declarative: the body is written byte-for-byte; ai-playbook records the change so it can reverse it.

**Click:** **▶ Run** on the `verify` block. **Notice:** It prints `created` — confirming the file exists.

**Click:** **Undo** on the `local` block. **Notice:** The file is removed from disk and the button returns to **Create**. Run `verify` again — this time the test exits non-zero and prints nothing.

**Undo:** The Undo button on the block is the recommended path. Alternatively: `git restore examples/projects/half-baked/config.local.yml`.

---

## Chapter 04 — Edit with Diff

File: [`examples/04-edit-with-diff.md`](../../examples/04-edit-with-diff.md)

Features: diff · view-diff · apply / undo

**Run:** `ai-playbook run --file examples/04-edit-with-diff.md`

**You'll see:** A playbook with a `diff` block targeting `projects/half-baked/config.yml`. The patch bumps the version field from `"1.0"` to `"2.0"`.

**Click:** **View diff** on the `bump` block. **Notice:** A side-by-side comparison opens — the old line highlighted red, the new line green. Without a mux this opens inline below the block; with a mux it opens in a float.

**Click:** **Apply**. **Notice:** `config.yml` is updated on disk and the button becomes **Undo**.

**Click:** **▶ Run** on the `verify` block. **Notice:** It prints `version: "2.0"` confirming the patch applied.

**Click:** **Undo** on the `bump` block. **Notice:** The file reverts to `"1.0"`. Running `verify` again prints `version: "1.0"`.

**Undo:** The Undo button, or `git restore examples/projects/half-baked/config.yml`.

---

## Chapter 05 — Diff Drift

File: [`examples/05-diff-drift.md`](../../examples/05-diff-drift.md)

Features: drift region · resolve manually · regenerate

**Run:** `ai-playbook run --file examples/05-diff-drift.md`

**You'll see:** A playbook with a `diff` block targeting `projects/drifted/settings.conf`. The patch was authored against an older version of the file; the file on disk has since been edited, so the patch context no longer matches.

**Click:** **Apply** on the `settimeout` block. **Notice:** Instead of applying, ai-playbook highlights a **drift region** — the lines where the patch's expected context does not match what the file actually contains. The Apply button is replaced by two alternatives.

**Click:** **Resolve manually**. **Notice:** The file opens in your `$EDITOR` (or in a mux pane) with conflict markers showing the expected context alongside the actual content. Edit the file to reach the intended state, save, and close.

Alternatively, **click:** **Regenerate** instead of Resolve manually. **Notice:** ai-playbook sends the current file content back to the model and asks it to rewrite the diff against the real file. A fresh patch appears in place of the drifted one.

> [!NOTE]
> Drift is not a format error — the patch is syntactically valid. It is a semantic mismatch detected via `git apply --check` before ai-playbook attempts to modify the file.

---

## Chapter 06 — Portable & env

File: [`examples/06-portable-and-env.md`](../../examples/06-portable-and-env.md)

Features: project_bound · $PROJECT_ROOT · env declaration · confirmation gate

**Run:** `ai-playbook run --file examples/06-portable-and-env.md`

**You'll see:** A playbook with `project_bound: true` and a `project_root:` key in its front matter. The `env:` map declares `PROJECT_ROOT` and `DATA_DIR`, each with a `value` and a `why` explanation.

**Click:** **▶ Run** on the `seed` block. **Notice:** Before the block executes, ai-playbook pauses and shows a grouped **Confirm / Customize** dialog — one row per env variable, showing its name, default value, and the `why` field. Accept the defaults or override any value.

**Notice:** Once confirmed, `$PROJECT_ROOT` and `$DATA_DIR` are exported into the shell environment. The block runs with cwd set to `examples/projects/portable` (not `examples/`), and `$DATA_DIR` resolves to `$PROJECT_ROOT/data`.

**Click:** **▶ Run** on the `show` block. **Notice:** It reads the file seeded by the `seed` block, demonstrating that `$DATA_DIR` is live in the session.

> [!TIP]
> Change `PROJECT_ROOT` in the confirmation gate dialog to any other path — all derived variables (`$DATA_DIR` and any others built on `$PROJECT_ROOT`) follow automatically, with no edits to the playbook file.

---

## Chapter 07 — Run Modes

File: [`examples/07-run-modes.md`](../../examples/07-run-modes.md)

Features: --assisted (confirm-each-step) · --auto · stop

> [!NOTE]
> The `--assisted` and `--auto` flags and the interactive viewer blocks in this chapter are shipped and work today.

**Run:** `ai-playbook run --file examples/07-run-modes.md`

**You'll see:** Three chained blocks (`build → test → status`) you can run interactively, followed by prose explaining three non-interactive invocation modes.

**Read:** The static blocks in the "Manual step-through", "Assisted run", and "Auto run" sections. They show three CLI invocations:

- `ai-playbook run --file examples/07-run-modes.md` — press Enter before each block (manual step-through)
- `ai-playbook run --assisted --file examples/07-run-modes.md` — guided pager with a ready cursor and a `[ Run ] [ Skip ] [ Quit ]` footer before each block
- `ai-playbook run --auto --file examples/07-run-modes.md` — runs all blocks without pausing; exits non-zero on first failure

**Notice:** The stop behaviour section explains that in `--assisted` and `--auto` modes, **Ctrl-C** aborts the current block and exits non-zero — so the calling script or CI step detects the failure.

---

## Chapter 08 — The Store

File: [`examples/08-the-store.md`](../../examples/08-the-store.md)

Features: list · search · show · [edit]+reload · validate

**Setup:** In the shell where you will run these commands, export the fixture store path:

```
export AI_PLAYBOOK_DATA_DIR="$PWD/examples/store"
```

**Run:** `ai-playbook list`

**You'll see:** A two-row table listing `note-rotate-logs` (operations) and `tidy-checkup` (maintenance) with their tags.

**Run:** `ai-playbook search logs`

**You'll see:** Only `note-rotate-logs` — search matches against tags, slug, and description.

**Run:** `ai-playbook show tidy-checkup`

**You'll see:** The `tidy-checkup` playbook opens in the viewer. Because it is file-backed (stored at `examples/store/tidy-checkup.md`), the viewer header shows an **[edit]** button.

**Click:** **[edit]**. **Notice:** Without a mux, the viewer suspends and opens the file in your `$EDITOR`. With a mux, the file opens in a new pane or tab while the viewer stays live. Edit the `description` line in the front matter, save, and return to the viewer — the updated description appears without reopening the file.

**Read:** The `## Validate a playbook` section. It shows `ai-playbook validate --file examples/01-hello-run.md` — a structural + AI review pass that checks front matter, `needs=` references, and fence balance.

---

## Chapter 09 — Fix it (authoring)

File: [`examples/09-fix-it.md`](../../examples/09-fix-it.md)

Features: create · assist (triage: command / answer / escalate) · followup ("try another fix") · regenerate · cached badge

> [!IMPORTANT]
> This chapter requires a configured model backend. Ensure `$AI_PLAYBOOK_MODEL` is set before starting (`echo "$AI_PLAYBOOK_MODEL"` to verify).

**Setup:** Open a terminal in the repo root.

**Run:** In your terminal (not the viewer):

```
cd examples/projects/broken-build
bash build.sh
```

**You'll see:** `build.sh: line 3: version.txt: No such file or directory` — the script reads `version.txt` but the repo ships `VERSION`.

**Run:** Choose one of the two authoring paths:

- `ai-playbook create "fix the build"` — skips triage; goes straight to authoring a playbook.
- `ai-playbook assist` — adds a triage step: the model decides whether the failure warrants a command, an answer, or a full playbook (escalate). For this project it should escalate.

**Notice (assist only):** If the model asks a followup question before drafting ("rename the file or patch the script?"), it appears inline without a mux, or in a floating overlay with a mux. Answer and the draft continues.

**You'll see:** A generated playbook opens in the viewer with one or more fix steps — a `file=` block creating `version.txt`, a `diff` block patching `build.sh`, or a shell block renaming the file.

**Click:** **▶ Run** on the first fix block. **Notice:** If the build now passes, the playbook's verify step (if present) will go green.

**If a step fails:** Click **Try another fix** below the failed block. **Notice:** The model generates a followup replacement block inline. You can compare the original and the new proposal side by side before choosing which to run.

**If the whole draft is off:** Click **[Regenerate ↺]** in the viewer header. **Notice:** The current draft is discarded; the model produces a fresh playbook using the same original context plus any new scrollback from your attempts.

**Re-run:** Once the fix is confirmed, exit the viewer and run `ai-playbook assist` again from the same directory. **Notice:** The viewer header shows a **[cached]** badge — ai-playbook found the previously-authored playbook and returned it without calling the model. Set `AI_PLAYBOOK_NO_CACHE=1` to force a fresh draft.

---

## You're done

You have now used every surface:

| Surface | Chapter |
|---------|---------|
| run ▶ / play ▷ / stop ■ / copy ⧉ | 01 |
| static blocks + callouts | 01 |
| needs= / verify / rollback | 02 |
| file= create / undo | 03 |
| diff apply / view-diff / undo | 04 |
| drift / resolve manually / regenerate | 05 |
| project_bound / $PROJECT_ROOT / env gate | 06 |
| --assisted / --auto run modes | 07 |
| list / search / show / [edit] / validate | 08 |
| create / assist / followup / cached | 09 |

From here, read the reference documentation for the full list of front-matter keys, block attributes, and CLI flags — or open any playbook in `examples/store/` and start editing.
