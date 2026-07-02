# `ai-playbook env` command (Implementation Plan)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `ai-playbook env <source>` prints a playbook's declared `env:` as a `--with-env`-compatible JSON object, resolving each value against the current environment and redacting sensitive ones to `""`.

**Architecture:** A new `internal/launcher/envcmd.go` mirrors `validate`'s source resolution, parses front matter, and emits `fm.Env` via a pure `resolveEnvJSON`. `internal/frontmatter` exports `IsRedactedMask` so the resolver can detect a build-time-masked default. `cmd/ai-playbook/main.go` dispatches the subcommand.

**Tech Stack:** Go stdlib (`encoding/json`, `flag`, `sort`); `internal/frontmatter` (`Redact`, `EnvValue`, `Parse`).

## Global Constraints

- Module `github.com/Townk/ai-playbook`. Repo at `~/Projects/langs/go/ai-playbook` (Bash cwd starts elsewhere — always `cd ~/Projects/langs/go/ai-playbook` / `git -C`).
- gpg-signed Conventional Commits (NEVER `--no-gpg-sign`; if signing times out STOP + report BLOCKED — user re-unlocks with `! echo x | gpg --clearsign`); verify `git log -1 "--format=%G?"` == `G` (quote the format — zsh globs a bare `%G?`). NO `Co-Authored-By`/AI-attribution trailers. `git add` explicit paths only. Commit only; do NOT push.
- Work on a NEW branch `feat/env-command` off `master`.
- Redaction MUST reuse `frontmatter.Redact` — do NOT reinvent secret detection. Sensitive → emit `""` (never the `<redacted>` literal, never the live secret).
- Output: pretty JSON (`json.MarshalIndent(_, "", "  ")`) + trailing newline to stdout; `{}` when no env; exit 0. Source errors → exit 2 (mirroring `validate`).
- Gates: `gofmt -l`, `go build ./...`, `go vet ./...`, `ineffassign@v0.2.0` (clean), `go test` on touched packages.

---

### Task 1: `frontmatter.IsRedactedMask`

**Files:**
- Modify: `internal/frontmatter/frontmatter.go`
- Test: `internal/frontmatter/frontmatter_test.go` (or the existing redact test file)

**Interfaces:**
- Produces (consumed by Task 2): `func IsRedactedMask(s string) bool`.

**Context:** `redactedMask = "<redacted>"` is an unexported const (`frontmatter.go:65`) that `Redact` substitutes for sensitive values. Task 2 (in the `launcher` package) needs to detect a declared default that is already this mask. Export a predicate rather than the const.

