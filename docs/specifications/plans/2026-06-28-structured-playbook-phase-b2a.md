# Structured Playbook Phase B2a — Implementation Plan (deterministic portability)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the model `adapt-on-run` with deterministic portability — a `project_bound` playbook is variabilized at authoring (`$PROJECT_ROOT`/`$HOME` + model-declared vars) and, at run, the host auto-sets `$PROJECT_ROOT` to the heuristic project root.

**Architecture:** New `playbook.Portabilize` rewrites project/home path prefixes in code blocks into shell vars at the structured-author capture (when `project_bound`); the schema gains `meta.env` (declared vars) folded into the front matter; the run path deletes the adapt model pass, gates on `store.Meta.ProjectBound`, and injects `PROJECT_ROOT` into the run driver's env.

**Tech Stack:** Go; the Phase A `internal/playbook` schema/renderer; B1 structured authoring; `internal/driver` (Options.Env), `internal/frontmatter`/`internal/store`/`internal/orchestrator`.

## Global Constraints

- Module `github.com/Townk/ai-playbook`. gpg-signed Conventional Commits; NO `Co-Authored-By` trailers; `git add` explicit paths; verify signing `git log -1 --format=%G?` == `G`.
- Reuse verbatim: Phase A `playbook.Playbook`/`Render`/the `submit_playbook` tool/capture; B1 `structuredStream`/`structuredBody`/`capturedMetaSeam`; `frontmatter.BuildEnv`/`EnvValue`; `store.Load`/`metaFromFM`.
- `Portabilize` rewrites ONLY runnable (non-static) `code` blocks + the top-level `verify` — never prose, never `static` blocks. Replacement is path-component-boundary-safe; `$PROJECT_ROOT` is applied BEFORE `$HOME` (project usually lives under home).
- A `project_bound` run **auto-sets** `PROJECT_ROOT` = the heuristic project root (`capture.ProjectRoot`/`projectRootFn`) — NO confirmation (that is B2b).
- Delete the model adapt entirely: `runcmd.go` `adaptOnRun`/`liveAdapt`/`adaptModelFn` + `internal/author/adapt.go` (`AdaptPrompt`). Stop writing `workdir`; old playbooks with `workdir` still parse (ignored).
- `gofmt -l` clean; `go vet` clean; touched packages pass `go test` (and `-race` for `internal/launcher`/`internal/ui`).
- Pre-run variable confirmation is **B2b — out of scope here.**

---

### Task 1: `store.Meta.ProjectBound` read-back

**Files:**
- Modify: `internal/store/store.go` (the `Meta` struct + `metaFromFM`)
- Test: `internal/store/store_test.go`

**Interfaces:**
- Produces: `store.Meta.ProjectBound bool`. Consumed by Task 6 (run gating) + Task 5 (front matter).

**Context:** Phase A added `frontmatter.FrontMatter.ProjectBound` but never read it back into `store.Meta`. `metaFromFM(fm, path, project)` (store.go ~120) builds `Meta` from the parsed front matter; `Meta` (store.go ~54-67) lacks `ProjectBound`.

- [ ] **Step 1: Write the failing test**

```go
func TestLoad_ReadsProjectBound(t *testing.T) {
	globalDir := t.TempDir()
	seamTo(t, globalDir, t.TempDir())
	writePB(t, globalDir, "pb", frontmatter.FrontMatter{Name: "PB", ProjectBound: true})
	m, _, err := Load("pb")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !m.ProjectBound {
		t.Fatal("Meta.ProjectBound must be read from front matter")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestLoad_ReadsProjectBound`
Expected: FAIL — `m.ProjectBound` undefined.

- [ ] **Step 3: Implement**

In `internal/store/store.go`, add to `Meta` (after `Workdir`):
```go
	ProjectBound bool
```
In `metaFromFM`, copy it (alongside the other field copies):
```go
	ProjectBound: fm.ProjectBound,
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit -m "feat(store): read project_bound from front matter into Meta"
```

---

### Task 2: `meta.env` schema field + seam mapping

**Files:**
- Modify: `internal/playbook/schema.go` (`Meta` + a new `EnvVar`)
- Modify: `internal/launcher/create_progress.go` (`capturedMetaSeam`)
- Test: `internal/playbook/schema_test.go`, `internal/launcher/create_progress_test.go`

**Interfaces:**
- Produces: `playbook.Meta.Env []playbook.EnvVar` (`EnvVar{Name, Why}`); `capturedMetaSeam` maps `lastPB.Meta.Env` → `orchestrator.PlaybookMeta.EnvNotes` (name→why).

