# ADR-0002: A single Go binary replacing the shell stack

**Status:** Accepted

**Date:** 2026-06-26

## Context and Problem Statement

The original `ai-assist` was a sprawling zsh implementation: a `libexec/` tree of
shell scripts (a dispatcher, workers, triage, render, cache, ask, remember, and
more) glued together with environment variables, named pipes, and subprocess
fan-out. It worked, but it was hard to test (shell is awkward to unit-test),
hard to maintain (control flow spread across many files and process boundaries),
coupled to a specific shell, and slow to start. We also want the assistant to be
**harness-agnostic** — Claude today, but pi/cursor and others later — which the
shell stack baked in only implicitly. How should the assistant be implemented
going forward?

## Decision Drivers

- Testability — real unit/integration tests, not shell harness gymnastics.
- Maintainability — typed, structured control flow in one codebase.
- Performance — fast startup and execution.
- Harness-agnosticism — a clean, pluggable model-harness abstraction.
- Single-binary distribution — one artifact to install and ship.

## Considered Options

- Keep and incrementally improve the existing zsh + `libexec/` shell stack.
- Rewrite as a single Go binary with a pluggable model-harness abstraction.
- Rewrite in another language (e.g. Rust, Python).

## Decision Outcome

Chosen option: "rewrite as a single Go binary", with a **pluggable model-harness
abstraction** (Claude is the only shipped harness today; pi/cursor are additive
later). The shell stack is retired and replaced wholesale. Go gives us a
statically-linked single binary, a real test story, strong concurrency for the
streaming/PTY work, and a mature ecosystem (bubbletea/lipgloss, goldmark/chroma,
the MCP go-sdk, creack/pty).

### Positive Consequences

- Genuinely testable — packages have unit and integration tests.
- Maintainable — typed structures and one coherent codebase.
- Fast — compiled startup and execution, no per-step subprocess churn.
- Portable — a single self-contained binary per platform.
- Harness-agnostic by construction.

### Negative Consequences

- A large up-front rewrite of a working system.
- Contributors must know Go rather than shell (a higher floor, but a more
  maintainable one).

## Pros and Cons of the Options

### Keep the shell stack

- Good, because it already works and needs no rewrite.
- Bad, because it is hard to test, hard to maintain, shell-coupled, and slow;
  harness-agnosticism is only implicit.

### Go rewrite

- Good, because testable, maintainable, fast, portable, single-binary, and
  harness-agnostic; excellent libraries for TUI/PTY/MCP.
- Bad, because it is a large rewrite and raises the contributor floor from shell
  to Go.

### Another language (Rust/Python)

- Good, because Rust is fast/safe; Python is quick to write.
- Bad, because Rust adds rewrite cost and a steeper curve with less payoff here;
  Python lacks easy single-binary distribution and is slower to start. Go best
  balances the drivers.