- [ ] **Step 1: Write the failing test** (add to the frontmatter test file):
```go
func TestIsRedactedMask(t *testing.T) {
	if !IsRedactedMask("<redacted>") {
		t.Error("the mask must be recognized")
	}
	if IsRedactedMask("") || IsRedactedMask("real-value") || IsRedactedMask("<redacted> ") {
		t.Error("only the exact mask string is the mask")
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `cd ~/Projects/langs/go/ai-playbook && go test ./internal/frontmatter/ -run TestIsRedactedMask` → FAIL (undefined).

- [ ] **Step 3: Implement** — add after the `redactedMask` const (or near `Redact`):
```go
// IsRedactedMask reports whether s is exactly the placeholder Redact substitutes
// for a sensitive value. Callers use it to detect a front-matter default that was
// already redacted at build time.
func IsRedactedMask(s string) bool { return s == redactedMask }
```

- [ ] **Step 4: Run to verify it passes** — `go test ./internal/frontmatter/ -run TestIsRedactedMask`; then `go test ./internal/frontmatter/`; `go build ./...`.

- [ ] **Step 5: Commit** —
```bash
cd ~/Projects/langs/go/ai-playbook && git add internal/frontmatter/frontmatter.go internal/frontmatter/frontmatter_test.go && git commit -m "feat(frontmatter): export IsRedactedMask predicate"
```
(Use whichever existing `_test.go` file in `internal/frontmatter` holds the redaction tests if you added the test there instead — stage that file.) Verify `git log -1 "--format=%G? %s"` == `G …`.

---

### Task 2: `internal/launcher/envcmd.go` + dispatch

**Files:**
- Create: `internal/launcher/envcmd.go`
- Modify: `cmd/ai-playbook/main.go` (dispatch + `usage()`)
- Test: `internal/launcher/envcmd_test.go`

**Interfaces:**
- Consumes: `frontmatter.Parse`, `frontmatter.Redact`, `frontmatter.IsRedactedMask` (Task 1), `frontmatter.EnvValue`, and the package seam `storeLoadFn` (already used by `validate`/`run` for slug loading).
- Produces: `func EnvMain() int`.

**Context:** `resolveValidateArgs` (`validatecmd.go:283`) is the template for `resolveEnvArgs` — exactly one of {bare positional, `--file`}, same error strings, but NO extra flags. `ValidateMain` (`validatecmd.go:64`) is the template for the source→content→`frontmatter.Parse` flow (file via `os.ReadFile`, slug via `storeLoadFn`; both source errors → exit 2). Keep the resolve+redact logic in a PURE `resolveEnvJSON(vars, getenv)` so it's unit-testable without touching the process env or `os.Args`.

- [ ] **Step 1: Write the failing tests** (`internal/launcher/envcmd_test.go`). Reuse the launcher test helpers `withArgs`, `swap`, the temp-file writer, and the stdout-capture helper already in the package (grep the `_test.go` files for `func withArgs`, `func swap`, `os.Pipe`, and the temp writer; reuse them rather than redefining):
```go
func TestResolveEnvJSON(t *testing.T) {
	vars := map[string]frontmatter.EnvValue{
		"PLAIN":     {Value: "default-plain"},           // no env → default
		"FROM_ENV":  {Value: "default"},                 // env set → env wins
		"API_KEY":   {Value: "<redacted>"},              // secret name → "" even if exported
		"MASKED_DEF": {Value: "<redacted>"},             // build-time masked, env unset → ""
		"HIENTROPY": {Value: "safe-default"},            // non-secret name, high-entropy env value → ""
	}
	getenv := func(name string) string {
		switch name {
		case "FROM_ENV":
			return "live-value"
		case "API_KEY":
			return "sk-supersecrettoken1234567890"
		case "HIENTROPY":
			return "Xk9$2mQ7!pL4wZ8#vR1nB6" // mixed-charset high-entropy → looksLikeSecret
		}
		return ""
	}
	out, redacted := resolveEnvJSON(vars, getenv)
	if out["PLAIN"] != "default-plain" {
		t.Errorf("PLAIN = %q, want default", out["PLAIN"])
	}
	if out["FROM_ENV"] != "live-value" {
		t.Errorf("FROM_ENV = %q, want live env value", out["FROM_ENV"])
	}
	for _, k := range []string{"API_KEY", "MASKED_DEF", "HIENTROPY"} {
		if out[k] != "" {
			t.Errorf("%s must be redacted to \"\", got %q", k, out[k])
		}
	}
	// redacted names sorted, and exactly the three sensitive ones
	want := []string{"API_KEY", "HIENTROPY", "MASKED_DEF"}
	if !reflect.DeepEqual(redacted, want) {
		t.Errorf("redacted = %v, want %v", redacted, want)
	}
}

func TestResolveEnvArgs(t *testing.T) {
	if ra, err := resolveEnvArgs([]string{"--file", "p.md"}); err != nil || ra.Kind != "file" || ra.Value != "p.md" {
		t.Fatalf("--file: ra=%+v err=%v", ra, err)
	}
	if ra, err := resolveEnvArgs([]string{"my-slug"}); err != nil || ra.Kind != "playbook" || ra.Value != "my-slug" {
		t.Fatalf("bare: ra=%+v err=%v", ra, err)
	}
	if _, err := resolveEnvArgs([]string{}); err == nil {
		t.Error("zero sources must error")
	}
	if _, err := resolveEnvArgs([]string{"slug", "--file", "p.md"}); err == nil {
		t.Error("two sources must error")
	}
}