- [ ] **Step 1: Write the failing test** (schema_test.go)

```go
func TestMeta_EnvRoundTrip(t *testing.T) {
	in := Playbook{Title: "T", Meta: Meta{Env: []EnvVar{{Name: "ANDROID_SDK_ROOT", Why: "the SDK"}}}}
	b, _ := json.Marshal(in)
	if !strings.Contains(string(b), `"name":"ANDROID_SDK_ROOT"`) || !strings.Contains(string(b), `"why":"the SDK"`) {
		t.Fatalf("env did not serialize: %s", b)
	}
	var out Playbook
	if err := json.Unmarshal(b, &out); err != nil || len(out.Meta.Env) != 1 || out.Meta.Env[0].Name != "ANDROID_SDK_ROOT" {
		t.Fatalf("env round-trip failed: %+v err=%v", out.Meta.Env, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/playbook/ -run TestMeta_EnvRoundTrip`
Expected: FAIL — `EnvVar`/`Meta.Env` undefined.

- [ ] **Step 3: Implement**

In `internal/playbook/schema.go`, add to `Meta` (after `ProjectBound`):
```go
	Env []EnvVar `json:"env,omitempty" jsonschema:"environment variables the playbook relies on (local resources, secrets) — declare each with name + why so a reader on another machine knows what to set"`
```
Add the type:
```go
// EnvVar is one declared environment variable the playbook relies on.
type EnvVar struct {
	Name string `json:"name" jsonschema:"the variable name, e.g. ANDROID_SDK_ROOT"`
	Why  string `json:"why,omitempty" jsonschema:"one line on what it is / why the playbook needs it"`
}
```
In `internal/launcher/create_progress.go` `capturedMetaSeam`, map `Env` → `EnvNotes` (the orchestrator `PlaybookMeta.EnvNotes` is `map[string]string` name→why that `buildFrontMatter` already passes to `frontmatter.BuildEnv`). In the `sess.lastPB.Load() != nil` branch, build:
```go
		notes := make(map[string]string, len(m.Env))
		for _, ev := range m.Env {
			if ev.Name != "" {
				notes[ev.Name] = ev.Why
			}
		}
		return orchestrator.PlaybookMeta{
			Description:  m.Description,
			Category:     m.Category,
			Tags:         m.Tags,
			ProjectBound: m.ProjectBound,
			EnvNotes:     notes,
		}, nil
```

