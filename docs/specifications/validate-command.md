# `ai-playbook validate` — structural + AI review (spec)

Status: proposed (2026-07-01). Implements the tutorial ch.08 ⏳ `validate` command
(`examples/08-the-store.md` "## Validate a playbook", `docs/guides/tutorial.md:187,212`) and
ROADMAP's "lint-able (`validate`)" line (`docs/ROADMAP.md:19,56,223-232`). Last release-gate
⏳ after the run modes (`--auto`/`--assisted`) shipped.

## Goal

`ai-playbook validate [<slug> | --file <path>]` — a CI-oriented linter for a playbook file:

- **Layer 1 — deterministic structural checks (always).** Front matter, `needs=` references,
  `needs=` cycles, duplicate block ids, and fence balance. Exit non-zero on any error.
- **Layer 2 — AI prose review (when the model backend is available; advisory).** A one-shot
  headless review of the playbook prose for inconsistencies, missing callouts, and
  non-idempotent/destructive steps. Printed as a summary; never affects the exit code.

Plain-text output (not the pager), so it drops into shell scripts and CI.

## Background — what exists (grounded)

- **Subcommand dispatch** is a hand-rolled `switch os.Args[1]` in `cmd/ai-playbook/main.go`
  (`case "run": os.Exit(launcher.RunMain())`, …, `default:` unknown→exit 2), plus a `usage()`
  block. Launcher commands (`internal/launcher/runcmd.go`, `storecmd.go`) parse `os.Args[2:]`
  with `flag.NewFlagSet(name, flag.ContinueOnError)` + `fs.StringVar(&file,"file",…)`, resolve a
  single source, and return int exit codes (2 usage / 1 runtime / 0 ok). `store.Load(slug)`
  (`storeLoadFn`, runcmd.go:42) resolves a `<slug>` to `(meta, body, err)`.
- **Front matter:** `frontmatter.Parse(content) (fm FrontMatter, body string, ok bool)`
  (`internal/frontmatter/frontmatter.go:257`). `ok` means "a leading `---`…`---` YAML fence
  parsed" — NOT "has required keys". `FrontMatter` (`frontmatter.go:213-223`): only `Name` is
  non-`omitempty`; there is **no required-field/schema validation anywhere** (verified: no
  `required`/schema check in `frontmatter` or `playbook`). The ch.08 example's required-key set
  (`name`, `description`, `category`, `created`) is validate's own rule to add.
- **Blocks:** `ui.Render(md, width, states, flashKey, …) ([]Line,[]Button,[]Block)`
  (`internal/ui/render.go:151`) is the only markdown→blocks parser; `autoRun` already calls it
  headlessly (`runcmd.go:210`). `Block` (`internal/ui/block.go:8-17`): `ID, Type, Lang, Needs,
  Static, File, Rollback, Payload`. `assignIDs` auto-fills `b1,b2,…` for id-less blocks.
  `internal/launcher` already imports `internal/ui`.
- **No existing `needs=`-existence, cycle, or fence-balance check.** `needsSatisfied`
  (`render.go:1234`) is runtime-scoped (has a dep *run*, not "does the id exist") and unexported.
  `normalizeFences` (`render.go:202`) silently *repairs* a malformed closing fence rather than
  reporting it — so fence-balance detection is net-new (a raw line scan; `normalizeFences`'
  fence-tracking is a reference). `playbook.Validate` (`internal/playbook/validate.go:13`) checks
  a *structured* `Playbook` (dup-id, per-block lang, ≥1 runnable, verify) — not a markdown file —
  so it's an intent reference, not reusable here. `ui.ValidatePlaybook(md) bool`
  (`internal/ui/validate.go:16`) = H1 + blocks>0 (reusable predicate, but coarse).
- **AI one-shot path:** `internal/author` has a non-streaming text→text call —
  `runMetadataOnce(systemPrompt, userMessage, opts) (string, error)` (`author/metadata.go:113`,
  drains `RunHarnessEvents`), with `ClassifyRequest` (`author/classify.go:157`) as a complete
  working example (sets `opts.Bare=true`, `MCPConfigPath=""` — no tools backend, `NoThinking=true`).
  Model resolution: `claudeModel()` (`author/author.go:81-89`) = `$ASSIST_MODEL` → `$AI_PLAYBOOK_MODEL`
  → `"sonnet"`. `config.Load()` never nil. Launcher already imports `internal/author`.
- **Graceful "no AI backend" pattern:** `looksLikeNoBackend(err)` (`internal/ui/results.go:14`,
  unexported) matches "executable file not found"/"no backend"/etc.; validate replicates it to
  skip the AI pass when the Claude CLI is absent/unauthenticated.

