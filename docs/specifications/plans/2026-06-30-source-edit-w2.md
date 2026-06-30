# Source edit + reload W2 (Implementation Plan)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An `[edit]` button on a file-backed playbook that opens `$EDITOR` on its `.md` source and reloads the viewer with the updated contents — no-mux via in-place editor suspend, mux via an editor tab + an mtime poll.

**Architecture:** Thread the on-disk source path into the viewer model (mirroring `SetProjectRoot`), gated so only stored/committed playbooks expose `[edit]` (temp-file viewers leave it empty). A header `[edit]` button branches on the mux signal: no-mux uses `tea.ExecProcess` (suspend → editor → resume → reload); mux spawns the editor in a docked pane and polls the source file's mtime on a tick to reload on save. Reload re-reads the `.md` into `m.md` + `reflow()`, preserving per-block transient state by id.

**Tech Stack:** Go; bubbletea v2 (`charm.land/bubbletea/v2`, `tea.ExecProcess`/`tea.Tick`); `internal/ui` (model/render), `internal/launcher` (storecmd), `internal/store` (path), `internal/mux` (SpawnDocked), `internal/orchestrator`.

## Global Constraints

- Module `github.com/Townk/ai-playbook`. gpg-signed Conventional Commits; NO `Co-Authored-By`; `git add` explicit paths; verify signing `git log -1 --format=%G?` == `G`.
- **File-backed only.** `[edit]` appears ONLY when `m.sourcePath != ""`. The source path is set ONLY from `storecmd.ShowMain` (the real store `.md` via `store.PathFor`); the temp-file viewer paths (cached-serve, create, inline) MUST leave it empty (they edit throwaway tmp files otherwise).
- **Mux-aware, mirroring FC1's view-diff branch:** no-mux (`m.asker == nil` / `m.askBridge != nil`) → `tea.ExecProcess(exec.Command(editor, source), cb)` (suspend the TUI, run on the real terminal, resume) → reload on the callback msg. mux (`m.asker != nil`) → spawn `editor source` in a **docked** pane (`mux.SpawnDocked`, NOT a float) + start a 1s mtime **poll** that reloads on change. The no-mux path needs NO poll (deterministic reload on editor-exit).
- **`$EDITOR` resolution:** `$VISUAL` → `$EDITOR` → `vi`.
- **Reload preserves transient state:** `reflow()` takes `m.blockStates` as input and never clears it, so per-block state survives a reload when block ids are stable. Reload = read `m.sourcePath` → strip front-matter (the `loadPlaybookSource` reference) → `m.md = body` → `reflow()`.
- **No new dependency** (poll, not fsnotify). NOT in scope: W1 (done); a general project-tree watcher.
- `gofmt -l`/`go vet`/`make lint` clean; touched packages pass `go test` (+ `-race` for `internal/ui`). Pre-existing `reengage_test` timeout is load-flaky — re-run in isolation if it trips.

---

### Task 1: Thread the on-disk source path into the model

**Files:**
- Modify: `internal/ui/main.go` (the `SetSourcePath` setter + consume-once), `internal/ui/model.go` (the `sourcePath` field)
- Modify: `internal/launcher/storecmd.go` (`ShowMain` calls `ui.SetSourcePath`)
- Test: `internal/ui/main_test.go` (or where `Set*` is tested), `internal/launcher/storecmd_test.go`

**Interfaces:**
- Produces: `func ui.SetSourcePath(p string)`; `model.sourcePath string` (non-empty ⇒ file-backed). Consumed by Tasks 2-4.

**Context:** Mirror `SetProjectRoot` EXACTLY (main.go:117-122: `var pendingProjectRoot string`; `func SetProjectRoot`; consumed at main.go:379-380 → stashed at main.go:421 `m.projectRoot = projectRoot`; field model.go:336). `storecmd.ShowMain` (storecmd.go:219-237) already resolves the real store path (`path, ok := resolveShow(slug)`) before reshaping `os.Args` + calling `uiMainFn()` — call `ui.SetSourcePath(path)` there. The temp-file paths (session.go cached-serve, create_progress.go, inline_input.go) do NOT call it.

