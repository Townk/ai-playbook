# File-change FC1 — In-process side-by-side diff view (Implementation Plan)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the dead-by-default external diff viewer with a pure-Go side-by-side diff view that works in every config (mux float / no-mux overlay).

**Architecture:** A new `internal/diff` package parses an authored unified patch (go-udiff is generate-only, so we write the parser) and renders it side-by-side (chroma-highlighted, add/del-tinted) with a unified fallback for narrow widths. An `ai-playbook diff <patch>` subcommand wraps it in a scrollable program for the mux float; the no-mux path shows it as an in-viewer overlay. The external `hunk`/`delta`/`less` chain is removed.

**Tech Stack:** Go; bubbletea v2 (`charm.land/bubbletea/v2`) + lipgloss v2; chroma (`github.com/alecthomas/chroma/v2`); `go-udiff` (deps, generate-only — NOT used for parsing).

## Global Constraints

- Module `github.com/Townk/ai-playbook`. gpg-signed Conventional Commits; NO `Co-Authored-By`; `git add` explicit paths; verify signing `git log -1 --format=%G?` == `G`.
- The diff payload is an **already-authored unified patch string** (the `diff` block's body). The parser must **tolerate miscounted `@@ -a,b +c,d @@` headers** (agents miscount them — the codebase uses `git apply --recount` for this) by driving off the body lines, NOT the header counts. Handle multi-file + multi-hunk patches.
- **Side-by-side is required**, with a **unified fallback for narrow terminals** (too few columns for two panes).
- mux ON → spawn `ai-playbook diff <patchfile>` in a `Float.SpawnFloat`; mux OFF → an in-viewer modal overlay. The model already distinguishes them: `m.asker != nil` (mux) vs `m.askBridge != nil` (no-mux), mirrored from the `r`/refine handler.
- **Drop:** `viewDiff`'s external spawn body, `diffViewerCmd`, `hunkBin`, `lookViewer`, the `AI_PLAYBOOK_HUNK_BIN` env var. **Keep** `writePatch` (apply/undo uses it). `KindViewDiff` + the `"diff"`/`"view-diff"` button stay.
- Reuse: `highlight(src, lang)` (`render.go:915`) for per-side syntax highlighting; the diff bg colors `diffAddBgANSI`/`diffDelBgANSI` (`theme.go`); `spliceOver` (`viewport.go:83`) + the help-modal compositing/scroll pattern (`model.go:2149`) for the overlay; `os.Executable()` for the self-spawn.
- NOT in scope: FC2 `file=` block; file-watching. `gofmt -l`/`go vet` clean; touched packages pass `go test`.

---

### Task 1: Unified-patch parser (`internal/diff`)

**Files:**
- Create: `internal/diff/parse.go`, `internal/diff/parse_test.go`

**Interfaces:**
- Produces:
  ```go
  type Op int
  const ( OpContext Op = iota; OpDel; OpAdd )
  type Line struct { Op Op; Text string }   // Text has NO leading +/-/space marker
  type Hunk struct { Lines []Line }
  type FileDiff struct { OldPath, NewPath string; Hunks []Hunk }
  func Parse(patch string) []FileDiff
  ```
  Consumed by Tasks 2-4.

**Context:** `go-udiff` has no parser (generate-only). The patch is authored markdown text; headers may miscount. Parse by scanning lines: `diff --git`/`--- `/`+++ ` start a file; `@@ ` starts a hunk; body lines `+`/`-`/` ` (or empty) append to the current hunk; anything else (e.g. `\ No newline`) is ignored. Drive off the body — never trust the `@@` counts.

- [ ] **Step 1: Write the failing test**

```go
package diff

import ("reflect"; "testing")

func TestParse_SingleHunk(t *testing.T) {
	patch := "--- a/foo.go\n+++ b/foo.go\n@@ -1,3 +1,3 @@\n ctx\n-old line\n+new line\n more\n"
	got := Parse(patch)
	want := []FileDiff{{OldPath: "a/foo.go", NewPath: "b/foo.go", Hunks: []Hunk{{Lines: []Line{
		{OpContext, "ctx"}, {OpDel, "old line"}, {OpAdd, "new line"}, {OpContext, "more"},
	}}}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse =\n%#v\nwant\n%#v", got, want)
	}
}

func TestParse_ToleratesMiscountedHeader(t *testing.T) {
	// header says 1,1 but the body has 3 lines — drive off the body, parse all 3.
	patch := "--- a/x\n+++ b/x\n@@ -1,1 +1,1 @@\n a\n-b\n+c\n"
	h := Parse(patch)[0].Hunks[0]
	if len(h.Lines) != 3 {
		t.Fatalf("miscounted header must not truncate the body: got %d lines", len(h.Lines))
	}
}

func TestParse_MultiFileMultiHunk(t *testing.T) {
	patch := "--- a/one\n+++ b/one\n@@ -1 +1 @@\n-x\n+y\n--- a/two\n+++ b/two\n@@ -1 +1 @@\n-p\n+q\n"
	got := Parse(patch)
	if len(got) != 2 || got[1].NewPath != "b/two" {
		t.Fatalf("multi-file parse wrong: %#v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/diff/ -run TestParse`
Expected: FAIL — package/symbols undefined.

- [ ] **Step 3: Implement** (`internal/diff/parse.go`)

```go
// Package diff parses already-authored unified patches and renders them.
package diff

import "strings"

type Op int

const (
	OpContext Op = iota
	OpDel
	OpAdd
)

type Line struct {
	Op   Op
	Text string
}
type Hunk struct{ Lines []Line }
type FileDiff struct {
	OldPath, NewPath string
	Hunks            []Hunk
}

// Parse turns a unified patch into structured file diffs. It drives off the body
// lines, never the @@ header counts (agents miscount them), so a wrong count never
// truncates a hunk.
func Parse(patch string) []FileDiff {
	var files []FileDiff
	var cur *FileDiff
	var hunk *Hunk
	for _, ln := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(ln, "--- "):
			files = append(files, FileDiff{OldPath: strings.TrimSpace(ln[4:])})
			cur, hunk = &files[len(files)-1], nil
		case strings.HasPrefix(ln, "+++ ") && cur != nil:
			cur.NewPath = strings.TrimSpace(ln[4:])
		case strings.HasPrefix(ln, "diff --git"):
			// tolerate a `diff --git` lead-in before ---/+++; ignore.
		case strings.HasPrefix(ln, "@@"):
			if cur == nil { // a hunk with no file header — synthesize one
				files = append(files, FileDiff{})
				cur = &files[len(files)-1]
			}
			cur.Hunks = append(cur.Hunks, Hunk{})
			hunk = &cur.Hunks[len(cur.Hunks)-1]
		case hunk != nil && strings.HasPrefix(ln, "-"):
			hunk.Lines = append(hunk.Lines, Line{OpDel, ln[1:]})
		case hunk != nil && strings.HasPrefix(ln, "+"):
			hunk.Lines = append(hunk.Lines, Line{OpAdd, ln[1:]})
		case hunk != nil && strings.HasPrefix(ln, " "):
			hunk.Lines = append(hunk.Lines, Line{OpContext, ln[1:]})
		case hunk != nil && ln == "":
			hunk.Lines = append(hunk.Lines, Line{OpContext, ""})
		default:
			// `\ No newline at end of file`, index lines, etc. — ignore.
		}
	}
	return files
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/diff/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/diff/parse.go internal/diff/parse_test.go
git commit -m "feat(diff): unified-patch parser (body-driven, miscount-tolerant)"
```

---

### Task 2: Side-by-side renderer (+ unified fallback)

**Files:**
- Create: `internal/diff/render.go`, `internal/diff/render_test.go`

**Interfaces:**
- Consumes: `FileDiff`/`Hunk`/`Line`/`Op` (Task 1).
- Produces: `func Render(files []FileDiff, width int, highlightFn func(code, lang string) string) string` — the full rendered diff (side-by-side when `width` allows two panes, else unified). `highlightFn` is injected (the UI passes `ui.highlight`; tests pass an identity fn) so `internal/diff` stays UI-decoupled. Consumed by Tasks 3 (entrypoint) + 4 (overlay).

**Context:** Side-by-side = two columns (left=old, right=new). Align rows: a context line → both columns; a run of dels then adds → pair them row-by-row (left del / right add), padding the shorter side with blanks. Highlight each side's content via `highlightFn(text, lang)` (lang inferred from the file path extension), then tint: left dels with `diffDelBgANSI`-equivalent, right adds with `diffAddBgANSI`-equivalent (pass the bg colors in, or hardcode the same hex as `theme.go`). Unified fallback when `width < minSideBySide` (e.g. `< 80`): one column, `-`/`+`/` ` prefixed + colored (the inline-diff look).

- [ ] **Step 1: Write the failing tests**

```go
package diff

import ("strings"; "testing")

func id(code, lang string) string { return code } // identity highlight for tests

func TestRender_SideBySide(t *testing.T) {
	files := []FileDiff{{NewPath: "b/x.txt", Hunks: []Hunk{{Lines: []Line{
		{OpContext, "keep"}, {OpDel, "old"}, {OpAdd, "new"},
	}}}}}
	out := Render(files, 120, id)
	// both old and new content present; laid out in two columns (a vertical separator)
	if !strings.Contains(out, "old") || !strings.Contains(out, "new") || !strings.Contains(out, "keep") {
		t.Fatalf("side-by-side missing content:\n%s", out)
	}
	if !strings.Contains(out, "│") { // a column separator
		t.Fatalf("no two-column separator in side-by-side:\n%s", out)
	}
}

func TestRender_NarrowFallsBackToUnified(t *testing.T) {
	files := []FileDiff{{NewPath: "b/x", Hunks: []Hunk{{Lines: []Line{{OpDel, "old"}, {OpAdd, "new"}}}}}}
	out := Render(files, 30, id)
	// unified: -old / +new prefixed lines, single column (no two-pane separator run)
	if !strings.Contains(out, "-old") || !strings.Contains(out, "+new") {
		t.Fatalf("narrow render not unified:\n%s", out)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/diff/ -run TestRender`
Expected: FAIL — `Render` undefined.

- [ ] **Step 3: Implement** (`internal/diff/render.go`)

Build it in pieces; the implementer writes the lipgloss column layout. Required shape:
```go
import (
	"path/filepath"; "strings"
	"charm.land/lipgloss/v2"
)

const minSideBySide = 80

func Render(files []FileDiff, width int, highlightFn func(code, lang string) string) string {
	if width < minSideBySide {
		return renderUnified(files, highlightFn)
	}
	return renderSideBySide(files, width, highlightFn)
}

// renderSideBySide lays each hunk into two columns: left=old (context+del),
// right=new (context+add). A run of dels then adds is paired row-by-row; the
// shorter side is blank-padded. Content is highlighted via highlightFn(text,
// langFromPath(path)) then add/del-tinted; a file header line precedes each file.
func renderSideBySide(files []FileDiff, width int, highlightFn func(string, string) string) string { /* … */ }

// renderUnified emits a single column: a file header, then `-`/`+`/` ` prefixed,
// red/green-colored lines (the inline-diff look) — for narrow terminals.
func renderUnified(files []FileDiff, highlightFn func(string, string) string) string { /* … */ }

func langFromPath(p string) string { return strings.TrimPrefix(filepath.Ext(p), ".") }
```
Pairing algorithm (for `renderSideBySide`'s per-hunk loop): walk the hunk's lines; emit context as `{left:text, right:text}`; buffer consecutive dels into a left-queue and adds into a right-queue; when the run ends (a context line or hunk end), zip the queues into rows (`max(len(dels),len(adds))` rows, blank-padding the shorter), then emit the context. Each cell = `highlightFn(text, lang)` background-filled to the column width (use `lipgloss.Width` for correct padding; left-dels get the del bg, right-adds the add bg). Columns joined by ` │ `; col width ≈ `(width-3)/2`.

- [ ] **Step 4: Run tests + the package**

Run: `go test ./internal/diff/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/diff/render.go internal/diff/render_test.go
git commit -m "feat(diff): side-by-side renderer with unified narrow fallback"
```

---

### Task 3: `ai-playbook diff` entrypoint

**Files:**
- Create: `internal/diff/main.go` (the subcommand `Main`) + `internal/diff/main_test.go`
- Modify: `cmd/ai-playbook/main.go` (dispatch + usage)

**Interfaces:**
- Consumes: `Parse` (T1), `Render` (T2).
- Produces: `func Main() int` — `ai-playbook diff <patchfile>`: read the patch file, render it, run a scrollable bubbletea program (a viewport over `Render`'s output) until the user quits (`q`/`esc`/`ctrl+c`).

**Context:** Mirrors the `mcp`/`input` entrypoint pattern (`cmd/ai-playbook/main.go:90`, `internal/input/main.go:21`): a subcommand reads `os.Args[2:]`. The float (Task 4) spawns `ai-playbook diff <patchfile>`. For highlighting, the entrypoint can pass an identity fn or a local chroma highlight (the in-viewer overlay in Task 4 uses `ui.highlight`); keep it simple — a small local highlight or identity is fine for the float (the renderer is the same).

- [ ] **Step 1: Write the failing test**

```go
func TestDiffMain_RendersPatchFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "p.patch")
	os.WriteFile(f, []byte("--- a/x\n+++ b/x\n@@ -1 +1 @@\n-old\n+new\n"), 0o644)
	// renderFile is the headless core Main wraps (Main runs the TUI; test the core).
	out := renderFile(f, 100)
	if !strings.Contains(out, "old") || !strings.Contains(out, "new") {
		t.Fatalf("renderFile output:\n%s", out)
	}
}
```
(Factor a headless `renderFile(path string, width int) string` that reads+parses+renders; `Main` wraps it in the TUI. Test `renderFile`, not the TUI.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/diff/ -run TestDiffMain`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

`internal/diff/main.go`: `renderFile(path, width)` (read file → `Parse` → `Render`); `Main()` parses `os.Args[2:]` (positional patch path), gets the terminal width, builds a minimal bubbletea model wrapping the rendered string in a scrollable viewport (reuse the project's viewport/scroll helpers or a simple line-window), quits on `q`/`esc`/`ctrl+c`. In `cmd/ai-playbook/main.go`, add `case "diff": os.Exit(diffpkg.Main())` (import `internal/diff`) near the `input` case, and add `diff` to the `usage()` string.

- [ ] **Step 4: Run test + build**

Run: `go test ./internal/diff/ -run TestDiffMain && go build ./...`
Expected: PASS + builds (the `diff` subcommand wired).

- [ ] **Step 5: Commit**

```bash
git add internal/diff/main.go internal/diff/main_test.go cmd/ai-playbook/main.go
git commit -m "feat(diff): ai-playbook diff entrypoint (scrollable side-by-side viewer)"
```

---

### Task 4: Mux-ON float + drop the external chain + amend ADR-0008

**Files:**
- Modify: `internal/orchestrator/orchestrator.go` (`viewDiff` → self-spawn; delete `diffViewerCmd`/`hunkBin`/`lookViewer`)
- Modify: `docs/architecture/adrs/0008-in-process-diff-view.md` (amendment) + `CHANGELOG` (the env-var removal)
- Test: `internal/orchestrator/orchestrator_test.go`

**Interfaces:**
- Consumes: the `ai-playbook diff` subcommand (T3); `writePatch` (stays); `Float.SpawnFloat`.

**Context:** `viewDiff` (`orchestrator.go:658-676`) currently spawns `diffViewerCmd(patch)` (the external chain). Replace the `Cmd` with `[]string{selfExe, "diff", patch}` (`selfExe` via `os.Executable()`). Delete `diffViewerCmd`/`hunkBin`/`lookViewer` + the `AI_PLAYBOOK_HUNK_BIN` reference. `writePatch` stays (the patch temp file is intentionally not removed — the float reads it async).

- [ ] **Step 1: Write the failing test**

```go
func TestViewDiff_SpawnsSelfDiffSubcommand(t *testing.T) {
	var got mux.SpawnOptions
	o := &Orchestrator{Float: &fakeFloat{spawn: func(opts mux.SpawnOptions) error { got = opts; return nil }}}
	_ = o.viewDiff("fix", "--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n")
	if len(got.Cmd) < 2 || got.Cmd[1] != "diff" {
		t.Fatalf("viewDiff must spawn `<self> diff <patch>`, got %v", got.Cmd)
	}
}
```
(Use/extend the existing orchestrator test float fake; assert the spawned Cmd is the self `diff` subcommand, not `hunk`/`delta`/`less`.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/orchestrator/ -run TestViewDiff_SpawnsSelfDiffSubcommand`
Expected: FAIL — still spawns the external tool.

- [ ] **Step 3: Implement**

In `viewDiff`, replace `Cmd: diffViewerCmd(patch)` with:
```go
	selfExe, _ := os.Executable()
	...
	Cmd: []string{selfExe, "diff", patch},
```
Delete `diffViewerCmd`, `hunkBin`, `lookViewer`. Grep `AI_PLAYBOOK_HUNK_BIN` → remove its only reference. Amend `docs/architecture/adrs/0008-in-process-diff-view.md`: add a dated note that the adapt-on-run `d` overlay shared surface was removed in B2a's dead-code sweep, so the renderer now serves only the diff-block "view diff"; and that the external chain (`hunk`/`delta`/`less`, `AI_PLAYBOOK_HUNK_BIN`) is now removed. Add a CHANGELOG entry for the env-var/external-viewer removal (find the CHANGELOG; if none, note it in the ADR's negative-consequences).

- [ ] **Step 4: Run tests + the package**

Run: `go test ./internal/orchestrator/`
Expected: PASS (the external chain has no remaining callers).

- [ ] **Step 5: Commit**

```bash
git add internal/orchestrator/orchestrator.go docs/architecture/adrs/0008-in-process-diff-view.md internal/orchestrator/orchestrator_test.go
git commit -m "feat(diff): mux float spawns the in-process diff viewer; drop external hunk/delta chain"
```
(Add the CHANGELOG file to the `git add` if one exists.)

---

### Task 5: Mux-OFF in-viewer diff overlay

**Files:**
- Modify: `internal/ui/model.go` (a `diffMode` overlay + the view-diff button branch) + possibly `internal/ui/diff_overlay.go` (new)
- Test: `internal/ui/diff_overlay_test.go` (or `model_test.go`)

**Interfaces:**
- Consumes: `diff.Render` (T2), `highlight` (`render.go:915`), `spliceOver` (`viewport.go:83`); the mux/no-mux signal (`m.asker` vs `m.askBridge`).

**Context:** Today a "view diff" click → `emitAction` → `orch.Do(KindViewDiff)` → `viewDiff` → float (silent no-op off-mux). Add the no-mux path: when there's NO mux (`m.asker == nil`, mirroring the `r`/refine branch at `model.go:1081-1104`), a view-diff click sets a new `m.diffMode` with the rendered side-by-side content + scroll offsets, and `viewString` composites it as a centered scrollable overlay (mirror the help modal: `model.go:2149-2173`, `spliceOver`, `helpXOff`/scroll). Mux-on keeps the `emitAction`→float path (Task 4). Close on `q`/`esc`; scroll with the help-modal key set.

- [ ] **Step 1: Write the failing test**

```go
func TestViewDiff_NoMuxOpensOverlay(t *testing.T) {
	m := newDiffOverlayTestModel(t) // a model with asker==nil (no-mux) + a diff button
	b := Button{Kind: "diff", BlockID: "fix", Payload: "--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n"}
	m2, _ := m.activateButton(b) // the button-handling entry the click/keys use
	if !m2.diffMode {
		t.Fatal("view-diff on no-mux must open the in-viewer diff overlay")
	}
	if !strings.Contains(strings.Join(m2.diffLines, "\n"), "b") {
		t.Fatal("diff overlay content not rendered")
	}
}
```
(Adapt to the real button-handling fn + model construction — read `model.go`'s button path + an existing overlay test for the harness; the assertion: no-mux view-diff → `diffMode` on, content rendered.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ui/ -run TestViewDiff_NoMuxOpensOverlay`
Expected: FAIL — `diffMode` undefined.

- [ ] **Step 3: Implement**

Add to `model`: `diffMode bool`, `diffLines []string`, `diffYOff`/`diffXOff int`. In the view-diff button branch (where `b.Kind=="diff"`/`"view-diff"` is handled, alongside the other kinds at `model.go:~770`/`~927`): if no mux (`m.asker == nil`), render `diff.Render(diff.Parse(b.Payload), m.width-4, m.highlight)` into `m.diffLines`, set `m.diffMode=true`, return (do NOT emitAction); else `emitAction(b)` (the float, Task 4). Add a `viewString` branch for `diffMode` that composites the diff box centered + scrollable via `spliceOver` (mirror the help-modal block), and key handling (scroll + `q`/`esc` to close, clearing `diffMode`). Reuse the help modal's scroll/compositing helpers.

- [ ] **Step 4: Run tests + the package (-race)**

Run: `go test ./internal/ui/ -run TestViewDiff_NoMuxOpensOverlay`
Run: `go build ./... && go test -race ./internal/ui/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/model.go internal/ui/diff_overlay.go internal/ui/diff_overlay_test.go
git commit -m "feat(ui): no-mux in-viewer side-by-side diff overlay"
```

---

## Self-Review

**Spec coverage (FC1):** unified-patch parser (T1) ✓; side-by-side renderer + unified fallback (T2) ✓; `ai-playbook diff` entrypoint (T3) ✓; mux-ON float self-spawn + drop external chain + ADR amendment + CHANGELOG (T4) ✓; mux-OFF overlay (T5) ✓.

**Type consistency:** `diff.Parse → []FileDiff` (T1) ↔ `diff.Render(files, width, highlightFn)` (T2) ↔ `renderFile`/`Main` (T3) ↔ the overlay's `diff.Render(diff.Parse(...))` (T5); `viewDiff` self-spawn (T4) ↔ the T3 `diff` subcommand; the mux/no-mux branch (`m.asker`) consistent with the `r`/refine precedent.

**Deferred (NOT FC1):** FC2 `file=` create block (next plan); file-watching (#3); word-level intra-line highlight (ADR-deferred).

**Open items the implementer must confirm against real code (flagged, not placeheld):**
- T2: the lipgloss column layout + the exact add/del bg hex (match `theme.go`'s `diffAddBgANSI`/`diffDelBgANSI`); the `minSideBySide` threshold.
- T3: the real bubbletea viewport/scroll helper to reuse for the float program; the `cmd/ai-playbook/main.go` import alias for `internal/diff`.
- T4: the existing orchestrator test float-fake; the CHANGELOG location.
- T5: the real button-handling fn + the model overlay-test harness; whether a separate `diff_overlay.go` or inline in `model.go` reads cleaner; reuse the help-modal scroll helpers (`helpXOff`/`hscrollbarRow`/`Window`).
