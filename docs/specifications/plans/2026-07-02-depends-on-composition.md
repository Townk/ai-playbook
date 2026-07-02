# `depends_on` — playbook composition (Implementation Plan)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `depends_on: [slug, …]` front matter runs a playbook's transitive dependencies (store slugs, topological order, cycle/dangling detection) headless before the parent; `--with-env` and `env` span the whole chain.

**Architecture:** A pure `analyzeDeps` (DFS mirroring `validate.detectCycles`) over the slug graph, fed by a store-backed loader, produces the run-ordered dependency list. `RunMain`/`autoRun` run the dependencies (via `autorun.Run`) before the parent; `env` and `validate` consume the same resolver.

**Tech Stack:** Go; `internal/frontmatter`, `internal/store`, `internal/autorun`, `internal/launcher`, `internal/validate`.

## Global Constraints

- Module `github.com/Townk/ai-playbook`. Repo at `~/Projects/langs/go/ai-playbook` (Bash cwd starts elsewhere — always `cd ~/Projects/langs/go/ai-playbook` / `git -C`).
- gpg-signed Conventional Commits (NEVER `--no-gpg-sign`; if signing times out STOP + report BLOCKED — user re-unlocks with `! echo x | gpg --clearsign`); verify `git log -1 "--format=%G?"` == `G` (quote the format — zsh globs a bare `%G?`). NO `Co-Authored-By`/AI-attribution trailers. `git add` explicit paths only. Commit only; do NOT push.
- Work on a NEW branch `feat/depends-on` off `master`.
- Dependencies are **store slugs** (global or `proj:`); a dependency is never a file path. A `--file` parent MAY declare dependency slugs. Transitive.
- Dependencies always run **headless** (`autorun.Run`) before the parent, in every parent mode; a dependency's `AutoRollback` = `!ra.NoAutoRollback`. **First dependency failure aborts the chain** (parent never runs; exit with that code). Cycle / dangling slug → **exit 2 before anything runs**.
- `--with-env` spans the chain (shared override map to every playbook's `resolveEnv`); the undeclared-key warning is emitted **once against the union** of all declared vars in the chain; `--with-env` stays `--auto`-only.
- Gates: `gofmt -l`, `go build ./...`, `go vet ./...`, `ineffassign@v0.2.0` (clean), `go test` on touched packages.

---

### Task 1: schema plumbing — `depends_on` on front matter, store, finalize

**Files:**
- Modify: `internal/frontmatter/frontmatter.go`, `internal/store/store.go`, `cmd/ai-playbook/finalize.go`
- Test: `internal/frontmatter/frontmatter_test.go`, `internal/store/store_test.go` (or existing), `cmd/ai-playbook/finalize_test.go` (or existing)

**Interfaces (produced, consumed by later tasks):**
- `frontmatter.FrontMatter.DependsOn []string`
- `store.Meta.DependsOn []string`

- [ ] **Step 1: Failing tests.**
  - `frontmatter`: a playbook with `depends_on:\n  - a\n  - b` round-trips through `Parse` (`fm.DependsOn == ["a","b"]`) and `Assemble(fm)` re-emits a `depends_on:` block (assert `strings.Contains(Assemble(fm), "depends_on")` and a Parse-of-Assemble round-trip preserves the slice).
  - `store`: `metaFromFM` copies `DependsOn` (build a `frontmatter.FrontMatter{DependsOn: []string{"x"}}`, assert the returned `Meta.DependsOn == ["x"]`).
  - `finalize`: `finalizeDoc` preserves an existing `depends_on`. Feed a `raw` playbook whose front matter has `depends_on: [dep-a]`; assert the returned `full` still contains `depends_on` / `dep-a` (mirror the existing finalize test's construction; `metaFn`/`lookup` can be the test's existing stubs).

- [ ] **Step 2: Run to verify they fail.**

- [ ] **Step 3: Implement.**
  - `internal/frontmatter/frontmatter.go`: add to `FrontMatter` (after `Tags`):
    ```go
    DependsOn    []string            `yaml:"depends_on,omitempty"`
    ```
  - `internal/store/store.go`: add to `Meta` (after `Tags` or near `Env`):
    ```go
    DependsOn []string
    ```
    and in `metaFromFM`, add to the `Meta{…}` literal:
    ```go
    DependsOn:    fm.DependsOn,
    ```
  - `cmd/ai-playbook/finalize.go` `finalizeDoc`: capture the old front matter and carry `DependsOn` forward. Change `_, body, _ := frontmatter.Parse(raw)` to `old, body, _ := frontmatter.Parse(raw)` and add `DependsOn: old.DependsOn,` to the rebuilt `frontmatter.FrontMatter{…}` literal.

- [ ] **Step 4: Run to verify they pass**; `go build ./...`; `go vet ./...`.

- [ ] **Step 5: Commit** —
  ```bash
  cd ~/Projects/langs/go/ai-playbook && git add internal/frontmatter/frontmatter.go internal/frontmatter/frontmatter_test.go internal/store/store.go internal/store/store_test.go cmd/ai-playbook/finalize.go cmd/ai-playbook/finalize_test.go && git commit -m "feat(frontmatter): depends_on field — front matter, store.Meta, finalize preservation"
  ```
  (Stage only the test files you actually touched.) Verify `%G?` == `G`.

---

### Task 2: pure dependency-graph core + shared helpers

**Files:**
- Create: `internal/launcher/deps.go`
- Modify: `internal/launcher/runcmd.go` (extract `blocksFor`), `internal/autorun/run.go` (`RunConfig.SuppressUndeclaredWarning`)
- Test: `internal/launcher/deps_test.go`

**Interfaces (produced):**
- `type depNode struct { Slug string; FM frontmatter.FrontMatter; Body string; Cwd string }`
- `type DepIssue struct { Kind string; Slug string; Path []string }` — `Kind` is `"dangling"` (with `Slug`) or `"cycle"` (with `Path`).
- `func analyzeDeps(rootDeps []string, load func(slug string) (depNode, error)) (order []depNode, issues []DepIssue)` — `order` is the dependencies in run order (post-order: a dependency precedes anything that needs it; the parent is NOT included); `issues` collects ALL dangling slugs and ALL distinct cycles.
- `func blocksFor(body string) []autorun.Block` (extracted from `autoRun`).
- `autorun.RunConfig.SuppressUndeclaredWarning bool`.

**Context:** mirror `validate.detectCycles` (3-color DFS, `unvisited=0/inStack=1/done=2`, back-edge → cycle path via the DFS stack, `cycleKey` dedup). `analyzeDeps` additionally collects each visited node into `order` in **post-order** so dependencies run before their dependents, and treats a `load` error as a `"dangling"` issue (marking that slug done so it is reported once).

- [ ] **Step 1: Failing tests** (`deps_test.go`) using a fake in-memory `load` (a `map[string][]string` slug→depends_on; unknown slug → error):
  ```go
  func fakeLoader(graph map[string][]string) func(string) (depNode, error) {
      return func(slug string) (depNode, error) {
          deps, ok := graph[slug]
          if !ok {
              return depNode{}, fmt.Errorf("no playbook for slug %q", slug)
          }
          return depNode{Slug: slug, FM: frontmatter.FrontMatter{DependsOn: deps}}, nil
      }
  }

  func slugs(nodes []depNode) []string {
      out := make([]string, len(nodes))
      for i, n := range nodes { out[i] = n.Slug }
      return out
  }

  func TestAnalyzeDeps_LinearOrder(t *testing.T) {
      // parent → a → b ; run order must be b, a
      g := map[string][]string{"a": {"b"}, "b": {}}
      order, issues := analyzeDeps([]string{"a"}, fakeLoader(g))
      if len(issues) != 0 { t.Fatalf("issues: %v", issues) }
      if got := slugs(order); !reflect.DeepEqual(got, []string{"b", "a"}) {
          t.Fatalf("order = %v, want [b a]", got)
      }
  }

  func TestAnalyzeDeps_DiamondDedup(t *testing.T) {
      // a→b, a→c, b→d, c→d : d appears once, before b and c; a last
      g := map[string][]string{"a": {"b", "c"}, "b": {"d"}, "c": {"d"}, "d": {}}
      order, issues := analyzeDeps([]string{"a"}, fakeLoader(g))
      if len(issues) != 0 { t.Fatalf("issues: %v", issues) }
      got := slugs(order)
      if len(got) != 4 { t.Fatalf("want 4 unique nodes, got %v", got) }
      pos := map[string]int{}
      for i, s := range got { pos[s] = i }
      if !(pos["d"] < pos["b"] && pos["d"] < pos["c"] && pos["b"] < pos["a"] && pos["c"] < pos["a"]) {
          t.Fatalf("bad topo order: %v", got)
      }
  }

  func TestAnalyzeDeps_Cycle(t *testing.T) {
      g := map[string][]string{"a": {"b"}, "b": {"a"}}
      _, issues := analyzeDeps([]string{"a"}, fakeLoader(g))
      var cycles int
      for _, is := range issues { if is.Kind == "cycle" { cycles++ } }
      if cycles != 1 { t.Fatalf("want exactly 1 cycle issue, got %v", issues) }
  }

  func TestAnalyzeDeps_Dangling(t *testing.T) {
      g := map[string][]string{"a": {"ghost"}}
      _, issues := analyzeDeps([]string{"a"}, fakeLoader(g))
      var dangling int
      for _, is := range issues { if is.Kind == "dangling" && is.Slug == "ghost" { dangling++ } }
      if dangling != 1 { t.Fatalf("want dangling ghost, got %v", issues) }
  }
  ```

- [ ] **Step 2: Run to verify they fail.**

- [ ] **Step 3: Implement.**
  - `internal/autorun/run.go`: add `SuppressUndeclaredWarning bool` to `RunConfig` (after `EnvOverrides`), and in `Run` gate the warning:
    ```go
    if !rc.SuppressUndeclaredWarning {
        warnUndeclared(out, rc.EnvVars, rc.EnvOverrides)
    }
    ```
  - `internal/launcher/runcmd.go`: extract the inline `ui.Render`→`autorun.Block` loop from `autoRun` into a reusable func, and call it from `autoRun`:
    ```go
    // blocksFor renders a playbook body and converts it to autorun.Block, the
    // headless-run representation (shared by --auto and the depends_on runner).
    func blocksFor(body string) []autorun.Block {
        _, _, uiBlocks := ui.Render(body, 80, nil, "")
        blocks := make([]autorun.Block, 0, len(uiBlocks))
        for _, b := range uiBlocks {
            blocks = append(blocks, autorun.Block{
                ID: b.ID, Command: b.Payload, Needs: b.Needs,
                Rollback: b.Rollback, Static: b.Static, Kind: kindFromType(b.Type),
            })
        }
        return blocks
    }
    ```
  - `internal/launcher/deps.go`: `depNode`, `DepIssue`, `analyzeDeps` (+ private `cyclePathFrom(stack []string, back string) []string` and `cycleKey(path []string) string` mirroring `internal/validate/validate.go`'s `detectCycles`/`cycleKey`). Post-order append into `order`; on `load` error append a `dangling` issue and mark the slug done.

- [ ] **Step 4: Run to verify they pass**; `go test ./internal/launcher/ ./internal/autorun/`; `go build ./...`; `go vet ./...`.

- [ ] **Step 5: Commit** —
  ```bash
  cd ~/Projects/langs/go/ai-playbook && git add internal/launcher/deps.go internal/launcher/deps_test.go internal/launcher/runcmd.go internal/autorun/run.go && git commit -m "feat(launcher): pure dependency-graph analysis (topo + cycle/dangling) + shared blocksFor"
  ```
  Verify `%G?` == `G`.

---

### Task 3: store-backed resolver + chain execution wiring

**Files:**
- Modify: `internal/launcher/deps.go` (loader, `resolveChain`, `runDeps`, `unionDeclared`, issue printing), `internal/launcher/runcmd.go` (`loadParent`, `RunMain`/`autoRun` wiring)
- Test: `internal/launcher/deps_test.go`, `internal/launcher/runcmd_test.go`

**Interfaces (produced, consumed by Task 4):**
- `func loadDepNode(slug string) (depNode, error)` — store-backed loader for `analyzeDeps`.
- `func resolveChain(rootDeps []string) (order []depNode, issues []DepIssue)` — `analyzeDeps(rootDeps, loadDepNode)`.
- `func loadParent(ra runArgs) (depNode, error)` — resolves the root playbook (file or slug) to a `depNode` (full FM + body + cwd).

**Context (integration facts):**
- `RunMain` (`runcmd.go:74-103`): after `resolveRunArgs` succeeds and before the `ra.Mode == modeAuto` fork is the uniform injection point.
- `autoRun` (`runcmd.go:164-233`): the headless path. Its `"playbook"` branch currently does NOT `frontmatter.Parse` (so a stored parent's `fm.Env` is empty — a pre-existing bug). Route `autoRun`'s parent load through `loadParent` so the parent gets its full FM (this incidentally fixes that env bug) AND its `DependsOn`.
- Store loader: `store.PathFor(slug) (string, bool)` (existence + path, no parse; add a `storePathForFn = store.PathFor` seam next to `storeLoadFn`), then `os.ReadFile(path)` + `frontmatter.Parse`. `cwd` = `resolveProjectRoot(fm.ProjectRoot)` when `fm.ProjectBound` else `""`. Unknown slug (`!ok`) → an error (→ `analyzeDeps` records a `dangling` issue).
- `os.Setenv("PROJECT_ROOT", cwd)` per project-bound playbook before its `Run` (matches `autoRun`), so each playbook in the chain anchors correctly.
- `autorunRunFn` is the `autorun.Run` seam (spy it in tests to assert order / abort / the suppression flag).
- `cfg.Driver.Shell` (from `config.Load()`) supplies `RunConfig.Shell`.

- [ ] **Step 1: Failing tests** (`runcmd_test.go` / `deps_test.go`), spying `autorunRunFn` and `storePathForFn`/`storeLoadFn`:
  - **Order + abort:** a parent with deps `[a]`, `a` deps `[b]`; a spied `autorunRunFn` records the `Slug` of each call and returns 0. Drive the chain (via the exported entry you wire, e.g. a testable `runChain(parent, order, ra, cfg)`); assert the recorded order is `b, a, <parent>`. Then make the spy return non-zero for `b`; assert `a` and the parent are NEVER invoked and the returned code is `b`'s.
  - **Suppression + union warning:** with `ra.Mode == modeAuto` and `EnvOverrides{"ONLY_DEP":"x","GHOST":"y"}` where `b` declares `ONLY_DEP` and the parent declares neither: assert every spied `RunConfig` has `SuppressUndeclaredWarning == true`, and the single union warning names `GHOST` but NOT `ONLY_DEP` (capture stdout).
  - **Issues → exit 2:** a parent whose dep is dangling (spied loader errors) → the chain entry returns 2 and `autorunRunFn` is never called.

- [ ] **Step 2: Run to verify they fail.**

- [ ] **Step 3: Implement.**
  - `internal/launcher/deps.go`:
    - `loadDepNode(slug)` (using `storePathForFn` + `os.ReadFile` + `frontmatter.Parse` + cwd). Wrap a not-found as an error carrying the slug.
    - `resolveChain(rootDeps)` = `analyzeDeps(rootDeps, loadDepNode)`.
    - `runDeps(nodes []depNode, overrides map[string]string, autoRollback bool, shell string, out io.Writer) int` — for each node: print `fmt.Fprintf(out, "\n→ dependency: %s\n", node.Slug)`; if `node.FM.ProjectBound` `os.Setenv("PROJECT_ROOT", node.Cwd)`; `code := autorunRunFn(autorun.RunConfig{Blocks: blocksFor(node.Body), EnvVars: node.FM.Env, EnvOverrides: overrides, Cwd: node.Cwd, Shell: shell, Slug: node.Slug, AutoRollback: autoRollback, SuppressUndeclaredWarning: true, Out: out})`; `if code != 0 { return code }`. Return 0.
    - `unionDeclared(nodes ...[]…)` helper: build `map[string]frontmatter.EnvValue` union of every node's `FM.Env` (used for the single `warnUndeclared`). Provide a form that takes the parent FM + the deps.
    - `printDepIssues(w io.Writer, issues []DepIssue)`: one line per issue — dangling: `ai-playbook: dependency %q not found in the store`; cycle: `ai-playbook: depends_on cycle: %s` with `strings.Join(path, " → ")`.
  - `internal/launcher/runcmd.go`:
    - `loadParent(ra runArgs) (depNode, error)`: `"file"` → `os.ReadFile`+`Parse`+cwd (reuse `runFile`'s cwd logic / `resolveProjectRoot`); `"playbook"` → `storeLoadFn` for existence (map its error), then read `meta.Path`+`Parse` for the full FM; `Slug` = the slug (or `""` for a file).
    - **`RunMain` wiring** — after `resolveRunArgs` succeeds:
      ```go
      parent, perr := loadParent(ra)
      if perr != nil { fmt.Fprintf(os.Stderr, "ai-playbook run: %v\n", perr); return 2 }
      if len(parent.FM.DependsOn) > 0 {
          order, issues := resolveChain(parent.FM.DependsOn)
          if len(issues) > 0 { printDepIssues(os.Stderr, issues); return 2 }
          if ra.Mode != modeAuto {
              // interactive/assisted parent: deps headless first (no --with-env), then the viewer
              if code := runDeps(order, nil, true, cfg.Driver.Shell, os.Stdout); code != 0 { return code }
          }
          // For modeAuto, autoRun runs the whole chain (deps + parent) itself — see below.
      }
      ```
      (For `modeAuto`, keep the existing `return autoRun(ra)`; `autoRun` owns the chain so the union warning sees both parent and deps.)
    - **`autoRun` wiring** — load the parent via `loadParent(ra)` (replacing the inline branch's fm/body/cwd derivation). If `parent.FM.DependsOn` is empty, run exactly the single-playbook path as today (but sourced from `parent`). If non-empty:
      ```go
      order, issues := resolveChain(parent.FM.DependsOn)
      if len(issues) > 0 { printDepIssues(os.Stderr, issues); return 2 }
      union := unionDeclared(parent.FM, order)          // parent + deps declared vars
      warnUndeclaredLauncher(os.Stdout, union, ra.EnvOverrides) // the SINGLE chain warning
      if code := runDeps(order, ra.EnvOverrides, !ra.NoAutoRollback, cfg.Driver.Shell, os.Stdout); code != 0 { return code }
      // then the parent, headless, suppressed:
      if parent.FM.ProjectBound { os.Setenv("PROJECT_ROOT", parent.Cwd) }
      return autorunRunFn(autorun.RunConfig{
          Blocks: blocksFor(parent.Body), EnvVars: parent.FM.Env, EnvOverrides: ra.EnvOverrides,
          Cwd: parent.Cwd, Shell: cfg.Driver.Shell, Slug: parent.Slug,
          AutoRollback: !ra.NoAutoRollback, SuppressUndeclaredWarning: true, Out: os.Stdout,
      })
      ```
      (`warnUndeclaredLauncher` is a launcher-side wrapper that calls into the same logic as `autorun.warnUndeclared`; since `warnUndeclared` is unexported in `autorun`, either export a thin `autorun.WarnUndeclared(out, vars, overrides)` or replicate the tiny sorted-warning loop in the launcher. Prefer exporting `autorun.WarnUndeclared` to keep one implementation.)
    - Keep the existing single-playbook `autoRun` behavior byte-for-byte when there are no deps (so the no-dep path and its tests are unchanged) — the cleanest structure is: `loadParent` → if no deps, build the one `RunConfig` and `return autorunRunFn(…)` with `SuppressUndeclaredWarning: false` (today's warning); if deps, the chain block above.

- [ ] **Step 4: Run to verify they pass**; `go test ./internal/launcher/`; `go build ./...`; `go vet ./...`; `gofmt -l internal/launcher internal/autorun`.

- [ ] **Step 5: Commit** —
  ```bash
  cd ~/Projects/langs/go/ai-playbook && git add internal/launcher/deps.go internal/launcher/deps_test.go internal/launcher/runcmd.go internal/launcher/runcmd_test.go internal/autorun/run.go && git commit -m "feat(launcher): run depends_on chain headless before the parent (--with-env spans the chain)"
  ```
  (Stage `internal/autorun/run.go` only if you export `WarnUndeclared`.) Verify `%G?` == `G`.

---

### Task 4: `env` traverses the chain + `validate` depends_on checks

**Files:**
- Modify: `internal/launcher/envcmd.go`, `internal/launcher/validatecmd.go`
- Test: `internal/launcher/envcmd_test.go`, `internal/launcher/validatecmd_test.go`

**Context:** `EnvMain` currently resolves ONE playbook's `fm.Env`. Extend it to resolve the chain and emit the **union**. `ValidateMain` runs `validate.Check` then prints findings; add `depends_on` findings from the resolver.

- [ ] **Step 1: Failing tests.**
  - `env`: a parent declaring `{A}` with a dependency declaring `{B}` (spied loader) → `EnvMain` stdout JSON has BOTH `A` and `B`. Collision: parent `{X: value p}` + dep `{X: value d}`, `X` unexported → output `X == "p"` (parent wins). Cycle/dangling in the chain → exit 2.
  - `validate`: a playbook with a dangling `depends_on` slug → a `depends_on` Error finding (exit 1); a dep cycle → a cycle Error finding.

- [ ] **Step 2: Run to verify they fail.**

- [ ] **Step 3: Implement.**
  - `envcmd.go` `EnvMain`: after `frontmatter.Parse(content)`, if `fm.DependsOn` non-empty, `order, issues := resolveChain(fm.DependsOn)`; on `len(issues) > 0` → `printDepIssues(os.Stderr, issues); return 2`. Build the union `map[string]frontmatter.EnvValue`: start from the **parent's** `fm.Env` (parent wins), then for each dep in `order` add any name not already present. Pass the union to `resolveEnvJSON`. (No deps → today's behavior, `fm.Env` only.)
  - `validatecmd.go` `ValidateMain`: after collecting `validate.Check` findings, if `fm.DependsOn` non-empty, `_, issues := resolveChain(fm.DependsOn)` and append a finding per issue (dangling → `Message: fmt.Sprintf("depends_on %q does not exist in the store", issue.Slug)`; cycle → `Message: "depends_on cycle: " + strings.Join(issue.Path, " → ")`), `Severity: validate.Error, Check: "depends_on"`. Fold them into the same exit-code / print path as the structural findings (so a dep issue makes `validate` exit non-zero). Keep `internal/validate` itself untouched (pure leaf); the resolver call lives here in the launcher.

- [ ] **Step 4: Run to verify they pass**; `go test ./internal/launcher/`; `go build ./...`; `go vet ./...`.

- [ ] **Step 5: Commit** —
  ```bash
  cd ~/Projects/langs/go/ai-playbook && git add internal/launcher/envcmd.go internal/launcher/envcmd_test.go internal/launcher/validatecmd.go internal/launcher/validatecmd_test.go && git commit -m "feat(launcher): env unions the depends_on chain; validate checks depends_on"
  ```
  Verify `%G?` == `G`.

---

### Task 5: docs

**Files:**
- Modify: a new composition section in `examples/` (extend `examples/07-run-modes.md` or add a short `## Composition with depends_on` there), `docs/ROADMAP.md`, `CHANGELOG.md`, `docs/BACKLOG.md`

- [ ] **Step 1:** Add a concise `## Composition with depends_on` section to `examples/07-run-modes.md` (a `{static}` illustration — dependencies are store slugs, so it's explanatory, not a live-runnable example): show a front-matter `depends_on: [setup-db]` snippet and explain that `run <parent>` runs each dependency headless in topological order first, aborting the chain on the first failure; that `--with-env` and `env` span the whole chain; and that `validate` flags cycles / missing dependency slugs.
- [ ] **Step 2:** `docs/ROADMAP.md` — mark Phase 3 `depends_on` **SHIPPED** (move it out of "remaining"): update the Phase 3 status line and the `depends_on` / cycle-detection bullets to `[DONE]`, bump "Last updated".
- [ ] **Step 3:** `CHANGELOG.md` `[Unreleased] → Added`: a `depends_on` bullet (transitive store-slug dependencies run headless before the parent in topological order; cycle/dangling detection; `--with-env` and `env` span the chain).
- [ ] **Step 4:** `docs/BACKLOG.md` — add two logged follow-ups: (a) `--auto` on a *stored* parent historically dropped `fm.Env` — now fixed via the shared loader, so remove/close if a matching item exists, else note it fixed; (b) the authoring regenerate/commit path (`orchestrator.buildFrontMatter`) drops `depends_on` on a re-author — add as a task.
- [ ] **Step 5: Commit** —
  ```bash
  cd ~/Projects/langs/go/ai-playbook && git add examples/07-run-modes.md docs/ROADMAP.md CHANGELOG.md docs/BACKLOG.md && git commit -m "docs: document depends_on composition; mark Phase 3 shipped"
  ```
  Verify `%G?` == `G`.

---

## Final verification (after all tasks)

- [ ] `cd ~/Projects/langs/go/ai-playbook && gofmt -l internal/frontmatter internal/store internal/autorun internal/launcher cmd/ai-playbook` empty; `go build ./... && go vet ./...` clean; `go run github.com/gordonklaus/ineffassign@v0.2.0 ./...` clean; `go test ./internal/frontmatter/ ./internal/store/ ./internal/autorun/ ./internal/launcher/` PASS.
- [ ] `go install ./cmd/ai-playbook`; live-verify with two throwaway store playbooks (write to the global store dir) where `parent` has `depends_on: [dep]`:
  - `run --auto <parent>` → runs `dep` (banner) then `parent`, in order.
  - make `dep`'s failing step fail → the chain aborts, `parent` never runs, non-zero exit.
  - add a cycle (`dep depends_on parent`) → `depends_on cycle: …`, exit 2, nothing runs.
  - a missing dep slug → `dependency "…" not found`, exit 2.
  - `env <parent>` → JSON unions parent + dep declared vars; `run --auto --with-env '{…both…}' <parent>` feeds both; a key no playbook declares → one union warning.
  - `validate <parent>` with a bad dep → a `depends_on` error finding.

## Self-review notes (coverage vs spec)

- Schema + store + finalize → Task 1. Pure graph (topo/cycle/dangling) + `blocksFor` + `SuppressUndeclaredWarning` → Task 2. Store loader + chain run + `RunMain`/`autoRun` wiring + chain-spanning `--with-env` + union warning → Task 3. `env` union + `validate` checks → Task 4. Docs → Task 5. `internal/validate` stays a pure leaf (dep checks live in the launcher). The stored-parent `fm.Env` bug is fixed incidentally by routing `autoRun` through `loadParent`.