func TestEnvMain_FileSmoke(t *testing.T) {
	pb := "---\nname: N\ndescription: D\ncategory: C\ncreated: 2026-01-01\nenv:\n  FOO:\n    value: bar\n---\n\n# T\n\n```bash {id=a}\ntrue\n```\n"
	path := writeValidateTemp(t, "envpb.md", pb) // reuse the launcher temp-file helper
	withArgs(t, []string{"ai-playbook", "env", "--file", path})
	out := captureStdout(t, func() { // reuse the package's os.Pipe capture helper
		if code := EnvMain(); code != 0 {
			t.Fatalf("EnvMain exit %d, want 0", code)
		}
	})
	var got map[string]string
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, out)
	}
	if got["FOO"] != "bar" {
		t.Errorf("FOO = %q, want bar", got["FOO"])
	}
}
```
If the launcher package's capture helper has a different name/signature than `captureStdout`, adapt the smoke test to it; if none exists, capture `os.Stdout` via `os.Pipe` inside the test. Ensure imports: `encoding/json`, `reflect`, and `github.com/Townk/ai-playbook/internal/frontmatter`.

- [ ] **Step 2: Run to verify they fail** — `go test ./internal/launcher/ -run 'ResolveEnvJSON|ResolveEnvArgs|EnvMain_FileSmoke'` → FAIL/build error.

- [ ] **Step 3: Implement** `internal/launcher/envcmd.go`:
```go
package launcher

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/Townk/ai-playbook/internal/frontmatter"
)

// envArgs is resolveEnvArgs's parsed result: the single playbook source.
type envArgs struct {
	Kind, Value string // "file" | "playbook"
}

