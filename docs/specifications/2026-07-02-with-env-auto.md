# `--with-env` for `--auto` — pass variable values on the CLI (Design)

**Status:** approved (2026-07-02)

## Problem

A `--auto` (headless) run of a project-bound playbook resolves its declared
`env:` variables from `os.Getenv` → the front-matter `value:` default → else
errors as required-and-missing (`internal/autorun/run.go:resolveEnv`). The only
way to supply a value non-interactively is to `export` it into the environment
first. There is no way to hand values directly to a single invocation — awkward
for CI matrices, one-off overrides, or config-file-driven runs.

## Goal

Add `run --with-env <value>` (valid only with `--auto`) that supplies variable
values as inline JSON or a JSON file, taking precedence over the ambient
environment.

## Design

### Flag & value resolution
- `run --with-env <value>`, **valid only with `--auto`** — otherwise a usage
  error (exit 2), mirroring `--no-auto-rollback`'s auto-only rule.
- The trimmed value starting with `{` → parse as **inline JSON**; otherwise →
  treat as a **file path**, read it, parse its contents as JSON.
- The JSON must be an **object of string→string**. Malformed JSON, a non-string
  value, or an unreadable file → **usage error, exit 2**.
- `{}` (empty object) is valid — no overrides.

### Per-variable precedence
For each front-matter-declared `env:` variable:
```
--with-env[name] (non-empty)  →  exported $name  →  front-matter value:  →  missing-required (exit 1)
```
`--with-env` beats an exported variable — an explicit CLI value is stronger
intent than the ambient environment. An **empty-string** override
(`{"FOO":""}`) is treated as *not provided* and falls through to the exported
value / default / missing, matching the existing `"" == missing` rule.

### Unknown keys
A `--with-env` key not declared in the playbook's `env:` map is **warned and
ignored** — `with-env: ignoring undeclared variable NAME` on the run's stdout
(the same stream as the existing `missing required env:` message), keys in
sorted order for deterministic output. Never an error.

### Implementation
- **`internal/autorun/run.go`**: `RunConfig` gains `EnvOverrides map[string]string`.
  `resolveEnv(vars, overrides)` applies the new precedence; an override for an
  already-exported variable is appended to the env slice so it wins by
  env last-wins semantics. `Run` prints the sorted undeclared-key warnings
  before resolving, then calls `resolveEnv(rc.EnvVars, rc.EnvOverrides)`.
- **`internal/launcher/runcmd.go`**: `runArgs` gains `EnvOverrides map[string]string`.
  `resolveRunArgs` registers the `--with-env` string flag, enforces the
  `--auto`-only rule, and resolves the value via a new `parseWithEnv(raw)`
  helper (inline-vs-file + JSON decode; errors bubble up as the exit-2 usage
  errors). `autoRun` forwards `ra.EnvOverrides` into `RunConfig.EnvOverrides`.

### Docs
`examples/07-run-modes.md` (and the ch.06 CI note) currently say auto mode
"requires the variables already set in the environment." Add a short note that
`--with-env` supplies them inline (JSON string or file path) without exporting.

## Non-goals
- No dotenv/YAML formats (JSON only). No `--with-env` for the interactive or
  `--assisted` modes (they have the confirmation gate). No merging of multiple
  `--with-env` flags (last one wins per stdlib `flag`; not specified further).

## Testing
- `internal/autorun`: `resolveEnv` precedence — override beats exported + default;
  empty override falls through; override wins over an exported var in the emitted
  env slice; missing still reported. `Run` prints a sorted warning for an
  undeclared override key and applies a declared one.
- `internal/launcher`: `parseWithEnv` — inline JSON, file path, malformed JSON,
  non-string value, unreadable file. `resolveRunArgs` — `--with-env` without
  `--auto` errors; with `--auto` + inline JSON sets `EnvOverrides`; bad JSON
  surfaces as the exit-2 error.

## Files
- `internal/autorun/run.go` (+ `run_test.go`)
- `internal/launcher/runcmd.go` (+ `runcmd_test.go`)
- `examples/07-run-modes.md`, `examples/06-portable-and-env.md`
