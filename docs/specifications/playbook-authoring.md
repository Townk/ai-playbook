# Playbook authoring quality (the rubric)

_Status: approved 2026-07-05 (design settled with the project owner). The
v0.12.1 mini-milestone: this rubric is the single source that the authoring
prompts, the `validate` quality warnings, the AI review pass, and the portable
authoring SKILL all derive from. Motivating case: an external LLM (Claude
Code), given only the schema spec, authored a syntactically valid playbook
with multi-step monolithic blocks, no `file=` usage, and no rollbacks._

## The rubric

A good playbook is a sequence of **checkpointed, individually confirmable
steps** that a person (or `--auto`) can run, verify, and undo. The rules:

1. **Atomicity — one logical step per block.** A block does ONE thing that a
   human confirms once: install one component, write one config, start one
   service. If a block needs a prose "and then…" to describe, split it.
   Multiple related shell commands are fine ONLY when they form one atomic
   action (e.g. `mkdir && cd && tar`), never when they are separate steps.
2. **`file=` for file creation.** A new file's full content goes in a
   `{id=x file=<path>}` block — NEVER a shell block with heredoc/`cat >`/
   `tee`. The create block is previewable, undoable, and diffable; the
   heredoc is none of those.
3. **Diff blocks for edits.** Changing an existing file uses a diff block
   (complete, `git apply`-able unified diff, paths relative to the project
   root) — not `sed -i` one-liners when the change is structural, and never a
   rewrite-the-whole-file heredoc.
4. **Rollback discipline.** Every step that MUTATES state (installs, writes,
   enables, registers) declares `rollback=<undo-id>` in its fence tag, naming
   a companion `{id=<undo-id>}` block that restores the pre-step state. On
   failure, completed steps' rollbacks run in REVERSE order — each rollback
   only undoes its own step. Read-only steps (checks, queries) need none.
5. **Verify, always.** Every playbook ends with `{id=verify needs=<last-step>}`
   proving the GOAL state: troubleshooting → re-run the originally failing
   command; how-to/onboarding → check the installed/configured/running state.
   One block, one authoritative check.
6. **Real dependencies, declared.** `needs=` for ordering ("B requires A
   succeeded"), `from=` for data ("B consumes A's stdout on stdin" — prefer it
   over `$(...)$APB_OUT_x` plumbing when the consumer reads the whole output;
   the quoted `APB_OUT_<id>` env and raw-path `APB_OUT_FILE_<id>` cover
   argument-style access). Do not serialize independent steps.
7. **`{static}` for illustration.** Sample output, expected trees, error
   captures: static/console blocks — never runnable.
8. **Portability + `env:`.** Declare every required environment variable in
   the `env:` front matter (name + why); use `$PROJECT_ROOT`, `$HOME`, and
   tool-resolved paths instead of hardcoded ones; the playbook must be
   runnable on a machine that is not the author's.
9. **Callouts for danger.** `warning`/`caution` callouts precede destructive
   or irreversible steps.

### Worked example (good vs bad)

The spec carries a compact bad-then-good pair modeled on the motivating case
(a system-bootstrap playbook): the bad form is one giant shell block with
heredocs and no verify; the good form is 4 atomic steps (a `file=` config
block, an install step with `rollback=`, an enable step with `rollback=`, a
`verify`), with `env:` declared. (Authored in full in the committed rubric
document — the SKILL embeds the same pair.)

## Surface 1 — the shared prompt fragment

- One Go constant in `internal/author` (e.g. `authoringRubric`) distilling
  rules 1–9 into prompt-voice guidance, embedded by BOTH `SystemPrompt` (the
  markdown path — which today lacks rollback, verify-for-how-tos, `from=`,
  `env:`, callouts, and granularity guidance entirely) and
  `StructuredToolInstruction` (which lacks rollback prose, `from=`, and
  granularity). The existing per-path text that duplicates rubric content is
  REPLACED by the fragment (single source; the paths cannot drift).
- `FinalPlaybookPrompt` keeps its lighter convention restatement (it operates
  on an existing document) — plan may embed the fragment if golden churn is
  acceptable; otherwise documented as deliberate.

## Surface 2 — `validate` quality warnings + AI review

Four new checks in `pkg/playbook/validate`, ALL Warning severity (exit code
untouched — the existing Error/Warning tier):

| Check | Fires when |
|---|---|
| `verify` | no `{id=verify}` block exists |
| `rollback` | ≥2 runnable non-verify blocks and ZERO rollback blocks |
| `file-block` | a shell/run block writes a file via heredoc (`<<` + `>`/`>>`/`tee` shape) — message suggests a `file=` block |
| `env-decl` | a runnable block references `${VAR}` (via the existing `frontmatter.ScanEnvRefs` machinery) not declared in `env:` and not a builtin (`APB_*`, `LAST_*`, `PROJECT_ROOT`, `HOME`, `PATH`, …) |

Conservative by design: detection must be certain; judgment calls (is THIS
block too coarse, does THIS step need rollback) belong to the AI review pass,
whose system prompt (`reviewSystemPrompt`) is rebuilt to embed the rubric and
review against it explicitly.

## Surface 3 — the SKILL + the `skill` verb

- `skills/playbook-authoring/SKILL.md` committed in-repo: superpowers-style
  frontmatter (`name: playbook-authoring`, a trigger `description`), body =
  harness-agnostic schema quick-reference + the rubric + an authoring
  checklist + the worked example + "iterate against `ai-playbook validate
  --file <path>` until warnings are addressed or consciously accepted".
- Embedded in the binary via `go:embed`; a test pins embed == repo file.
- New public `ai-playbook skill` verb: `skill show` (print to stdout),
  `skill install [--to <dir>] [--force]` (default
  `~/.claude/skills/playbook-authoring/SKILL.md`; creates dirs; refuses to
  overwrite without `--force`; prints the installed path). climeta-registered
  with `Subcommands` completion; man/completion via the pipeline.
- Shipped in release archives (one goreleaser `files:` line).

## Out of scope

- Aggressive mechanical heuristics (per-block command caps, mutating-command
  detection) — recorded as rejected: false positives train authors to ignore
  warnings.
- Quality checks as Errors; auto-fixing; a `skill` update/uninstall verb.
- Per-harness skill formats beyond the one markdown artifact.

## Testing

- Warning tables per new check (trigger + non-trigger + Warning severity +
  exit code unchanged); env-decl builtin allowlist cases.
- Prompt goldens: the fragment present in both paths (byte-stable), removed
  duplicates gone; unchanged prompts characterized.
- AI review prompt golden containing the rubric.
- Skill verb tables (show; install to temp dir; existing-file refusal;
  --force; dir creation); the embed-matches-repo-file test.
- docs-check green (new man/completion); goreleaser check (archive line).
