# `ai-playbook validate` (Implementation Plan)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `ai-playbook validate [<slug> | --file <path>]` — a CI-oriented playbook linter: deterministic structural checks (front matter, `needs=`, cycles, duplicate ids, fence balance) that set the exit code, plus an advisory one-shot AI prose review that never fails the build.

**Architecture:** A pure leaf package `internal/validate` holds the `Finding` model + the deterministic `Check(...)` (imports only `internal/frontmatter` + stdlib — never `internal/ui`). `internal/author` gains an exported one-shot `ReviewOnce` (reusing the `ClassifyRequest`/`runMetadataOnce` no-tools path). `internal/launcher/validatecmd.go` glues it: read the source (file or `<slug>` via `store.Load`), `frontmatter.Parse`, `ui.Render`→`[]validate.Block`, `validate.Check`, the AI pass (graceful no-backend skip, `--no-ai` opt-out), then a plain-text report + exit code. `cmd/ai-playbook/main.go` dispatches `validate`.

**Tech Stack:** Go; `internal/frontmatter` (`Parse`/`FrontMatter`), `internal/ui` (`Render`/`Block` — launcher-side only), `internal/author` (`runMetadataOnce`/`ClassifyRequest` shape, `claudeModel`), `internal/store` (`Load`), `internal/launcher`, `cmd/ai-playbook`.

## Global Constraints

- Module `github.com/Townk/ai-playbook`. gpg-signed Conventional Commits (`git commit`, NEVER `--no-gpg-sign`); verify `git log -1 --format=%G?` == `G`. **NO `Co-Authored-By`/AI-attribution trailers.** `git add` explicit paths only. Commit only at each task's final step.
- **Repo at `~/Projects/langs/go/ai-playbook`** — all `go`/`git` run there (`cd …` or `git -C …`); the default shell cwd is a different repo.
- **`internal/validate` is a LEAF** — imports only `internal/frontmatter` + Go stdlib. It MUST NOT import `internal/ui` (the launcher does the `ui.Render` parse and converts `ui.Block`→`validate.Block`). This keeps the checks unit-testable in isolation.
- **Exit codes:** `0` = no `Error` findings (warnings/AI advisory); `1` = ≥1 `Error` finding; `2` = usage/IO error (bad flags, unreadable file, unknown slug). The AI review NEVER changes the exit code.
- **AI pass is advisory + graceful:** attempted after Layer 1 unless `--no-ai`; on a no-backend error (Claude CLI absent/unauth — error string matches "executable file not found"/"not found"/"no backend") print a one-line skip note and continue. It never fails the run.
- Verification gates (in the repo): `gofmt -l` (empty on touched dirs), `go build ./...`, `go vet ./...`, `go run github.com/gordonklaus/ineffassign@v0.2.0 ./...` (clean — catches what `go test`/`build`/`gofmt` miss), `go test` on touched packages.

---

### Task 1: `internal/validate` — Finding model + block/front-matter checks

**Files:**
- Create: `internal/validate/validate.go`, `internal/validate/validate_test.go`

**Interfaces:**
- Produces (consumed by Tasks 2, 4):
```go
package validate

import "github.com/Townk/ai-playbook/internal/frontmatter"

type Severity int
const ( Error Severity = iota; Warning )

type Finding struct {
	Severity Severity
	Check    string // "front-matter"|"duplicate-id"|"needs"|"cycle"|"fence"|"runnable"|"lang"
	Message  string
	Where    string // block id | "line N" | "front matter"
}

// Block is validate's DTO — the launcher converts ui.Block into this (validate never imports ui).
type Block struct {
	ID     string
	Type   string // "shell"|"run"|"diff"|"static"|"create"
	Lang   string
	Needs  []string
	Static bool
}

// Check runs every deterministic check and returns findings (nil ⇔ structurally clean).
// rawBody = the front-matter-stripped markdown (Task 2's fence scan uses it); fmOK =
// frontmatter.Parse's ok. This task implements every check EXCEPT fence balance (Task 2).
func Check(rawBody string, fm frontmatter.FrontMatter, fmOK bool, blocks []Block) []Finding

// HasError reports whether any finding is an Error (drives the exit code).
func HasError(findings []Finding) bool
```

