# Sandbox Tutorial Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `examples/` — fake projects + demo playbooks exercising every ai-playbook feature — plus `docs/guides/tutorial.md`, a guided hands-on tour.

**Architecture:** Content, not code. Top-level chapter playbooks (`examples/NN-*.md`) run via `ai-playbook run --file`; they act on fake projects under `examples/projects/` via relative paths (cwd = the `.md`'s dir = `examples/`), except ch.06 which is `project_bound`. The tour is deterministic + offline for ch.01–08; ch.09 authors live. The guide sequences it all.

**Tech Stack:** ai-playbook playbook `.md` format (YAML front matter + fenced `{id=…}` blocks); shell-script fake projects; Markdown guide.

## Global Constraints

- Spec: `docs/specifications/sandbox-tutorial.md` (read it — the coverage matrix is the completeness gate).
- gpg-signed Conventional Commits; NO `Co-Authored-By`; `git add` explicit paths; verify signing `git log -1 --format=%G?` == `G`.
- **Playbook format (author to the CODE):** front matter is a YAML **map** — `name`, `description`, `category`, `tags` (list), `env` (map `NAME: {value, why}`), `project_root`, `project_bound`, `created`. Body = `# <Title>` + `## <Section>` + fenced blocks. Fence: ` ```<lang> {id=<id> needs=<a,b> rollback=<id> file=<path>}` or ` ```<lang> {static}`. Kinds: bash/sh/zsh→shell, diff/patch→diff, `file=`→create, other langs→run, text/console/output/log/json or `{static}`→static. Callouts: `> [!TYPE]` (note|tip|important|warning|caution). `file=` body = full file content; `diff` body = unified patch. Min valid: H1 + ≥1 fenced block.
- **cwd rule:** chapter playbooks at `examples/` top level → blocks reference `projects/<X>/…` relatively. Ch.06 only: `project_bound: true`, `project_root: examples/projects/portable`.
- **Safety:** every run block idempotent, relative-path, in-project-dir; never touch `$HOME`/system. Static + callout blocks for illustration.
- **mux:** write for no-mux; add `> [!NOTE]` mux callouts where behavior differs (view-diff, `[edit]`, assist ask).
- **Forward features (⏳):** ch.07 (`--assisted`/`--auto`) + ch.08 `validate` are written as if shipped; mark each ⏳ scenario with an HTML comment `<!-- ⏳ needs <feature> (not yet built) -->` in the guide so it's found + verified when the feature lands.
- Verification is **well-formedness + invariants** (the TUI render is manual — `run --file` needs a TTY; do NOT try to launch the viewer headless). Each task gives concrete checks.

---

### Task 1: Scaffold + the 5 fake projects + store fixtures

**Files (create):**
- `examples/README.md`
- `examples/projects/tidy-shop/{README.md,build.sh,test.sh,shop.sh}`
- `examples/projects/half-baked/{README.md,config.yml}` (NO `config.local.yml` — ch.03 creates it)
- `examples/projects/drifted/{README.md,settings.conf}` (pre-drifted vs ch.05's patch)
- `examples/projects/portable/{README.md,data/.gitkeep}`
- `examples/projects/broken-build/{README.md,build.sh,VERSION}` (build.sh FAILS)
- `examples/store/{tidy-checkup.md,note-rotate-logs.md}` (2 store fixtures w/ front matter)

**Context:** These are the substrate the playbooks act on. Keep each tiny, realistic, idempotent. The drifted file + the broken build carry load-bearing invariants (verified below).

- [ ] **Step 1: Write the project files**

`examples/projects/tidy-shop/build.sh`:
```bash
#!/usr/bin/env bash
set -euo pipefail
echo "tidy-shop: building…"
echo "tidy-shop: build OK"
```
`examples/projects/tidy-shop/test.sh`:
```bash
#!/usr/bin/env bash
set -euo pipefail
echo "tidy-shop: tests passed"
```
`examples/projects/tidy-shop/shop.sh`: a tiny echo-based CLI (`list`/`add <item>` printing to stdout; no persistence needed). `chmod +x` all three.
`examples/projects/half-baked/config.yml`: a small YAML with a **stale** `version: "1.0"` line (ch.04 patches it to `"2.0"`). Do NOT create `config.local.yml`.
`examples/projects/drifted/settings.conf`: an INI-ish file. Ch.05's diff patch will expect a context that does NOT match this file — ship `timeout = 99` here while the patch's context assumes `timeout = 30` (so `git apply --check` fails). (The exact patch is authored in Task 3; this file must NOT match it.)
`examples/projects/broken-build/build.sh`:
```bash
#!/usr/bin/env bash
set -euo pipefail
echo "broken-build: building version $(cat version.txt)"   # FAILS: version.txt missing (the file is VERSION)
```
(The obvious, fixable break: the script reads `version.txt` but the repo ships `VERSION`. The fix-it playbook/author creates `version.txt` or corrects the script.) `examples/projects/broken-build/VERSION`: `1.0.0`.
`examples/store/tidy-checkup.md` + `note-rotate-logs.md`: valid playbooks with full front matter (distinct `name`/`category`/`tags`) so `list`/`search` have material. Keep their bodies short (an H1 + 1–2 blocks).
Each `README.md`: 2–3 lines on what the project is + (for broken-build) "ask ai-playbook to fix me (see ch.09)".
`examples/README.md`: the entry point — what `examples/` is, prerequisites, the safety note, a chapter index (link the NN-*.md + `docs/guides/tutorial.md`).

- [ ] **Step 2: Verify the invariants**

```bash
cd "$REPO/examples/projects/broken-build" && ! bash build.sh   # build MUST fail (missing version.txt)
grep -q 'version: "1.0"' "$REPO/examples/projects/half-baked/config.yml"   # stale value present
test ! -e "$REPO/examples/projects/half-baked/config.local.yml"           # the to-be-created file is absent
grep -q 'timeout = 99' "$REPO/examples/projects/drifted/settings.conf"     # pre-drifted value
bash "$REPO/examples/projects/tidy-shop/build.sh" && bash "$REPO/examples/projects/tidy-shop/test.sh"  # healthy
```
Expected: broken-build fails; the others pass / greps match. (Store-fixture validity is checked in Task 5.)

- [ ] **Step 3: Commit**

```bash
git add examples/README.md examples/projects examples/store
git commit -m "docs(examples): scaffold the sandbox project garden (fake projects + store fixtures)"
```

---

### Task 2: Chapters 01–02 — basics, needs/verify/rollback

**Files (create):** `examples/01-hello-run.md`, `examples/02-needs-verify-rollback.md`

**Context:** Both act on `projects/tidy-shop/` via relative paths. 01 demos run/play/stop/copy/static/callouts; 02 demos needs→blocked, verify, rollback. Prose between blocks is yours; the BLOCKS below are load-bearing (they trigger the features) — reproduce their fence tags + intent exactly.

- [ ] **Step 1: Write `01-hello-run.md`**

Front matter (`name`, `description`, `category: tutorial`, `tags: [tutorial, basics]`, `created: 2026-06-30`). Body `# Hello, run` + sections. Required blocks:
- a **shell** block (run + play + copy): ` ```bash {id=build}` → `bash projects/tidy-shop/build.sh`.
- a **run** (non-shell-exec) block to contrast: e.g. ` ```python {id=hello}` printing a line (run button, no play).
- a **stop** demo: ` ```bash {id=wait}` → `echo waiting…; sleep 5; echo done` (run it → the stop button appears mid-run).
- a **static** block: ` ```text {static}` showing expected output.
- **callouts**: at least a `> [!TIP]` and a `> [!WARNING]`.

- [ ] **Step 2: Write `02-needs-verify-rollback.md`**

Required blocks (on tidy-shop):
- ` ```bash {id=prep}` → `mkdir -p projects/tidy-shop/.work && echo seeded > projects/tidy-shop/.work/seed`.
- ` ```bash {id=use needs=prep}` → reads `.work/seed` — shows the **blocked notice** until `prep` runs.
- a **rollback** pair: ` ```bash {id=stage rollback=undo-stage}` creates a marker; ` ```bash {id=undo-stage}` removes it; plus a deliberately failing later step ` ```bash {id=boom needs=stage}` → `exit 1` so the reader sees rollback fire in reverse.
- a top-level **verify**: a `## Verify` section with ` ```bash {id=verify}` → `bash projects/tidy-shop/test.sh`.

- [ ] **Step 3: Verify well-formedness**

```bash
for f in examples/01-hello-run.md examples/02-needs-verify-rollback.md; do
  head -1 "$REPO/$f" | grep -qx -- '---'                 # front matter opens
  grep -qE '^# ' "$REPO/$f"                               # has an H1
  [ $(( $(grep -c '^```' "$REPO/$f") % 2 )) -eq 0 ]       # fences balanced
done
grep -q 'needs=prep' "$REPO/examples/02-needs-verify-rollback.md"
grep -q 'rollback=' "$REPO/examples/02-needs-verify-rollback.md"
```
Expected: all pass.

- [ ] **Step 4: Commit**

```bash
git add examples/01-hello-run.md examples/02-needs-verify-rollback.md
git commit -m "docs(examples): chapters 01-02 — run basics + needs/verify/rollback"
```

---

### Task 3: Chapters 03–05 — file= create, diff apply/undo, diff drift

**Files (create):** `examples/03-create-a-file.md`, `examples/04-edit-with-diff.md`, `examples/05-diff-drift.md`

**Context:** 03+04 act on `projects/half-baked/`; 05 on `projects/drifted/`. The diff/drift blocks are load-bearing: 04's patch MUST apply cleanly to `half-baked/config.yml`; 05's patch MUST NOT apply to `drifted/settings.conf` (so the drift region shows).

- [ ] **Step 1: Write `03-create-a-file.md`**

A **create** block: ` ```yaml {id=local file=projects/half-baked/config.local.yml}` whose body is the full content of a local-override config (a few YAML lines). The reader clicks `create` → the file appears; `undo` → it's gone. Add a verify block `test -f projects/half-baked/config.local.yml && echo created`.

- [ ] **Step 2: Write `04-edit-with-diff.md`**

A **diff** block patching the stale value: ` ```diff {id=bump}` with a unified patch
```
--- a/projects/half-baked/config.yml
+++ b/projects/half-baked/config.yml
@@ …context… @@
-version: "1.0"
+version: "2.0"
```
(Match `config.yml`'s real surrounding lines so it applies cleanly.) Demos view-diff → apply → undo.

- [ ] **Step 3: Write `05-diff-drift.md`**

A **diff** block ` ```diff {id=settimeout}` whose patch context assumes `timeout = 30` (e.g. `-timeout = 30` / `+timeout = 60`) — but `drifted/settings.conf` ships `timeout = 99`, so `git apply --check` fails → the **drift region** ([resolve manually] + [regenerate]). Prose explains both buttons + the no-mux/mux callout for the diff view.

- [ ] **Step 4: Verify the apply/drift invariants** (git apply --check, the real trigger)

```bash
cd "$REPO"
# extract 04's patch and confirm it APPLIES to config.yml:
#   (manually or via a heredoc) git apply --check <04 patch>   → exit 0
# extract 05's patch and confirm it FAILS against settings.conf:
#   git apply --check <05 patch>   → non-zero
```
(Author the check to extract each diff block's body and run `git apply --check --recount --ignore-whitespace` — 04 must pass, 05 must fail. This is the load-bearing demo trigger.) Also run the Task-2 well-formedness loop over the 3 files.

- [ ] **Step 5: Commit**

```bash
git add examples/03-create-a-file.md examples/04-edit-with-diff.md examples/05-diff-drift.md
git commit -m "docs(examples): chapters 03-05 — file= create, diff apply/undo, drift"
```

---

### Task 4: Chapter 06 — portable + env confirmation gate

**Files (create):** `examples/06-portable-and-env.md`

**Context:** The ONLY `project_bound` chapter. cwd becomes `examples/projects/portable`; `$PROJECT_ROOT` is exported; the front-matter `env:` map fires the grouped confirmation gate on first run.

- [ ] **Step 1: Write `06-portable-and-env.md`**

Front matter:
```yaml
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
```
Body: blocks that reference `$PROJECT_ROOT`/`$DATA_DIR` (e.g. ` ```bash {id=seed}` → `mkdir -p "$DATA_DIR" && echo seeded > "$DATA_DIR/seed"`). Prose explains: first run pops the grouped **Confirm / Customize** gate (≤5 vars/dialog); the values are exported before the block runs.

- [ ] **Step 2: Verify**

```bash
grep -q 'project_bound: true' "$REPO/examples/06-portable-and-env.md"
grep -qE 'env:' "$REPO/examples/06-portable-and-env.md" && grep -q 'PROJECT_ROOT:' "$REPO/examples/06-portable-and-env.md"
# front-matter parses: the block between the first two --- is valid YAML with an env map
```
Expected: pass. (Confirm the front matter parses — a quick `python -c`/`yq` YAML load of the front-matter block, or note manual.)

- [ ] **Step 3: Commit**

```bash
git add examples/06-portable-and-env.md
git commit -m "docs(examples): chapter 06 — portable + env confirmation gate"
```

---

### Task 5: Chapters 07–08 — run modes + the store

**Files (create):** `examples/07-run-modes.md`, `examples/08-the-store.md`

**Context:** 07 demos `--assisted` (confirm-each-step) + `--auto` (⏳ not yet built — write as if shipped). 08 demos the store (`list`/`search`/`show` + `[edit]`+reload) using the Task-1 `examples/store/` fixtures via `$AI_PLAYBOOK_DATA_DIR`, + `validate` (⏳). These chapters are largely PROSE walkthroughs of CLI invocations (07 also a small runnable playbook).

- [ ] **Step 1: Write `07-run-modes.md`**

A short runnable playbook (2–3 idempotent shell blocks on tidy-shop) whose VALUE is being run three ways. Prose: `ai-playbook run --file examples/07-run-modes.md` (manual run-each), `--assisted` (confirm-each-step prompt), `--auto` (run-all). Mark the `--assisted`/`--auto` paragraphs with `<!-- ⏳ needs assisted/auto run modes (not yet built) -->`. Include the stop button note.

- [ ] **Step 2: Write `08-the-store.md`**

Pure walkthrough (no special blocks needed beyond an H1 + a static block or two): how to point `$AI_PLAYBOOK_DATA_DIR` at `examples/store`, then `ai-playbook list`, `search <tag>`, `show <slug>` (→ the `[edit]` button appears since it's file-backed; edit + save → reload, with the mux/no-mux callout), and `validate --file …` (mark `<!-- ⏳ needs validate (not yet built) -->`). Reference the two fixtures by name.

- [ ] **Step 3: Verify**

```bash
# the store fixtures are valid + discoverable
AI_PLAYBOOK_DATA_DIR="$REPO/examples/store" "$REPO"/path/to/ai-playbook list 2>/dev/null | grep -qi 'tidy-checkup' \
  || echo "MANUAL: verify list once ai-playbook is on PATH"
# well-formedness loop over 07 + 08; grep the ⏳ markers exist
grep -q '⏳' "$REPO/examples/07-run-modes.md" "$REPO/examples/08-the-store.md"
```
Expected: well-formed; ⏳ markers present. (The `list` check is best-effort — if `ai-playbook` isn't on PATH in the task env, note it MANUAL.)

- [ ] **Step 4: Commit**

```bash
git add examples/07-run-modes.md examples/08-the-store.md
git commit -m "docs(examples): chapters 07-08 — run modes + the store"
```

---

### Task 6: Chapter 09 (fix-it) + the tutorial guide

**Files:**
- Create: `examples/09-fix-it.md`, `docs/guides/tutorial.md`
- Modify: `examples/README.md` (ensure the chapter index is complete)

**Context:** 09 is the authoring chapter — it's mostly prose guiding the reader to point `ai-playbook` at `projects/broken-build/`. The guide is the ordered walkthrough tying ch.01→09 together.

- [ ] **Step 1: Write `09-fix-it.md`**

Prose walkthrough: `cd examples/projects/broken-build`, observe `bash build.sh` fail, then `ai-playbook create "fix the build"` (force-author) OR `ai-playbook assist` after the failure (triage → escalate). Explain: capture (last command/exit/cwd/scrollback), the authored structured playbook opening in the viewer, the re-engage surface (a failed step → "try another fix" / regenerate), and the cached badge on a re-run. Prerequisite callout: needs a configured model backend. mux/no-mux callout for the assist `ask`.

- [ ] **Step 2: Write `docs/guides/tutorial.md`**

The ordered tour. Open with: what this is, install/prereqs (model backend only for ch.09), the no-mux/mux note, the safety note (real shell in the project dir; nothing runs until you click; apply/create demos modify tracked fixtures → `undo` or `git restore examples/`). Then one section per chapter, each step shaped `**Run:** <cmd>` → `**You'll see:** …` → `**Click:** …` → `**Notice:** …` → `**Undo:** …`. Sequence 01→09. Carry the ⏳ markers for assisted/auto/validate. End with "you've now used every surface + authored one."

- [ ] **Step 3: Verify coverage (the completeness gate)**

```bash
# every feature-row keyword from the spec's coverage matrix appears somewhere in the tour:
for kw in run play stop copy static callout needs verify rollback "file=" diff drift "resolve manually" regenerate PROJECT_ROOT env assisted auto list search show edit validate create assist followup cached; do
  grep -rqi -- "$kw" "$REPO/examples" "$REPO/docs/guides/tutorial.md" || echo "MISSING coverage: $kw"
done
# well-formedness on 09; the guide links every chapter file
for n in 01 02 03 04 05 06 07 08 09; do grep -q "$n-" "$REPO/docs/guides/tutorial.md" || echo "guide missing ch $n"; done
```
Expected: no MISSING / no missing-chapter lines.

- [ ] **Step 4: Commit**

```bash
git add examples/09-fix-it.md docs/guides/tutorial.md examples/README.md
git commit -m "docs(examples,guides): chapter 09 fix-it + the guided tutorial"
```

---

## Self-Review

**Spec coverage:** the scaffold + 5 projects + store fixtures (T1); ch.01–02 basics (T2); ch.03–05 file=/diff/drift (T3); ch.06 portable+env (T4); ch.07–08 run-modes+store (T5); ch.09 fix-it + the guide (T6). The coverage-matrix completeness gate runs in T6 Step 3. The drift trigger (git apply --check fails) is verified in T3; the broken build (fails) in T1.

**Forward features:** ch.07 `--assisted`/`--auto` + ch.08 `validate` written as if shipped, tagged `⏳` (grep-able) for verification when built.

**Determinism/safety:** blocks idempotent + relative + in-project-dir; verification is well-formedness + invariants (no headless TUI launch — manual render check noted).

**Open items the implementer confirms against real code:**
- T1: small realistic project content; `chmod +x` the scripts; the broken-build break is obvious + fixable.
- T3: the 04 patch context must match `config.yml` (applies); the 05 patch context must mismatch `settings.conf` (drifts) — the git-apply-check is the gate.
- T4: the front-matter `env:` map shape (NAME: {value, why}) parses; `project_root` is the portable project.
- T5: how to point `$AI_PLAYBOOK_DATA_DIR` at `examples/store`; the fixtures carry distinct name/category/tags.
- T6: the coverage grep is the completeness backstop — fill any MISSING keyword before committing.