- [ ] **Step 1: Write the failing tests**

```go
// internal/ui/main_test.go — consume-once round-trip (extract a takeSourcePath() helper Main calls)
func TestSetSourcePath_ConsumeOnce(t *testing.T) {
	SetSourcePath("/store/x.md")
	if got := takeSourcePath(); got != "/store/x.md" {
		t.Fatalf("takeSourcePath = %q, want /store/x.md", got)
	}
	if got := takeSourcePath(); got != "" {
		t.Fatalf("second take must be empty (consume-once), got %q", got)
	}
}
```
```go
// internal/launcher/storecmd_test.go — ShowMain wires the store path
func TestShowMain_SetsSourcePath(t *testing.T) {
	var got string
	defer swapSetSourcePath(func(p string) { got = p })() // a test seam over ui.SetSourcePath
	// ... drive ShowMain for a known slug whose pathFor → /store/known.md (reuse the storecmd test seams) ...
	if got != "/store/known.md" {
		t.Fatalf("ShowMain must SetSourcePath to the store path, got %q", got)
	}
}
```
(Adapt to the real storecmd test seams — `pathForFn`/`uiMainFn` are already injectable per the grounding; add a `setSourcePathFn` seam var over `ui.SetSourcePath` if storecmd doesn't already indirect it.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ui/ -run TestSetSourcePath; go test ./internal/launcher/ -run TestShowMain_SetsSourcePath`
Expected: FAIL — `SetSourcePath`/`takeSourcePath`/the wiring undefined.

- [ ] **Step 3: Implement**

`main.go`: add `var pendingSourcePath string`; `func SetSourcePath(p string) { pendingSourcePath = p }`; a `func takeSourcePath() string { p := pendingSourcePath; pendingSourcePath = ""; return p }` consume-once helper; call it in `Main()` next to where `projectRoot` is consumed (main.go:379-380) and stash `m.sourcePath = takeSourcePath()` next to main.go:421. `model.go`: add `sourcePath string` to the model struct (near `projectRoot`).
`storecmd.go`: in `ShowMain`, after resolving `path` and before `uiMainFn()`, call `ui.SetSourcePath(path)` (via the test seam if one is added).

- [ ] **Step 4: Run tests + the packages**

Run: `go test ./internal/ui/ ./internal/launcher/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/main.go internal/ui/model.go internal/launcher/storecmd.go internal/ui/main_test.go internal/launcher/storecmd_test.go
git commit -m "feat(ui): thread the playbook source path into the viewer (file-backed gate)"
```

---

### Task 2: The `[edit]` header button + `$EDITOR` resolution

**Files:**
- Modify: `internal/ui/render.go` or `internal/ui/model.go` (`appendEditButton` + the header render), new `internal/ui/editor.go` (`resolveEditor`)
- Test: `internal/ui/*_test.go`

**Interfaces:**
- Consumes: `model.sourcePath` (T1).
- Produces: a screen-fixed `Button{Kind:"edit", BlockID:"edit", Screen:true}` registered when `m.sourcePath != ""`; `func resolveEditor() string`. Consumed by Tasks 3-4.

**Context:** Mirror `appendCachedButton` (model.go:1838, called from `reflow()` at model.go:510) — a `Screen:true` playbook-level button on the header row (`pillRow := m.bodyTop()-2`, model.go:1846; `bodyTop()` model.go:574). Add `appendEditButton()` (gated `m.sourcePath != ""`) called from `reflow()` beside `appendCachedButton()`. Render the `[edit]` affordance in the header area (mirror `cachedBadge`/`cachedBadgeRow` model.go:1792/1892). `Screen` buttons hit-test by absolute Y in `buttonAt` (button.go:37-42).

- [ ] **Step 1: Write the failing tests**

```go
func TestEditButton_OnlyWhenFileBacked(t *testing.T) {
	fb := newTestModelFileBacked(t, "/store/x.md") // m.sourcePath set
	fb.reflow()
	if !hasButton(fb.buttons, "edit") { t.Fatal("file-backed playbook must have an [edit] button") }

	eph := newTestModel(t) // m.sourcePath == ""
	eph.reflow()
	if hasButton(eph.buttons, "edit") { t.Fatal("ephemeral playbook must NOT have an [edit] button") }
}

func TestResolveEditor_Order(t *testing.T) {
	t.Setenv("VISUAL", ""); t.Setenv("EDITOR", "")
	if resolveEditor() != "vi" { t.Fatal("fallback must be vi") }
	t.Setenv("EDITOR", "nano"); if resolveEditor() != "nano" { t.Fatal("$EDITOR wins over fallback") }
	t.Setenv("VISUAL", "code -w"); if resolveEditor() != "code -w" { t.Fatal("$VISUAL wins over $EDITOR") }
}
```
(Adapt `newTestModelFileBacked`/`hasButton` to the real harness; `resolveEditor` may return a string the caller splits into argv — keep it as the raw env value, split with `strings.Fields` at the call site.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ui/ -run 'TestEditButton|TestResolveEditor'`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

`internal/ui/editor.go`: `func resolveEditor() string { for _, k := range []string{"VISUAL", "EDITOR"} { if v := strings.TrimSpace(os.Getenv(k)); v != "" { return v } }; return "vi" }`.
`model.go`/`render.go`: add `appendEditButton()` mirroring `appendCachedButton` — gated `if m.sourcePath == "" { return }`; register a `Screen:true` `Button{Kind:"edit", BlockID:"edit", …}` on the header row + render a small `[edit]` affordance (pill/glyph) in the header (mirror `cachedBadge`). Call `m.appendEditButton()` in `reflow()` beside `m.appendCachedButton()` (model.go:510).

- [ ] **Step 4: Run tests + the package**

Run: `go test ./internal/ui/ -run 'TestEditButton|TestResolveEditor' && go build ./... && go test ./internal/ui/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/editor.go internal/ui/model.go internal/ui/render.go internal/ui/*_test.go
git commit -m "feat(ui): [edit] header button (file-backed) + $EDITOR resolution"
```

---

### Task 3: Reload helper + the no-mux edit (`tea.ExecProcess`)

**Files:**
- Modify: `internal/ui/model.go` (`reloadSource` + `reloadMsg` + the no-mux `[edit]` dispatch)
- Test: `internal/ui/*_test.go`

**Interfaces:**
- Consumes: `model.sourcePath` (T1), `resolveEditor` (T2), `reflow`, `m.blockStates`.
- Produces: `func (m *model) reloadSource() error` (read `sourcePath` → `m.md` → `reflow()`); `type reloadMsg struct { Err error }`. Consumed by Task 4 (the mux poll reuses `reloadSource`).

**Context:** `reflow()` (model.go:508) rebuilds `m.blocks` from `m.md` and reads (never clears) `m.blockStates`, so transient state survives. `loadPlaybookSource` (main.go:182) is the front-matter-stripping reference. `tea.ExecProcess(c *exec.Cmd, fn tea.ExecCallback) tea.Cmd`; `tea.ExecCallback func(error) tea.Msg` — the callback returns a msg on editor exit.

- [ ] **Step 1: Write the failing tests**

```go
func TestReloadSource_UpdatesMdAndPreservesState(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "p.md")
	os.WriteFile(src, []byte("# T\n\n```bash {id=one}\necho hi\n```\n"), 0o644)
	m := newTestModelFileBacked(t, src)
	m.blockStates["one"] = blockRunState{Status: "ok"} // transient state to preserve
	// edit the source on disk
	os.WriteFile(src, []byte("# T2\n\n```bash {id=one}\necho bye\n```\n"), 0o644)
	if err := m.reloadSource(); err != nil { t.Fatal(err) }
	if !strings.Contains(m.md, "echo bye") { t.Fatal("m.md must reflect the edited source") }
	if m.blockStates["one"].Status != "ok" { t.Fatal("transient block state must survive reload") }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ui/ -run TestReloadSource`
Expected: FAIL — `reloadSource` undefined.

- [ ] **Step 3: Implement**

`model.go`: 
```go
type reloadMsg struct{ Err error }

func (m *model) reloadSource() error {
	if m.sourcePath == "" { return nil }
	data, err := os.ReadFile(m.sourcePath)
	if err != nil { return err }
	m.md = stripFrontMatter(string(data)) // reuse loadPlaybookSource's stripping (extract if needed)
	m.reflow()
	return nil
}
```
Add the no-mux `[edit]` dispatch: in the screen-button click handler, for `b.Kind == "edit"` when `m.asker == nil` (no-mux): `parts := strings.Fields(resolveEditor()); parts = append(parts, m.sourcePath); cmd := exec.Command(parts[0], parts[1:]...); return m, tea.ExecProcess(cmd, func(err error) tea.Msg { return reloadMsg{Err: err} })`. Add a `case reloadMsg:` handler → `_ = m.reloadSource(); return m, nil`.

- [ ] **Step 4: Run tests + the package**

Run: `go test ./internal/ui/ -run TestReloadSource && go build ./... && go test ./internal/ui/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/model.go internal/ui/*_test.go
git commit -m "feat(ui): reloadSource + no-mux [edit] via tea.ExecProcess"
```

---

### Task 4: The mux edit (docked editor tab + mtime poll)

**Files:**
- Modify: `internal/orchestrator/orchestrator.go` (`EditSource` — docked spawn), `internal/ui/model.go` (the mux `[edit]` dispatch + `sourcePollMsg` + the poll)
- Test: `internal/orchestrator/orchestrator_test.go`, `internal/ui/*_test.go`

**Interfaces:**
- Consumes: `resolveEditor` (T2), `reloadSource` (T3), `model.sourcePath`, `m.orch.Float`.
- Produces: `func (o *Orchestrator) EditSource(editor, path string) error` (docked spawn); `type sourcePollMsg struct{}`; `func (m model) sourcePollCmd() tea.Cmd`; `model.sourceMtime time.Time`.

**Context:** Mirror FC1's `viewDiff` (orchestrator.go:681-703) but `SpawnDocked` (mux.go:206, tiled — NOT `SpawnFloat`) + `Floating:false`. The UI reaches the mux via `m.orch.Float` (orchestrator.go:118; guard `m.orch != nil && m.orch.Float != nil`). The poll mirrors the `tea.Tick` loops (model.go:103/109): a 1s `sourcePollMsg`; the handler stats the source mtime, reloads on change, re-arms. Start the poll only after the mux `[edit]` spawn.

- [ ] **Step 1: Write the failing tests**

```go
// orchestrator: EditSource spawns the editor docked
func TestEditSource_SpawnsDocked(t *testing.T) {
	var got mux.SpawnOptions
	o := &Orchestrator{Float: &fakeDockMux{dock: func(opts mux.SpawnOptions) error { got = opts; return nil }}}
	_ = o.EditSource("nano", "/store/x.md")
	if len(got.Cmd) < 2 || got.Cmd[0] != "nano" || got.Cmd[len(got.Cmd)-1] != "/store/x.md" {
		t.Fatalf("EditSource must spawn `nano … /store/x.md` docked, got %v", got.Cmd)
	}
}
```
```go
// ui: a source mtime change triggers a reload
func TestSourcePoll_ReloadsOnMtimeChange(t *testing.T) {
	dir := t.TempDir(); src := filepath.Join(dir, "p.md")
	os.WriteFile(src, []byte("# A\n\n```bash {id=one}\necho a\n```\n"), 0o644)
	m := newTestModelFileBacked(t, src)
	st, _ := os.Stat(src); m.sourceMtime = st.ModTime()
	os.Chtimes(src, time.Now().Add(time.Hour), time.Now().Add(time.Hour)) // bump mtime
	os.WriteFile(src, []byte("# A\n\n```bash {id=one}\necho b\n```\n"), 0o644)
	m2i, _ := m.Update(sourcePollMsg{})
	if !strings.Contains(m2i.(model).md, "echo b") { t.Fatal("a newer source mtime must reload") }
}
```
(Adapt the orchestrator fake to the existing float-fake pattern; if `SpawnDocked` isn't on the test fake interface, extend it.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/orchestrator/ -run TestEditSource; go test ./internal/ui/ -run TestSourcePoll`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

`orchestrator.go`:
```go
// EditSource opens the playbook source in a docked editor pane (mux only).
func (o *Orchestrator) EditSource(editor, path string) error {
	if o.Float == nil { return nil }
	parts := append(strings.Fields(editor), path)
	return o.Float.SpawnDocked(mux.SpawnOptions{Cmd: parts, Cwd: o.projectRoot(), Name: "edit", Floating: false})
}
```
`model.go`: the mux `[edit]` dispatch — when `b.Kind == "edit"` and `m.asker != nil` (mux): `if m.orch != nil { _ = m.orch.EditSource(resolveEditor(), m.sourcePath) }`; capture `st, _ := os.Stat(m.sourcePath); m.sourceMtime = st.ModTime()`; `return m, m.sourcePollCmd()`. Add `type sourcePollMsg struct{}`; `func (m model) sourcePollCmd() tea.Cmd { return tea.Tick(time.Second, func(time.Time) tea.Msg { return sourcePollMsg{} }) }`; a `case sourcePollMsg:` handler: `if m.sourcePath == "" { return m, nil }; if st, err := os.Stat(m.sourcePath); err == nil && st.ModTime().After(m.sourceMtime) { m.sourceMtime = st.ModTime(); _ = m.reloadSource() }; return m, m.sourcePollCmd()` (re-arm to keep watching).

- [ ] **Step 4: Run tests + the packages (-race)**

Run: `go test ./internal/orchestrator/ -run TestEditSource; go test ./internal/ui/ -run TestSourcePoll`
Run: `go build ./... && go test -race ./internal/ui/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/orchestrator/orchestrator.go internal/ui/model.go internal/orchestrator/orchestrator_test.go internal/ui/*_test.go
git commit -m "feat(ui): mux [edit] — docked editor pane + source mtime poll reload"
```

---

## Self-Review

**Spec coverage (W2):** source-path threading + file-backed gate (T1) ✓; the `[edit]` header button + `$EDITOR` resolution (T2) ✓; reload + no-mux `tea.ExecProcess` (T3) ✓; mux docked editor + mtime poll (T4) ✓. Reload preserves `blockStates` (T3 — reflow reads, never clears). No fsnotify / no project-tree watcher.

**Type consistency:** `SetSourcePath`/`model.sourcePath` (T1) ↔ the `[edit]` gate (T2) ↔ `reloadSource`/the dispatch (T3) ↔ `EditSource`/`sourcePollMsg`/`sourceMtime` (T4); `resolveEditor` (T2) used by both T3 (ExecProcess) + T4 (EditSource); `reloadSource` (T3) reused by the T4 poll.

**Risks (from grounding):** source-path threading is the prereq — set ONLY from `ShowMain`, gated `m.sourcePath != ""` so temp-file viewers never expose `[edit]` (T1); the mux path needs the poll (no editor-exit signal across a tab) while no-mux is deterministic (T3 vs T4); reload must not clobber in-flight state (reflow keeps `blockStates` by id, T3).

**Open items the implementer must confirm against real code (flagged, not placeheld):**
- T1: the `SetProjectRoot` consume-once pattern (main.go:117-122/379-380/421) + the `storecmd` test seams (`pathForFn`/`uiMainFn`) to wire/assert `SetSourcePath`; whether a `setSourcePathFn` seam is needed.
- T2: `appendCachedButton`/`cachedBadge` (model.go:1838/1792/1846) to mirror for the header `[edit]` button; the screen-button hit-test (button.go:37-42); the `newTestModelFileBacked`/`hasButton` harness.
- T3: `loadPlaybookSource` (main.go:182) front-matter stripping to reuse (extract a `stripFrontMatter` if it's inline); the screen-button click-dispatch site + the `m.asker`/`m.askBridge` branch (model.go:1234-1248); `tea.ExecProcess` import.
- T4: `SpawnDocked` (mux.go:206) + the float-fake test pattern to extend; `m.orch.Float` access + the `viewDiff` precedent (orchestrator.go:681); the `tea.Tick` loop pattern (model.go:103).