**Context:** This task implements, inside `Check`, these checks (append `Finding`s; leave a clearly-marked spot where Task 2 will add the fence scan):
1. **front-matter** (Error): if `!fmOK` → one `{Error,"front-matter","missing or malformed front matter (a playbook needs a leading --- YAML block)","front matter"}` and SKIP the required-key loop. Else for each of `fm.Name, fm.Description, fm.Category, fm.Created` that is `strings.TrimSpace(...)==""` → `{Error,"front-matter",`missing required key "<k>"`,"front matter"}` (keys reported as `name`/`description`/`category`/`created`).
2. **duplicate-id** (Error): `count := map[string]int{}` over `blocks`; for each id with count>1 (report once) → `{Error,"duplicate-id",`block id "<id>" is used <n> times`,<id>}`.
3. **needs** (Error): `idSet := map[string]bool` of every `b.ID`; for each block, each `need` in `b.Needs` not in `idSet` → `{Error,"needs",`block "<id>" needs "<need>", which does not exist`,<id>}`.
4. **runnable** (Warning): if no block has `!Static` (or `len(blocks)==0`) → `{Warning,"runnable","no runnable blocks — nothing to execute",""}`.
5. **lang** (Warning): for each `!Static` block with `strings.TrimSpace(Lang)==""` → `{Warning,"lang",`block "<id>" has no language`,<id>}`.
(Cycle detection is Task 1 too — see below; fence balance is Task 2.)

