# Run modes P1 — shared core + `--auto` (Implementation Plan)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `ai-playbook run --auto --file <pb>` — an inline, headless (no-TUI) runner that executes a playbook's blocks in `needs=` order, stops at the first failure, rolls back completed steps in reverse by default (`--no-auto-rollback` opts out), writes a run log + prints a summary, and exits non-zero on any failure. Plus the shared `internal/autorun` core that Plan 2 (`--assisted`) will reuse.

**Architecture:** A new leaf package `internal/autorun` holds a plain `Block` DTO + the pure control logic (`Sequence`/`NextRunnable`/`RollbackPairs`), the run log/summary, and a headless execution engine (`Execute` over an injected `StepRunner`; `Run` wires the real orchestrator-backed runner). `internal/autorun` imports only `orchestrator`/`driver`/`frontmatter`/`cache` — **never `internal/ui`** (none of those import `ui`, so `ui` can still import `autorun`). The launcher does the markdown→blocks parsing (it already imports `ui`): `ui.Render(md, 80, nil, "")` yields `[]ui.Block`, which it converts to `[]autorun.Block` and hands to `autorun.Run`. `ui.beginRollback` is refactored to call `autorun.RollbackPairs` so the visible and headless rollbacks share one definition.

**Tech Stack:** Go; `internal/orchestrator` (`New`/`Do`/`Action`/`Kind`), `internal/driver` (`Open`/`Options`/`Result`), `internal/frontmatter` (`Parse`/`EnvValue`), `internal/cache` (`DefaultRoot`), `internal/ui` (`Render`/`Block` — launcher-side only), `internal/launcher` (`runcmd.go`). No bubbletea in the `--auto` path.

## Global Constraints

- Module `github.com/Townk/ai-playbook`. gpg-signed Conventional Commits (`git commit`, NEVER `--no-gpg-sign`); verify `git log -1 --format=%G?` == `G`. **NO `Co-Authored-By`/AI-attribution trailers.** `git add` explicit paths only. Commit only at each task's final step.
- **Repo lives at `~/Projects/langs/go/ai-playbook`** — all `go`/`git` commands run there (`cd ~/Projects/langs/go/ai-playbook && …` or `git -C …`). The default shell cwd is a different repo.
- **No import cycle.** `internal/autorun` MUST NOT import `internal/ui` (verified: `Block`+`Render` live in `ui`; `ui` imports `autorun`, not the reverse). `autorun` may import `orchestrator`/`driver`/`frontmatter`/`cache` only (none import `ui`).
- **Never continue past a failure** (spec §3). First non-zero step exit stops the loop.
- **Rollback polarity:** under `--auto`, auto-rollback is ON by default; `--no-auto-rollback` opts out. `--no-auto-rollback` without `--auto` is a usage error. `--auto-rollback` (the existing default-pager opt-in) combined with `--auto` is a usage error. (`--assisted` is Plan 2 — do NOT add it here.)
- **Status string literals are shared with `ui`** by value: `""` (never run), `"ok"`, `"failed"`, `"stopped"`, `"rolledback"`, and the NEW `"skipped"`. `autorun` defines its own consts with these exact string values so the `ui`↔`autorun` conversion is a copy.
- Verification gates (run in the repo): `gofmt -l internal/autorun internal/launcher internal/ui` (empty), `go build ./...`, `go vet ./...`, `go run github.com/gordonklaus/ineffassign@v0.2.0 ./...` (clean — local `go test`/`build`/`gofmt` do NOT catch `ineffassign`), and `go test` on every touched package (`internal/ui` also `-race`). The `internal/ui` suite is slow (~2.5 min) and `reengage_test` is load-flaky — re-run in isolation if it trips.

---

### Task 1: `internal/autorun` — DTO + pure control logic

**Files:**
- Create: `internal/autorun/sequence.go`
- Test: `internal/autorun/sequence_test.go`

**Interfaces:**
- Produces (consumed by Tasks 3, 4, 6, 7):
```go
package autorun

type StepKind int
const ( KindRun StepKind = iota; KindApplyDiff; KindCreateFile )

// Block is autorun's own DTO — decoupled from internal/ui.Block. The launcher and
// internal/ui convert their ui.Block into this.
type Block struct {
	ID       string
	Command  string   // ui.Block.Payload
	Needs    []string
	Rollback string   // id of the block that undoes this one; "" if none
	Static   bool
	Kind     StepKind
}

// Status string literals shared by value with internal/ui.blockRunState.Status.
const (
	StatusOK         = "ok"
	StatusFailed     = "failed"
	StatusSkipped    = "skipped"
	StatusRolledBack = "rolledback"
)

// Sequence returns the forward-runnable blocks in document order: not Static and
// not a rollback TARGET (never referenced by another block's Rollback).
func Sequence(blocks []Block) []Block

// NextRunnable returns the first Sequence block whose status is "" (never run) and
// whose every Need has status StatusOK. ok=false when none remain. A block whose Need
// is skipped/failed/unrun is itself not runnable (→ effectively auto-skipped).
func NextRunnable(blocks []Block, status map[string]string) (Block, bool)

// RollbackPairs returns reverse-order (originID, targetID) for every block whose
// status is StatusOK and whose Rollback is non-empty. Order: last-applied first.
func RollbackPairs(blocks []Block, status map[string]string) [][2]string
```