## 1. CLI + subcommand wiring

- `cmd/ai-playbook/main.go`: add `case "validate": os.Exit(launcher.ValidateMain())` + a `usage()`
  line `validate [<slug>|--file <path>]   structural + AI review of a playbook`.
- `internal/launcher/validatecmd.go` (new, mirrors `runcmd.go`): `func ValidateMain() int`.
  `resolveValidateArgs(os.Args[2:]) (kind, value string, noAI bool, err error)` with
  `--file <path>` / a bare `<slug>` (exactly one; both → usage error) and an optional
  `--no-ai` flag (skip Layer 2 even when a backend is available — useful for a fully
  deterministic CI check). Source resolution mirrors `resolveRunArgs`: `--file` reads the path;
  a `<slug>` resolves via `storeLoadFn` (`store.Load`) to its body. Returns exit `2` on a usage
  error, else runs the checks and returns `0`/`1`.

## 2. Layer 1 — deterministic structural checks

New leaf package **`internal/validate`** (pure; imports only `internal/frontmatter` + stdlib —
NOT `internal/ui`, so it stays a testable leaf and the launcher does the `ui.Render` parse and
hands validate a DTO). Public API:

```go
package validate

type Severity int
const ( Error Severity = iota; Warning )

type Finding struct {
    Severity Severity
    Check    string // "front-matter" | "needs" | "cycle" | "duplicate-id" | "fence" | "runnable" | "lang"
    Message  string
    Where    string // block id, or "line N", or "front matter" — human context
}

// Block is validate's DTO (the launcher converts ui.Block → this; validate never imports ui).
type Block struct { ID, Type, Lang string; Needs []string; Static bool }

// Check runs every deterministic check and returns findings (empty ⇔ structurally clean).
// rawBody is the front-matter-stripped markdown (for the fence scan); fmOK is frontmatter.Parse's ok.
func Check(rawBody string, fm frontmatter.FrontMatter, fmOK bool, blocks []Block) []Finding
```

Checks (each appends `Finding`s):

1. **Front matter (Error).** `!fmOK` → one Error `front-matter: missing or malformed front
   matter (a playbook needs a leading --- YAML block)`. Else, for each required key that is empty
   — `Name`, `Description`, `Category`, `Created` — an Error `front-matter: missing required key
   "<k>"`. (Tags/env/project_* stay optional.)
2. **Duplicate ids (Error).** Build `count[id]` over `blocks`; any id with count>1 → Error
   `duplicate-id: block id "<id>" is used <n> times`.
3. **`needs=` existence (Error).** `idSet := {b.ID}`; for each block, each `need` not in `idSet`
   → Error `needs: block "<id>" needs "<need>", which does not exist`.
4. **`needs=` cycles (Error).** DFS over the graph `id → Needs` (edges only to ids that exist);
   on a back-edge, one Error `cycle: needs= cycle: <a> → <b> → … → <a>` (report the cycle path).
   (ROADMAP calls this advisory; we make it an Error — a cyclic chain can never run.)
5. **Fence balance (Error).** Raw line scan of `rawBody` for ``` ``` ```/`~~~` code fences
   (CommonMark rule: an opening fence records its char+run-length+line; a line that is the same
   char, run-length ≥ the opener, and info-string-empty closes it). Any fence still open at EOF →
   Error `fence: unclosed code fence opened at line <N>`. (Front-matter `---` is not a code
   fence; the scan ignores it.)
6. **No runnable block (Warning).** If no block is actionable (all `Static`, or zero blocks) →
   Warning `runnable: no runnable blocks — nothing to execute`.
7. **Missing language (Warning).** For each non-static block with empty `Lang` → Warning
   `lang: block "<id>" has no language (syntax highlighting + type detection need one)`.

The launcher builds `blocks` by `ui.Render(body, 80, nil, "")` then converting each `ui.Block` →
`validate.Block` (`ID, Type, Lang, Needs, Static`).

## 3. Layer 2 — AI prose review (advisory)

- Skipped entirely when `--no-ai` is passed. Otherwise attempted once, after Layer 1.
- **One-shot call.** Add an exported helper in `internal/author`, e.g.
  `func ReviewOnce(systemPrompt, userMessage string) (string, error)` — a thin wrapper over
  `runMetadataOnce` with the `ClassifyRequest` option shape (`Bare=true`, `MCPConfigPath=""`,
  `NoThinking=true`) so it needs no MCP/tools backend. Model = `claudeModel()`
  (`$ASSIST_MODEL`→`$AI_PLAYBOOK_MODEL`→`sonnet`).
- **Prompt.** System prompt: a concise playbook reviewer — "review this ai-playbook for prose
  inconsistencies, missing/needed callouts, and steps that look non-idempotent, destructive, or
  non-reversible; be brief; if it looks good, say so." User message: the playbook body.
- **Graceful skip.** On an error matching the no-backend pattern (replicate `looksLikeNoBackend`
  in `internal/validate` or the launcher — check the error string for "executable file not
  found"/"no backend"/"not found"), print one line: `AI review skipped — no model backend
  (install + authenticate the Claude CLI, or set AI_PLAYBOOK_MODEL)` and continue. A non-backend
  error (e.g. a transient failure) prints `AI review failed: <err>` and is likewise advisory
  (does not change the exit code).

## 4. Output format + exit code

Plain text to stdout. Group in order: **errors**, **warnings**, **AI review**.

```
✗ 3 problems in examples/01-hello-run.md

  ERROR  front-matter  missing required key "created"                  (front matter)
  ERROR  needs         block "test" needs "biuld", which does not exist (block test)
  WARN   lang          block "notes" has no language                    (block notes)

  AI review:
  <the model's short summary, indented>