// resolveEnvArgs resolves the single playbook source from the `env` arguments —
// exactly one of {bare positional, --file}, mirroring resolveValidateArgs.
func resolveEnvArgs(args []string) (envArgs, error) {
	fs := flag.NewFlagSet("env", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var file string
	fs.StringVar(&file, "file", "", "path to a markdown file")
	if perr := fs.Parse(args); perr != nil {
		return envArgs{}, perr
	}
	rest := fs.Args()
	if len(rest) > 1 {
		return envArgs{}, fmt.Errorf("specify exactly one of <slug> or --file")
	}
	positional := ""
	if len(rest) == 1 {
		positional = rest[0]
	}
	count := 0
	for _, s := range []string{file, positional} {
		if s != "" {
			count++
		}
	}
	switch {
	case count == 0:
		return envArgs{}, fmt.Errorf("specify a playbook: env <slug> | --file <path>")
	case count > 1:
		return envArgs{}, fmt.Errorf("specify exactly one of <slug> or --file")
	}
	if file != "" {
		return envArgs{Kind: "file", Value: file}, nil
	}
	return envArgs{Kind: "playbook", Value: positional}, nil
}

// resolveEnvJSON resolves each declared var against getenv (env value when set,
// else the declared default) and redacts sensitive ones to "". Returns the
// name→value map and the sorted names of the redacted vars. Pure — getenv is
// injected so tests never touch the process environment.
func resolveEnvJSON(vars map[string]frontmatter.EnvValue, getenv func(string) string) (map[string]string, []string) {
	out := make(map[string]string, len(vars))
	var redacted []string
	for name, ev := range vars {
		raw := ev.Value
		if v := getenv(name); v != "" {
			raw = v
		}
		if _, isRedacted := frontmatter.Redact(name, raw); isRedacted || frontmatter.IsRedactedMask(raw) {
			out[name] = ""
			redacted = append(redacted, name)
			continue
		}
		out[name] = raw
	}
	sort.Strings(redacted)
	return out, redacted
}

// EnvMain implements `ai-playbook env <source>`: it resolves a playbook's
// declared env: map against the current environment and prints it as a
// --with-env-compatible JSON object on stdout (sensitive values emitted empty and
// listed on stderr). Source-resolution errors return exit 2, mirroring validate.
func EnvMain() int {
	ra, err := resolveEnvArgs(os.Args[2:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook env: %v\n", err)
		return 2
	}

	var content string
	switch ra.Kind {
	case "file":
		data, rerr := os.ReadFile(ra.Value)
		if rerr != nil {
			fmt.Fprintf(os.Stderr, "ai-playbook env: %v\n", rerr)
			return 2
		}
		content = string(data)
	case "playbook":
		_, body, lerr := storeLoadFn(ra.Value)
		if lerr != nil {
			fmt.Fprintf(os.Stderr, "ai-playbook env: %v\n", lerr)
			return 2
		}
		content = body
	}

	fm, _, _ := frontmatter.Parse(content)
	out, redacted := resolveEnvJSON(fm.Env, os.Getenv)

	data, merr := json.MarshalIndent(out, "", "  ")
	if merr != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook env: %v\n", merr)
		return 1
	}
	fmt.Fprintln(os.Stdout, string(data))
	if len(redacted) > 0 {
		fmt.Fprintf(os.Stderr, "env: redacted %d sensitive variable(s): %s\n", len(redacted), strings.Join(redacted, ", "))
	}
	return 0
}
```
NOTE: the final `strings.Join` needs `"strings"` in the import block — add it. (`json.MarshalIndent` of an empty/nil map yields `{}`, satisfying the no-env case.)

Wire the dispatch in `cmd/ai-playbook/main.go` — add a case alongside `validate`:
```go
	case "env":
		os.Exit(launcher.EnvMain())
```
and add `env` to the `usage()` string, next to `validate`, e.g.:
```
… |validate [<slug>|--file <path>]|env [<slug>|--file <path>]| …
```

- [ ] **Step 4: Run to verify they pass** — `go test ./internal/launcher/ -run 'ResolveEnvJSON|ResolveEnvArgs|EnvMain_FileSmoke'`; then `go test ./internal/launcher/`; `go build ./...`; `go vet ./...`; `gofmt -l internal/launcher cmd/ai-playbook`.

- [ ] **Step 5: Commit** —
```bash
cd ~/Projects/langs/go/ai-playbook && git add internal/launcher/envcmd.go internal/launcher/envcmd_test.go cmd/ai-playbook/main.go && git commit -m "feat(launcher): ai-playbook env — dump declared env as --with-env JSON"
```
Verify `git log -1 "--format=%G? %s"` == `G …`.

---

### Task 3: docs — the `env` round-trip

**Files:**
- Modify: `examples/07-run-modes.md`

**Context:** ch.07's auto-run `[!TIP]` (Task from the `--with-env` work) already introduces `--with-env`. Add a short follow-on paragraph (right after that TIP) showing how to generate the JSON with `env`.

- [ ] **Step 1:** After the `--with-env` `[!TIP]` block, add a short paragraph:
  > To scaffold that JSON, run `ai-playbook env --file <playbook>` (or `ai-playbook env <slug>`): it prints the declared variables as a JSON object — each resolved from your current environment, with sensitive values (tokens, keys, passwords) left empty and noted on stderr. Redirect it to a file, fill in the blanks, and pass it back:
  > ```bash {static}
  > ai-playbook env --file examples/06-portable-and-env.md > env.json
  > ai-playbook run --auto --with-env env.json --file examples/06-portable-and-env.md
  > ```

- [ ] **Step 2: Verify** the chapter still reads cleanly (fences/tables intact); no code gates for docs.

- [ ] **Step 3: Commit** —
```bash
cd ~/Projects/langs/go/ai-playbook && git add examples/07-run-modes.md && git commit -m "docs(examples): document the ai-playbook env round-trip"
```
Verify `git log -1 "--format=%G? %s"` == `G …`.

---

## Final verification (after all tasks)

- [ ] `cd ~/Projects/langs/go/ai-playbook && gofmt -l internal/frontmatter internal/launcher cmd/ai-playbook` empty; `go build ./... && go vet ./...` clean; `go run github.com/gordonklaus/ineffassign@v0.2.0 ./...` clean; `go test ./internal/frontmatter/ ./internal/launcher/` PASS.
- [ ] `go install ./cmd/ai-playbook`, then live-verify:
  - `ai-playbook env --file examples/06-portable-and-env.md` → JSON with `PROJECT_ROOT`/`DATA_DIR` (resolved from env or their declared defaults), keys sorted, exit 0.
  - Export a fake secret var only if a playbook declares one; otherwise construct a temp playbook with `env: { API_KEY: { value: x } }` and confirm `API_KEY` comes out `""` with a `env: redacted 1 sensitive variable(s): API_KEY` note on stderr while stdout stays valid JSON (`… 2>/dev/null | jq .`).
  - Round-trip: `ai-playbook env --file <pb> > env.json` then `ai-playbook run --auto --with-env env.json --file <pb>` runs green.
  - `ai-playbook env` (no source) → usage error exit 2; `ai-playbook env --file nope.md` → exit 2.

## Self-review notes (coverage vs spec)

- `IsRedactedMask` → Task 1. Command + source resolution + resolve-against-env + redact-to-"" + sorted stderr note + `{}`-on-empty + dispatch/usage → Task 2 (`resolveEnvArgs`/`resolveEnvJSON`/`EnvMain`). Docs round-trip → Task 3. Redaction reuses `frontmatter.Redact`; `nonPortableEnv` needs no handling (never in `fm.Env`). Non-goals (no `why`, no execution, no project-bind resolution) respected.
