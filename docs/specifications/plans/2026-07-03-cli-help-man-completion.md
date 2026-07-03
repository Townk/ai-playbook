# CLI help, man pages, zsh completion (Implementation Plan)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** comprehensive `--help`, generated man pages, and a zsh completion script, all driven from one `internal/climeta` command-metadata registry. Keep the hand-rolled dispatch + per-command `flag.FlagSet` parsing.

**Architecture:** `internal/climeta` holds the registry (`[]Command`) + pure renderers (`Overview`, `Help`, `Man`, `Zsh`). `cmd/ai-playbook/main.go` uses it for `--help`/`-h`/`help [<cmd>]`. `cmd/docgen` imports it to generate `docs/man/*.1` + `completions/_ai-playbook` (committed, `//go:generate` + `make docs`). goreleaser packages the generated files.

**Tech Stack:** Go stdlib only (roff + zsh emitted as strings; no cobra/new deps).

## Global Constraints

- Module `github.com/Townk/ai-playbook`. Repo at `~/Projects/langs/go/ai-playbook` (Bash cwd starts elsewhere — always `cd ~/Projects/langs/go/ai-playbook` / `git -C`).
- gpg-signed Conventional Commits (NEVER `--no-gpg-sign`; if signing times out STOP + report BLOCKED — user re-unlocks with `! echo x | gpg --clearsign`); verify `git log -1 "--format=%G?"` == `G`. NO `Co-Authored-By`/AI trailers. `git add` explicit paths. Commit only; do NOT push.
- Work on a NEW branch `feat/cli-help-man-completion` off `master`.
- No new dependencies; no cobra migration; existing dispatch + `flag.FlagSet` parsing unchanged.
- **Registry data is copied VERBATIM from the code** — flag descriptions are the exact `fs.StringVar`/`fs.BoolVar`/`fs.IntVar` usage strings. The inventory in `docs/specifications/2026-07-03-cli-help-man-completion.md` names every source location; the implementer must confirm each against the actual code.
- Gates: `gofmt -l`, `go build ./...`, `go vet ./...`, `go run github.com/gordonklaus/ineffassign@v0.2.0 ./...`, `make lint` (golangci-lint — REQUIRED, it gates CI), `go test` on touched packages.

---

### Task 1: `internal/climeta` — registry + `Overview`/`Help`

**Files:**
- Create: `internal/climeta/climeta.go` (types + `Commands` var + `Overview`/`Help`), `internal/climeta/climeta_test.go`
- (Populate `Commands` from the sources listed below.)

**Interfaces (produced):**
```go
type Flag struct { Name, Placeholder, Desc string; Bool bool }
type Command struct {
    Name string; Aliases []string; Summary, Synopsis, Long, Args string
    Flags []Flag; Examples []string; Internal, SlugArg bool
}
var Commands []Command
func Overview() string
func Help(name string) (string, bool) // resolves aliases; false if unknown
func Lookup(name string) (Command, bool)
```

**Registry data — source of truth (read each, copy flags/descriptions verbatim):**
- **User-facing** (document fully — every flag, args, 1–3 examples): `assist` (no flags; `[<prompt>]`), `create` (`--template` reserved; `<prompt>`), `list` (`--format`), `search` (`--format`; `<query>`), `show` (`<slug>`, SlugArg), `edit` (`<slug>`, SlugArg), `run` (`resolveRunArgs` in `internal/launcher/runcmd.go` — `--playbook/--file/--auto/--assisted/--auto-rollback/--no-auto-rollback/--with-env`; `[<slug>]`, SlugArg; include the mode mutual-exclusion in Long), `validate` (`resolveValidateArgs` — `--file/--no-ai/--plain/--quiet`; `[<slug>]`, SlugArg), `env` (`resolveEnvArgs` — `--file`; `[<slug>]`, SlugArg).
- **Internal** (`Internal: true`; Summary + Synopsis + KEY flags only): `finalize` (`--dry-run`; `<file.md>`), `session` (`--request/--debug-log/--title`), `answer` (`--request/--content/--cached/--title/--cwd`), `mcp` (`--socket`), `diff` (`<patchfile>`), `input` (**do NOT enumerate the ~40 theme flags** — Summary "internal input widget", plus only `--type/--out/--measure`), `selftest`, `version`.
- Aliases: `assist` has `Aliases: ["troubleshoot"]`.

