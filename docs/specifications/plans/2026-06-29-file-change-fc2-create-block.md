# File-change FC2 — `file=` create block (Implementation Plan)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A `file=<path>` code block whose body IS a new file's content — deterministic write with a `create`→`undo` tab — completing the file-change feature's create-a-file role.

**Architecture:** A `File` field on the schema + the viewer `Block`; `file=` round-trips through the fence tag (`parseFenceInfo` already parses it); the viewer renders a path tab + a `create` button reusing the diff block's `Status`-driven apply/undo toggle; a pure-Go orchestrator `createFile` writes (cwd-anchored, backup-on-overwrite) and `undoCreate` restores; the `submit_playbook` handler rejects a `file=` on an existing path (suggest `diff`).

**Tech Stack:** Go; `internal/playbook` (schema/render), `internal/ui` (block.go/render.go/model.go/inprocess.go), `internal/orchestrator`, `internal/tools` (submit_playbook), `internal/author` (prompt).

## Global Constraints

- Module `github.com/Townk/ai-playbook`. gpg-signed Conventional Commits; NO `Co-Authored-By`; `git add` explicit paths; verify signing `git log -1 --format=%G?` == `G`.
- A `file=` block represents a NEW file: `{kind:"code", lang, file:<relative path>, code:<content>}`. `diff` blocks (edit existing) are already representable (`lang:"diff"`) — FC2 does NOT change diff handling.
- **Create policy:** authoring-time — the `submit_playbook` handler REJECTS a `file=` whose path already exists in the project (message: use a `diff` block). Run-time drift — `createFile` overwrites, capturing a backup; `undo` restores the backup, or deletes a genuinely-new file.
- **Write:** pure-Go `os.WriteFile` (matching `CommitPlaybook` at orchestrator.go:458), path resolved against `o.Drv.Cwd()` / project root (orchestrator.go:687) — NOT the process cwd; `MkdirAll` the parent.
- **Path smuggling:** `Action`/`Block` have no path field, so create actions carry `{path, body}` via a JSON `Payload` — one encode/decode pair (`orchestrator.EncodeFileAction`/`decodeFileAction`) used by BOTH the UI button (encode) and `createFile` (decode).
- **All-sites wiring:** new kinds `KindCreateFile`/`KindUndoCreate` must be added in EVERY site — orchestrator enum/`String`/`Do`, `kindOf`, `isShellActionKind`, BOTH click-dispatch blocks (mouse model.go:802 + keyboard model.go:997), the `orchCmd` result `case` (inprocess.go:149). A missed site = a silently inert button.
- The `create`→`undo` toggle reuses `blockRunState.Status` (`"ok"` ⇒ undo) verbatim — mirror the diff button cluster (render.go:646-668) + its `regionW += 2` reservation (render.go:580-592).
- `gofmt -l`/`go vet`/`make lint` clean; touched packages pass `go test` (+ `-race` for `internal/ui`).
- NOT in scope: FC1 (done); file-watching (#3).

---

### Task 1: Schema `File` field + render + Block recognition

**Files:**
- Modify: `internal/playbook/schema.go` (`ContentItem.File`), `internal/playbook/render.go` (`fence` emits `file=`)
- Modify: `internal/ui/block.go` (`Block.File`), `internal/ui/render.go` (`code()` recognizes `file=`)
- Test: `internal/playbook/render_test.go`, `internal/ui/block_test.go`

**Interfaces:**
- Produces: `playbook.ContentItem.File string`; the fence tag `{id=… file=<path>}`; `ui.Block.File string` + `Block.Type == "create"` when `file=` is set. Consumed by Tasks 2-3.

- [ ] **Step 1: Write the failing tests**

`internal/playbook/render_test.go`:
```go
func TestRender_FileBlock(t *testing.T) {
	pb := Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
		{Kind: "code", Lang: "go", File: "cmd/x/main.go", ID: "new", Code: "package main\n"}}}}}
	out := Render(pb)
	if !strings.Contains(out, "file=cmd/x/main.go") {
		t.Fatalf("fence missing file= tag:\n%s", out)
	}
}
```
`internal/ui/block_test.go`:
```go
func TestCode_FileBlockRecognized(t *testing.T) {
	lines, _, blocks := Render("```go {id=new file=cmd/x/main.go}\npackage main\n```\n", 80, nil, "")
	_ = lines
	var b *Block
	for i := range blocks { if blocks[i].ID == "new" { b = &blocks[i] } }
	if b == nil || b.File != "cmd/x/main.go" || b.Type != "create" {
		t.Fatalf("file= block not recognized: %+v", b)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/playbook/ -run TestRender_FileBlock; go test ./internal/ui/ -run TestCode_FileBlockRecognized`
Expected: FAIL — `File` undefined / not recognized.

- [ ] **Step 3: Implement**

`internal/playbook/schema.go` `ContentItem` — add `File string \`json:"file,omitempty" jsonschema:"for a NEW file: the relative path; the block body is the file's full content (use a diff block to EDIT an existing file)"\``.
`internal/playbook/render.go` `fence` — add a `file` param (or read it from the item); when non-empty emit it inside the tag: `{id=<id> file=<path>}` (place after `id`, before `needs`/`rollback`). Update `fence`'s caller (the `code` item render) to pass `item.File`.
`internal/ui/block.go` `Block` — add `File string`.
`internal/ui/render.go` `code()` (~render.go:529-535) — after `blk.Type = classifyType(lang, blk.Static)`, recognize a create block:
```go
	if f := attrs["file"]; f != "" {
		blk.File = f
		blk.Type = "create"
	}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/playbook/ ./internal/ui/ -run 'TestRender_FileBlock|TestCode_FileBlockRecognized'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/playbook/schema.go internal/playbook/render.go internal/ui/block.go internal/ui/render.go internal/playbook/render_test.go internal/ui/block_test.go
git commit -m "feat(playbook): file= create block — schema File field + render + viewer recognition"
```

---

### Task 2: Orchestrator create/undo write-action

**Files:**
- Modify: `internal/orchestrator/orchestrator.go` (`Kind` enum + `String` + `Do` + `createFile`/`undoCreate` + the encode/decode + the backup map)
- Test: `internal/orchestrator/orchestrator_test.go`

**Interfaces:**
- Produces: `KindCreateFile`/`KindUndoCreate`; `func EncodeFileAction(path, body string) string` + `func decodeFileAction(payload string) (path, body string, err error)` (JSON); `Do` handles the two kinds. Consumed by Task 3 (UI encodes via `EncodeFileAction`).

**Context:** Mirror `applyDiff` (orchestrator.go:638) but pure-Go. Resolve the path against `o.Drv.Cwd()`/project root (orchestrator.go:687). On create: if the file exists, capture its content into an in-orchestrator backup map (keyed by abs path); `MkdirAll` the parent; `os.WriteFile`. On undo: restore the backup if one was captured, else delete the file. Return `driver.Result{Exit:0}` (or `Exit:-1,Err` on failure) so the `resultMsg` toggle works.

- [ ] **Step 1: Write the failing tests**

```go
func TestCreateFile_WritesAndUndoDeletes(t *testing.T) {
	dir := t.TempDir()
	o := newTestOrchInDir(t, dir) // an Orchestrator whose Drv.Cwd()==dir (reuse the apply/undo test harness)
	payload := EncodeFileAction("sub/new.txt", "hello\n")
	if res := o.Do2(KindCreateFile, payload); res.Exit != 0 { t.Fatalf("create: %+v", res) }
	if got, _ := os.ReadFile(filepath.Join(dir, "sub/new.txt")); string(got) != "hello\n" {
		t.Fatalf("file not written: %q", got)
	}
	if res := o.Do2(KindUndoCreate, payload); res.Exit != 0 { t.Fatalf("undo: %+v", res) }
	if _, err := os.Stat(filepath.Join(dir, "sub/new.txt")); !os.IsNotExist(err) {
		t.Fatal("undo of a new file must delete it")
	}
}

func TestCreateFile_OverwriteUndoRestores(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "x.txt"), []byte("ORIG\n"), 0o644)
	o := newTestOrchInDir(t, dir)
	payload := EncodeFileAction("x.txt", "NEW\n")
	o.Do2(KindCreateFile, payload)
	o.Do2(KindUndoCreate, payload)
	if got, _ := os.ReadFile(filepath.Join(dir, "x.txt")); string(got) != "ORIG\n" {
		t.Fatalf("undo of an overwrite must restore the backup, got %q", got)
	}
}
```
(`Do2(kind, payload)` is shorthand for `o.Do(Action{Kind:kind, Payload:payload})` — use the real `Do`; adapt the test harness to the existing orchestrator apply/undo tests' way of building an Orchestrator with a driver whose cwd is `dir`.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/orchestrator/ -run TestCreateFile`
Expected: FAIL — kinds/funcs undefined.

- [ ] **Step 3: Implement**

Add to the `Kind` enum (after `KindUndoDiff`): `KindCreateFile`, `KindUndoCreate`; add their `String()` cases (`"create"`, `"undo-create"`). Add the encode/decode:
```go
type fileAction struct { Path, Body string }
func EncodeFileAction(path, body string) string {
	b, _ := json.Marshal(fileAction{Path: path, Body: body})
	return string(b)
}
func decodeFileAction(payload string) (string, string, error) {
	var fa fileAction
	if err := json.Unmarshal([]byte(payload), &fa); err != nil { return "", "", err }
	return fa.Path, fa.Body, nil
}
```
Add a backup map to the `Orchestrator` struct: `createBackups map[string]*[]byte // abs path → prior content (nil pointer = file was new)`. In `Do`, add:
```go
	case KindCreateFile:
		return o.createFile(a.Payload), nil
	case KindUndoCreate:
		return o.undoCreate(a.Payload), nil
```
Implement `createFile`/`undoCreate` (resolve `path` against the driver cwd via the existing `projectRoot`/`o.Drv.Cwd()` helper; `MkdirAll(filepath.Dir(abs))`; backup; write/restore/delete). Lazily init `createBackups`.

- [ ] **Step 4: Run tests + the package**

Run: `go test ./internal/orchestrator/ -run TestCreateFile`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/orchestrator/orchestrator.go internal/orchestrator/orchestrator_test.go
git commit -m "feat(orchestrator): createFile/undoCreate write-action (cwd-anchored, backup-restore)"
```

---

### Task 3: UI — the `file=` tab, `create`→`undo` button, dispatch wiring

**Files:**
- Modify: `internal/ui/render.go` (the `create` tab + button in `code()`), `internal/ui/inprocess.go` (`kindOf` + the `orchCmd` result case), `internal/ui/model.go` (`isShellActionKind` + both click-dispatch blocks)
- Test: `internal/ui/render_test.go` (or `block_test.go`)

**Interfaces:**
- Consumes: `Block.File`/`Block.Type=="create"` (T1), `orchestrator.KindCreateFile`/`KindUndoCreate` + `EncodeFileAction` (T2), `blockRunState.Status` toggle.

**Context:** Mirror the diff button cluster (render.go:646-668) + its `regionW` reservation (render.go:580-592). For a `create` block, the tab shows the block-type icon + the **relative path** (`Block.File`) in place of the lang label, then the action separator + a `create` button that flips to `undo` when `Status=="ok"`. The button `Payload = orchestrator.EncodeFileAction(blk.File, blk.Payload)`.

- [ ] **Step 1: Write the failing test**

```go
func TestCreateBlock_TabAndButton(t *testing.T) {
	_, buttons, _ := Render("```go {id=new file=cmd/x/main.go}\npackage main\n```\n", 100, nil, "")
	var has bool
	for _, b := range buttons { if b.BlockID == "new" && b.Kind == "create" { has = true } }
	if !has { t.Fatal("create block has no create button") }
	// applied → undo button
	_, buttons2, _ := Render("```go {id=new file=cmd/x/main.go}\npackage main\n```\n", 100,
		map[string]blockRunState{"new": {Status: "ok"}}, "")
	var undo bool
	for _, b := range buttons2 { if b.BlockID == "new" && b.Kind == "undo-create" { undo = true } }
	if !undo { t.Fatal("applied create block must show undo-create") }
}
```
(Confirm the rendered tab shows the path — assert the joined lines contain `cmd/x/main.go` — adapt with `joinText`/the real button-kind strings.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ui/ -run TestCreateBlock_TabAndButton`
Expected: FAIL.

- [ ] **Step 3: Implement**

Add a glyph const near render.go:494 (e.g. `glyphCreate` — a file/new glyph). In `code()`: when `blk.Type == "create"`, render the tab with `blk.File` as the label (in place of the lang text — reuse the icon mechanism, swap the label) and `langW = lipgloss.Width(blk.File)`; in the `regionW` math add `regionW += 2` for the create/undo button; in the button cluster emit `create` (when `Status != "ok"`) or `undo-create` (when `Status == "ok"`), `Payload: orchestrator.EncodeFileAction(blk.File, blk.Payload)`. (Mirror the diff cluster's structure precisely so the fill math + hit-boxes stay aligned.)
`internal/ui/inprocess.go` `kindOf` (~99): add `case "create": return orchestrator.KindCreateFile, true` + `case "undo-create": return orchestrator.KindUndoCreate, true`; in the `orchCmd` result `case` (~149) add `KindCreateFile`/`KindUndoCreate` so their result flows back as a `resultMsg`.
`internal/ui/model.go` `isShellActionKind` (~379): add `"create"`, `"undo-create"` to the kind list. Both click-dispatch blocks (~802 mouse, ~997 keyboard): add a `create`/`undo-create` branch mirroring the `apply-diff`/`undo-diff` Status/Action set (`st.Action="create"`/`"undo"`; `st.Status="running"`), then `emitAction(b)`.

- [ ] **Step 4: Run tests + the package**

Run: `go test ./internal/ui/ -run TestCreateBlock_TabAndButton`
Run: `go build ./... && go test ./internal/ui/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/render.go internal/ui/inprocess.go internal/ui/model.go internal/ui/render_test.go
git commit -m "feat(ui): file= create block tab + create→undo button + dispatch wiring"
```

---

### Task 4: Authoring-time reject-if-exists (`submit_playbook`)

**Files:**
- Modify: `internal/tools/tools.go` (a `Deps.ValidateFileBlocks` hook + the `submit_playbook` handler call)
- Modify: `internal/launcher/session.go` (wire the hook with the project root)
- Test: `internal/tools/tools_test.go`, `internal/launcher/session_test.go`

**Interfaces:**
- Produces: `tools.Deps.ValidateFileBlocks func(pb playbook.Playbook) error` — returns a non-nil error (the model-facing message) when a `file=` block targets an existing project path. The handler returns it as `reply{Error}`.

**Context:** The `submit_playbook` handler (tools.go:266-282) validates + calls `OnPlaybook`. Add an environmental check BEFORE `OnPlaybook`: if `ValidateFileBlocks` is set and returns an error, reply with that error (so the model retries with a `diff` block). The FS/project-root logic lives in the launcher (which has `req.ProjectRoot`), injected via the Deps hook — `internal/tools` stays FS-decoupled.

- [ ] **Step 1: Write the failing tests**

`internal/launcher/session_test.go` (the checker logic):
```go
func TestValidateFileBlocks_RejectsExistingPath(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "exists.go"), []byte("x"), 0o644)
	check := fileBlockValidator(dir) // the launcher's checker constructor
	pb := playbook.Playbook{Sections: []playbook.Section{{Content: []playbook.ContentItem{
		{Kind: "code", File: "exists.go", Code: "y"}}}}}
	if err := check(pb); err == nil || !strings.Contains(err.Error(), "diff") {
		t.Fatalf("must reject existing file= path + suggest diff, got %v", err)
	}
	pb.Sections[0].Content[0].File = "new.go"
	if err := check(pb); err != nil {
		t.Fatalf("a new path must be accepted, got %v", err)
	}
}
```
`internal/tools/tools_test.go`: a `submit_playbook` call whose `ValidateFileBlocks` returns an error → `reply.Error` carries it (don't call `OnPlaybook`).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/launcher/ -run TestValidateFileBlocks; go test ./internal/tools/ -run TestSubmit`
Expected: FAIL — `fileBlockValidator`/`ValidateFileBlocks` undefined.

- [ ] **Step 3: Implement**

In `internal/launcher/session.go`, add `func fileBlockValidator(projectRoot string) func(playbook.Playbook) error` — walk every `code` `ContentItem` with a non-empty `File`; `os.Stat(filepath.Join(projectRoot, file))`; if it exists, return `fmt.Errorf("file %q already exists — use a diff block to edit an existing file (file= is for new files)", file)`. Wire it into the session's `tools.Deps.ValidateFileBlocks` (where `OnPlaybook`/`Deps` is built, with `req.ProjectRoot`).
In `internal/tools/tools.go`: add `ValidateFileBlocks func(pb playbook.Playbook) error` to `Deps`; in the `submit_playbook` handler, after schema validation and BEFORE `OnPlaybook`: `if s.deps.ValidateFileBlocks != nil { if err := s.deps.ValidateFileBlocks(pb); err != nil { return reply{Error: err.Error()} } }`.

- [ ] **Step 4: Run tests + the packages**

Run: `go test ./internal/launcher/ ./internal/tools/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tools/tools.go internal/launcher/session.go internal/tools/tools_test.go internal/launcher/session_test.go
git commit -m "feat(submit_playbook): reject file= on an existing path (suggest diff)"
```

---

### Task 5: Prompt vocabulary (diff + file= blocks)

**Files:**
- Modify: `internal/author/structured.go` (`StructuredToolInstruction`); the markdown `ToolInstruction` (find it — `internal/author`)
- Test: `internal/author/structured_test.go` (+ the markdown instruction's test)

**Interfaces:** none (prompt text).

**Context:** Document both file-change block roles so the model authors them. The structured block bullet (`structured.go` ~28-30) gains the `diff` + `file=` vocabulary; the markdown `ToolInstruction` gets the parallel fence vocabulary.

- [ ] **Step 1: Write the failing test**

```go
	for _, want := range []string{"lang:\"diff\"", "file:", "new file", "diff block"} {
		if !strings.Contains(StructuredToolInstruction(), want) {
			t.Errorf("structured instruction missing file-change vocabulary %q", want)
		}
	}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/author/ -run TestStructuredToolInstruction`
Expected: FAIL — vocabulary absent.

- [ ] **Step 3: Implement**

Append to `StructuredToolInstruction` (and the markdown `ToolInstruction` with the parallel fence form): a paragraph — to **edit an existing file** use a `diff` block (`lang:"diff"` with a unified patch); to **create a NEW file** use a `file=` block (`lang:<lang>`, set `file:<relative path>`, the body is the new file's FULL content) — use `file=` ONLY for files that don't exist yet; edit existing files with a `diff` block.

- [ ] **Step 4: Run test**

Run: `go test ./internal/author/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/author/structured.go internal/author/structured_test.go
git commit -m "feat(author): prompt vocabulary for diff (edit) + file= (create) blocks"
```
(Add the markdown-instruction file + its test to `git add` if separate.)

---

## Self-Review

**Spec coverage (FC2):** schema `File` + render + recognition (T1) ✓; create/undo write-action with backup-restore (T2) ✓; the `file=` tab + `create`→`undo` + all-sites dispatch wiring (T3) ✓; authoring reject-if-exists → suggest diff (T4) ✓; prompt vocabulary for diff + file= (T5) ✓.

**Type consistency:** `ContentItem.File`/`Block.File` (T1) ↔ the `file=` fence + `code()` recognition; `EncodeFileAction`/`decodeFileAction` + `KindCreateFile`/`KindUndoCreate` (T2) ↔ the UI button `Payload`/`kindOf` (T3); `blockRunState.Status` toggle reused (T3); `Deps.ValidateFileBlocks` (T4) ↔ the launcher's `fileBlockValidator`.

**Risks carried from the spec (all-sites wiring):** the new kinds must land in orchestrator enum/`String`/`Do`, `kindOf`, `isShellActionKind`, BOTH click-dispatch blocks, the `orchCmd` result case (T3) — a missed site = an inert button. The tab `regionW` math must reserve the create/undo button (T3) or the fill tears.

**Deferred (NOT FC2):** file-watching (#3); the assisted-run feature (ROADMAP Phase 2).

**Open items the implementer must confirm against real code (flagged, not placeheld):**
- T1: `fence`'s real signature (add the `file` param + thread `item.File` from its caller); `code()`'s exact `attrs`/`blk` construction at render.go:529-535.
- T2: the orchestrator apply/undo test harness (build an Orchestrator with a driver cwd == a temp dir); the real `projectRoot`/`o.Drv.Cwd()` resolution at orchestrator.go:687.
- T3: the diff button cluster + `regionW` math (render.go:580-668) to mirror exactly; the two click-dispatch blocks (model.go:802 + 997).
- T4: where the session builds `tools.Deps` (with `OnPlaybook`) to add the `ValidateFileBlocks` hook with `req.ProjectRoot`.
- T5: the markdown `ToolInstruction`'s location + its test.