Add **cycle detection** (Error) here as well (it's a `needs=` graph check): DFS over `id → [needs that exist in idSet]`; on a back-edge, emit ONE `{Error,"cycle",`needs= cycle: a → b → … → a`,""}` reporting the cycle path (colors/order not critical — the path is). Guard against reporting the same cycle multiple times (track visited/in-stack; emit the first cycle found, or dedupe by the sorted id-set).

`HasError`: `for _, f := range findings { if f.Severity==Error { return true } }; return false`.

- [ ] **Step 1: Write the failing tests**

```go
package validate

import ("testing"; "github.com/Townk/ai-playbook/internal/frontmatter")

func fm(name, desc, cat, created string) frontmatter.FrontMatter {
	return frontmatter.FrontMatter{Name: name, Description: desc, Category: cat, Created: created}
}
func has(fs []Finding, check string, sev Severity) bool {
	for _, f := range fs { if f.Check == check && f.Severity == sev { return true } }
	return false
}

func TestCheck_FrontMatterRequiredKeys(t *testing.T) {
	// missing front matter entirely
	if fs := Check("", frontmatter.FrontMatter{}, false, nil); !has(fs, "front-matter", Error) {
		t.Fatal("!fmOK must yield a front-matter error")
	}
	// present but missing "created"
	fs := Check("", fm("N", "D", "C", ""), true, []Block{{ID: "a", Type: "shell"}})
	if !has(fs, "front-matter", Error) { t.Fatal("empty required key must be an error") }
	// all keys present → no front-matter error
	fs = Check("", fm("N", "D", "C", "2026-01-01"), true, []Block{{ID: "a", Type: "shell", Lang: "bash"}})
	if has(fs, "front-matter", Error) { t.Fatalf("complete front matter must not error: %+v", fs) }
}

func TestCheck_DanglingNeeds(t *testing.T) {
	blocks := []Block{{ID: "a", Type: "shell", Lang: "bash"}, {ID: "b", Type: "shell", Lang: "bash", Needs: []string{"nope"}}}
	fs := Check("", fm("N", "D", "C", "x"), true, blocks)
	if !has(fs, "needs", Error) { t.Fatal("dangling needs= must error") }
}

func TestCheck_DuplicateId(t *testing.T) {
	blocks := []Block{{ID: "a", Type: "shell", Lang: "bash"}, {ID: "a", Type: "shell", Lang: "bash"}}
	if fs := Check("", fm("N", "D", "C", "x"), true, blocks); !has(fs, "duplicate-id", Error) {
		t.Fatal("duplicate id must error")
	}
}

func TestCheck_Cycle(t *testing.T) {
	blocks := []Block{{ID: "a", Type: "shell", Lang: "bash", Needs: []string{"b"}}, {ID: "b", Type: "shell", Lang: "bash", Needs: []string{"a"}}}
	if fs := Check("", fm("N", "D", "C", "x"), true, blocks); !has(fs, "cycle", Error) {
		t.Fatal("a→b→a must be a cycle error")
	}
}

func TestCheck_Warnings(t *testing.T) {
	// all static → no-runnable warning; a missing lang → lang warning
	blocks := []Block{{ID: "a", Type: "static", Static: true}}
	if fs := Check("", fm("N", "D", "C", "x"), true, blocks); !has(fs, "runnable", Warning) {
		t.Fatal("all-static must warn no-runnable")
	}
	blocks = []Block{{ID: "a", Type: "shell", Lang: ""}}
	fs := Check("", fm("N", "D", "C", "x"), true, blocks)
	if !has(fs, "lang", Warning) { t.Fatal("missing lang must warn") }
	if HasError(fs) { t.Fatal("warnings-only must not report HasError") }
}

func TestCheck_Clean(t *testing.T) {
	blocks := []Block{{ID: "a", Type: "shell", Lang: "bash"}, {ID: "b", Type: "shell", Lang: "bash", Needs: []string{"a"}}}
	if fs := Check("", fm("N", "D", "C", "x"), true, blocks); len(fs) != 0 {
		t.Fatalf("clean playbook must have no findings; got %+v", fs)
	}
}
```

- [ ] **Step 2: Run to verify they fail** — `cd ~/Projects/langs/go/ai-playbook && go test ./internal/validate/` → FAIL (undefined).
- [ ] **Step 3: Implement** `internal/validate/validate.go` per Interfaces + Context (leave a `// fence balance: Task 2` marker inside `Check`).
- [ ] **Step 4: Run to verify they pass** — `go test ./internal/validate/`.
- [ ] **Step 5: Commit** — `git add internal/validate/validate.go internal/validate/validate_test.go && git commit -m "feat(validate): Finding model + front-matter/needs/cycle/dup-id/warning checks"`

---

### Task 2: `internal/validate` — fence-balance scan

**Files:**
- Modify: `internal/validate/validate.go` (add the fence scan into `Check`), `internal/validate/validate_test.go`

**Interfaces:**
- Consumes: `Check`'s `rawBody` param (Task 1). No new exported API — adds `{Error,"fence",…}` findings.

**Context:** Add a raw-line scan of `rawBody` for unbalanced ``` ``` ```/`~~~` code fences (net-new — the UI parser silently *repairs* them; reference the fence-tracking in `internal/ui/render.go:202-242` `normalizeFences`, but REPORT don't repair). CommonMark-ish rule:
- Split `rawBody` into lines. Track `inFence bool`, `fenceChar byte` (backtick or `~`), `fenceLen int`, `openLine int` (1-based).
- Not in a fence: a line whose first non-space (≤3 leading spaces) run is ≥3 of the same fence char opens a fence — record `fenceChar`, `fenceLen` (the run length), `openLine`; `inFence=true`. (An opening ``` may carry an info string like `bash {id=a}` — that's fine.)
- In a fence: a line that is (after ≤3 leading spaces) ONLY a run of `fenceChar` of length ≥ `fenceLen` with no trailing non-space info string closes it — `inFence=false`.
- At EOF: if `inFence` → `{Error,"fence",`unclosed code fence opened at line <openLine>`,`line <openLine>`}`.
Front-matter `---` is not a code fence, so it's ignored. Keep this in a helper `fenceFindings(rawBody string) []Finding` and call it from `Check`.

- [ ] **Step 1: Write the failing tests**

```go
func TestCheck_FenceBalance(t *testing.T) {
	ok := fm("N", "D", "C", "x")
	// balanced → no fence finding
	balanced := "# T\n\n```bash\ntrue\n```\n"
	if fs := Check(balanced, ok, true, []Block{{ID: "a", Type: "shell", Lang: "bash"}}); has(fs, "fence", Error) {
		t.Fatalf("balanced fences must not error: %+v", fs)
	}
	// unclosed → fence error
	unclosed := "# T\n\n```bash\ntrue\n"
	if fs := Check(unclosed, ok, true, nil); !has(fs, "fence", Error) {
		t.Fatal("an unclosed ``` fence must error")
	}
	// tilde fence, closed by a longer run → balanced
	tilde := "~~~\nx\n~~~\n"
	if fs := Check(tilde, ok, true, nil); has(fs, "fence", Error) {
		t.Fatalf("balanced tilde fence must not error: %+v", fs)
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/validate/ -run FenceBalance` → FAIL.
- [ ] **Step 3: Implement** the `fenceFindings` scan + call it from `Check`.
- [ ] **Step 4: Run to verify it passes** — `go test ./internal/validate/`.
- [ ] **Step 5: Commit** — `git add internal/validate/validate.go internal/validate/validate_test.go && git commit -m "feat(validate): unbalanced-code-fence detection"`

---

### Task 3: `internal/author` — `ReviewOnce` one-shot

**Files:**
- Modify: `internal/author/` (add `ReviewOnce` — put it beside `runMetadataOnce`/`ClassifyRequest`, e.g. in `classify.go` or a new `review.go`), + a test file

**Interfaces:**
- Produces (consumed by Task 4): `func ReviewOnce(systemPrompt, userMessage string) (string, error)` — a one-shot text→text call on the authoring model, no MCP/tools.

**Context:** Mirror `ClassifyRequest` (`internal/author/classify.go:157`) — it sets `opts.Bare=true`, `opts.MCPConfigPath=""` (no tools backend), `opts.NoThinking=true`, and calls `runMetadataOnce(systemPrompt, userMessage, opts) (string, error)` (`internal/author/metadata.go:113`). `ReviewOnce` does the same but returns the raw text (no JSON parse) and does NOT need the classify retry loop (one attempt is fine; return the error to the caller so it can detect a no-backend condition). Use the same `AuthorOptions` construction `ClassifyRequest` uses (read it for the exact fields — model via `claudeModel()`/the default, `Bare`, `MCPConfigPath`, `NoThinking`). Read `classify.go` + `metadata.go` before writing to copy the exact option shape.

- [ ] **Step 1: Write the failing test** — reuse the existing author test seam (`ClassifyRequest`/`runMetadataOnce` are tested with a fake harness/agent — read `internal/author/*_test.go` to find the seam, e.g. a swappable `RunHarnessEvents`/agent). Assert:

```go
func TestReviewOnce_ReturnsHarnessText(t *testing.T) {
	// swap the harness/agent seam so runMetadataOnce drains fake events → "looks good".
	// (Mirror the existing ClassifyRequest test's fake wiring.)
	got, err := ReviewOnce("you are a reviewer", "the playbook body")
	if err != nil { t.Fatalf("err: %v", err) }
	if got == "" { t.Fatal("ReviewOnce must return the harness text") }
}
```
(Adapt to the real author test seam; if `ClassifyRequest`'s test swaps a package var, reuse it. If there's no seam, add one mirroring how classify is tested.)

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/author/ -run ReviewOnce` → FAIL.
- [ ] **Step 3: Implement** `ReviewOnce`.
- [ ] **Step 4: Run to verify it passes** — `go test ./internal/author/`.
- [ ] **Step 5: Commit** — `git add internal/author/ && git commit -m "feat(author): ReviewOnce one-shot text review (no MCP)"` (stage only the files you added/changed).

---

### Task 4: launcher `validatecmd` + dispatch + AI wiring

**Files:**
- Create: `internal/launcher/validatecmd.go`, `internal/launcher/validatecmd_test.go`
- Modify: `cmd/ai-playbook/main.go` (dispatch case + `usage()` line)

**Interfaces:**
- Consumes: `validate.Check`/`validate.HasError`/`validate.Block`/`validate.Finding` (Tasks 1-2), `author.ReviewOnce` (Task 3), `ui.Render`, `frontmatter.Parse`, `store.Load` (via `storeLoadFn`).
- Produces: `func ValidateMain() int`; `resolveValidateArgs(args []string) (kind, value string, noAI bool, err error)`; seam `var reviewFn = author.ReviewOnce`.

**Context:**
- `resolveValidateArgs` — mirror `resolveRunArgs` (`runcmd.go`): `flag.NewFlagSet("validate", flag.ContinueOnError)` with `--file` + a bare positional `<slug>` (exactly one; zero/both → usage error) + `fs.BoolVar(&noAI,"no-ai",false,…)`. Returns `("file",path,noAI,nil)` / `("playbook",slug,noAI,nil)`.
- `ValidateMain`:
  1. `ra..., err := resolveValidateArgs(os.Args[2:])`; err → `fmt.Fprintf(os.Stderr,…)`, return `2`.
  2. Load source → `content string` + a display name: `kind=="file"` → `os.ReadFile(value)` (err → stderr, return 2); `kind=="playbook"` → `_, body, err := storeLoadFn(value)` (err → stderr, return 2). Display name = the file path or the slug.
  3. `fm, body, ok := frontmatter.Parse(content)`.
  4. `_, _, uiBlocks := ui.Render(body, 80, nil, "")`; convert each `ui.Block`→`validate.Block{ID,Type,Lang,Needs,Static}`.
  5. `findings := validate.Check(body, fm, ok, blocks)`.
  6. **AI pass** (unless `noAI`): `text, err := reviewFn(<systemPrompt>, body)`. On success → capture `text`. On error: if it looks like a no-backend error (check the error string for "executable file not found"/"not found"/"no backend" — a small `isNoBackend(err)` helper in this file, mirroring `internal/ui/results.go:14`) → set an "AI review skipped — no model backend (install + authenticate the Claude CLI, or set AI_PLAYBOOK_MODEL)" note; else set an "AI review failed: <err>" note. Never abort. System prompt: a concise reviewer instruction (inconsistencies, missing/needed callouts, non-idempotent/destructive/non-reversible steps; be brief; say so if clean).
  7. **Report** (plain text to stdout): print a header (`✗ N problems in <name>` when errors, else `✓ <name>: structurally valid` or `… valid (K warnings)`), then errors, then warnings (each `LEVEL  <check>  <message>  (<where>)`), then the `AI review:` block (the text or the skip/fail note) when the AI pass ran.
  8. **Exit:** `if validate.HasError(findings) { return 1 }; return 0`.
- `cmd/ai-playbook/main.go`: add `case "validate": os.Exit(launcher.ValidateMain())` to the dispatch switch + a `usage()` line `  validate [<slug>|--file <path>]   structural + AI review of a playbook`.

**Context (test seams):** `runcmd_test.go` shows the pattern — `storeLoadFn`/`uiMainFn` are swappable package vars; add `var reviewFn = author.ReviewOnce` so the test stubs the AI pass without a live model. Reuse the temp-`.md` + `os.Args` swap patterns.

- [ ] **Step 1: Write the failing tests**

```go
func TestResolveValidateArgs(t *testing.T) {
	if k, v, _, err := resolveValidateArgs([]string{"--file", "x.md"}); err != nil || k != "file" || v != "x.md" {
		t.Fatalf("--file: %s/%s err=%v", k, v, err)
	}
	if k, v, _, err := resolveValidateArgs([]string{"myslug"}); err != nil || k != "playbook" || v != "myslug" {
		t.Fatalf("slug: %s/%s err=%v", k, v, err)
	}
	if _, _, no, _ := resolveValidateArgs([]string{"--no-ai", "s"}); !no { t.Error("--no-ai must parse") }
	if _, _, _, err := resolveValidateArgs(nil); err == nil { t.Error("zero sources must error") }
	if _, _, _, err := resolveValidateArgs([]string{"s", "--file", "x.md"}); err == nil { t.Error("two sources must error") }
}

func TestValidateMain_CleanVsError(t *testing.T) {
	defer swap(&reviewFn, func(_, _ string) (string, error) { return "looks good", nil })()
	// clean playbook temp file → exit 0
	clean := "---\nname: N\ndescription: D\ncategory: C\ncreated: 2026-01-01\n---\n\n# T\n\n```bash {id=a}\ntrue\n```\n"
	// … write to a temp .md, set os.Args to {"ai-playbook","validate","--file",path} …
	if code := ValidateMain(); code != 0 { t.Fatalf("clean → exit %d, want 0", code) }
	// a dangling needs= → exit 1
	bad := "---\nname: N\ndescription: D\ncategory: C\ncreated: x\n---\n\n# T\n\n```bash {id=a needs=ghost}\ntrue\n```\n"
	// … write, set os.Args … 
	if code := ValidateMain(); code != 1 { t.Fatalf("dangling needs → exit %d, want 1", code) }
}

func TestValidateMain_NoAISkip(t *testing.T) {
	var called bool
	defer swap(&reviewFn, func(_, _ string) (string, error) { called = true; return "", nil })()
	// clean file + --no-ai → reviewFn not called, exit 0
	// … write, os.Args {"…","validate","--no-ai","--file",path} …
	if code := ValidateMain(); code != 0 || called { t.Fatalf("--no-ai must skip the AI pass (called=%v, code=%d)", called, code) }
}
```
(`swap` = the launcher tests' var-swap helper; add one if absent. Adapt the temp-md/os.Args scaffolding to the existing `runcmd_test.go` helpers.)

- [ ] **Step 2: Run to verify they fail** — `go test ./internal/launcher/ -run Validate` → FAIL.
- [ ] **Step 3: Implement** `validatecmd.go` + the `cmd/ai-playbook/main.go` dispatch/usage.
- [ ] **Step 4: Run to verify they pass** — `go test ./internal/launcher/`; `go build ./...`.
- [ ] **Step 5: Commit** — `git add internal/launcher/validatecmd.go internal/launcher/validatecmd_test.go cmd/ai-playbook/main.go && git commit -m "feat(validate): validate subcommand — resolve/report/exit + AI pass wiring"`

---

### Task 5: docs — flip ch.08 `validate` ⏳ + fix the AI-pass wording

**Files:**
- Modify: `examples/08-the-store.md`, `docs/guides/tutorial.md`

**Context:** `validate` now ships. In `examples/08-the-store.md` "## Validate a playbook": remove the `<!-- ⏳ … -->` marker; keep the three structural bullets (front-matter keys, `needs=` existence, fence balance); **fix the AI-pass line** — it currently reads "The AI review pass (requires `AI_PLAYBOOK_MODEL` to be set) …". Reword to match the shipped behavior: the AI review runs when the Claude CLI backend is available and is **skipped with a note otherwise**; set `AI_PLAYBOOK_MODEL` (or `ASSIST_MODEL`) to choose the model; add `--no-ai` to skip it for a purely deterministic/CI run. In `docs/guides/tutorial.md`: drop the `validate ⏳` markers (features line `:187`, the "Read" line `:212`, coverage row `:269`) and remove the ch.08 "documented as if shipped" note (`:190`) since validate now works.

- [ ] **Step 1: Edit** both files per Context.
- [ ] **Step 2: Verify** — `grep -n "⏳\|requires .*AI_PLAYBOOK_MODEL" examples/08-the-store.md docs/guides/tutorial.md`: no `validate ⏳` remains; the AI-pass wording matches the graceful-skip behavior. (No test — docs.)
- [ ] **Step 3: Commit** — `git add examples/08-the-store.md docs/guides/tutorial.md && git commit -m "docs(validate): document shipped validate command (ch.08)"`

---

## Final verification (after all tasks)

- [ ] `cd ~/Projects/langs/go/ai-playbook && gofmt -l internal/validate internal/author internal/launcher cmd/ai-playbook` → empty.
- [ ] `go build ./... && go vet ./...` → clean; `go run github.com/gordonklaus/ineffassign@v0.2.0 ./...` → clean.
- [ ] `go test ./internal/validate/ ./internal/author/ ./internal/launcher/` → PASS.
- [ ] `go install ./cmd/ai-playbook`, then live-verify: `ai-playbook validate --file examples/01-hello-run.md` (clean → exit 0, `echo $?`); a hand-broken copy (dangling `needs=`, or a missing `created` key, or an unclosed fence) → exit 1 with the finding printed; `--no-ai` runs deterministic-only; with the Claude CLI unavailable, the AI pass prints its skip note (not an error).

## Self-review notes (coverage vs spec)

- Spec §2 deterministic checks → Task 1 (front-matter/dup-id/needs/cycle/warnings) + Task 2 (fence). §3 AI pass → Task 3 (`ReviewOnce`) + Task 4 (graceful skip, `--no-ai`). §1 CLI wiring + §4 output/exit → Task 4. §5 docs → Task 5. `internal/validate` leaf invariant (no `ui` import) enforced by the launcher doing the `ui.Render`→DTO conversion (Task 4).