**Context:**
- `Overview()` prints: a one-line intro, then two groups (user-facing, then internal/advanced) with `Name` left-padded to a common width + `Summary`; ends with `Run 'ai-playbook <command> --help' for details.` Do NOT print internal-command flag detail in the overview.
- `Help(name)` resolves aliases via `Lookup`, then prints: `USAGE\n  <Synopsis>`, a blank line, `Long`, then `FLAGS` (each `  --name <ph>   desc`, aligned), then `EXAMPLES` (each `  <line>`). Omit empty sections.

- [ ] **Step 1: Failing tests** (`climeta_test.go`):
  - `TestOverview_ListsEveryCommandOnce`: `Overview()` contains each `Commands[i].Name` exactly once, contains "run", "validate", "env", and the details footer; user commands appear before internal ones.
  - `TestHelp_ResolvesAliasAndFlags`: `Help("troubleshoot")` returns ok and equals `Help("assist")`; `Help("run")` contains "--with-env" and its verbatim description substring and at least one example; `Help("nope")` returns ok=false.
  - `TestRegistry_NoEmptySummaries`: every command has a non-empty `Name` and `Summary`.

- [ ] **Step 2: Run to verify they fail.**
- [ ] **Step 3: Implement** the types, the `Commands` registry (populated verbatim from the sources above), and `Overview`/`Help`/`Lookup`.
- [ ] **Step 4: Run to verify they pass**; `go build ./...`; `go vet ./...`; `make lint`.
- [ ] **Step 5: Commit** — `git add internal/climeta/climeta.go internal/climeta/climeta_test.go && git commit -m "feat(climeta): CLI command registry + Overview/Help renderers"`. Verify `%G?`==`G`.

---

### Task 2: wire `--help` into `main.go` + drift guard

**Files:**
- Modify: `cmd/ai-playbook/main.go` (dispatch + replace `usage()`)
- Test: `cmd/ai-playbook/main_test.go` (or new), `internal/climeta/drift_test.go`

**Context:** `main()` switch (`cmd/ai-playbook/main.go`). Add, BEFORE the per-command dispatch:
- Top-level `-h`/`--help`/`help`: if a command name follows (`help run`, or `--help` alone), print `climeta.Help(cmd)` (or `climeta.Overview()` if none) to **stdout**, exit 0.
- For a subcommand invocation, if `wantsHelp(os.Args[2:])` (scans for a bare `-h`/`--help` token), print `climeta.Help(os.Args[1])` to stdout and exit 0 BEFORE calling `launcher.XMain()` — so no subcommand's `flag.FlagSet` ever sees `--help`.
- Replace `usage()`'s body with `fmt.Fprintln(os.Stderr, climeta.Overview())` for the error paths (no-args, unknown subcommand — keep exit 2); `--help`/`help` prints `climeta.Overview()` to **stdout** exit 0.

- [ ] **Step 1: Failing tests.**
  - `main_test.go` (using the existing `withArgs`-style harness if present, else `os.Args` save/restore + capture stdout): `ai-playbook --help` prints the overview to stdout and the process would exit 0; `ai-playbook help run` prints run's help (contains "--with-env"); `ai-playbook run --help` prints run's help (NOT a flag-parse error). Since `main()` calls `os.Exit`, extract the help-dispatch into a testable `helpFor(args []string) (text string, handled bool)` pure function and test THAT, keeping `main` a thin wrapper.
  - `internal/climeta/drift_test.go` — **the drift guard**: for each user-facing command, the set of flag names it actually parses must be ⊆ the registry's flags. Obtain the parsed flag names by calling the command's flagset constructor if exported, else by a small per-command `[]string` of expected flags defined IN the test (kept next to the resolver) — assert every one has a `climeta.Flag` with that `Name`. (Internal commands exempt.)

