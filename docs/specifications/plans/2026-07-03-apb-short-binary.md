# `apb` short-name binary (Implementation Plan)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** ship `apb` as a first-class short alias of `ai-playbook` (same code, both binaries in releases, `go install …/cmd/apb`), name-aware in help & completion — by extracting the entrypoint into an importable `internal/cli` package.

**Tech Stack:** Go stdlib; goreleaser v2; existing `internal/climeta`, `internal/launcher`.

## Global Constraints

- Module `github.com/Townk/ai-playbook`. Repo at `~/Projects/langs/go/ai-playbook` (Bash cwd starts elsewhere — always `cd ~/Projects/langs/go/ai-playbook` / `git -C`).
- gpg-signed Conventional Commits (NEVER `--no-gpg-sign`; if signing times out STOP + report BLOCKED — user re-unlocks with `! echo x | gpg --clearsign`); verify `git log -1 "--format=%G?"` == `G`. NO `Co-Authored-By`/AI trailers. `git add` explicit paths. Commit only; do NOT push.
- Work on a NEW branch `feat/apb-binary` off `master`.
- Both binaries must behave IDENTICALLY; the only invocation-name effect is the `--help`/`help` program name.
- **Version ldflags is a hard coordination point:** moving `version` to `internal/cli.Version` changes the ldflags target — every build path (goreleaser + Makefile) must update, and a snapshot build must be verified to stamp the real version (not `dev`).
- Gates: `gofmt -l`, `go build ./...`, `go vet ./...`, `ineffassign`, `make lint` (golangci-lint — REQUIRED), `go test` on touched packages, and `make docs` idempotence for the regenerated completion.

---

### Task 1: extract the entrypoint into `internal/cli`

**Files:**
- Create: `internal/cli/` (move `cmd/ai-playbook/{main.go,finalize.go}` + their tests here, as `package cli`)
- Modify: `cmd/ai-playbook/main.go` (→ thin wrapper), `Makefile` (ldflags path)

**Interfaces (produced):** `func cli.Run(args []string) int`, `var cli.Version = "dev"`.

**Context (from inventory):** `cmd/ai-playbook/main.go` has `main()`, `helpFor`, `wantsHelp`, `usage`, `mcpMain`, `selftest`, `var version`; `finalize.go` has `finalize()`/`finalizeDoc()`; tests: `main_test.go`, `finalize_test.go`, `authoring_events_test.go`, `mcp_e2e_test.go`. `internal/cli` may import `internal/launcher`, `internal/diff`, `internal/input`, `internal/climeta` (no cycle — none import `cli`).

- [ ] **Step 1:** Create `internal/cli`. Move `main.go`'s non-`main` code into it as `package cli`: rename `func main()` → `func Run(args []string) int` — replace every `os.Exit(X)` with `return X`, and read the subcommand from `args[1:]` (was `os.Args[1:]`). Rename `var version` → `var Version`. Move `helpFor`, `wantsHelp`, `usage`, `mcpMain`, `selftest`, and `finalize.go`'s `finalize`/`finalizeDoc` into `internal/cli` (as `package cli`). Move the 4 test files, changing their package to `cli` and any `main`-package references accordingly.
- [ ] **Step 2:** `cmd/ai-playbook/main.go` becomes:
  ```go
  package main
  import (
      "os"
      "github.com/Townk/ai-playbook/internal/cli"
  )
  func main() { os.Exit(cli.Run(os.Args)) }
  ```
- [ ] **Step 3:** Update the ldflags target everywhere it appears for the LOCAL build: `Makefile` (any `-X main.version` → `-X github.com/Townk/ai-playbook/internal/cli.Version`). (goreleaser is Task 2.) Grep `-X main.version` / `main.version` across the repo and fix all.
- [ ] **Step 4: Verify** — `go build ./...`; `go vet ./...`; `gofmt -l`; **`make lint`**; `go test ./internal/cli/ ./...` (the moved tests pass in their new package; nothing else broke). Manually: `go run ./cmd/ai-playbook --help`, `... run --help`, `... version` all still work.
- [ ] **Step 5: Commit** — `git add internal/cli cmd/ai-playbook Makefile && git commit -m "refactor(cli): extract cmd/ai-playbook entrypoint into internal/cli (Run + Version)"`. Verify `%G?`==`G`. (Use `git add` on the specific moved/new paths; ensure the deleted `cmd/ai-playbook/{main.go-old,finalize.go}` removals are staged — `git add -A cmd/ai-playbook internal/cli` is acceptable here since the move spans deletes+adds, but do NOT `git add` unrelated paths.)

---

### Task 2: `cmd/apb` + goreleaser second build

**Files:**
- Create: `cmd/apb/main.go`
- Modify: `.goreleaser.yml`, `Makefile`