**Context:** Pure functions, no I/O, no local imports. `Sequence` first builds the rollback-target set (`for _, b := range blocks { if b.Rollback != "" { targets[b.Rollback]=true } }`), then filters `!b.Static && !targets[b.ID]`. `NextRunnable` walks `Sequence(blocks)` and returns the first with `status[b.ID]==""` and all needs `StatusOK`. `RollbackPairs` iterates `blocks` in REVERSE, emitting `[2]string{b.ID, b.Rollback}` when `status[b.ID]==StatusOK && b.Rollback!=""` — this mirrors the current `beginRollback` reverse loop (`internal/ui/model.go:~2260`, which walks `i := len(blocks)-1; i>=0; i--` collecting origins+targets).

- [ ] **Step 1: Write the failing tests**

```go
package autorun

import "testing"

func TestSequence_SkipsStaticAndRollbackTargets(t *testing.T) {
	blocks := []Block{
		{ID: "a", Kind: KindRun, Rollback: "undo-a"},
		{ID: "undo-a", Kind: KindRun},          // rollback target → skipped
		{ID: "note", Static: true},             // static → skipped
		{ID: "b", Kind: KindRun, Needs: []string{"a"}},
	}
	got := Sequence(blocks)
	var ids []string
	for _, b := range got { ids = append(ids, b.ID) }
	want := []string{"a", "b"}
	if len(ids) != 2 || ids[0] != want[0] || ids[1] != want[1] {
		t.Fatalf("Sequence ids = %v, want %v", ids, want)
	}
}

func TestNextRunnable_RespectsNeedsAndStatus(t *testing.T) {
	blocks := []Block{
		{ID: "a", Kind: KindRun},
		{ID: "b", Kind: KindRun, Needs: []string{"a"}},
	}
	// a not yet run → next is a.
	if b, ok := NextRunnable(blocks, map[string]string{}); !ok || b.ID != "a" {
		t.Fatalf("first NextRunnable = %v,%v want a,true", b.ID, ok)
	}
	// a ok → next is b.
	if b, ok := NextRunnable(blocks, map[string]string{"a": StatusOK}); !ok || b.ID != "b" {
		t.Fatalf("after a ok NextRunnable = %v,%v want b,true", b.ID, ok)
	}
	// a skipped → b unrunnable → none.
	if _, ok := NextRunnable(blocks, map[string]string{"a": StatusSkipped}); ok {
		t.Fatal("b must be unrunnable when its need a is skipped")
	}
	// all ok → none.
	if _, ok := NextRunnable(blocks, map[string]string{"a": StatusOK, "b": StatusOK}); ok {
		t.Fatal("no runnable step when all ok")
	}
}

func TestRollbackPairs_ReverseOkOnly(t *testing.T) {
	blocks := []Block{
		{ID: "a", Rollback: "undo-a"},
		{ID: "b", Rollback: "undo-b"},
		{ID: "c"}, // no rollback
	}
	status := map[string]string{"a": StatusOK, "b": StatusOK, "c": StatusFailed}
	got := RollbackPairs(blocks, status)
	want := [][2]string{{"b", "undo-b"}, {"a", "undo-a"}} // reverse, ok-with-rollback only
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("RollbackPairs = %v, want %v", got, want)
	}
}
```

- [ ] **Step 2: Run to verify they fail** — `cd ~/Projects/langs/go/ai-playbook && go test ./internal/autorun/` → FAIL (package/functions undefined).
- [ ] **Step 3: Implement** `internal/autorun/sequence.go` per the Interfaces + Context above.
- [ ] **Step 4: Run to verify they pass** — `go test ./internal/autorun/` → PASS.
- [ ] **Step 5: Commit** — `git add internal/autorun/sequence.go internal/autorun/sequence_test.go && git commit -m "feat(autorun): block DTO + pure Sequence/NextRunnable/RollbackPairs"`

---

### Task 2: `internal/autorun` — run log + summary

**Files:**
- Create: `internal/autorun/log.go`
- Test: `internal/autorun/log_test.go`

**Interfaces:**
- Produces (consumed by Tasks 3, 4):
```go
// StepResult is one executed (or skipped/cancelled) step, for the summary + log.
type StepResult struct {
	ID         string `json:"id"`
	Command    string `json:"command"`
	Exit       int    `json:"exit"`
	Status     string `json:"status"` // "ok" | "failed" | "skipped" | "rolledback" | "cancelled"
	OutputPath string `json:"output,omitempty"`
}

// Summarize renders a one-line-per-step summary table (human-readable, to stdout).
func Summarize(results []StepResult) string

// WriteRunLog writes results as JSON to <dir>/runs/<stamp>-<slug>.json and returns the
// path. dir is the data-dir root (cache.DefaultRoot()); stamp is a caller-supplied
// timestamp (injected for deterministic tests). Creates <dir>/runs if absent.
func WriteRunLog(dir, stamp, slug string, results []StepResult) (string, error)
```