- [ ] **Step 4: Run tests** (add a seam test in create_progress_test.go asserting a captured `meta.Env` surfaces in the seam's `EnvNotes`)

```go
func TestCapturedMetaSeam_MapsEnv(t *testing.T) {
	sess := newFakeSession(t) // reuse the Phase-A/B1 helper that opens a real tools backend
	pb := playbook.Playbook{Title: "T", Meta: playbook.Meta{Env: []playbook.EnvVar{{Name: "FOO", Why: "bar"}}}}
	raw, _ := json.Marshal(pb)
	_, _ = tools.Dial(sess.socket, tools.Call{Tool: "submit_playbook", Playbook: raw})
	meta, err := capturedMetaSeam(sess)("")
	if err != nil || meta.EnvNotes["FOO"] != "bar" {
		t.Fatalf("seam EnvNotes = %v err=%v, want FOO=bar", meta.EnvNotes, err)
	}
}
```
Run: `go test ./internal/playbook/ ./internal/launcher/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/playbook/schema.go internal/playbook/schema_test.go internal/launcher/create_progress.go internal/launcher/create_progress_test.go
git commit -m "feat(playbook): meta.env declared vars → front-matter EnvNotes"
```

---

### Task 3: Structured prompt — portability vars

**Files:**
- Modify: `internal/author/structured.go` (`StructuredToolInstruction`)
- Test: `internal/author/structured_test.go`

- [ ] **Step 1: Write the failing test** (extend the existing `want` list)

```go
	for _, want := range []string{
		"$PROJECT_ROOT", "$HOME", "do not hardcode", "meta.env",
	} {
		if !strings.Contains(StructuredToolInstruction(), want) {
			t.Errorf("structured instruction missing portability guidance %q", want)
		}
	}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/author/ -run TestStructuredToolInstruction`
Expected: FAIL — the new substrings are absent.

- [ ] **Step 3: Implement** — append a portability paragraph to `StructuredToolInstruction`'s returned string:

```go
		"\n### Portability\n" +
		"Reference machine- or project-specific local resources through shell variables, " +
		"do not hardcode absolute paths: use `$PROJECT_ROOT` for anything under the project " +
		"directory (the host sets it at run), `$HOME` for home paths, and the standard tool " +
		"variables (`$ANDROID_SDK_ROOT`, `$JAVA_HOME`, …) for SDK/tool locations. Declare each " +
		"non-standard variable the playbook relies on in `meta.env` (name + why) so a reader on " +
		"another machine knows what to set.\n"
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/author/ -run TestStructuredToolInstruction`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/author/structured.go internal/author/structured_test.go
git commit -m "feat(author): structured prompt — use \$PROJECT_ROOT/\$HOME/tool vars + declare meta.env"
```

---

### Task 4: `playbook.Portabilize` pure transform

**Files:**
- Create: `internal/playbook/portabilize.go`
- Test: `internal/playbook/portabilize_test.go`

**Interfaces:**
- Produces: `func playbook.Portabilize(pb *Playbook, projectRoot, home string)` (mutates pb's runnable+verify code blocks). Consumed by Task 5.

- [ ] **Step 1: Write the failing tests**

```go
package playbook

import "testing"

func TestPortabilize_ProjectAndHome(t *testing.T) {
	pb := Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
		{Kind: "code", Lang: "bash", ID: "fix", Code: "cd /Users/me/Proj/app && cat /Users/me/.sdkrc"},
		{Kind: "code", Lang: "console", Static: true, Code: "/Users/me/Proj/x"}, // static: untouched
	}}}, Verify: &Step{Lang: "bash", Code: "ls /Users/me/Proj"}}
	Portabilize(&pb, "/Users/me/Proj", "/Users/me")
	if got := pb.Sections[0].Content[0].Code; got != "cd $PROJECT_ROOT/app && cat $HOME/.sdkrc" {
		t.Fatalf("runnable block = %q", got)
	}
	if got := pb.Sections[0].Content[1].Code; got != "/Users/me/Proj/x" {
		t.Errorf("static block must be untouched, got %q", got)
	}
	if got := pb.Verify.Code; got != "ls $PROJECT_ROOT" {
		t.Errorf("verify = %q", got)
	}
}

func TestPortabilize_BoundaryAndNoMangle(t *testing.T) {
	pb := Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
		{Kind: "code", Lang: "bash", ID: "a", Code: "echo /Users/me/Project2/x"}, // /Users/me/Proj is a substring — must NOT match
	}}}}
	Portabilize(&pb, "/Users/me/Proj", "/Users/me")
	if got := pb.Sections[0].Content[0].Code; got != "echo $HOME/Project2/x" {
		t.Fatalf("substring must not be mangled to PROJECT_ROOT; home prefix applies: %q", got)
	}
}
```
(Note the second case: `/Users/me/Project2` is not a `$PROJECT_ROOT` boundary match, but its `/Users/me` prefix IS a `$HOME` match → `$HOME/Project2/x`.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/playbook/ -run TestPortabilize`
Expected: FAIL — `Portabilize` undefined.

- [ ] **Step 3: Implement**