```

- Clean structural result: `✓ examples/01-hello-run.md: structurally valid` (+ the AI review
  block if run). Warnings alone still print `✓ … valid (N warnings)` — warnings do not fail.
- **Exit code:** `0` when no `Error` findings (warnings/AI advisory), `1` when ≥1 `Error`, `2`
  on a usage/IO error (bad flags, unreadable file, unknown slug). The AI review never changes the
  exit code.

## 5. Docs

- `examples/08-the-store.md` "## Validate a playbook": remove the `<!-- ⏳ … -->` marker; keep the
  three structural bullets; **fix the AI-pass wording** — it currently says "requires
  `AI_PLAYBOOK_MODEL` to be set", but the model resolves to `sonnet` by default, so reword to
  "runs when the Claude CLI backend is available (skipped with a note otherwise); set
  `AI_PLAYBOOK_MODEL` to pick the model". Mention `--no-ai` for a purely deterministic check.
- `docs/guides/tutorial.md`: drop the `validate ⏳` markers (features line `:187`, the "Read"
  line `:212`, coverage row `:269`) and the ch.08 "documented as if shipped" note `:190`.

## Components (decomposition)

- **`internal/validate`** (new leaf) — `Finding`/`Severity`/`Block` types + `Check(...)` with the
  seven deterministic checks. Pure; unit-tested in isolation.
- **`internal/author`** — add the exported `ReviewOnce` one-shot (wraps `runMetadataOnce`).
- **`internal/launcher/validatecmd.go`** (new) — `ValidateMain` + `resolveValidateArgs`; reads
  the source (file/slug), `frontmatter.Parse`, `ui.Render`→`[]validate.Block`, `validate.Check`,
  the AI pass (unless `--no-ai`, graceful skip), the report + exit code.
- **`cmd/ai-playbook/main.go`** — the `validate` dispatch case + `usage()` line.
- **Docs** — the ch.08 example + tutorial edits above.

## Testing

- **`internal/validate` (pure):** table tests for each check — missing/malformed front matter;
  each missing required key; duplicate ids; a dangling `needs=`; a `needs=` cycle (self-loop and
  a→b→a); an unclosed code fence (and a correctly-balanced one → no finding); no-runnable-block
  and missing-lang warnings; a fully-clean playbook → zero findings. Assert `Severity`/`Check`
  per finding, and that warnings-only yields no `Error`.
- **`internal/author`:** `ReviewOnce` returns the harness text on success and propagates a
  no-backend error unchanged (mirror the existing `ClassifyRequest`/`runMetadataOnce` test seams;
  no live model call).
- **`internal/launcher`:** `resolveValidateArgs` — `--file`, a bare `<slug>`, `--no-ai`, and the
  usage errors (zero/both sources). `ValidateMain` end-to-end via seams (a temp `.md` file): a
  clean playbook → exit 0; one with a dangling `needs=` → exit 1 with the finding printed; the AI
  pass stubbed via a seam (`var reviewFn = author.ReviewOnce`) so no live model call — assert it's
  invoked when a backend is present and skipped on a no-backend error and with `--no-ai`.

## Out of scope

- "Missing `{id=verify}`" and "mutating block without `{rollback}`" as **deterministic** checks —
  they are context-dependent (ch.01 legitimately has neither), so they're left to the advisory AI
  pass (ROADMAP routes idempotency/destructive/reversibility to the model anyway).
- A pager/TUI output mode (ROADMAP "open: pager vs plain") — validate is plain-text/CI by design.
- Validating the structured `playbook.Playbook` (that path is already gated at submit time by
  `playbook.Validate`); validate operates on markdown files only.
- Auto-fixing findings (report only).
