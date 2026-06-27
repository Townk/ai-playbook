# Zero-assumption defaults: honor $SHELL, multiplexer off by default

- **Status:** Accepted
- **Date:** 2026-06-27

## Context and Problem Statement

[ADR-0006](0006-progressive-enhancement-portable-core.md) made the shell and the
multiplexer *optional* (progressive enhancement) but left the **defaults** tuned to
the original author's environment: the shell driver preferred **zsh** regardless of
the user's login shell, and the multiplexer integration **auto-activated** whenever
`$ZELLIJ` was set. ADR-0006 explicitly deferred flipping these defaults.

For a public, portable tool the out-of-box defaults should make the *fewest*
assumptions about the user's environment:

- A bash (or any) user invoking ai-playbook with no config got a **zsh** driver, not
  their own shell — surprising, and wrong if zsh isn't even installed the way they
  expect.
- Running inside zellij silently switched on the floating-pane/docked-pane UX. That
  couples the default experience to one specific multiplexer and means the **no-mux
  path** (the portable baseline) was rarely the default — so it got little real
  exercise.

We are about to wire the dotfiles integration, so this is the moment to set the
defaults deliberately rather than inherit them.

## Decision Drivers

- **Least surprise / portability:** a no-config run should work anywhere and respect
  the user's actual environment (their `$SHELL`).
- **The portable baseline must be first-class:** making no-mux the default forces the
  inline UX to be solid (it is now the path most runs take).
- **Config consistency:** opt-in should look the same across every integration.
- **Explicit over implicit:** integrations turn on because the user asked, not because
  an env var happened to be present.

## Considered Options

1. **Keep ADR-0006's defaults** (zsh-first; mux auto-on under `$ZELLIJ`).
2. **Zero-assumption defaults:** honor `$SHELL`; multiplexer off unless explicitly
   opted in via config. *(chosen)*
3. Honor `$SHELL` but keep mux auto-on under `$ZELLIJ` (flip only the shell).

## Decision Outcome

Chosen: **Option 2.**

- **Shell default = honor `$SHELL`.** The compiled-in default is `[driver] shell = ""`
  (auto): `driver.resolveShell` uses `$SHELL` when its basename names a supported
  shell (zsh/bash/sh) and it is present, falling back zsh → bash → sh otherwise. An
  explicit `[driver] shell = "zsh"|"bash"|"sh"` still pins it. A zsh user is
  unaffected (`$SHELL`=zsh → zsh).
- **Multiplexer default = off.** A no-config run uses the inline (no-mux) UX even
  inside zellij. The integration is opted in with **`[mux] backend = "zellij"`**; the
  `$ZELLIJ`-presence auto-enable is removed. Per-command `[mux]` template overrides
  remain for fine-grained control (tier-2).
- **Uniform opt-in shape** across integrations: `[driver] shell`, `[agent] harness`,
  `[mux] backend` are all `[section] selector = "name"`.

### Positive Consequences

- Works out of the box on any supported shell, honoring the login shell.
- The no-mux inline path is the default → continuously dogfooded and kept solid.
- One consistent mental model for enabling/picking each integration.
- The mux is on only by explicit intent, not an ambient env var.

### Negative Consequences

- **Behavior change (effectively breaking for pre-existing users):** anyone who relied
  on auto-zellij must now add `[mux] backend = "zellij"`. Acceptable pre-1.0; recorded
  in the CHANGELOG under *Changed* and surfaced in the docs.
- Slightly more config to get the full floating-pane experience back.

## Pros and Cons of the Options

### Option 1 — keep ADR-0006 defaults
- **Good:** zero migration; the author's setup keeps working untouched.
- **Bad:** non-portable defaults; surprises non-zsh users; the no-mux path stays
  under-exercised; implicit env-driven activation.

### Option 2 — zero-assumption defaults (chosen)
- **Good:** portable, least-surprise, dogfoods the baseline, consistent opt-in.
- **Bad:** breaking for auto-zellij users; a bit more config for the full UX.

### Option 3 — flip shell only
- **Good:** fixes the most common surprise (wrong shell) with no mux migration.
- **Bad:** leaves the implicit env-driven mux activation and the under-exercised
  no-mux path; inconsistent (one integration implicit, one explicit).