- [ ] **Step 1:** `cmd/apb/main.go` = the identical thin wrapper (`package main`; `func main() { os.Exit(cli.Run(os.Args)) }`).
- [ ] **Step 2:** `.goreleaser.yml`: add a second `builds` entry `id: apb`, `main: ./cmd/apb`, `binary: apb`, with the SAME `env`/`goos`/`goarch`/`mod_timestamp`, and `ldflags: -s -w -X github.com/Townk/ai-playbook/internal/cli.Version={{ .Version }}`. **Also update the EXISTING `ai-playbook` build's ldflags** to the new `internal/cli.Version` path (it still says `main.version`). Ensure the archive includes both build ids (goreleaser archives all builds by default; if the archive `builds:` is filtered, add `apb`).
- [ ] **Step 3:** `Makefile`: a build target produces both binaries (e.g. `go build -ldflags '-X …/internal/cli.Version=$(VERSION)' -o bin/ai-playbook ./cmd/ai-playbook` and `... -o bin/apb ./cmd/apb`).
- [ ] **Step 4: Verify** — `go build ./cmd/ai-playbook ./cmd/apb` clean; `go vet`; `make lint`. **`go run github.com/goreleaser/goreleaser/v2@latest release --snapshot --clean`** SUCCEEDS; `tar tzf dist/*linux_amd64.tar.gz` lists BOTH `ai-playbook` and `apb` (+ man pages + completion); and the **version is stamped**: extract the darwin/native binary from `dist/` and run `./<apb binary> --version` → shows the snapshot version (e.g. `0.5.0-SNAPSHOT-…`), NOT `dev`. Then `rm -rf dist`.
- [ ] **Step 5: Commit** — `git add cmd/apb .goreleaser.yml Makefile && git commit -m "feat(build): ship apb short-name binary (goreleaser + make)"`. Verify `%G?`==`G`.

---

### Task 3: name-aware help + completion for both names

**Files:**
- Modify: `internal/cli` (thread program name), `internal/climeta/{climeta.go,zsh.go}` (+ tests), `completions/_ai-playbook` (regenerated)

- [ ] **Step 1:** Make the help renderers name-aware. Change `climeta.Overview()`→`Overview(prog string)` and `Help(name)`→`Help(prog, name string)` (OR add a package-level `climeta.Prog` the entrypoint sets once — pick the lower-churn option and update ALL callers + tests + `cmd/docgen`, keeping man generation on the canonical `ai-playbook`). In `cli.Run`, compute `prog := filepath.Base(args[0])` and pass it to the help path so `apb --help` prints "apb". `usage()`'s error-path overview likewise uses `prog`.
- [ ] **Step 2:** `internal/climeta/zsh.go`: header → `#compdef ai-playbook apb`. The `_ai-playbook_slugs` helper must call the INVOKED binary, not a hardcoded `ai-playbook` — use the completed command name (e.g. `${words[1]}` or `$service`) in the `list --format fuzzy-data-source` call, so completion works when only `apb` is installed. Keep it `zsh -n`-valid.
- [ ] **Step 3:** Regenerate: `make docs` (updates `completions/_ai-playbook`); commit the regenerated file.
- [ ] **Step 4:** Tests — `climeta`: `Overview("apb")` contains "apb" and `Help("apb","run")` says `apb run …`; `Zsh()` header is `#compdef ai-playbook apb` and the slug helper uses the invoked-name variable (not a literal `ai-playbook list`). `cli`: `Run([]string{"apb","--help"})` returns 0 and its text mentions "apb"; `Run([]string{"ai-playbook","--help"})` mentions "ai-playbook". Update the existing climeta/help tests for the new signatures.
- [ ] **Step 5: Verify + Commit** — gates + `make lint` + `make docs && git diff --exit-code completions docs/man` (idempotent). Manually `go build -o /tmp/apb ./cmd/apb && /tmp/apb --help | head -1` shows "apb". `git add internal/cli internal/climeta completions cmd/docgen && git commit -m "feat(cli): name-aware help + completion for both ai-playbook and apb"`. Verify `%G?`==`G`.

---

### Task 4: docs

**Files:**
- Modify: `README.md`, `CHANGELOG.md`, `docs/BACKLOG.md`

- [ ] **Step 1:** README — near Install, note that the tool also installs as **`apb`** (a short alias; `go install …/cmd/apb@latest`, or use the `apb` binary from a release archive); all commands/flags/help are identical.
- [ ] **Step 2:** CHANGELOG `[Unreleased] → Added`: a short `apb` bullet (short-name binary, both shipped in releases, `apb --help` name-aware).
- [ ] **Step 3:** `docs/BACKLOG.md` — remove/close the "(small, cheap) Make `cmd/ai-playbook` `main` dispatch unit-testable" idea (now done via the `internal/cli` extraction) — note it resolved 2026-07-03.
- [ ] **Step 4: Commit** — `git add README.md CHANGELOG.md docs/BACKLOG.md && git commit -m "docs: document the apb short-name binary; close the dispatch-testability item"`. Verify `%G?`==`G`.

---

## Final verification (after all tasks)

- [ ] `cd ~/Projects/langs/go/ai-playbook && gofmt -l internal/cli internal/climeta cmd/ai-playbook cmd/apb cmd/docgen` empty; `go build ./... && go vet ./...` clean; `ineffassign` clean; `make lint` clean; `go test ./internal/cli/ ./internal/climeta/ ./cmd/...` PASS; `make docs && git diff --exit-code docs/man completions` clean.
- [ ] `go build -o /tmp/ai-playbook ./cmd/ai-playbook && go build -o /tmp/apb ./cmd/apb`: both run; `/tmp/apb --help | head -1` says "apb", `/tmp/ai-playbook --help | head -1` says "ai-playbook"; both dispatch a real command identically (e.g. `/tmp/apb list`).
- [ ] `goreleaser release --snapshot --clean` → every archive contains `ai-playbook` + `apb` + `docs/man/*.1` + `completions/_ai-playbook`; a built `apb --version` shows the stamped snapshot version (not `dev`).

## Self-review notes (coverage vs spec)

- Extraction → Task 1 (also closes the dispatch-testability backlog item). `cmd/apb` + goreleaser + ldflags → Task 2. Name-aware help + dual `#compdef` + invoked-name slug helper → Task 3. Docs → Task 4. Man pages stay canonical `ai-playbook`; both binaries behave identically; version-stamping explicitly verified.