```go
package playbook

import "regexp"

// Portabilize rewrites machine-specific absolute path prefixes in the playbook's
// RUNNABLE (non-static) code blocks + the top-level verify into shell variables, so
// a project_bound playbook relocates without a model adapt: projectRoot → $PROJECT_ROOT
// (the host sets it at run), then home → $HOME. projectRoot is applied FIRST (most
// specific — a project under home variabilizes the project, not just home). Matching
// is path-component-boundary-safe (the prefix must be preceded by start/space/quote/
// =/( and followed by /, end, space, quote, :, )) so a coincidental substring is never
// mangled. Static blocks (literal output) and prose are left untouched. Mutates pb.
func Portabilize(pb *Playbook, projectRoot, home string) {
	rewrite := func(s string) string {
		s = replacePathPrefix(s, projectRoot, "$PROJECT_ROOT")
		s = replacePathPrefix(s, home, "$HOME")
		return s
	}
	for si := range pb.Sections {
		for ci := range pb.Sections[si].Content {
			it := &pb.Sections[si].Content[ci]
			if it.Kind == "code" && !it.Static {
				it.Code = rewrite(it.Code)
			}
		}
	}
	if pb.Verify != nil {
		pb.Verify.Code = rewrite(pb.Verify.Code)
	}
}

// replacePathPrefix replaces prefix with repl wherever prefix appears as a whole path
// component — preceded by a start/separator boundary and followed by a path boundary
// — preserving the surrounding boundary characters. Empty prefix → no change.
func replacePathPrefix(s, prefix, repl string) string {
	if prefix == "" {
		return s
	}
	re := regexp.MustCompile(`(^|[\s"'=(])` + regexp.QuoteMeta(prefix) + `(/|$|[\s"':)])`)
	return re.ReplaceAllString(s, `${1}`+repl+`${2}`)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/playbook/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/playbook/portabilize.go internal/playbook/portabilize_test.go
git commit -m "feat(playbook): Portabilize — variabilize project/home paths in code blocks"
```

---

### Task 5: Apply `Portabilize` at capture + declare `PROJECT_ROOT`

**Files:**
- Modify: `internal/launcher/create_progress.go` (`structuredBody`/`structuredStream` — apply Portabilize when `project_bound`)
- Modify: `internal/orchestrator/orchestrator.go` (`buildFrontMatter` — inject `PROJECT_ROOT` into the env when `project_bound`)
- Test: `internal/launcher/create_progress_test.go`, `internal/orchestrator/orchestrator_test.go`

**Interfaces:**
- Consumes: `playbook.Portabilize` (Task 4); `PlaybookMeta.ProjectBound`.

**Context:** B1's `structuredBody(sess, fallback)` renders `playbook.Render(*sess.lastPB.Load())`. It needs the authoring `projectRoot` (`req.ProjectRoot`) + `home` to Portabilize. `buildFrontMatter` (orchestrator) builds the env via `BuildEnv`, which SKIPS vars absent from the shell — so `PROJECT_ROOT` (not in the shell) must be injected explicitly.

- [ ] **Step 1: Write the failing tests**

create_progress_test.go (extend the structured-body test): a captured `project_bound` playbook whose block references the authoring project dir renders with `$PROJECT_ROOT`:
```go
func TestStructuredBody_PortabilizesProjectBound(t *testing.T) {
	sess := newFakeSession(t)
	pb := playbook.Playbook{Title: "T", Meta: playbook.Meta{ProjectBound: true},
		Sections: []playbook.Section{{Heading: "S", Content: []playbook.ContentItem{
			{Kind: "code", Lang: "bash", ID: "fix", Code: "cd /proj/app"}}}}}
	raw, _ := json.Marshal(pb)
	_, _ = tools.Dial(sess.socket, tools.Call{Tool: "submit_playbook", Playbook: raw})
	body := structuredBody(sess, "/proj", "/home", nil) // (sess, projectRoot, home, fallback)
	if !strings.Contains(body, "cd $PROJECT_ROOT/app") {
		t.Fatalf("project_bound body must be portabilized: %s", body)
	}
}
```
orchestrator_test.go:
```go
func TestBuildFrontMatter_DeclaresProjectRoot(t *testing.T) {
	re := &Reengage{Req: capture.Request{},
		Metadata: func(string) (PlaybookMeta, error) { return PlaybookMeta{ProjectBound: true}, nil }}
	fm := re.buildFrontMatter("# Playbook — T\n\n```bash {id=fix}\ncd $PROJECT_ROOT\n```\n")
	if _, ok := fm.Env["PROJECT_ROOT"]; !ok {
		t.Fatalf("project_bound front matter must declare PROJECT_ROOT, got env=%v", fm.Env)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/launcher/ -run TestStructuredBody_Portabilizes; go test ./internal/orchestrator/ -run TestBuildFrontMatter_DeclaresProjectRoot`
Expected: FAIL — signature mismatch / no PROJECT_ROOT.

- [ ] **Step 3: Implement**

In `internal/launcher/create_progress.go`, give `structuredBody` the authoring `projectRoot`+`home` and Portabilize a `project_bound` capture before render:
```go
func structuredBody(sess *session, projectRoot, home string, fallback func() string) string {
	if sess != nil {
		if last := sess.lastPB.Load(); last != nil {
			pb := *last
			if pb.Meta.ProjectBound {
				playbook.Portabilize(&pb, projectRoot, home)
			}
			return playbook.Render(pb)
		}
	}
	if fallback != nil {
		return fallback()
	}
	return ""
}
```
Thread `req.ProjectRoot` + `home, _ := os.UserHomeDir()` from the callers (`structuredStream`'s `body` closure in create + escalate) into `structuredBody`.

In `internal/orchestrator/orchestrator.go` `buildFrontMatter`, after the `env := frontmatter.BuildEnv(...)` line, inject PROJECT_ROOT for `project_bound`:
```go
	if meta.ProjectBound {
		if env == nil {
			env = map[string]frontmatter.EnvValue{}
		}
		if _, ok := env["PROJECT_ROOT"]; !ok {
			env["PROJECT_ROOT"] = frontmatter.EnvValue{Why: "the project directory; the host sets it to your project root at run"}
		}
	}
```
(Leave `Value` empty — the host supplies it at run.) Keep returning `env` in the `FrontMatter`.

- [ ] **Step 4: Run tests + the two packages**

Run: `go test ./internal/launcher/ ./internal/orchestrator/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/launcher/create_progress.go internal/orchestrator/orchestrator.go internal/launcher/create_progress_test.go internal/orchestrator/orchestrator_test.go
git commit -m "feat(create): portabilize project_bound captures + declare PROJECT_ROOT in front matter"
```

---

### Task 6: Run path — gate on `project_bound`, set `$PROJECT_ROOT`, delete the model adapt

**Files:**
- Modify: `internal/launcher/runcmd.go` (`runPlaybook`/`runFile`/`renderAdapted`/`resolveTargetDir`; delete `adaptOnRun`/`liveAdapt`/`adaptModelFn`)
- Delete: `internal/author/adapt.go` (`AdaptPrompt`) + its test
- Modify: `internal/ui/main.go` (a `SetProjectRoot` seam → inject `PROJECT_ROOT` into the run driver's env)
- Modify: `internal/orchestrator/orchestrator.go` (`buildFrontMatter` — stop writing `Workdir`)
- Test: `internal/launcher/runcmd_test.go`, `internal/ui/main_test.go` (or the existing run tests)

**Interfaces:**
- Consumes: `store.Meta.ProjectBound` (Task 1), `projectRootFn`, `ui.SetProjectRoot`.

**Context (verbatim today):** `runPlaybook` → `resolveTargetDir(meta)` → `adaptOnRun(meta, body, target, adaptModelFn)` → `renderAdapted(renderFile, target, bannerSlug, origFile)` which reshapes `os.Args` to `run --file <tmp> --cwd <target>` → `uiMainFn()`. `internal/ui/main.go` opens the run driver via `driver.Open(driver.Options{Cwd: runCwd, Shell: pendingShell})` (`driver.Options` has an `Env []string`).

- [ ] **Step 1: Write the failing test** (runcmd_test.go — gate routing without a model)

```go
func TestRunPlaybook_ProjectBoundSetsProjectRoot(t *testing.T) {
	origLoad, origPR, origUI, origSPR := storeLoadFn, projectRootFn, uiMainFn, setProjectRootFn
	t.Cleanup(func() { storeLoadFn, projectRootFn, uiMainFn, setProjectRootFn = origLoad, origPR, origUI, origSPR })
	storeLoadFn = func(string) (store.Meta, string, error) {
		return store.Meta{Slug: "pb", ProjectBound: true}, "# Playbook — T\n\n```bash {id=fix}\ncd $PROJECT_ROOT\n```\n", nil
	}
	projectRootFn = func() string { return "/new/proj" }
	var gotPR string
	setProjectRootFn = func(p string) { gotPR = p }
	uiMainFn = func() int { return 0 } // no real viewer
	if code := runPlaybook("pb"); code != 0 {
		t.Fatalf("runPlaybook = %d", code)
	}
	if gotPR != "/new/proj" {
		t.Fatalf("project_bound run must set PROJECT_ROOT to the heuristic root, got %q", gotPR)
	}
}
```
(Introduce a `setProjectRootFn = ui.SetProjectRoot` seam so the test observes the call without a viewer.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/launcher/ -run TestRunPlaybook_ProjectBoundSetsProjectRoot`
Expected: FAIL — `setProjectRootFn` / `ui.SetProjectRoot` undefined; `adaptModelFn` removed.

- [ ] **Step 3: Implement**

Delete `adaptOnRun`, `liveAdapt`, and the `adaptModelFn` var from `runcmd.go`; delete `internal/author/adapt.go` + its test (`git rm`). Drop the now-unused imports (`author`, `agentstream`, the `runCreateProgress`/`thinkingTail` refs).

Add the seam + rewrite `runPlaybook` (and mirror in `runFile`):
```go
// setProjectRootFn injects the run driver's PROJECT_ROOT (the heuristic project root)
// for a project_bound playbook. Seam so tests observe it without a viewer.
var setProjectRootFn = ui.SetProjectRoot

func runPlaybook(slug string) int {
	meta, body, lerr := storeLoadFn(slug)
	if lerr != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook run: %v\n", lerr)
		return 1
	}
	cwd := ""
	if meta.ProjectBound {
		root := projectRootFn()
		setProjectRootFn(root) // the run driver exports PROJECT_ROOT=<root>
		cwd = root
	}
	return renderStored(body, cwd)
}

// renderStored writes body to a temp file and runs it via the `run --file` viewer
// (no adapt, no banner). cwd (the project root for a project_bound run, else "") is
// passed as --cwd so the driver opens there.
func renderStored(body, cwd string) int {
	f, err := writeTempMarkdown("playbook", body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook run: %v\n", err)
		return 1
	}
	saved := os.Args
	args := []string{os.Args[0], "run", "--file", f}
	if cwd != "" {
		args = append(args, "--cwd", cwd)
	}
	os.Args = args
	code := uiMainFn()
	os.Args = saved
	return code
}
```
Rewrite `runFile`'s front-matter branch the same way (gate on `fm.ProjectBound`; `renderStored(fmBody, cwd)`; drop `adaptOnRun`). Delete `resolveTargetDir` + `renderAdapted` (now unused) — confirm no other callers first.

In `internal/ui/main.go`, add the seam (mirror `SetShell`):
```go
var pendingProjectRoot string

// SetProjectRoot stashes PROJECT_ROOT for the next ui.Main() run driver (a
// project_bound playbook run). Consumed (and cleared) by Main; injected into the
// driver's environment so the playbook's $PROJECT_ROOT resolves.
func SetProjectRoot(root string) { pendingProjectRoot = root }
```
Consume it where the run driver is opened (the `file != ""` branch): build the env and clear the pending var:
```go
	projectRoot := pendingProjectRoot
	pendingProjectRoot = "" // consume once
	...
	env := os.Environ()
	if projectRoot != "" {
		env = append(env, "PROJECT_ROOT="+projectRoot)
	}
	d, derr = driver.Open(driver.Options{Cwd: runCwd, Shell: pendingShell, Env: env})
```

In `internal/orchestrator/orchestrator.go` `buildFrontMatter`, remove the `Workdir: …` field from the returned `frontmatter.FrontMatter{…}` (stop writing it).

- [ ] **Step 4: Run the test + the full suite**

Run: `go test ./internal/launcher/ -run TestRunPlaybook_ProjectBoundSetsProjectRoot`
Run: `go build ./... && go test ./...`
Expected: all PASS (the deleted-adapt path has no callers; the old adapt tests are removed).

- [ ] **Step 5: Commit**

```bash
git add internal/launcher/runcmd.go internal/ui/main.go internal/orchestrator/orchestrator.go internal/launcher/runcmd_test.go
git rm internal/author/adapt.go internal/author/adapt_test.go
git commit -m "feat(run): gate on project_bound + set PROJECT_ROOT; delete the model adapt-on-run"
```

---

## Self-Review

**Spec coverage (B2a):** `store.Meta.ProjectBound` (T1) ✓; `meta.env` + seam (T2) ✓; structured prompt portability guidance (T3) ✓; `Portabilize` (T4) ✓; apply-at-capture + declare `PROJECT_ROOT` (T5) ✓; run-time gate + auto-set `PROJECT_ROOT` + delete model adapt + stop writing `workdir` (T6) ✓.

**Deferred (NOT in B2a):** the pre-run variable confirmation (B2b); re-engagement → structured (B3); the viewer-UX-polish + `file=`/diff backlogs.

**Type consistency:** `playbook.EnvVar{Name,Why}` (T2) ↔ `capturedMetaSeam` EnvNotes mapping; `Portabilize(pb, projectRoot, home)` (T4) ↔ `structuredBody(sess, projectRoot, home, fallback)` (T5); `store.Meta.ProjectBound` (T1) ↔ run gate (T6); `ui.SetProjectRoot`/`setProjectRootFn`/`pendingProjectRoot` (T6) consistent.

**Open items for the implementer to confirm:**
- T2/T5 tests reference a `newFakeSession` helper — use the real one from the Phase-A/B1 launcher tests (or call `openSession` in-package); the assertion is what matters.
- T6: confirm `resolveTargetDir`/`renderAdapted` have no remaining callers before deleting; confirm `runFile`'s no-front-matter branch (raw file, no adapt) is unchanged.

**Placeholder scan:** every code step carries real code; the fake-session helper + the exact removed-import list are the only "match the real names" notes, flagged explicitly.
