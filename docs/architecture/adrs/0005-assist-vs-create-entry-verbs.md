# ADR-0005: Split `assist` and `create` entry verbs

**Status:** Accepted

**Date:** 2026-06-26

## Context and Problem Statement

A single entry verb (the old `troubleshoot`) conflated two genuinely different
intents: "help me with this" (which may resolve to a one-line command, a short
answer, or a full playbook, and is reactive to a terminal failure) and "make me a
playbook" (direct authoring). Conflating them muddied the cache-badge semantics
too: it was unclear when a result was served from cache versus freshly produced.
Should there be one entry verb or two?

## Decision Drivers

- Clarity of intent — the user should say what they want, not have it inferred.
- Clean cache-badge semantics — only one path should ever serve a cache hit, so
  the cached/regenerate badge has an unambiguous meaning.

## Considered Options

- Keep one entry verb that infers intent (the status quo).
- Split into two verbs: `assist` (triage) and `create` (direct authoring).

## Decision Outcome

Chosen option: "split into two verbs".

- **`assist`** = triage. It is the **only** triage path: classify the request
  into command / answer / escalate, reactive to terminal failures, and **cache-
  served** — it shows the cache/regenerate badge on a cache hit.
- **`create`** = direct authoring. It is **always fresh** — no triage, no cache
  serve. It writes to **both the store and the cache** (so a later `assist` can
  hit it) but never *serves* a cache hit and never shows the badge; it surfaces a
  "similar playbooks already exist: …" banner from a store search.

This makes "served from cache" mean exactly "an `assist` cache hit". The decision
is designed and slated for the roadmap's Phase 1.

### Positive Consequences

- Unambiguous verbs — the user states intent directly.
- Clean cache-badge semantics — the badge appears only for `assist` cache hits.

### Negative Consequences

- Two code paths to build and maintain instead of one.

## Pros and Cons of the Options

### One entry verb (infer intent)

- Good, because there is only one command to learn and one path to maintain.
- Bad, because intent is inferred (sometimes wrongly) and the cache-badge meaning
  is muddy when authoring and triage share a path.

### Split `assist` / `create`

- Good, because intent is explicit and cache-badge semantics are clean (only
  `assist` serves cache hits).
- Bad, because there are now two code paths.
