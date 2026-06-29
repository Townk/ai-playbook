# Structured Playbook — Phase B2: deterministic portability (no model adapt)

Status: agreed (2026-06-28). Sub-spec of `structured-playbook-output.md` (Phase B: B1 → B2 → B3). This is B2. B1 (escalate → structured + ProgressWidget) shipped.

## Goal

Replace the model-based **adapt-on-run** with **deterministic portability**: a
`project_bound` playbook is made portable at AUTHORING time (the model uses shell
variables for local resources; a safe pass variabilizes any leftover project/home
paths), and at RUN time the host just sets `$PROJECT_ROOT` — no model adapt pass.

## Background — why no model adapt

Today `adapt-on-run` (`runcmd.go` `adaptOnRun`/`liveAdapt` + `author/adapt.go`
`AdaptPrompt`) is a per-run model rewrite that re-specializes paths/versions/names
for the target dir. It is **blind** (`liveAdapt` has no tools — it guesses from the
target-dir string), slow (a model call per run), and the shaky part (versions/names)
never worked well. The load-bearing thing a relocated playbook needs is the
**directory**; everything else usually carries over or is the user's environment. So
B2 drops the model adapt entirely in favor of a deterministic, two-layer portability
model. (Versions/package-names/tooling are explicitly NOT adapted — accepted trade.)

## Design

### 1. Authoring prompt — portable by construction

Add to the structured authoring instruction (`internal/author/structured.go`
`StructuredToolInstruction`): reference machine/project-specific local resources via
**shell variables**, never hardcoded absolute paths —
- `$PROJECT_ROOT` for anything under the project directory (the host sets it at run),
- `$HOME` for home paths,
- standard tool variables (`$ANDROID_SDK_ROOT`, `$JAVA_HOME`, …) for SDK/tool
  locations,
and **declare** each non-standard variable the playbook relies on in `meta.env`
(name + why), so a reader on another machine knows what to set.

### 2. Schema — `meta.env` declaration

Add to `internal/playbook` `Meta`:
```go
Env []EnvVar `json:"env,omitempty" jsonschema:"environment variables the playbook relies on (local resources, secrets) — name + why; the host documents them in the front matter"`
// type EnvVar struct { Name string `json:"name"`; Why string `json:"why,omitempty"` }
```
The model lists required vars + a one-line why. This feeds the front matter even for
vars absent from the authoring shell (the existing `BuildEnv` value-scan only catches
vars present in the shell). `capturedMetaSeam` maps `meta.Env` → the orchestrator
`PlaybookMeta.EnvNotes` (name→why) it already passes to `BuildEnv`.

### 3. Safe pass — deterministic variabilization (`project_bound` only)

A new pure transform `playbook.Portabilize(pb *Playbook, projectRoot, home string)`
applied at the structured-author capture (the shared `structuredBody` path) when
`pb.Meta.ProjectBound`, BEFORE render/store:
- For each **runnable + verify** code block, replace, on path-component boundaries
  (prefix followed by `/`, quote, or end-of-token):
  1. the `projectRoot` prefix → `$PROJECT_ROOT` (do this FIRST — most specific),
  2. the `home` prefix → `$HOME` (after, so a project under home variabilizes the
     project, not just home).
- Ensure `PROJECT_ROOT` is declared in the front-matter env (the host adds it with a
  why like "the project directory; the host sets it to your project root at run").

`projectRoot` = the authoring context's project root (`req.ProjectRoot`); `home` =
`os.UserHomeDir`. Non-`project_bound` playbooks are untouched (a general how-to).

### 4. Run-time — set `$PROJECT_ROOT`, gate on `project_bound`, drop adapt

In the run path (`runcmd.go`):
- **`project_bound = false`** → render/run the stored playbook **as-is** (no adapt,
  no target resolution).
- **`project_bound = true`** → target = the **heuristic project root of cwd**
  (`capture.ProjectRoot` / `projectRootFn`); the run sets **`PROJECT_ROOT=<target>`**
  in the driver's environment before any block executes (so `$PROJECT_ROOT` resolves
  in the blocks). The other vars come from the user's shell (`$HOME`, `$ANDROID_SDK_ROOT`,
  …) or the front-matter values.
- **Delete** `adaptOnRun`/`liveAdapt` (the model adapt pass) and `author/adapt.go`
  `AdaptPrompt`. **Delete** `resolveTargetDir`'s stored-`workdir` branch + the
  target-dir float/ask. Stop writing `workdir` to the front matter (`project_bound`
  replaces it); old playbooks with a `workdir` field still parse (field ignored).
**Split: B2a vs B2b.** B2a (this plan) is the deterministic portability above — a
`project_bound` run **auto-sets `PROJECT_ROOT`** to the heuristic project root, no
prompt. The interactive **pre-run variable confirmation** (grouped confirm/customize
dialogs over `PROJECT_ROOT` + `meta.env`; balanced grouping
`ceil(N / ceil(N/5))`, `N==0` guard; export the edited values before blocks) is
**deferred to B2b** — its no-mux confirm/customize surface needs its own design pass
(`input.RunInline` is text-only, so it would render in-viewer-before-blocks via the
ask overlay, a new viewer phase).

## Removed / unchanged

- **Removed:** the per-run model adapt (`adaptOnRun`/`liveAdapt`/`AdaptPrompt`); the
  `workdir` front-matter write + the target-dir ask; the adapt inline-progress.
- **Unchanged:** Phase A schema/renderer/tool; B1's structured authoring + viewer;
  the front-matter `BuildEnv` machinery (now fed `meta.Env`); re-engagement (B3).

## Testing

- **`playbook.Portabilize`:** a block path under `projectRoot` → `$PROJECT_ROOT/…`;
  a home path → `$HOME/…`; a project-under-home path → `$PROJECT_ROOT/…` (project
  wins, ordering); a coincidental substring NOT on a boundary is left alone;
  non-`project_bound` pb is unchanged; `PROJECT_ROOT` declared in the result/env.
- **Schema/seam:** `meta.Env` round-trips; `capturedMetaSeam` maps `meta.Env` →
  `EnvNotes` → front-matter env (with why), even for a var absent from the shell.
- **Prompt:** the structured instruction names `$PROJECT_ROOT`, `$HOME`, and "declare
  … env".
- **Run gating:** a `project_bound=false` playbook runs with no target resolution + no
  adapt; a `project_bound=true` run sets `PROJECT_ROOT` in the driver to the heuristic
  project root and the blocks resolve it. The deleted adapt path has no callers.

## Out of scope

B3 (re-engagement → structured + collapse finalize); the viewer-UX-polish backlog;
the file-change-representation (`file=`/diff) spec.
