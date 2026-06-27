# ADR-0004: Playbooks as literate-config Markdown

**Status:** Accepted

**Date:** 2026-06-26

## Context and Problem Statement

The central artifact ai-playbook produces is the *playbook*. It must be all of:
human-readable (you can read it like documentation), runnable (the steps execute),
reusable (re-run later, adapt to a new project), version-controllable (commit it,
diff it, review it), and authorable by **either the model or a human**. What
on-disk format satisfies all of these at once?

## Decision Drivers

- Docs-as-code — the artifact reads as documentation and lives in Git.
- Portability — no proprietary format; works with any editor/tool.
- Model- or hand-authored — the same format whether the model writes it from your
  live context or you type it by hand.
- Git-friendly — line-oriented, reviewable diffs.

## Considered Options

- A custom structured format (JSON/TOML/YAML describing steps).
- A notebook format (e.g. `.ipynb`-style JSON).
- Literate-config Markdown — prose plus fenced, tagged, runnable code blocks.

## Decision Outcome

Chosen option: "literate-config Markdown". A playbook is a Markdown document with:

- an **H1** title and free **prose** describing intent;
- **YAML front matter** for metadata — `name`, `description`, `category`, `tags`,
  `env` (shipped), plus `workdir` (Phase 1) and `depends_on` (Phase 3) per the
  roadmap;
- **fenced runnable blocks** tagged on the language line with `{id=...}`. The
  runner keys run/diff/apply and success-detection (`{id=verify}`) on the id;
  multi-language steps run via interpreter heredocs (shell + python/node/ruby/perl).

This is the same format the model emits and a human can edit — see the schema
contract in [`playbook-schema.md`](../../specifications/playbook-schema.md).

### Positive Consequences

- Portable and tool-agnostic — it is just Markdown.
- Multi-language — any interpreter via a heredoc block.
- Reviewable — reads as docs, diffs cleanly, version-controls naturally.

### Negative Consequences

- A schema (front-matter fields + block tags) to define, maintain, and teach the
  model to emit correctly.

## Pros and Cons of the Options

### A custom structured format

- Good, because unambiguous and easy to machine-parse.
- Bad, because not human-readable as docs, awkward to hand-author, and not a
  natural fit for prose-driven runbooks.

### A notebook format

- Good, because runnable cells are a familiar model.
- Bad, because the JSON-wrapped format diffs badly in Git, is editor-bound, and is
  not pleasant to read or hand-edit.

### Literate-config Markdown

- Good, because human-readable, runnable, portable, multi-language, Git-friendly,
  and equally model- or hand-authored.
- Bad, because it requires a defined schema layered over Markdown that both the
  runner and the model must honor.
