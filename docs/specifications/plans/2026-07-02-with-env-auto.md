# `--with-env` for `--auto` (Implementation Plan)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `run --with-env <inline-JSON | JSON-file-path>` (valid only with `--auto`) supplies front-matter `env:` values on the CLI, taking precedence over the ambient environment.

**Architecture:** `internal/autorun` gains an `EnvOverrides` tier in `resolveEnv` (override → exported → front-matter default → missing) and warns undeclared keys in `Run`. `internal/launcher` parses/validates the flag and forwards it. Docs get a short note.

**Tech Stack:** Go stdlib (`encoding/json`, `flag`, `sort`); `internal/frontmatter` (`EnvValue`).

## Global Constraints

- Module `github.com/Townk/ai-playbook`. Repo at `~/Projects/langs/go/ai-playbook` (the Bash cwd starts elsewhere — always `cd ~/Projects/langs/go/ai-playbook` / `git -C`).
- gpg-signed Conventional Commits (NEVER `--no-gpg-sign`; if signing times out STOP + report BLOCKED — user re-unlocks with `! echo x | gpg --clearsign`); verify `git log -1 "--format=%G?"` == `G` (quote the format — zsh globs a bare `%G?`). NO `Co-Authored-By`/AI-attribution trailers. `git add` explicit paths only. Commit only; do NOT push.
- Work on a NEW branch `feat/with-env-auto` off `master` (do NOT commit the feature on `master` directly).
- Precedence (exact): `--with-env[name]` (non-empty) → exported `$name` → front-matter `value:` → missing-required (exit 1). Empty-string override falls through (treated as not-provided).
- `--with-env` valid ONLY with `--auto`; malformed JSON / non-string value / unreadable file → exit 2 (usage). Unknown keys → warn-and-ignore (never error).
- Gates: `gofmt -l`, `go build ./...`, `go vet ./...`, `ineffassign@v0.2.0` (clean), `go test` on touched packages.

---

### Task 1: `autorun` — `EnvOverrides` tier + undeclared-key warning

**Files:**
- Modify: `internal/autorun/run.go` (`RunConfig`, `resolveEnv`, `Run`)
- Test: `internal/autorun/run_test.go`

**Interfaces:**
- Produces (consumed by Task 2): `RunConfig.EnvOverrides map[string]string`.
- Changed: `resolveEnv(vars map[string]frontmatter.EnvValue, overrides map[string]string) (env []string, missing []struct{ name, why string })`.

**Context:** current `resolveEnv` (`run.go:46`) resolves `os.Getenv(name)` → `ev.Value` → missing, and appends `name=resolved` only when the var isn't already in `os.Environ()`. The override must (a) win over an exported var and (b) reach the child — `os/exec` uses the LAST value for a duplicate key, so appending `name=override` after `os.Environ()` makes it win. `Run` (`run.go:198`) calls `resolveEnv(rc.EnvVars)` and prints missing to `out`.

- [ ] **Step 1: Write the failing tests** (add to `internal/autorun/run_test.go`):