- [ ] **Step 2: Run to verify they fail.**
- [ ] **Step 3: Implement** `helpFor`/`wantsHelp`, wire them into `main()`, replace `usage()`.
- [ ] **Step 4: Verify** — tests; `go build`; `go vet`; `make lint`; and manually: `go run ./cmd/ai-playbook --help`, `... help run`, `... run --help`, `... env -h` all show real help.
- [ ] **Step 5: Commit** — `git add cmd/ai-playbook/main.go cmd/ai-playbook/main_test.go internal/climeta/drift_test.go && git commit -m "feat(cli): real --help and per-command help from the registry"`. Verify `%G?`==`G`.

---

### Task 3: man pages — `climeta.Man` + `cmd/docgen` + packaging

**Files:**
- Create: `internal/climeta/man.go` (+ test), `cmd/docgen/main.go`, `docs/man/*.1` (generated output, committed)
- Modify: `Makefile` (`docs` target + `//go:generate`), `.goreleaser.yml`

**Context:**
- `climeta.Man(c Command) string` emits roff: `.TH AI-PLAYBOOK-<CMD> 1`, `.SH NAME` (`ai-playbook-<cmd> \- <Summary>`), `.SH SYNOPSIS`, `.SH DESCRIPTION` (Long), `.SH OPTIONS` (each flag as `.TP` + `\fB--name\fR \fI<ph>\fR` + desc), `.SH EXAMPLES`, `.SH "SEE ALSO"` (`ai-playbook(1)`). `ManOverview()` emits `ai-playbook.1` listing all commands. Escape roff specials (leading `.`/`'`, backslash, hyphen `-`→`\-`). Generate a page for every non-internal command **plus `finalize`** (user-invokable).
- `cmd/docgen/main.go`: `climeta` → write `docs/man/ai-playbook.1` + `docs/man/ai-playbook-<cmd>.1`, and (Task 4) `completions/_ai-playbook`. Accept an out-dir arg (default repo-relative).
- `Makefile`: a `docs:` target = `go run ./cmd/docgen`. Add `//go:generate go run ./cmd/docgen` in `internal/climeta/climeta.go`.
- `.goreleaser.yml`: add to the `archives[0]` a `files:` list including `docs/man/*.1` (+ `completions/_ai-playbook`, `README.md`, `LICENSE`).

- [ ] **Step 1: Failing test** (`man_test.go`): `Man(runCmd)` starts with `.TH`, contains `.SH NAME`, `.SH SYNOPSIS`, `.SH OPTIONS`, the `--with-env` flag rendered as `\fB\-\-with\-env\fR`, and a `.SH "SEE ALSO"`; no un-escaped leading-dot content lines.
- [ ] **Step 2: Verify it fails.**
- [ ] **Step 3: Implement** `Man`/`ManOverview`, `cmd/docgen`, the Makefile target + go:generate, and run `make docs` to GENERATE + COMMIT `docs/man/*.1`. Add the goreleaser `files:`.
- [ ] **Step 4: Verify** — the test; `go run ./cmd/docgen` is idempotent (`make docs && git diff --exit-code docs/man`); `man docs/man/ai-playbook-run.1` renders without roff errors (or `groff -man -Tascii docs/man/ai-playbook-run.1 >/dev/null` clean); `goreleaser release --snapshot --clean` still succeeds and the archive contains the man pages (`tar tzf dist/*linux_amd64.tar.gz | grep '\.1'`).
- [ ] **Step 5: Commit** — `git add internal/climeta/man.go internal/climeta/man_test.go cmd/docgen/main.go docs/man Makefile .goreleaser.yml internal/climeta/climeta.go && git commit -m "feat(cli): generate man pages from the registry; package in releases"`. Verify `%G?`==`G`.

---

### Task 4: zsh completion — `climeta.Zsh` + dynamic slugs

