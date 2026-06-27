# Progressive enhancement: a portable core with optional shell, multiplexer, and PTY

- **Status:** Accepted
- **Date:** 2026-06-27

## Context and Problem Statement

Cutting the v0.3.0 release surfaced that the binary hard-requires three
environment dependencies: a **zsh** shell, a **zellij** multiplexer, and
**Unix PTY + signals**. The Windows release target failed to compile because
`internal/driver` uses `golang.org/x/sys/unix`; the test suite fails on any
runner without zsh; and the `assist` flow assumes a mux for its docked pane.

This coupling was **inherited** from the original `ai-assist` (a zsh + zellij
shell-script stack). The Go rewrite ([ADR-0002](0002-go-binary-replacing-the-shell-stack.md))
removed the zsh *implementation*, but carried the zsh/zellij/Unix coupling
forward as hard requirements rather than decoupling it. The intent was always
that zsh facilities and mux integration are a **plus** (fidelity, a docked
docked UX), not a precondition for the tool to work.

Concretely (from a coupling audit):

- **Shell** — `internal/driver/driver.go:66` hardcodes `exec.Command("zsh", "-il")`,
  and `runID()` emits zsh-specific job scripts (`${(q)…}`, `print -r --`,
  `[[ … ]]`, the `errexit`-subshell model). No seam.
- **Multiplexer** — already abstracted behind the `mux.Mux` interface
  (`internal/mux`, config-driven templates); zellij is the default impl. The
  diff-viewer float and scrollback dump already nil-check. Callers in
  `cmd/ai-playbook` assume a live mux for the input float and docked panes.
- **PTY/signals** — `internal/driver` uses `creack/pty` plus `unix.Kill`,
  `unix.TIOCGPGRP`, `unix.IoctlGetInt` to drive an interactive shell (Ctrl-C
  passthrough, foreground process-group targeting, SIGTERM→SIGKILL). Unix-only;
  no seam.

A large **portable core already exists** with zero env coupling: `author`,
`cache`, `frontmatter`, `triage`, `kb`, `agentstream`, and the bubbletea
render/model layer.

This also intersects test reliability: the lowest-coverage packages are exactly
the env-coupled ones (`cmd/ai-playbook` 35%, `mcpserver` 42%, `mux` 50%). Clean
seams make these unit-testable.

## Decision Drivers

- **Reach / portability** — run on any Unix, under any POSIX shell, with or
  without a multiplexer; don't force the user's exact environment.
- **Progressive enhancement** — zsh, the mux, and rich PTY driving should be
  *enhancements when present*, not requirements.
- **Testability** — a portable core behind seams is unit-testable, directly
  lifting coverage on the weakest packages.
- **Future platforms/harnesses** — establish the seams that a later Windows
  (ConPTY) or headless/CI path would plug into.
- **Avoid premature scope** — don't take on a full cross-platform rewrite now.

## Considered Options

1. **Status quo** — keep zsh + zellij + Unix-PTY as hard requirements.
2. **Progressive enhancement** — a portable core plus optional integration
   layers behind seams, with graceful degradation. *(chosen)*
3. **Full multi-platform rewrite now** — shell-agnostic + ConPTY Windows +
   headless, all at once.

## Decision Outcome

Chosen: **Option 2 — progressive enhancement**. Keep the portable core
env-independent, and put each environment integration behind a seam with a
graceful fallback, delivered in stages by ascending effort:

- **Stage 1 — multiplexer optional.** Add a null/no-op `mux.Mux` and select it
  when no mux is detected (e.g. `$ZELLIJ` unset and no config override). Without
  a mux: read the request inline (CLI arg / stdin), run the session as an
  inline full-screen TUI, print answers to stdout. The `mux.Mux` interface and
  the existing nil-checks make this low-effort.
- **Stage 2 — shell-agnostic.** Add a configurable shell (`[driver] shell`,
  default `zsh`, falling back to `$SHELL`); refactor `runID()` job generation so
  the zsh-isms (`${(q)}`, `print -r`, `[[ ]]`, the errexit-subshell model) have
  bash/POSIX-sh equivalents (per-shell adapters). zsh stays the default for
  fidelity (aliases/functions/shims).
- **Stage 3 — interactive driving optional (deferred / evaluate).** Introduce a
  `Driver` interface with the PTY+signals impl as default and a `PipeDriver`
  (non-TTY, timeout-based, no signal delivery) for headless/CI; a Windows
  ConPTY impl is a separate future effort. High effort; not required for the
  near-term decoupling.

Stages 1 and 2 are the near-term work; Stage 3 is recorded but deferred.

### Positive Consequences

- The tool runs in far more environments (any Unix, any POSIX shell, no mux).
- zsh and the mux become genuine pluses, matching the original intent.
- Seams make the env-coupled packages unit-testable → coverage rises where it's
  weakest.
- Establishes the extension points for future Windows/headless/harness work.

### Negative Consequences

- More code paths (fallbacks) to build, test, and maintain.
- Shell-agnostic job generation must reconcile quoting / `errexit` / expansion
  differences across zsh/bash/sh — a real risk of subtle behavioral drift,
  mitigated by per-shell tests.
- The `PipeDriver` fallback is a *degraded* mode (no interactive TTY programs,
  no SIGINT passthrough).
- Windows remains out of reach until Stage 3's ConPTY work.

## Pros and Cons of the Options

### Option 1 — Status quo

- **Good:** simplest; one code path; highest fidelity on the author's setup.
- **Bad:** Unix + zsh + zellij locked; can't `go install`-and-run elsewhere;
  the env-coupled packages stay hard to test (low coverage).

### Option 2 — Progressive enhancement (chosen)

- **Good:** broad reach with graceful degradation; testable core; zsh/mux as
  pluses; incremental and low-risk (mux seam already exists).
- **Bad:** several fallback paths to maintain; cross-shell correctness work.

### Option 3 — Full multi-platform rewrite now

- **Good:** maximal reach (incl. Windows) in one move.
- **Bad:** large and premature; ConPTY is a separate API surface; high risk for
  little near-term gain.
