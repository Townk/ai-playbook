# Structured playbook output (design)

Status: agreed (2026-06-28). Brainstorm → spec.

## Problem

The authoring model emits playbook **markdown** directly and keeps drifting on
*structure*: a missing `# Playbook — <task>` H1, a conversational preamble, an
occasional block with no id. Prompt-hardening reduces this but cannot guarantee
it — the model owns the formatting. We have hit format-deviation failures
repeatedly. We want the model to supply **data** and have **us** render the
markdown, so the structure is deterministic and drift becomes impossible.

## Decision

The model returns a **structured playbook object** via a **`submit_playbook` MCP
tool** whose input JSON schema *is* the playbook. We drive the **claude CLI**
(not the Anthropic API), so `instructor-go` doesn't fit — but claude's **tool-use
loop** validates a tool call's arguments against the tool's `inputSchema` and
makes the model retry a malformed call, which *enforces* the shape. The agent
diagnoses as today (`run`/`ask`), then calls `submit_playbook(<structured>)` as
its FINAL action instead of writing markdown. Our backend validates the object
and a **deterministic renderer** emits the markdown into the existing
store/cache/viewer pipeline. Reuses our MCP framework (`internal/tools` +
`internal/mcpserver`).

Alternatives rejected: prompt-and-parse JSON (no hard schema enforcement; the
model can still drift); keep-markdown + a validator/repair loop (still parsing
free-form markdown — never fully eliminates drift).

## Schema (Hybrid: enforced skeleton + free-text prose)

```
Playbook {
  title:   string                 // → "# Playbook — <title>"
  intro?:  string                 // optional lead prose before the first section (markdown)
  sections: [ Section {
     heading: string              // → "## <heading>"
     content: [ ContentItem {     // ORDERED, heterogeneous flow (prose & code interleave)
        kind:      "text" | "callout" | "code"
        text?:     string         // kind=text|callout — literate markdown
        lang?:     string         // kind=code — bash|zsh|sh|python|diff|console|…
        code?:     string         // kind=code — block content
        id?:       string         // kind=code — we auto-assign when omitted
        needs?:    [string]       // kind=code — value-passing deps
        rollback?: string         // kind=code — rollback-for id
        static?:   bool           // kind=code — non-runnable (console/illustrative)
     } ]
  } ]
  verify?: { lang, code, needs?[] }  // the outcome-check (rendered last as {id=verify})
  meta: { name, description, category, tags[], project_bound }
}
```

A section's `content` is an ORDERED, heterogeneous list — pure-text sections
(scenario/goal/constraint), prose → code → *closing* prose, and callouts all fall
out naturally (real playbooks interleave freely; a rigid "step = prose + block"
did not fit). The **text/callout/intro fields stay free markdown** (narrative
quality preserved); everything structural — H1, `##` headings, callout framing,
fenced blocks, block tags, front matter — is ours to render, so the model cannot
mis-place it. `kind` is a flat discriminator (not a nested `oneOf`) for tool-use
reliability.

## Rendering (deterministic)

- `title` → `# Playbook — <title>`; `intro` → prose under it.
- each `section` → `## <heading>`, then its `content[]` IN ORDER:
  `text` → prose; `callout` → a `> ` note; `code` → a fenced block
  ```` ```<lang> {id=<id> needs=… rollback=… static} ````.
- `verify` → a final ```` {id=verify needs=…} ```` block.
- `meta` → the YAML front matter.
- We own id assignment (auto when omitted), uniqueness, and tag emission.

## Single authoring pass (drop the separate finalize model call)

Today two model passes run: **author** (diagnose + draft the working playbook)
and **finalize** (on `w` wrap-up, clean + generalize into the reusable saved
artifact). The schema removes the *format* reason for the second pass, so the
**structured authoring produces a final-quality, generalized, reusable playbook
directly**, and the `w` wrap-up just **persists the current structured object**
(render → store) — **no second model call**.

A structured *polish/generalize* pass is held **in reserve**: reintroduce it only
if Phase A shows a single pass yields terse or under-generalized playbooks. It is
a measured mitigation, not a default.

## Metadata folded in

The `meta` block (front-matter fields) is part of the same `submit_playbook`
call, so the **separate `PlaybookMetadata` model pass is dropped** — one model
round-trip instead of two.

`project_bound` (bool, model-supplied) replaces the stored `workdir` path and
gates adapt-on-run:

- **`false`** — the playbook is a general how-to; **skip adapt-on-run** and
  render as-is (nothing to specialize, faster).
- **`true`** — the playbook is specific to a project/working directory;
  adapt-on-run targets the **heuristic project root of the current working
  directory** (`capture.ProjectRoot` / `projectRootFn`). No stored path, no
  target-dir prompt — run a project-bound playbook from within the project you
  want it applied to.

This removes `resolveTargetDir`'s stored-workdir + ask-the-user branches entirely
(Phase B).

## Validation & retry

- **Schema-level:** claude's tool-use loop enforces the JSON schema (types,
  required fields) and retries a malformed `submit_playbook` call.
- **Semantic (ours):** after a valid call, check verify present (for a
  troubleshooting playbook), unique ids, ≥1 runnable block; on failure, re-ask
  the model with the specific errors. Bounded retries, then surface an error.

## Streaming / UX

Structured output is not streamable token-by-token (the tool call arrives when
the model finishes). So **create, adapt-on-run, AND escalate** all use the same
shape: **inline progress** (spinner + `Waiting…` + the model-activity line) while
the model works, then **render the complete playbook and open the viewer**. No
live "watch it build." (Confirmed acceptable — escalate already prints to the
activity line, not a stream.)

## Phasing

- **Phase A:** the Hybrid schema, the deterministic renderer, the
  `submit_playbook` tool, and schema+semantic validation/retry; migrate
  **`create`** (already collect-then-render → lowest risk). **Prove prose quality
  vs today's free-markdown output.**
- **Phase B** (after A validates): escalate-author, adapt-on-run (now gated on
  `project_bound`, targeting the heuristic project root — no stored workdir/prompt),
  and the re-engagement producers (regenerate / followup / proactive-amend).
  Collapse finalize to persist-only on `w`. Add the structured polish pass only if
  Phase A showed it is needed.

## Tradeoffs / risks

- **Prose quality** under a schema can be terser than free markdown; the
  free-text prose fields mitigate it — validated empirically in Phase A.
- **Tool-use reliability:** enforced by claude + our retry; the agent's
  `run`/`ask`/`remember` tools still work (the agent diagnoses, then submits).
- **Lost live streaming** for escalate — accepted; unified on inline progress.
- **Build cost:** schema + renderer + tool + migration across producers.

## Testing

- **Renderer:** structured object → expected markdown (H1, `##`, fenced blocks
  with tags, front matter) — golden cases incl. multi-section + a diff/static block.
- **`submit_playbook` tool:** schema validation; handler maps validated args → render.
- **Semantic validation:** verify-present, unique-ids, runnable-block; the re-ask
  path on a violation.
- **`create` end-to-end:** structured pass → render → store/cache/viewer; meta
  from the schema (no separate metadata pass); a prose-quality eyeball vs today.