**Context:** `WriteRunLog` uses `os.MkdirAll(filepath.Join(dir,"runs"), 0o755)` then `os.WriteFile(path, json.MarshalIndent({...}, "", "  "), 0o644)`. Marshal a small envelope `struct{ Slug string; Stamp string; Steps []StepResult }`. `Summarize` formats each result like `  ✓ ok       build   (exit 0)` / `  ✗ failed   test    (exit 1)` / `  ↺ rolledback build` / `  – skipped  status`; keep it plain ASCII-safe glyphs consistent with the pager (`✓`/`✗`/`↺`/`–`). The data-dir root comes from `cache.DefaultRoot()` (`internal/cache/cache.go:51`) — Task 4 passes it in; this task takes `dir` as a parameter so the test uses a temp dir.

- [ ] **Step 1: Write the failing tests**

```go
func TestWriteRunLog_WritesJSON(t *testing.T) {
	dir := t.TempDir()
	results := []StepResult{
		{ID: "build", Command: "b.sh", Exit: 0, Status: StatusOK},
		{ID: "test", Command: "t.sh", Exit: 1, Status: StatusFailed},
	}
	path, err := WriteRunLog(dir, "20260701T120000Z", "seven", results)
	if err != nil { t.Fatal(err) }
	if want := filepath.Join(dir, "runs", "20260701T120000Z-seven.json"); path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), `"id": "test"`) || !strings.Contains(string(raw), `"exit": 1`) {
		t.Fatalf("log missing step records:\n%s", raw)
	}
}

func TestSummarize_RendersEachStatus(t *testing.T) {
	out := Summarize([]StepResult{
		{ID: "build", Status: StatusOK, Exit: 0},
		{ID: "test", Status: StatusFailed, Exit: 1},
		{ID: "status", Status: StatusSkipped},
	})
	for _, want := range []string{"build", "test", "status", "exit 1"} {
		if !strings.Contains(out, want) { t.Errorf("summary missing %q:\n%s", want, out) }
	}
}
```
(imports: `encoding/json` not needed in test; `os`, `path/filepath`, `strings`, `testing`.)

- [ ] **Step 2: Run to verify they fail** — `go test ./internal/autorun/ -run 'RunLog|Summarize'` → FAIL.
- [ ] **Step 3: Implement** `internal/autorun/log.go`.
- [ ] **Step 4: Run to verify they pass.**
- [ ] **Step 5: Commit** — `git add internal/autorun/log.go internal/autorun/log_test.go && git commit -m "feat(autorun): run-log writer + summary formatter"`

---

### Task 3: `internal/autorun` — `Execute` control-flow engine

**Files:**
- Create: `internal/autorun/execute.go`
- Test: `internal/autorun/execute_test.go`

**Interfaces:**
- Consumes: `Block`, `NextRunnable`, `RollbackPairs`, `StepResult`, `Summarize`, `WriteRunLog` (Tasks 1-2).
- Produces (consumed by Task 4):
```go
// Step is a unit handed to a StepRunner.
type Step struct { ID, Command string; Kind StepKind }

// StepRunner executes one step and returns its exit + captured output path.
// Task 4 supplies the real orchestrator-backed impl; tests supply a fake.
type StepRunner interface { RunStep(s Step) (exit int, outputPath string) }

// Config drives Execute. Out receives the streamed per-step headers + final summary.
type Config struct {
	Blocks       []Block
	AutoRollback bool
	Out          io.Writer
	LogDir       string // cache.DefaultRoot(); "" skips the log file
	Stamp        string // timestamp for the log filename
	Slug         string
}

// Execute runs the forward loop over an injected runner and returns the process exit
// code: 0 iff every step ran ok; else the failed step's exit code (min 1).
func Execute(cfg Config, r StepRunner) int
```

