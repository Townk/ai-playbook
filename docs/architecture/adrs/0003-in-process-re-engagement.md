# ADR-0003: In-process orchestration and re-engagement

**Status:** Accepted

**Date:** 2026-06-26

## Context and Problem Statement

In the shell stack, the UI (pager) and the model/work were separate processes
wired together with named-pipe FIFOs (`--input-fifo`, `--actions-fifo`,
`--results-fifo`) and a broker process that shuttled messages between them. This
multi-process IPC was fragile: FIFO lifecycle, ordering, partial reads, and broker
liveness were all failure modes, and the shell driving the run-blocks was a
different shell from the one the agent diagnosed in. With the Go rewrite (see
[ADR-0002](0002-go-binary-replacing-the-shell-stack.md)) we can choose a different
boundary. How should the orchestrator, the model, and the pager communicate, and
how should the agent reach its tools?

## Decision Drivers

- Simplicity — fewer moving parts and process boundaries.
- Robustness — eliminate IPC/FIFO fragility.
- No IPC fragility — no named pipes or broker to lose liveness.
- Shared-shell fidelity — authoring and run-blocks must execute in the *same*
  shell so behavior matches what the agent diagnosed.

## Considered Options

- Keep the multi-process FIFO + broker design from the shell stack.
- Drive everything in-process within the Go binary, with an MCP tools backend
  over a unix socket for the agent.

## Decision Outcome

Chosen option: "drive everything in-process". The Go orchestrator runs the model,
the run-blocks, and re-engagement (regenerate / follow-up / wrap-up) in the **same
process** as the pager. The headless agent reaches its `run` / `ask` / `remember`
tools via an **MCP backend over a unix socket** (the only out-of-process channel,
and a clean one). A single shared shell driver serves **both** authoring and the
playbook's run-blocks, so what the agent saw and what the user runs are the same
shell. The FIFO/broker code has been fully removed.

### Positive Consequences

- Simpler — one process, typed in-process calls instead of pipe messaging.
- Robust — no FIFO lifecycle or broker liveness to manage.
- Shared-shell fidelity — authoring and run-blocks share one driver/shell.

### Negative Consequences

- The orchestrator lives in the UI process, coupling their lifecycles.

## Pros and Cons of the Options

### Multi-process FIFO + broker

- Good, because UI and worker are cleanly separated as processes.
- Bad, because FIFO/broker IPC is fragile (lifecycle, ordering, liveness) and the
  agent's shell differs from the run-block shell.

### In-process orchestration (MCP over a unix socket for tools)

- Good, because it is simpler and robust, removes IPC fragility, and gives
  shared-shell fidelity; the one remaining boundary (MCP over a socket) is a
  clean, standard protocol.
- Bad, because the orchestrator now lives inside the UI process.
