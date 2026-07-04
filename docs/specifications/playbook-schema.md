# Playbook schema

A playbook is **literate-config Markdown** (see
[ADR-0004](../architecture/adrs/0004-playbooks-as-literate-config-markdown.md)):
an H1 title, free prose, YAML front matter for metadata, and fenced code blocks
tagged on the language line to mark which are runnable. This document is the
**contract** between the authoring model, a human author, and the runner. It is
derived from the Schema section of [`../ROADMAP.md`](../ROADMAP.md) and evolves
across phases — each field/tag is annotated with its phase availability.

## YAML front matter

A leading `---` … `---` block. Fields:

| Field         | Type         | Availability | Meaning |
|---------------|--------------|--------------|---------|
| `name`        | string       | shipped      | Human title of the playbook. |
| `description` | string       | shipped      | One-line summary. |
| `category`    | string       | shipped      | Grouping label (e.g. `git`, `docker`). |
| `tags`        | list[string] | shipped      | Search/filter keywords. |
| `env`         | list/map     | shipped      | Environment variables the playbook expects. |
| `workdir`     | string       | Phase 1      | Target directory the playbook applies to; adapt-on-run resolves it (and asks if absent/stale). Home paths normalize to `~`. |
| `depends_on`  | list[slug]   | Phase 3      | Playbooks to run fully, in topological order, before this one. |

`name`/`description`/`category`/`tags`/`env` are assembled jointly by ai-playbook
and the model; `finalize` backfills front matter onto older playbooks.

## Fenced-block tags

Tags go on the opening fence's language line, e.g. ` ```bash {id=install} `. The
runner keys run/diff/apply and success-detection on the `id`.

| Tag             | Availability | Meaning |
|-----------------|--------------|---------|
| `{id=<id>}`     | shipped      | A runnable step. An id is auto-assigned when absent. |
| `{id=verify}`   | shipped      | The final whole-setup verification; success detection keys on this block. |
| `{needs=<id>[,<id>...]}` | shipped | Gate: the block won't run — no run button in the viewer, skipped in `--auto` — until every listed id is `ok`. |
| `{rollback=<id>}` | Phase 2    | The rollback for step `<id>`; on failure, completed steps' rollbacks run in REVERSE order. |
| `{from=<id>}`   | Phase 6      | Data dependency (ADR-0010): wires the named producer's retained stdout to this block's **stdin**; implies `needs=<id>` for gating/ordering/invalidation. Only `shell`/`run` blocks may declare or be targeted by `from=`. See [Value-passing](#value-passing) below. |
| `{static}`      | shipped      | A non-runnable block (no run button) — illustrative output, config samples, etc. |

Only **top-level** fenced blocks carry block authority: a fence tag (`id=`,
`rollback=`, `file=`, `{static}`) on a code block nested inside a list or
blockquote is inert — it is neither runnable, nor a rollback command, nor
validated. The parser (`playbook.ParseBlocks`), the renderer, `validate`, and
the run engine all share this rule (settled 2026-07-04; the renderer honored
nested `rollback=` tags before that).

Multi-language steps run via interpreter heredocs — shell plus
`python`/`node`/`ruby`/`perl`.

## Value-passing

A block can consume data a prior block produced two ways: session env vars
(every identified run, always on) and a declared `from=` data edge (Phase 6,
ADR-0010) that wires a producer's stdout to a consumer's stdin.

### Env vars (every identified run)

After each identified (`id=`-tagged) block runs, the live session shell gets:

| Var | Content | Notes |
|---|---|---|
| `APB_OUT_<id>` / `APB_ERR_<id>` | full stdout/stderr, **shell-quoted** (`printf %q` form; zsh uses its `${(q)}` equivalent) | re-expands word-split- and glob-safely when consumed as shell text; awkward for raw or binary consumption |
| `APB_EXIT_<id>` | exit code | |
| `APB_OUT_FILE_<id>` / `APB_ERR_FILE_<id>` | **raw path** to the retained capture file — no quoting, no size limit | args-passing idiom: `--input "$(cat "$APB_OUT_FILE_<id>")"` |

`<id>` is the block's id with any character outside `[A-Za-z0-9_]` replaced by
`_`. The `FILE` vars point at files retained under the session directory,
overwritten when the same id re-runs and removed when the session closes.

### `from=<id>`

New fence attribute on `shell` and `run` blocks (`run` = the script blocks —
python/node/ruby/perl; a python consumer reading `sys.stdin` is the flagship
case). `` {id=filter from=build} `` wires the `build` block's retained stdout
directly to `filter`'s **stdin** — raw bytes, no shell-quoting and no size
limit, unlike the `APB_OUT_<id>` env vars above.

- **`from=` implies `needs=`**: the target joins the block's effective needs
  set for gating, `--auto` ordering, and dependent invalidation, without being
  added to the textual `needs=` attribute.
- **Validation:** the target must exist and must not be the block itself; the
  target must be a runnable `shell`/`run` block (`static`/`diff`/`create` have
  no output and are rejected as either the producer or the consumer); only one
  producer per block (a comma list is a validation error); ordering/cycle
  checks run over the **combined** `needs= ∪ from=` graph.
- **The producer stays independently runnable** — running it standalone
  simply pre-materializes its capture for later consumers.
- **Auto-materialization** (viewer and `--assisted`, `from=` chains only):
  running a consumer whose upstream `from=` producer(s) haven't run this
  session runs the whole chain in order first — each step is an ordinary
  block run with its own status, log, and (in `--assisted`) its own
  confirmation. A producer already `ok` this session is NOT re-run; a failing
  step stops the chain and downstream steps never start. Plain `needs=`-only
  dependencies still just gate — they never auto-run. `--auto` is unaffected:
  topological order over the combined graph already materializes producers
  before consumers.
- **Out of scope in v1:** multiple producers per block (`from=a,b`), stream
  selectors (`from=id.err`), streaming/concurrent pipes, cross-session capture
  persistence.

See [`cross-block-piping.md`](cross-block-piping.md) and
[ADR-0010](../architecture/adrs/0010-cross-block-data-flow.md) for the full
design.

## Example

````markdown
---
name: Set up a Python venv
description: Create and activate a project virtualenv with pinned deps
category: python
tags: [python, venv, setup]
env:
  - PYTHON: python3
workdir: ~/projects/example
---

# Set up a Python venv

Create an isolated environment and install the locked dependencies.

```bash {id=create-venv}
$PYTHON -m venv .venv
```

```bash {id=install}
.venv/bin/pip install -r requirements.txt
```

This is what a healthy tree looks like (not run):

```text {static}
.venv/
requirements.txt
```

```bash {id=verify}
.venv/bin/python -c "import sys; print(sys.prefix)"
```
````