```go
func TestResolveEnv_OverridePrecedence(t *testing.T) {
	t.Setenv("PR_EXPORTED", "from-env")
	vars := map[string]frontmatter.EnvValue{
		"PR_OVERRIDE": {Value: "from-default", Why: "x"}, // override beats default
		"PR_EXPORTED": {Value: "from-default", Why: "x"}, // override beats exported env
		"PR_DEFAULT":  {Value: "from-default", Why: "x"}, // no override, no env → default
	}
	overrides := map[string]string{"PR_OVERRIDE": "from-cli", "PR_EXPORTED": "from-cli"}
	env, missing := resolveEnv(vars, overrides)
	if len(missing) != 0 {
		t.Fatalf("unexpected missing: %v", missing)
	}
	// last-wins: the resolved value for each var is what the child would see.
	want := map[string]string{"PR_OVERRIDE": "from-cli", "PR_EXPORTED": "from-cli", "PR_DEFAULT": "from-default"}
	got := lastEnvValues(env, []string{"PR_OVERRIDE", "PR_EXPORTED", "PR_DEFAULT"})
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestResolveEnv_EmptyOverrideFallsThrough(t *testing.T) {
	t.Setenv("PR_EMPTY", "from-env")
	vars := map[string]frontmatter.EnvValue{"PR_EMPTY": {Value: "from-default", Why: "x"}}
	env, missing := resolveEnv(vars, map[string]string{"PR_EMPTY": ""}) // empty → not provided
	if len(missing) != 0 {
		t.Fatalf("unexpected missing: %v", missing)
	}
	if got := lastEnvValues(env, []string{"PR_EMPTY"})["PR_EMPTY"]; got != "from-env" {
		t.Errorf("empty override must fall through to exported env; got %q", got)
	}
}

func TestResolveEnv_MissingStillReported(t *testing.T) {
	vars := map[string]frontmatter.EnvValue{"PR_MISSING": {Value: "", Why: "needed"}}
	_, missing := resolveEnv(vars, nil)
	if len(missing) != 1 || missing[0].name != "PR_MISSING" {
		t.Fatalf("missing = %v, want [PR_MISSING]", missing)
	}
}

// lastEnvValues returns, for each requested name, the LAST value in env (the
// value os/exec would give the child for a duplicate key).
func lastEnvValues(env []string, names []string) map[string]string {
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	out := map[string]string{}
	for _, e := range env {
		if i := strings.IndexByte(e, '='); i >= 0 && want[e[:i]] {
			out[e[:i]] = e[i+1:]
		}
	}
	return out
}

func TestRun_WarnsUndeclaredOverride(t *testing.T) {
	var buf bytes.Buffer
	rc := RunConfig{
		Blocks:       nil, // no blocks → Execute no-ops after env preflight
		EnvVars:      map[string]frontmatter.EnvValue{"KNOWN": {Value: "v", Why: "x"}},
		EnvOverrides: map[string]string{"KNOWN": "v", "BOGUS": "z", "ALSO_BOGUS": "z"},
		Out:          &buf,
		Now:          func() string { return "T" },
	}
	_ = Run(rc)
	got := buf.String()
	if !strings.Contains(got, "with-env: ignoring undeclared variable ALSO_BOGUS") ||
		!strings.Contains(got, "with-env: ignoring undeclared variable BOGUS") {
		t.Fatalf("expected sorted undeclared-key warnings; got:\n%s", got)
	}
	// sorted order: ALSO_BOGUS before BOGUS
	if strings.Index(got, "ALSO_BOGUS") > strings.Index(got, "BOGUS ") {
		t.Errorf("warnings must be sorted; got:\n%s", got)
	}
}
```

Ensure the test file imports `bytes`, `strings`, and `github.com/Townk/ai-playbook/internal/frontmatter` (add any missing). `TestRun_WarnsUndeclaredOverride` runs `Run` with no blocks; confirm that path opens the driver and returns cleanly in this test environment — if opening a real driver with no blocks is problematic, instead assert the warning via a smaller seam by calling a helper `warnUndeclared(out, vars, overrides)` you extract in Step 3 and test that directly (keep `Run` calling it).

- [ ] **Step 2: Run to verify they fail** — `cd ~/Projects/langs/go/ai-playbook && go test ./internal/autorun/ -run 'ResolveEnv|WarnsUndeclared'` → FAIL/build error (`resolveEnv` arity; `EnvOverrides` field).

- [ ] **Step 3: Implement** in `internal/autorun/run.go`:

Add the field to `RunConfig` (after `EnvVars`):
```go
	EnvOverrides map[string]string // CLI --with-env values (name → value); win over exported env
```

