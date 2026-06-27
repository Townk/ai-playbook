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
| `{rollback=<id>}` | Phase 2    | The rollback for step `<id>`; on failure, completed steps' rollbacks run in REVERSE order. |
| `{static}`      | shipped      | A non-runnable block (no run button) — illustrative output, config samples, etc. |

Multi-language steps run via interpreter heredocs — shell plus
`python`/`node`/`ruby`/`perl`.

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
