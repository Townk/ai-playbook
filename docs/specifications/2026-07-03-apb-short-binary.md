# `apb` short-name binary (Design)

**Status:** approved (Option B, 2026-07-03) · infrastructure track

## Problem

`ai-playbook` is a long name to type. The user wants a first-class short alias,
`apb`, produced by the build and releases — same behavior, name-aware in help and
completion.

## Goal

A second binary, `apb`, built from the same code as `ai-playbook`:
- `go install github.com/Townk/ai-playbook/cmd/apb@latest` works.
- Release archives ship both `ai-playbook` and `apb`.
- `apb --help` reads "apb" (help uses the invocation name); completion works for
  both names.

## Design (Option B — extract to a shared package)

### 1. Extract the entrypoint → `internal/cli`
Today `cmd/ai-playbook` (package `main`) holds the dispatch plus local funcs
(`main`, `helpFor`, `wantsHelp`, `usage`, `mcpMain`, `selftest`, the `finalize`
subcommand in `finalize.go`) and `var version`. Move all of it into a new
importable package **`internal/cli`**, exposing:
```go
func Run(args []string) int   // args == os.Args; returns the process exit code
var Version = "dev"           // ldflags target moves here
```
`Run` does everything `main()` did (help interception, the dispatch switch,
`version`), returning an int instead of calling `os.Exit`. The moved test files
(`main_test.go`, `finalize_test.go`, `authoring_events_test.go`, `mcp_e2e_test.go`)
come along as `package cli`. `cmd/ai-playbook/main.go` becomes:
```go
func main() { os.Exit(cli.Run(os.Args)) }
```
**Bonus:** this makes the dispatch unit-testable (closes the BACKLOG "make
`cmd/ai-playbook` main dispatch unit-testable" item — `Run` can be driven in
tests without `os.Exit`).

### 2. `cmd/apb`
`cmd/apb/main.go` = the identical thin wrapper: `func main() { os.Exit(cli.Run(os.Args)) }`.

### 3. Version ldflags — the coordination point
`version` moves from `main` to `internal/cli.Version`, so the ldflags target
changes from `-X main.version=…` to
`-X github.com/Townk/ai-playbook/internal/cli.Version=…`. **Both** the
`.goreleaser.yml` builds and any `Makefile`/build ldflags MUST update, or
released binaries silently report `dev`. A snapshot build must be verified to
stamp the real version.

### 4. goreleaser — two builds
Add a second `builds` entry (`id: apb`, `main: ./cmd/apb`, `binary: apb`, same
env/goos/goarch/ldflags as `ai-playbook`). The archive includes both binaries
(each archive then carries `ai-playbook` + `apb` + the man pages + completion).
Accepted cost: the archive is ~2× the binary size; both are the same static Go
binary.

### 5. Name-aware help
`Run` derives the program name from `filepath.Base(args[0])` and threads it into
the help renderers so `apb --help` prints "apb" (e.g. `climeta.Overview(prog)` /
`climeta.Help(prog, name)`, or a settable `climeta.Prog` the entrypoint sets
once). Man pages stay canonically `ai-playbook`-named (they document the tool,
not the alias). The invocation-name change is confined to the interactive
`--help`/`help` output.

### 6. Completion — both names
The completion header becomes `#compdef ai-playbook apb`. The dynamic slug helper
must call **the invoked binary**, not a hardcoded `ai-playbook` (a user with only
`apb` on PATH would otherwise get no slugs) — use the completed command
(`$words[1]` / `$service`) to run `list --format fuzzy-data-source`. Regenerate
`completions/_ai-playbook` via `make docs`.

### 7. Makefile / docs
`make build` (or equivalent) builds both binaries with the correct ldflags. A
short README note that `apb` is the short alias.

## Non-goals
- No separate `apb`-named man pages (the tool's canonical name is `ai-playbook`;
  `man ai-playbook` covers both). A one-line "also installed as `apb`" mention in
  `ai-playbook(1)` is enough.
- No behavior difference between the two binaries whatsoever.
- No Homebrew formula (still deferred).

## Testing
- `internal/cli`: `Run` dispatches correctly (the moved tests pass unchanged in
  the new package); a new test that `Run([]string{"apb","--help"})` returns 0 and
  its help text says "apb", while `Run([]string{"ai-playbook","--help"})` says
  "ai-playbook".
- `climeta`: `Overview`/`Help` render with the passed program name.
- Completion: `Zsh()` header is `#compdef ai-playbook apb` and the slug helper
  uses the invoked command name; `zsh -n` clean.
- Build: `go build ./cmd/ai-playbook ./cmd/apb` clean; `goreleaser --snapshot`
  produces both binaries in each archive and stamps the version (`apb --version`
  in the built artifact shows the snapshot version, not `dev`).

## Files
- New: `internal/cli/*.go` (moved from `cmd/ai-playbook`), `cmd/apb/main.go`
- Modify: `cmd/ai-playbook/main.go` (→ thin wrapper), `internal/climeta/*` (prog
  name; completion header + slug helper), `completions/_ai-playbook` (regenerated),
  `.goreleaser.yml`, `Makefile`, `README.md`, `CHANGELOG.md`, `docs/BACKLOG.md`
  (close the dispatch-testability item)

## Tasks (SDD)
1. Extract `cmd/ai-playbook` entrypoint → `internal/cli` (`Run` + `Version`);
   thin `cmd/ai-playbook/main.go`; move tests; update ldflags path (Makefile).
2. `cmd/apb` + goreleaser second build + verify snapshot ships both + version
   stamping.
3. Name-aware help (`apb --help` reads "apb") + completion `#compdef ai-playbook
   apb` + invoked-name slug helper; regenerate completion.
4. Docs — README `apb` note + CHANGELOG + close the BACKLOG dispatch-testability item.