**Files:**
- Create: `internal/climeta/zsh.go` (+ test), `completions/_ai-playbook` (generated, committed)
- Modify: `cmd/docgen/main.go` (also emit the completion), `.goreleaser.yml` (already includes it via Task 3's `files:` if listed there — else add), `README.md`

**Context:**
- `climeta.Zsh() string` emits a `#compdef ai-playbook` script: a top-level `_describe` of subcommands (Name + Summary), then a per-command `_arguments` listing its flags (`'--auto[run headless …]'` etc., descriptions from the registry, escaping `[`/`]`/`:`). For commands with `SlugArg`, the positional completes via a helper:
  ```zsh
  _ai-playbook_slugs() {
    local -a slugs
    slugs=(${(f)"$(ai-playbook list --format fuzzy-data-source 2>/dev/null | awk -F$'\x1f' '{print $2":"$1}')"})
    _describe -t playbooks 'playbook' slugs
  }
  ```
  (field 2 = slug value, field 1 = display description; `\x1f` is the US delimiter emitted by `list --format fuzzy-data-source`.) Wire `run`/`show`/`edit`/`validate`/`env` (and `run --playbook`) to it; `list`/`search` `--format` completes `human fuzzy-data-source json`.
- Internal commands: offer the subcommand name in the top-level list (Summary), but no flag/arg completion (keep the script lean).
- `cmd/docgen` writes `completions/_ai-playbook`.

- [ ] **Step 1: Failing test** (`zsh_test.go`): `Zsh()` starts with `#compdef ai-playbook`, contains every non-internal command name, contains the `_ai-playbook_slugs` helper and `list --format fuzzy-data-source`, and contains `--with-env` and `fuzzy-data-source` for `list`. Basic sanity: balanced quotes count is even (no obvious unterminated string).
- [ ] **Step 2: Verify it fails.**
- [ ] **Step 3: Implement** `Zsh`, extend `docgen`, run `make docs` to generate + commit `completions/_ai-playbook`. Ensure `.goreleaser.yml` archives include it.
- [ ] **Step 4: Verify** — the test; `make docs && git diff --exit-code completions`; lint the script with zsh if available (`zsh -n completions/_ai-playbook` — no syntax errors); snapshot archive contains it.
- [ ] **Step 5: Commit** — `git add internal/climeta/zsh.go internal/climeta/zsh_test.go cmd/docgen/main.go completions/_ai-playbook .goreleaser.yml README.md && git commit -m "feat(cli): generate zsh completion with dynamic store-slug completion"`. Verify `%G?`==`G`.

---

### Task 5: docs

**Files:**
- Modify: `README.md` (install man pages + completion), `CHANGELOG.md`

- [ ] **Step 1:** README — a short "Shell completion & man pages" section: from a release archive, copy `_ai-playbook` into an `fpath` dir (e.g. `~/.zsh/completions`, `autoload -U compinit`) and the `*.1` into a `man` dir (or `MANPATH`); note `ai-playbook <command> --help` for inline help.
- [ ] **Step 2:** CHANGELOG `[Unreleased] → Added`: comprehensive `--help` (top-level + per-command), generated man pages, and a zsh completion script with dynamic store-slug completion — all packaged in release archives.
- [ ] **Step 3:** Verify the README renders (fences intact).
- [ ] **Step 4: Commit** — `git add README.md CHANGELOG.md && git commit -m "docs: document --help, man pages, and shell completion"`. Verify `%G?`==`G`.

---

## Final verification (after all tasks)

- [ ] `cd ~/Projects/langs/go/ai-playbook && gofmt -l internal/climeta cmd/ai-playbook cmd/docgen` empty; `go build ./... && go vet ./...` clean; `ineffassign` clean; `make lint` clean; `go test ./internal/climeta/ ./cmd/...` PASS; `make docs && git diff --exit-code docs/man completions` (generated files are up to date).
- [ ] `go install ./cmd/ai-playbook`, then: `ai-playbook --help` (readable grouped overview), `ai-playbook help run` / `ai-playbook run --help` / `ai-playbook env -h` (real per-command help), `man docs/man/ai-playbook-run.1` renders, and (in a zsh with the completion sourced) `ai-playbook run <TAB>` offers store slugs.
- [ ] `goreleaser release --snapshot --clean` succeeds; a release archive contains `docs/man/*.1` and `completions/_ai-playbook`.

## Self-review notes (coverage vs spec)

- Registry + Overview/Help → Task 1. `--help` wiring + drift guard → Task 2. Man generation + packaging → Task 3. zsh completion + dynamic slugs → Task 4. Docs → Task 5. No cobra; `flag.FlagSet` parsing untouched; `input`'s theme flags intentionally not enumerated; generated files committed + release-packaged.