Replace `resolveEnv`:
```go
// resolveEnv computes the preflighted env slice for the driver, per rc.EnvVars.
// Precedence per declared var: a non-empty overrides[name] (CLI --with-env) wins,
// else os.Getenv(name), else ev.Value; if the result is "" the var is
// required-and-missing. An override/default that differs from the exported value
// is appended so it wins by os/exec last-wins semantics. missing holds
// (name, why) pairs for every missing var, in map-iteration order.
func resolveEnv(vars map[string]frontmatter.EnvValue, overrides map[string]string) (env []string, missing []struct{ name, why string }) {
	env = os.Environ()
	existing := make(map[string]bool, len(env))
	for _, e := range env {
		for i := 0; i < len(e); i++ {
			if e[i] == '=' {
				existing[e[:i]] = true
				break
			}
		}
	}

	for name, ev := range vars {
		resolved := ""
		if v, ok := overrides[name]; ok && v != "" {
			resolved = v
		} else if v := os.Getenv(name); v != "" {
			resolved = v
		} else {
			resolved = ev.Value
		}
		if resolved == "" {
			missing = append(missing, struct{ name, why string }{name, ev.Why})
			continue
		}
		// Append when newly declared OR when the resolved value differs from what
		// is already exported (an override/default that must win by last-wins).
		if !existing[name] || os.Getenv(name) != resolved {
			env = append(env, name+"="+resolved)
		}
	}
	return env, missing
}
```

Add the warning helper and call it from `Run` before `resolveEnv`:
```go
// warnUndeclared prints a sorted warning for every override key not declared in
// the playbook's env map (they are ignored, never fatal).
func warnUndeclared(out io.Writer, vars map[string]frontmatter.EnvValue, overrides map[string]string) {
	var unknown []string
	for name := range overrides {
		if _, ok := vars[name]; !ok {
			unknown = append(unknown, name)
		}
	}
	sort.Strings(unknown)
	for _, name := range unknown {
		fmt.Fprintf(out, "with-env: ignoring undeclared variable %s\n", name)
	}
}
```
In `Run`, change:
```go
	env, missing := resolveEnv(rc.EnvVars)
```
to:
```go
	warnUndeclared(out, rc.EnvVars, rc.EnvOverrides)
	env, missing := resolveEnv(rc.EnvVars, rc.EnvOverrides)
```
Add `"sort"` to `run.go`'s imports (`io` is already imported).

- [ ] **Step 4: Run to verify they pass** — `go test ./internal/autorun/ -run 'ResolveEnv|WarnsUndeclared'`; then `go test ./internal/autorun/`; `go build ./...`; `go vet ./...`.

- [ ] **Step 5: Commit** —
```bash
cd ~/Projects/langs/go/ai-playbook && git add internal/autorun/run.go internal/autorun/run_test.go && git commit -m "feat(autorun): --with-env override tier in resolveEnv + undeclared-key warning"
```
Verify `git log -1 "--format=%G? %s"` == `G …`.

---

### Task 2: `launcher` — `--with-env` flag, parse, wire

**Files:**
- Modify: `internal/launcher/runcmd.go` (`runArgs`, `resolveRunArgs`, `autoRun`, new `parseWithEnv`)
- Test: `internal/launcher/runcmd_test.go`

**Interfaces:**
- Consumes: `autorun.RunConfig.EnvOverrides` (Task 1).
- Produces: `runArgs.EnvOverrides map[string]string`; `func parseWithEnv(raw string) (map[string]string, error)`.

**Context:** `resolveRunArgs` (`runcmd.go:362`) registers flags on a `flag.FlagSet`, validates source/mode combinations (each returns an error → the caller prints it and exits 2, `runcmd.go:81-84`), then builds `ra`. `autoRun` (`runcmd.go:223`) constructs the `RunConfig` literal.

- [ ] **Step 1: Write the failing tests** (add to `internal/launcher/runcmd_test.go`):