**Context:** The loop, using a local `status := map[string]string{}` and `var results []StepResult`:
1. `b, ok := NextRunnable(cfg.Blocks, status)`; if `!ok` break.
2. `fmt.Fprintf(cfg.Out, "[%s] %s\n", b.ID, b.Command)`.
3. `exit, out := r.RunStep(Step{b.ID, b.Command, b.Kind})`.
4. record `StepResult`; `if exit==0 { status[b.ID]=StatusOK } else { status[b.ID]=StatusFailed; failedExit=exit; break }`.
5. After the loop, if a step failed AND `cfg.AutoRollback`: `pairs := RollbackPairs(cfg.Blocks, status)`; for each `(origin,target)` run `r.RunStep(Step{target, <command of target>, KindRun})`, set `status[origin]=StatusRolledBack`, append a `rolledback` StepResult. (Look up the target's command from `cfg.Blocks` by id.)
6. `WriteRunLog(cfg.LogDir, cfg.Stamp, cfg.Slug, results)` if `LogDir!=""`; `fmt.Fprint(cfg.Out, Summarize(results))`.
7. Return `0` if no failure else `max(1, failedExit)`.

Note: forward loop never marks `StatusSkipped` itself in `--auto` (auto runs everything runnable). `StatusSkipped` exists for Plan 2 (`--assisted`) and for `NextRunnable`'s need-checking; `Execute` still handles it correctly (a skipped need blocks downstream). No-rollback-blocks + failure → `RollbackPairs` returns empty → nothing to roll back (matches spec "nothing to undo → stop + log").

- [ ] **Step 1: Write the failing tests** — a fake runner records calls and returns scripted exits:

```go
type fakeRunner struct {
	calls []string
	exits map[string]int // id → exit (default 0)
}
func (f *fakeRunner) RunStep(s Step) (int, string) {
	f.calls = append(f.calls, s.ID)
	return f.exits[s.ID], "" // 0 unless scripted
}

func TestExecute_StopsAtFirstFailure_NoLaterSteps(t *testing.T) {
	blocks := []Block{
		{ID: "a", Kind: KindRun, Command: "a"},
		{ID: "b", Kind: KindRun, Command: "b", Needs: []string{"a"}},
		{ID: "c", Kind: KindRun, Command: "c", Needs: []string{"b"}},
	}
	r := &fakeRunner{exits: map[string]int{"b": 3}}
	code := Execute(Config{Blocks: blocks, Out: io.Discard}, r)
	if code != 3 { t.Errorf("exit code = %d, want 3 (failed step's exit)", code) }
	// c must never run (never continue past a failure).
	for _, id := range r.calls { if id == "c" { t.Error("c ran after b failed") } }
}

func TestExecute_AutoRollback_ReverseOfCompleted(t *testing.T) {
	blocks := []Block{
		{ID: "a", Kind: KindRun, Command: "a", Rollback: "undo-a"},
		{ID: "undo-a", Kind: KindRun, Command: "undo-a"},
		{ID: "b", Kind: KindRun, Command: "b", Needs: []string{"a"}},
	}
	r := &fakeRunner{exits: map[string]int{"b": 1}}
	code := Execute(Config{Blocks: blocks, AutoRollback: true, Out: io.Discard}, r)
	if code == 0 { t.Error("failure must exit non-zero") }
	// after a ok then b fails, undo-a must have run.
	ran := false
	for _, id := range r.calls { if id == "undo-a" { ran = true } }
	if !ran { t.Error("auto-rollback must run undo-a for the completed step a") }
}

func TestExecute_NoAutoRollback_LeavesState(t *testing.T) {
	blocks := []Block{
		{ID: "a", Kind: KindRun, Command: "a", Rollback: "undo-a"},
		{ID: "undo-a", Kind: KindRun, Command: "undo-a"},
		{ID: "b", Kind: KindRun, Command: "b", Needs: []string{"a"}},
	}
	r := &fakeRunner{exits: map[string]int{"b": 1}}
	Execute(Config{Blocks: blocks, AutoRollback: false, Out: io.Discard}, r)
	for _, id := range r.calls { if id == "undo-a" { t.Error("no-auto-rollback must NOT run undo-a") } }
}

func TestExecute_AllGreen_ExitZero(t *testing.T) {
	blocks := []Block{{ID: "a", Kind: KindRun, Command: "a"}, {ID: "b", Kind: KindRun, Command: "b", Needs: []string{"a"}}}
	if code := Execute(Config{Blocks: blocks, Out: io.Discard}, &fakeRunner{}); code != 0 {
		t.Errorf("all-green exit = %d, want 0", code)
	}
}
```

- [ ] **Step 2: Run to verify they fail** — `go test ./internal/autorun/ -run TestExecute` → FAIL.
- [ ] **Step 3: Implement** `internal/autorun/execute.go` per Context.
- [ ] **Step 4: Run to verify they pass.**
- [ ] **Step 5: Commit** — `git add internal/autorun/execute.go internal/autorun/execute_test.go && git commit -m "feat(autorun): headless Execute engine (stop-on-failure + rollback)"`

---

### Task 4: `internal/autorun` — orchestrator-backed runner, env preflight, `Run`

**Files:**
- Create: `internal/autorun/run.go`
- Test: `internal/autorun/run_test.go`

**Interfaces:**
- Consumes: `Execute`, `Config`, `Step`, `StepRunner`, `Block` (Task 3); `orchestrator.New/Do/Action/Kind*`, `driver.Open/Options`, `frontmatter.EnvValue`, `cache.DefaultRoot`.
- Produces (consumed by Task 6):
```go
// RunConfig is the launcher's headless-run request.
type RunConfig struct {
	Blocks       []Block
	EnvVars      map[string]frontmatter.EnvValue // declared front-matter env (var → {value,why})
	Cwd          string
	Shell        string   // driver.Options.Shell selector
	Slug         string
	AutoRollback bool
	Out          io.Writer // default os.Stdout when nil
	Now          func() string // timestamp source; default UTC "20060102T150405Z"
}

// Run resolves + preflights env, opens a driver + orchestrator (headless, no float/mux),
// and executes RunConfig.Blocks via Execute. Returns the process exit code. Missing
// required env vars → prints them (+why) and returns non-zero BEFORE opening the driver.
func Run(rc RunConfig) int
```

**Context:**
- **Env preflight + build env slice.** For each `name, ev := range rc.EnvVars`: resolved = `os.Getenv(name)` if present in the environment, else `ev.Value`; if resolved is `""` → it's *required and missing* → collect `name` + `ev.Why`. If any missing: `fmt.Fprintf(rc.Out, "missing required env: %s — %s\n", name, why)` for each and `return 1` (before opening the driver — matches spec §3.1 / the ch.07 `[!TIP]`). Otherwise build `env := os.Environ()` and append `name+"="+resolved` for each declared var NOT already in the environment (defaults filled in). (`rc.Cwd`/`PROJECT_ROOT` are already resolved by the launcher in Task 6 and passed via `rc.Cwd` + baked into env there — this task just consumes `rc.EnvVars` for the *playbook-declared* vars.)
- **Driver + orch (headless).** `d, err := driver.Open(driver.Options{Cwd: rc.Cwd, Shell: rc.Shell, Env: env})`; `defer d.Close()`. `orch := orchestrator.New(d, noopMux{})` — a tiny package-local `noopMux` implementing `orchestrator.Mux` (`Copy(string) error { return nil }`, `Play(string) error { return nil }`); do NOT call `WithFloat` (nil Float is fine — we only run/apply/create). Do NOT reuse `ui.BuildOrch` (that would import `ui`).
- **orchRunner.** A `StepRunner` whose `RunStep(s Step)` maps `s.Kind`→`orchestrator.Kind` (`KindRun→orchestrator.KindRun`, `KindApplyDiff→orchestrator.KindApplyDiff`, `KindCreateFile→orchestrator.KindCreateFile`), calls `res, _ := orch.Do(orchestrator.Action{Kind: k, ID: s.ID, Payload: s.Command})`, streams `res.Out`/`res.Err` to `rc.Out`, writes the output to a temp log (reuse the `os.CreateTemp("", "apb-run-"+id+"-*.log")` shape from `internal/ui/inprocess.go:437`; a private copy here is fine — do NOT import `ui`), and returns `(res.Exit, logpath)`.
- **Wire Execute:** `return Execute(Config{Blocks: rc.Blocks, AutoRollback: rc.AutoRollback, Out: out, LogDir: cache.DefaultRoot(), Stamp: now(), Slug: rc.Slug}, &orchRunner{orch: orch, out: out})`.

**Testing note:** `orchestrator.Do(KindRun)` calls the real pty driver, so the integration test runs trivial shell (`true`/`false`) — the orchestrator suite already exercises a real driver, so this is safe in CI. Skip with `testing.Short()` if driver open is unavailable.

- [ ] **Step 1: Write the failing tests**

```go
func TestRun_MissingRequiredEnv_ExitsBeforeDriver(t *testing.T) {
	var out strings.Builder
	code := Run(RunConfig{
		Blocks:  []Block{{ID: "a", Kind: KindRun, Command: "true"}},
		EnvVars: map[string]frontmatter.EnvValue{"MUST_SET": {Value: "", Why: "the API token"}},
		Out:     &out,
	})
	if code == 0 { t.Error("missing required env must exit non-zero") }
	if !strings.Contains(out.String(), "MUST_SET") || !strings.Contains(out.String(), "the API token") {
		t.Errorf("must name the missing var + why:\n%s", out.String())
	}
}

func TestRun_AllGreen_Integration(t *testing.T) {
	if testing.Short() { t.Skip("opens a real driver") }
	var out strings.Builder
	code := Run(RunConfig{
		Blocks: []Block{
			{ID: "a", Kind: KindRun, Command: "true"},
			{ID: "b", Kind: KindRun, Command: "true", Needs: []string{"a"}},
		},
		Slug: "t", Out: &out, Now: func() string { return "STAMP" },
	})
	if code != 0 { t.Fatalf("all-green exit = %d, want 0\n%s", code, out.String()) }
}

func TestRun_Failure_Integration(t *testing.T) {
	if testing.Short() { t.Skip("opens a real driver") }
	var out strings.Builder
	code := Run(RunConfig{
		Blocks: []Block{{ID: "boom", Kind: KindRun, Command: "false"}},
		Slug: "t", Out: &out, Now: func() string { return "STAMP" },
	})
	if code == 0 { t.Error("a failing block must exit non-zero") }
}
```

- [ ] **Step 2: Run to verify they fail** — `go test ./internal/autorun/ -run TestRun` → FAIL.
- [ ] **Step 3: Implement** `internal/autorun/run.go` (the `noopMux`, `orchRunner`, env resolve, `Run`).
- [ ] **Step 4: Run to verify they pass** — `go test ./internal/autorun/` (full package).
- [ ] **Step 5: Commit** — `git add internal/autorun/run.go internal/autorun/run_test.go && git commit -m "feat(autorun): orchestrator-backed runner + env preflight + Run"`

---

### Task 5: `--auto` interrupt (Ctrl-C aborts, exits non-zero)

**Files:**
- Modify: `internal/autorun/run.go` (wrap `RunStep` execution with SIGINT handling)
- Test: `internal/autorun/run_test.go`

**Interfaces:**
- Consumes/extends Task 4's `orchRunner`/`Run`. No new exported API.

**Context:** Spec §3.4 + ch.07 "The stop button". Install `sig := make(chan os.Signal, 1); signal.Notify(sig, os.Interrupt)` in `Run` (defer `signal.Stop(sig)`). In `orchRunner.RunStep`, run `orch.Do(...)` on a goroutine delivering the `driver.Result` on a channel; `select` on that channel vs `sig`. On `sig`: call `orch.Do(orchestrator.Action{Kind: orchestrator.KindStop})` (→ `d.Stop()` SIGTERMs the child), wait for the in-flight `Do` to return, mark the step `cancelled`, and signal `Execute` to stop (return a sentinel exit like `130` and set a `cancelled` flag the runner exposes so `Execute` breaks without rollback ambiguity — simplest: `RunStep` returns exit `130`, and `Execute` already treats non-zero as failure → stops; the summary shows `cancelled`). Keep it minimal: a cancelled step is a failure (non-zero exit) that stops the loop; `--auto`'s default rollback still applies to already-completed steps (acceptable — matches "roll back completed steps").

**Testing note:** signal delivery is awkward to unit-test deterministically; test the *mechanism* by making `RunStep` interruptible via an injected stop channel rather than a real OS signal — add an unexported `stopCh chan struct{}` on `orchRunner` that the test closes mid-run against a `sleep 5` block, asserting the block returns quickly with a non-zero exit and the summary marks it `cancelled`. (Real SIGINT wiring in `Run` is thin and covered by manual live-verification.)

```go
func TestRunStep_Interrupt_CancelsQuickly(t *testing.T) {
	if testing.Short() { t.Skip("opens a real driver") }
	// Open a real driver+orch via an unexported test constructor mirroring Run's build,
	// give orchRunner a stopCh, launch RunStep on "sleep 5" on a goroutine, close stopCh,
	// and assert it returns within ~1s with exit != 0.
}
```
(Adapt to the real construction — factor Task 4's driver+orch build into an unexported helper `newOrchRunner(rc) (*orchRunner, func(), error)` so both `Run` and this test share it.)

- [ ] **Step 1: Write the failing test** (interruptible `RunStep` via `stopCh`).
- [ ] **Step 2: Run to verify it fails.**
- [ ] **Step 3: Implement** the SIGINT wiring in `Run` + the `select`/`stopCh` in `RunStep`.
- [ ] **Step 4: Run to verify it passes** — `go test ./internal/autorun/`.
- [ ] **Step 5: Commit** — `git add internal/autorun/run.go internal/autorun/run_test.go && git commit -m "feat(autorun): Ctrl-C aborts the running block and exits non-zero"`

---

### Task 6: launcher — `--auto`/`--no-auto-rollback` flags + `RunMain` headless branch

**Files:**
- Modify: `internal/launcher/runcmd.go` (`resolveRunArgs`, `RunMain`, a new `autoRun` helper)
- Test: `internal/launcher/runcmd_test.go`

**Interfaces:**
- Consumes: `autorun.Run`, `autorun.Block`, `autorun.KindRun/KindApplyDiff/KindCreateFile`; `ui.Render` (→ `[]ui.Block`); `frontmatter.Parse`.
- Produces: `resolveRunArgs` new signature:
```go
type runMode int
const ( modeDefault runMode = iota; modeAuto ) // modeAssisted added in Plan 2

type runArgs struct {
	Kind, Value    string // "file"|"playbook", the path/slug
	Mode           runMode
	AutoRollback   bool // existing default-pager opt-in (--auto-rollback)
	NoAutoRollback bool // --no-auto-rollback (only valid with --auto)
}
func resolveRunArgs(args []string) (runArgs, error)
```

**Context:**
- **Flags.** Add to the `flag.FlagSet` in `resolveRunArgs` (`runcmd.go:220-262`): `fs.BoolVar(&auto,"auto",false,...)`, `fs.BoolVar(&noAutoRB,"no-auto-rollback",false,...)` (keep the existing `--auto-rollback`/`--playbook`/`--file`). Validation (usage errors → return `runArgs{}, fmt.Errorf(...)`): `--no-auto-rollback` set but `--auto` not → error; `--auto` AND `--auto-rollback` both set → error. Map `--auto`→`Mode: modeAuto`. Change the return type from the current 4-tuple to `(runArgs, error)`; update the existing source-count logic to fill `runArgs.Kind/Value`.
- **RunMain branch.** In `RunMain` (`runcmd.go:62-83`), after `ra, err := resolveRunArgs(os.Args[2:])`: if `ra.Mode == modeAuto` → `return autoRun(ra)` (headless; never calls `ui.Main`/`setAutoRollbackFn`). Else keep today's path (`setAutoRollbackFn(ra.AutoRollback)` then `runFile`/`runPlaybook`).
- **`autoRun(ra runArgs) int`.** Resolve the source to `(md string, cwd string, fm frontmatter.FrontMatter)`: for `Kind=="file"` read the file + `frontmatter.Parse` + reuse `resolveProjectRoot`/the `runFile` cwd rule (`runcmd.go` `runFile`); for `Kind=="playbook"` `storeLoadFn(slug)` + project-root like `runPlaybook`. Build blocks: `_, _, uiBlocks := ui.Render(bodyWithoutFrontMatterAsUIRendersIt, 80, nil, "")` — NOTE `ui.Render` takes the FULL markdown (it strips front matter internally like the viewer); pass the same string `runFile`/`renderStored` pass to the viewer (the raw file/body). Convert each `ui.Block`→`autorun.Block`: `Kind` from `ui.Block.Type` (`"shell"|"run"→KindRun`, `"diff"→KindApplyDiff`, `"create"→KindCreateFile`, `"static"→` skip by leaving Static), `Command: b.Payload`, `Needs`, `Rollback`, `Static`. Build env: `env` handled inside `autorun.Run` from `fm.Env`; pass `PROJECT_ROOT` by prepending to the process env is NOT needed — `autorun.Run` reads `os.Environ()`; instead set `rc.Cwd` and, for project-bound, export `PROJECT_ROOT` by adding it to `rc.EnvVars`? NO — `PROJECT_ROOT` is not a front-matter env var. Simplest: `autoRun` sets `os.Setenv("PROJECT_ROOT", root)` before `autorun.Run` for a project-bound source (mirrors how the viewer injects it via the driver env), then calls `autorun.Run(autorun.RunConfig{Blocks, EnvVars: fm.Env, Cwd: cwd, Shell: cfg.Driver.Shell, Slug: slugFrom(ra), AutoRollback: !ra.NoAutoRollback})`. Return its exit code.
- **AutoRollback under auto:** pass `AutoRollback: !ra.NoAutoRollback` (ON by default).

**Context (test seams):** `runcmd_test.go` already stubs `uiMainFn`, `storeLoadFn`, `setAutoRollbackFn`, etc. Add an `autorunRunFn = autorun.Run` seam var so the test asserts the auto branch calls it with the converted blocks + `AutoRollback` polarity, without opening a real driver.

- [ ] **Step 1: Write the failing tests**

```go
func TestResolveRunArgs_AutoFlags(t *testing.T) {
	// --auto sets Mode.
	ra, err := resolveRunArgs([]string{"--auto", "--file", "x.md"})
	if err != nil || ra.Mode != modeAuto || ra.Kind != "file" || ra.Value != "x.md" {
		t.Fatalf("--auto: %+v err=%v", ra, err)
	}
	// --no-auto-rollback without --auto → error.
	if _, err := resolveRunArgs([]string{"--no-auto-rollback", "--file", "x.md"}); err == nil {
		t.Error("--no-auto-rollback without --auto must error")
	}
	// --auto + --auto-rollback → error.
	if _, err := resolveRunArgs([]string{"--auto", "--auto-rollback", "--file", "x.md"}); err == nil {
		t.Error("--auto with --auto-rollback must error")
	}
}

func TestRunMain_AutoBranch_CallsAutorun(t *testing.T) {
	var gotAutoRB bool
	var gotIDs []string
	defer swap(&autorunRunFn, func(rc autorun.RunConfig) int {
		gotAutoRB = rc.AutoRollback
		for _, b := range rc.Blocks { gotIDs = append(gotIDs, b.ID) }
		return 0
	})()
	// point os.Args at a temp file with two run blocks; default (no --no-auto-rollback).
	// ... write temp .md with {id=a}/{id=b needs=a} ... set os.Args ...
	if code := RunMain(); code != 0 { t.Fatalf("RunMain auto = %d", code) }
	if !gotAutoRB { t.Error("auto default must set AutoRollback=true") }
	if len(gotIDs) != 2 { t.Errorf("blocks converted = %v, want [a b]", gotIDs) }
}
```
(`swap` = the test's existing var-swap helper; add one if absent. Reuse the temp-md + os.Args patterns already in `runcmd_test.go`.)

- [ ] **Step 2: Run to verify they fail** — `go test ./internal/launcher/ -run 'ResolveRunArgs_Auto|AutoBranch'` → FAIL.
- [ ] **Step 3: Implement** the flag parsing, `runArgs` return, `RunMain` branch, `autoRun`, and the `autorunRunFn` seam. Update the EXISTING `resolveRunArgs` callers/tests to the new signature.
- [ ] **Step 4: Run to verify they pass** — `go test ./internal/launcher/`.
- [ ] **Step 5: Commit** — `git add internal/launcher/runcmd.go internal/launcher/runcmd_test.go && git commit -m "feat(run): --auto headless mode + --no-auto-rollback flag"`

---

### Task 7: refactor `ui.beginRollback` to share `autorun.RollbackPairs`

**Files:**
- Modify: `internal/ui/model.go` (`beginRollback` + a small `toAutorunBlocks` helper)
- Test: existing `internal/ui/wave3_rollback_test.go` must stay green (behavior parity)

**Interfaces:**
- Consumes: `autorun.Block`, `autorun.RollbackPairs`, `autorun.KindRun`, `autorun.Status*`.
- Produces: no new exported API; `beginRollback` behavior unchanged.

**Context:** `beginRollback(failedID)` (`internal/ui/model.go:~2260`) currently collects `origins`/`targets` with an inline reverse loop over `m.blocks` filtering `b.Rollback!="" && m.blockStates[b.ID].Status=="ok"`. Replace ONLY that collection with `autorun.RollbackPairs`:
1. Add `func (m model) toAutorunBlocks() ([]autorun.Block, map[string]string)` — map each `m.blocks` entry to `autorun.Block{ID, Command: m.blockCommand(b.ID) or b.Payload, Needs: b.Needs, Rollback: b.Rollback, Static: b.Static, Kind: KindRun}` and `status[id] = m.blockStates[id].Status`.
2. In `beginRollback`: `pairs := autorun.RollbackPairs(ab, status)`; derive `origins`/`targets` from `pairs` (`origin=pairs[i][0]`, `target=pairs[i][1]`) preserving the current reverse order. Everything after (capturing `failedState`, `resetDependents`, marking origins `"rolledback"`, running targets, `RollingBack`, `rollbackPending`) stays EXACTLY as-is.
3. This adds the first `internal/ui → internal/autorun` import — confirm `go build ./...` shows no cycle (there is none: `autorun` doesn't import `ui`).

**Guard:** the user-verified rollback UX must not change. Run the full rollback suite and confirm identical behavior.

- [ ] **Step 1: Write the failing test** — assert `RollbackPairs` and `beginRollback` agree on order, exercised through the existing scenario:

```go
func TestBeginRollback_UsesRollbackPairsOrder(t *testing.T) {
	// two applied steps a,b each with a rollback target; after beginRollback both origins
	// are "rolledback" and both targets are "running", in reverse (b before a) —
	// identical to TestBeginRollbackResetsAndRuns but asserting the shared-helper path.
	// (If TestBeginRollbackResetsAndRuns already covers this post-refactor, extend it
	// rather than duplicate.)
}
```
(Prefer extending the existing `TestBeginRollbackResetsAndRuns`/`TestRollbackChain_*` — they already assert origins `"rolledback"` + targets `"running"`; the refactor must keep them green.)

- [ ] **Step 2: Run the existing rollback tests (they pass today)** — `go test ./internal/ui/ -run 'Rollback'` → PASS (baseline before refactor).
- [ ] **Step 3: Implement** the `toAutorunBlocks` helper + swap the collection in `beginRollback`.
- [ ] **Step 4: Run to verify parity** — `go test ./internal/ui/ -run 'Rollback' -count=1` → still PASS; `go build ./...` (no cycle).
- [ ] **Step 5: Commit** — `git add internal/ui/model.go internal/ui/wave3_rollback_test.go && git commit -m "refactor(ui): beginRollback shares autorun.RollbackPairs"`

---

### Task 8: docs — flip ch.07 ⏳ markers + fix stale default-run prose + ROADMAP flag note

**Files:**
- Modify: `examples/07-run-modes.md`, `docs/guides/tutorial.md`, `docs/ROADMAP.md`

**Context:** No code — align docs with the shipped `--auto`. `examples/07-run-modes.md`: remove the two `<!-- ⏳ needs assisted/auto run modes (not yet built) -->` comments — BUT the "Assisted run" section is still unbuilt (Plan 2), so remove ONLY the "Auto run" section's ⏳ marker; leave the "Assisted run" ⏳ in place until Plan 2. Fix the "Manual step-through" section: it claims `run --file` "waits for you to press Enter" — reword to describe the shipped fullscreen viewer (opens the interactive pager; click **Run** on each block; `q` to quit). `docs/guides/tutorial.md:164`: the ch.07 features line `--assisted (confirm-each-step) · --auto · stop ⏳` — drop the ⏳ from the `--auto` portion only (keep `--assisted` marked). `docs/ROADMAP.md:49-65`: note the shipped flag polarity (`--auto-rollback` = default-pager opt-in; `--no-auto-rollback` = `--auto` opt-out).

- [ ] **Step 1: Edit** the three files per Context.
- [ ] **Step 2: Verify** — re-read each edited section; confirm no `--auto` ⏳ remains and the "Manual step-through" prose matches the pager. (No test — docs.)
- [ ] **Step 3: Commit** — `git add examples/07-run-modes.md docs/guides/tutorial.md docs/ROADMAP.md && git commit -m "docs(run): document shipped --auto mode (ch.07 + ROADMAP)"`

---

## Final verification (after all tasks)

- [ ] `cd ~/Projects/langs/go/ai-playbook && gofmt -l internal/autorun internal/launcher internal/ui` → empty.
- [ ] `go build ./... && go vet ./...` → clean; confirm no import cycle.
- [ ] `go run github.com/gordonklaus/ineffassign@v0.2.0 ./...` → clean.
- [ ] `go test ./internal/autorun/ ./internal/launcher/ && go test -race ./internal/ui/` → PASS.
- [ ] `go install ./cmd/ai-playbook`, then live-verify: `ai-playbook run --auto --file examples/07-run-modes.md` (all-green → exit 0 + summary; `echo $?`), and a hand-edited failing variant (a middle block `exit 1`) → stops, rolls back the earlier step, non-zero exit; `--no-auto-rollback` variant leaves state; check the run log under `${XDG_DATA_HOME:-~/.local/share}/ai-playbook/runs/`.

## Self-review notes (coverage vs spec)

- Spec §1 flags → Task 6 (`--auto`, `--no-auto-rollback`, validation; `--assisted` deferred to Plan 2 as noted). §2 shared core → Tasks 1-3 (Sequence/NextRunnable/RollbackPairs/Summarize/WriteRunLog) + `"skipped"` status const (Task 1). §3 `--auto` engine → Tasks 3-5 (loop, stop-on-failure, rollback default/opt-out, env preflight, log+summary, exit codes, Ctrl-C). §5 env (auto) → Task 4. §6 docs → Task 8 (auto portion; assisted doc stays ⏳). `beginRollback` refactor → Task 7.
- Deferred to Plan 2 (assisted): `--assisted` flag + mutual-exclusion with `--auto`; `SetRunMode` seam; `readyID` cursor; `scrollBlockToFraction`; the focusable footer; `"skipped"` marking in the guided walk (the const + `NextRunnable` handling ship here so Plan 2 only adds the UI).
