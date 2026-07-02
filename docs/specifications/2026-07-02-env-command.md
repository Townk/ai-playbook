# `ai-playbook env` — dump a playbook's env as a `--with-env` template (Design)

**Status:** approved (2026-07-02)

## Problem

`run --auto --with-env <JSON>` lets you supply a playbook's declared `env:`
values on the CLI, but you have to hand-write that JSON by reading the front
matter. There's no way to generate the object.

## Goal

A companion command that prints a playbook's declared env as a JSON object in
the exact shape `--with-env` consumes — resolving each value against the current
environment, and redacting sensitive values — so the round-trip is:
```
ai-playbook env --file playbook.md > env.json   # edit env.json …
ai-playbook run --auto --with-env env.json --file playbook.md
```

## Design

### Command
`ai-playbook env <source>`, mirroring `validate`'s source resolution:
- `env --file <path>` → a markdown file
- `env <slug>` (bare positional) → a stored playbook
- Exactly one source; zero or two → usage error (exit 2). Unreadable file /
  unknown slug → exit 2.

It parses the front matter and writes `fm.Env` as **pretty-printed JSON**
(2-space indent, keys sorted by `json.Marshal`'s map ordering, trailing newline)
to **stdout**; no declared env → `{}`; exit 0.

### Value resolution (per declared var `name`, default `ev.Value`)
1. `raw` = `os.Getenv(name)` when exported (non-empty), else `ev.Value`.
2. `frontmatter.Redact(name, raw)`:
   - **redacted** (name matches `(?i)(TOKEN|KEY|SECRET|PASS|CREDENTIAL)`, or the
     value is high-entropy per `looksLikeSecret`) → emit **`""`**.
   - **not redacted** → emit `raw` (the resolved value).
3. A declared default already equal to the `<redacted>` mask (masked at build
   time by value-entropy, env unset) → also **`""`** (new
   `frontmatter.IsRedactedMask`).

So non-sensitive vars carry their live environment value (falling back to the
declared default when unexported); sensitive vars come out empty — never leaking
a real secret into the generated file. `nonPortableEnv` vars (HOME/PATH/…) never
appear in `fm.Env` (skipped at build time), so no extra filtering is needed.

### Secret reporting
Emitting `""` (not the `<redacted>` literal) keeps the output round-trippable: an
empty value in `--with-env` falls through to the exported environment at run
time, so the real secret is provided once via `export` and never written to
`env.json`. When any var is redacted, `env` prints a single note to **stderr**
(stdout stays pure JSON):
```
env: redacted 2 sensitive variable(s): API_KEY, DB_PASSWORD
```
with the names sorted.

### Implementation
- `internal/frontmatter/frontmatter.go`: export `func IsRedactedMask(s string) bool`
  (`s == redactedMask`).
- `internal/launcher/envcmd.go` (new): `EnvMain()`, `resolveEnvArgs()` (mirrors
  `resolveValidateArgs`), and a pure `resolveEnvJSON(vars, getenv) (map[string]string, []string)`
  holding the resolve+redact logic (getenv injected for testing).
- `cmd/ai-playbook/main.go`: add `case "env": os.Exit(launcher.EnvMain())` and list
  `env` in `usage()`.
- Docs: a note in `examples/07-run-modes.md` showing the `env → edit → --with-env`
  round-trip.

## Non-goals
- No `why`/annotation in the output (flat string→string, round-trippable). An
  annotated variant is a possible later add.
- No `--auto`-style execution; `env` only reads and prints.
- No project-bind resolution — `env` reports the declaration resolved against the
  ambient environment, not a simulated run.

## Testing
- `internal/frontmatter`: `IsRedactedMask` true for `<redacted>`, false otherwise.
- `internal/launcher`: `resolveEnvJSON` — non-sensitive resolves to env value when
  set / default when unset; secret-by-name emits `""` even when exported (no
  leak); build-time-masked default (env unset) emits `""`; high-entropy env value
  on a non-secret name emits `""`; redacted-name list is sorted. `resolveEnvArgs`
  — `--file`, bare slug, zero-source error, two-source error. A `--file` smoke of
  `EnvMain` (capturing stdout) → valid JSON.

## Files
- `internal/frontmatter/frontmatter.go` (+ test)
- `internal/launcher/envcmd.go` (new, + test)
- `cmd/ai-playbook/main.go`
- `examples/07-run-modes.md`