```go
func TestParseWithEnv(t *testing.T) {
	// inline JSON
	m, err := parseWithEnv(`{"A":"1","B":"two"}`)
	if err != nil || m["A"] != "1" || m["B"] != "two" {
		t.Fatalf("inline: m=%v err=%v", m, err)
	}
	// leading whitespace still detected as inline
	if m, err := parseWithEnv("  {\"A\":\"1\"}"); err != nil || m["A"] != "1" {
		t.Fatalf("ws-inline: m=%v err=%v", m, err)
	}
	// file path
	dir := t.TempDir()
	f := filepath.Join(dir, "env.json")
	if werr := os.WriteFile(f, []byte(`{"C":"3"}`), 0o644); werr != nil {
		t.Fatal(werr)
	}
	if m, err := parseWithEnv(f); err != nil || m["C"] != "3" {
		t.Fatalf("file: m=%v err=%v", m, err)
	}
	// malformed JSON
	if _, err := parseWithEnv(`{bad`); err == nil {
		t.Error("malformed inline JSON must error")
	}
	// non-string value
	if _, err := parseWithEnv(`{"A":1}`); err == nil {
		t.Error("non-string value must error")
	}
	// unreadable file
	if _, err := parseWithEnv(filepath.Join(dir, "nope.json")); err == nil {
		t.Error("unreadable file must error")
	}
}

func TestResolveRunArgs_WithEnv(t *testing.T) {
	// --with-env requires --auto
	if _, err := resolveRunArgs([]string{"--file", "p.md", "--with-env", `{"A":"1"}`}); err == nil {
		t.Error("--with-env without --auto must error")
	}
	// --auto + inline JSON populates EnvOverrides
	ra, err := resolveRunArgs([]string{"--file", "p.md", "--auto", "--with-env", `{"A":"1"}`})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ra.EnvOverrides["A"] != "1" {
		t.Errorf("EnvOverrides = %v, want A=1", ra.EnvOverrides)
	}
	// bad JSON surfaces as an error (caller maps to exit 2)
	if _, err := resolveRunArgs([]string{"--file", "p.md", "--auto", "--with-env", `{bad`}); err == nil {
		t.Error("bad --with-env JSON must error")
	}
}
```
Ensure the test file imports `os` and `path/filepath` (add if missing).

- [ ] **Step 2: Run to verify they fail** — `go test ./internal/launcher/ -run 'ParseWithEnv|ResolveRunArgs_WithEnv'` → FAIL/build error.

- [ ] **Step 3: Implement** in `internal/launcher/runcmd.go`:

Add to `runArgs`:
```go
	EnvOverrides   map[string]string // --with-env values (valid only with --auto)
```

Add the parser (place near `resolveRunArgs`):
```go
// parseWithEnv resolves a --with-env flag value into a name→value map. A value
// whose first non-space rune is '{' is parsed as inline JSON; otherwise it is a
// path to a JSON file. The JSON must be an object of string→string. Malformed
// JSON, a non-string value, or an unreadable file is an error (the caller maps
// it to the exit-2 usage path).
func parseWithEnv(raw string) (map[string]string, error) {
	data := []byte(raw)
	if !strings.HasPrefix(strings.TrimLeft(raw, " \t\r\n"), "{") {
		b, err := os.ReadFile(raw)
		if err != nil {
			return nil, fmt.Errorf("--with-env: %v", err)
		}
		data = b
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("--with-env: invalid JSON: %v", err)
	}
	return m, nil
}
```

In `resolveRunArgs`, register the flag (with the others):
```go
	var withEnv string
	fs.StringVar(&withEnv, "with-env", "", "with --auto, supply env var values as inline JSON or a JSON file path")
```
Add the auto-only validation (in the validation block, e.g. after the `assisted && auto` check):
```go
	if withEnv != "" && !autoMode {
		return runArgs{}, fmt.Errorf("--with-env is only valid with --auto")
	}
```
After `ra` is built (before `return ra, nil`):
```go
	if withEnv != "" {
		overrides, perr := parseWithEnv(withEnv)
		if perr != nil {
			return runArgs{}, perr
		}
		ra.EnvOverrides = overrides
	}
```

In `autoRun`, add to the `RunConfig` literal (after `AutoRollback:`):
```go
		EnvOverrides: ra.EnvOverrides,
```

Add `"encoding/json"` to `runcmd.go`'s imports (`os`, `strings`, `fmt`, `flag`, `io` are already imported — confirm).

- [ ] **Step 4: Run to verify they pass** — `go test ./internal/launcher/ -run 'ParseWithEnv|ResolveRunArgs'`; then `go test ./internal/launcher/`; `go build ./...`; `go vet ./...`.

- [ ] **Step 5: Commit** —
```bash
cd ~/Projects/langs/go/ai-playbook && git add internal/launcher/runcmd.go internal/launcher/runcmd_test.go && git commit -m "feat(launcher): run --with-env (JSON string or file) for --auto env values"
```
Verify `git log -1 "--format=%G? %s"` == `G …`.

---

### Task 3: docs — mention `--with-env`

**Files:**
- Modify: `examples/07-run-modes.md`, `examples/06-portable-and-env.md`

**Context:** ch.07's Auto-run TIP and ch.06's CI paragraph say auto mode needs the variables exported into the environment. Add that `--with-env` supplies them inline without exporting. Keep it short; do not restructure the chapters.

- [ ] **Step 1:** In `examples/07-run-modes.md`, extend the auto-run `[!TIP]` (the block that currently says to export required vars) with a sentence:
  > Or pass them inline with `--with-env` — a JSON object (`--with-env '{"PROJECT_ROOT":"/path"}'`) or a path to a JSON file. Values given this way take precedence over the environment; undeclared keys are ignored with a warning.

- [ ] **Step 2:** In `examples/06-portable-and-env.md`, in the paragraph describing `ai-playbook run --auto …` (the "runs unattended: it requires the variables already set in the environment" sentence), append:
  > — or supply them with `--with-env '{"PROJECT_ROOT":"…","DATA_DIR":"…"}'` (or `--with-env env.json`) instead of exporting.

- [ ] **Step 3: Verify** the two files still read cleanly (no broken fences/tables); no code gates needed for docs.

- [ ] **Step 4: Commit** —
```bash
cd ~/Projects/langs/go/ai-playbook && git add examples/07-run-modes.md examples/06-portable-and-env.md && git commit -m "docs(examples): document run --with-env for --auto env values"
```
Verify `git log -1 "--format=%G? %s"` == `G …`.

---

## Final verification (after all tasks)

- [ ] `cd ~/Projects/langs/go/ai-playbook && gofmt -l internal/autorun internal/launcher` empty; `go build ./... && go vet ./...` clean; `go run github.com/gordonklaus/ineffassign@v0.2.0 ./...` clean; `go test ./internal/autorun/ ./internal/launcher/` PASS.
- [ ] `go install ./cmd/ai-playbook`, then live-verify (non-project dir so nothing is pre-exported):
  - `ai-playbook run --auto --file examples/06-portable-and-env.md` → still errors listing the missing `PROJECT_ROOT`/`DATA_DIR`.
  - `ai-playbook run --auto --with-env '{"PROJECT_ROOT":"examples/projects/portable","DATA_DIR":"examples/projects/portable/data"}' --file examples/06-portable-and-env.md` → runs green.
  - Same with a `--with-env env.json` file path → runs green.
  - Add a bogus key (`{"NOPE":"x", …}`) → prints `with-env: ignoring undeclared variable NOPE` and still runs.
  - `ai-playbook run --with-env '{}' --file …` (no `--auto`) → usage error, exit 2. `--with-env '{bad'` with `--auto` → exit 2.

## Self-review notes (coverage vs spec)

- Override tier + precedence + empty-fallthrough + last-wins → Task 1 `resolveEnv`. Undeclared-key sorted warning → Task 1 `warnUndeclared`/`Run`. Flag + inline-vs-file + JSON + exit-2 errors + auto-only → Task 2 `parseWithEnv`/`resolveRunArgs`. Wiring → Task 2 `autoRun`. Docs → Task 3. No changes to interactive/assisted modes (out of scope).
