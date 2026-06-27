# ADR-0001: Record architecture decisions

**Status:** Accepted

**Date:** 2026-06-26

## Context and Problem Statement

ai-playbook is a solo project that has already made several non-obvious
architectural choices (a Go rewrite of a shell stack, in-process re-engagement,
literate-config playbooks, split entry verbs). Those choices have rationale that
lives only in the author's head and in scattered commit messages. As the project
grows — and especially when revisiting it after a break, or onboarding any future
contributor — that rationale is easy to lose, leading to re-litigated decisions
and accidental regressions. How should we capture significant architectural
decisions durably?

## Decision Drivers

- Durable rationale — the *why* must outlive memory and commit archaeology.
- Onboarding — a future contributor (or future self) can read the trail.
- Avoid re-litigating — settled decisions stay settled unless explicitly superseded.
- Low ceremony — a solo project cannot afford a heavyweight process.

## Considered Options

- No ADRs — rely on commit messages, code comments, and memory.
- A wiki / external doc tool — capture decisions outside the repo.
- MADR-style ADRs — lightweight Markdown decision records committed in-repo.

## Decision Outcome

Chosen option: "MADR-style ADRs", in a MADR-lite form captured by
[`template.md`](template.md) and stored under `docs/architecture/adrs/`. ADRs are
numbered, immutable once Accepted (changed only by a new ADR that supersedes
them), and version-controlled alongside the code they describe. The template drops
MADR's "Deciders" field, which is noise for a solo project.

### Positive Consequences

- Decisions and their rationale live in the repo, reviewed and diffed like code.
- New significant decisions have an obvious home and a consistent shape.
- The history of *why* is greppable and linkable from code and other docs.

### Negative Consequences

- A small per-decision authoring cost.
- Discipline required to actually write one when a decision is made.

## Pros and Cons of the Options

### No ADRs

- Good, because zero overhead.
- Bad, because rationale is lost; decisions get silently reversed.

### A wiki / external doc tool

- Good, because rich editing and cross-linking.
- Bad, because it drifts from the code, needs separate auth/hosting, and is not
  versioned with the change it explains.

### MADR-style ADRs

- Good, because in-repo, versioned, reviewable, low ceremony, and a recognized
  convention.
- Bad, because it is a manual habit to maintain.
