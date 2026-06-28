# Structured Playbook Phase B1 — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Migrate the escalate path to structured `submit_playbook` authoring (like create), and extract the authoring-progress render into one reusable `ProgressWidget` shared by the inline and in-viewer hosts.

**Architecture:** A new `internal/ui/ProgressWidget` wraps the existing render helpers (`spinnerLine`/`workingLabel`/`activityLineStr`/`collapseLine`); the inline host (`progressAskModel`) and the in-viewer "thinking" block (`model`) embed it. `RunStream` gains a structured mode (`StreamOptions.Structured` + `Body func() string`): the model drains the agent narration instead of accumulating it, and on EOF renders the captured structured playbook. `authorPlaybook` (escalate) flips to `Structured: true` and supplies a `Body` closure over `sess.lastPB`; create + escalate share one structured-authoring core.

**Tech Stack:** Go; `charm.land/bubbletea/v2` + `charm.land/lipgloss/v2`; the Phase A `internal/playbook` schema/renderer/`submit_playbook` tool/capture (reused verbatim).

## Global Constraints

- Module path `github.com/Townk/ai-playbook`. gpg-signed Conventional Commits; NO `Co-Authored-By`/AI-attribution trailers; `git add` explicit paths; verify signing via `git log -1 --format=%G?` == `G`.
- Reuse Phase A verbatim: `playbook.Playbook`/`playbook.Render`, the `submit_playbook` tool, `tools.Deps.OnPlaybook` → `session.lastPB atomic.Pointer[playbook.Playbook]`, `ui.SetFinalDraft`, `capturedMetaSeam`.
- `ProgressWidget.Render` MUST produce the same components as today (spinner + the existing 16 `workingPhrases` escalating one per `workingStepSec`=15s + elapsed + the `activityLineStr` activity line). No new phrases, no new render helpers.
- Escalate's structured result is a **final draft, not auto-persisted on EOF**: `finalDraft=true`, `persistOnFinish=false` — the user reviews and presses `w` (mirrors create; the cache body is stored by the launcher as today).
- Re-engagement stays markdown (B3). Adapt + `project_bound` gating + `workdir` removal are B2. Do NOT touch them.
- `gofmt -l` clean; `go vet` clean; the touched packages pass `go test` (and `-race` for `internal/ui` / `internal/launcher`).

---

### Task 1: `ProgressWidget` component

**Files:**
- Create: `internal/ui/progress_widget.go`
- Test: `internal/ui/progress_widget_test.go`

**Interfaces:**
- Produces: `type ui.ProgressWidget struct{}` with methods `Tick()`, `SetActivity(summary string)`, `Reset()`, `Elapsed() int`, `Render(width int) string`. Consumed by Tasks 2 (inline host) + 3 (in-viewer).

- [ ] **Step 1: Write the failing test**

```go
package ui

import (
	"strings"
	"testing"
)

func TestProgressWidget_Render(t *testing.T) {
	var w ProgressWidget
	// 0s, no activity → spinner + first phrase + "0s", single line.
	got := w.Render(80)
	if !strings.Contains(got, "Working…") || !strings.Contains(got, "0s") {
		t.Fatalf("render at 0s = %q, want first phrase + 0s", got)
	}
	if strings.Contains(got, "\n") {
		t.Errorf("no-activity render must be a single line: %q", got)
	}
	// With activity → a second line carrying the (collapsed) summary.
	w.SetActivity("running   gg build")
	got = w.Render(80)
	if !strings.Contains(got, "\n") || !strings.Contains(got, "running gg build") {
		t.Errorf("activity render must add a collapsed activity line: %q", got)
	}
}

func TestProgressWidget_TickAndElapsed(t *testing.T) {
	var w ProgressWidget
	for i := 0; i < 155; i++ { // 155 ticks = 15.5s
		w.Tick()
	}
	if w.Elapsed() != 15 {
		t.Fatalf("Elapsed() = %d, want 15 (155 ticks / 10)", w.Elapsed())
	}
	// At 15s the phrase has escalated past the first entry.
	if got := w.Render(80); strings.Contains(got, "Working… ") && !strings.Contains(got, "15s") {
		t.Errorf("render at 15s = %q, want elapsed 15s", got)
	}
}

func TestProgressWidget_Reset(t *testing.T) {
	var w ProgressWidget
	w.Tick()
	w.SetActivity("x")
	w.Reset()
	if w.Elapsed() != 0 || strings.Contains(w.Render(80), "\n") {
		t.Errorf("Reset must clear elapsed + activity, got render %q", w.Render(80))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ui/ -run TestProgressWidget`
Expected: FAIL — `ProgressWidget` undefined.

- [ ] **Step 3: Implement the widget**

```go
package ui

// ProgressWidget is the reusable authoring-progress render shared by every host
// (the inline no-mux/arg progress and the in-viewer "thinking" block). It owns the
// spinner frame, the elapsed-time ticks (100ms each), and the latest activity
// summary, and renders the one canonical progress block (spinner + the escalating
// "Working…" phrase + elapsed, with the activity line below). Hosts drive it via
// Tick()/SetActivity() on their existing tick/activity messages and call Render in
// their View, so any change to the progress look propagates everywhere.
type ProgressWidget struct {
	frame    int    // spinner frame (advances each Tick)
	ticks    int    // 100ms ticks; elapsed seconds = ticks / 10
	activity string // latest collapsed activity summary ("" → no activity line)
}

// Tick advances the spinner frame and the elapsed-time counter by one 100ms tick.
func (w *ProgressWidget) Tick() { w.frame++; w.ticks++ }

// SetActivity replaces the activity summary shown below the spinner (collapsed to a
// single legible line). Pass "" to clear it.
func (w *ProgressWidget) SetActivity(summary string) {
	if summary == "" {
		w.activity = ""
		return
	}
	w.activity = collapseLine(summary)
}

// Reset returns the widget to its initial state (frame 0, elapsed 0, no activity) —
// used when a host starts a fresh authoring/thinking phase.
func (w *ProgressWidget) Reset() { w.frame = 0; w.ticks = 0; w.activity = "" }

// Elapsed returns the elapsed whole seconds (ticks/10).
func (w *ProgressWidget) Elapsed() int { return w.ticks / 10 }

// Render returns the progress block: "<spinner> <phrase> <Ns>" and, when an activity
// summary is set, the activity line below it, truncated to width.
func (w *ProgressWidget) Render(width int) string {
	line := spinnerLine(w.frame, workingLabel(w.Elapsed()), w.Elapsed())
	if w.activity == "" {
		return line
	}
	return line + "\n" + activityLineStr(w.activity, width)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/ui/ -run TestProgressWidget`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/progress_widget.go internal/ui/progress_widget_test.go
git commit -m "feat(ui): reusable ProgressWidget (spinner + phrases + elapsed + activity)"
```

---

### Task 2: Inline host (`progressAskModel`) uses `ProgressWidget`

**Files:**
- Modify: `internal/launcher/create_progress.go` (the `progressAskModel` struct + its tick/activity handlers + its view; the `WaitingLine` call site)
- Modify: `internal/ui/waiting.go` (make `WaitingLine` delegate to a `ProgressWidget`, or remove if unused after this task)
- Test: `internal/launcher/create_progress_test.go` (assert the inline view still renders the spinner/activity)

**Interfaces:**
- Consumes: `ui.ProgressWidget` (Task 1).

**Context:** `progressAskModel` (create_progress.go ~49-63) has `frame int`, `ticks int`, `activity string` and renders via `ui.WaitingLine(m.frame, m.ticks/10, m.activity, m.width)` (~line 152). Its tick handler increments `frame`/`ticks`; its `paActMsg` handler sets `activity`.

- [ ] **Step 1: Write the failing test**

Add to `internal/launcher/create_progress_test.go`:
```go
func TestProgressAskModel_RendersWidget(t *testing.T) {
	m := newProgressAskModel(nil, nil, nil) // act/done/reqs nil → static
	m.width = 80
	m.pw.Tick() // advance one tick via the embedded widget
	m.pw.SetActivity("compiling")
	out := m.View()
	if !strings.Contains(out, "compiling") {
		t.Fatalf("inline progress view must render the widget activity, got %q", out)
	}
}
```
(If `newProgressAskModel` has a different constructor signature, match it; the assertion is: the view renders the embedded `ProgressWidget`.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/launcher/ -run TestProgressAskModel_RendersWidget`
Expected: FAIL — `m.pw` undefined.

- [ ] **Step 3: Implement**

In `internal/launcher/create_progress.go`:
- Replace the `frame int`, `ticks int`, `activity string` fields on `progressAskModel` with `pw ui.ProgressWidget`.
- In the tick handler (the 100ms `tea.Tick` case that did `m.frame++; m.ticks++`): call `m.pw.Tick()`.
- In `paActMsg` (the activity handler that did `m.activity = …`): call `m.pw.SetActivity(string(msg))` (match the actual msg type/field).
- In the view (where it called `ui.WaitingLine(m.frame, m.ticks/10, m.activity, m.width)`): call `m.pw.Render(m.width)`.

In `internal/ui/waiting.go`, make `WaitingLine` delegate so any remaining caller stays identical:
```go
// WaitingLine renders the shared progress block (spinner + escalating phrase +
// elapsed + activity). Thin wrapper over ProgressWidget for callers that hold raw
// frame/elapsed/activity values rather than a widget.
func WaitingLine(frame, elapsedSec int, activity string, width int) string {
	w := ProgressWidget{frame: frame, ticks: elapsedSec * 10, activity: collapseLine(activity)}
	return w.Render(width)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/launcher/ -run TestProgressAskModel`
Run: `go test ./internal/ui/ -run TestWaiting` (if a WaitingLine test exists; else skip)
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/launcher/create_progress.go internal/ui/waiting.go internal/launcher/create_progress_test.go
git commit -m "refactor(launcher): inline progress host embeds ProgressWidget"
```

---

### Task 3: In-viewer "thinking" block uses `ProgressWidget`

**Files:**
- Modify: `internal/ui/model.go` (the `model` struct fields `spinFrame`/`spinTicks`/`activityLine` → an embedded `ProgressWidget`; the tick + activity handlers; the thinking-block render ~2238-2279)
- Test: `internal/ui/model_test.go` (assert the viewer's thinking render goes through the widget)

**Interfaces:**
- Consumes: `ui.ProgressWidget` (Task 1).

**Context:** the model renders the thinking block (~model.go:2238-2279) as `spinnerLine(m.spinFrame, workingLabel(elapsed), elapsed)` + `activityLineStr(m.activityLine, cw)`. The 100ms tick handler increments `m.spinFrame`/`m.spinTicks`; the `activityMsg` handler sets `m.activityLine = collapseLine(msg.summary)` (~model.go:1430); a new thinking session resets `m.spinFrame=0; m.spinTicks=0` (streamEventsMsg thinkEvent ~597-598); `m.activityLine` is cleared when real content arrives (~588).

- [ ] **Step 1: Write the failing test**

Add to `internal/ui/model_test.go`:
```go
func TestModelThinkingRendersWidget(t *testing.T) {
	m := newModel("agent", "")
	m.width, m.height = 80, 24
	m.thinking = true
	m.progress.SetActivity("diagnosing the failure")
	out := m.viewString()
	if !strings.Contains(out, "diagnosing the failure") {
		t.Fatalf("thinking block must render the ProgressWidget activity, got %q", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ui/ -run TestModelThinkingRendersWidget`
Expected: FAIL — `m.progress` undefined.

- [ ] **Step 3: Implement**

In `internal/ui/model.go`:
- Add `progress ProgressWidget` to the `model` struct. Remove `spinFrame`/`spinTicks` and route `activityLine` through the widget: replace the three fields' uses:
  - the 100ms tick handler (`m.spinFrame++; m.spinTicks++`) → `m.progress.Tick()`;
  - the new-thinking-session reset (`m.spinFrame = 0; m.spinTicks = 0`) → `m.progress.Reset()`;
  - the activity handler (`m.activityLine = collapseLine(msg.summary)`) → `m.progress.SetActivity(msg.summary)`;
  - the real-content clear (`m.activityLine = ""`) → `m.progress.SetActivity("")`;
  - the thinking-block render → `m.progress.Render(cw)` (cw = content width), replacing the `spinnerLine(...) + activityLineStr(...)` composition.
- If `activityLine` is read elsewhere (e.g. tests), expose it via the widget or keep a thin accessor; do not leave dangling references.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/ui/ -run 'TestModelThinking|TestActivity'`
Expected: PASS (existing activity tests still green — they may need `m.activityLine` → `m.progress.SetActivity`/render assertions updated).

- [ ] **Step 5: Commit**

```bash
git add internal/ui/model.go internal/ui/model_test.go
git commit -m "refactor(ui): viewer thinking block embeds ProgressWidget"
```

---

### Task 4: `RunStream` structured mode (drain narration, render captured on EOF)

**Files:**
- Modify: `internal/ui/stream_run.go` (`StreamOptions` + `RunStream`)
- Modify: `internal/ui/model.go` (the `model` fields + `streamEventsMsg` handler)
- Test: `internal/ui/model_test.go`

**Interfaces:**
- Produces: `StreamOptions.Structured bool`, `StreamOptions.Body func() string`; `model.structured bool`, `model.bodyProvider func() string`. Consumed by Task 5 (escalate).

**Context:** `streamEventsMsg` (model.go:569-665): `textEvent` does `m.md += e.text` and ends thinking (583-589); on `msg.eof` the `finalDraft` branch (622-643) strips preamble + sets the title from the H1 + junk-guards. In structured authoring there is no playbook in the stream (it arrives via `submit_playbook`→`OnPlaybook`), so the narration must be drained and the captured playbook rendered on EOF.

- [ ] **Step 1: Write the failing test**

```go
func TestStructuredStreamRendersBodyOnEOF(t *testing.T) {
	m := newModel("agent", "")
	m.width, m.height = 80, 24
	m.streaming, m.thinking = true, true
	m.finalDraft = true
	m.structured = true
	m.bodyProvider = func() string { return "# Restore wrapper\n\n```bash {id=fix}\necho hi\n```\n" }
	// A narration textEvent must NOT become the playbook in structured mode.
	m1, _ := m.Update(streamEventsMsg{events: []streamEvent{textEvent{text: "let me diagnose…"}}})
	m = m1.(model)
	if strings.Contains(m.md, "diagnose") {
		t.Fatalf("structured mode must drain narration, not accumulate it: md=%q", m.md)
	}
	// On EOF, m.md becomes the captured rendered playbook.
	m2, _ := m.Update(streamEventsMsg{eof: true})
	m = m2.(model)
	if !strings.Contains(m.md, "# Restore wrapper") || !strings.Contains(m.md, "{id=fix}") {
		t.Fatalf("structured EOF must render bodyProvider(): md=%q", m.md)
	}
}
```
(Match the real `streamEvent`/`textEvent`/`streamEventsMsg` type names from model.go.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ui/ -run TestStructuredStreamRendersBodyOnEOF`
Expected: FAIL — `m.structured`/`m.bodyProvider` undefined.

- [ ] **Step 3: Implement**

In `internal/ui/model.go`:
- Add to `model`: `structured bool` and `bodyProvider func() string`.
- In `streamEventsMsg`, the `textEvent` case: when `m.structured`, skip accumulation + the thinking-end (the narration is not the playbook):
  ```go
  case textEvent:
      if m.structured {
          break // structured authoring: stream carries narration, not the playbook; drain it
      }
      m.md += e.text
      m.dirty = true
      if strings.TrimSpace(e.text) != "" { m.thinking = false; m.progress.SetActivity("") }
  ```
- In the `msg.eof` branch, BEFORE the `if m.finalDraft {` block, inject the captured body:
  ```go
  if m.structured && m.bodyProvider != nil {
      m.md = m.bodyProvider()
      m.dirty = true
  }
  ```
  The existing `finalDraft` block then strips the preamble, sets the title, and junk-guards `m.md` (now the rendered playbook). `persistOnFinish` stays false → no auto-persist; `w` persists.

In `internal/ui/stream_run.go`:
- Add to `StreamOptions`: `Structured bool` and `Body func() string`.
- In `RunStream`, after `m := newModel(harness, "")`, when `opts.Structured`:
  ```go
  m.structured = opts.Structured
  m.bodyProvider = opts.Body
  if opts.Structured {
      m.finalDraft = true // the captured structured playbook is a final draft (w persists, r refines)
  }
  ```
  (Set alongside the existing `m.activity = opts.Activity` etc.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/ui/ -run 'TestStructuredStreamRendersBodyOnEOF|TestRunStream'`
Run: `go test ./internal/ui/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/stream_run.go internal/ui/model.go internal/ui/model_test.go
git commit -m "feat(ui): RunStream structured mode — drain narration, render captured playbook on EOF"
```

---

### Task 5: Escalate → structured (shared core + `authorPlaybook` flip)

**Files:**
- Modify: `internal/launcher/session.go` (`authorPlaybook`)
- Modify: `internal/launcher/create_progress.go` (factor the shared structured-authoring core; reuse `capturedMetaSeam`)
- Test: `internal/launcher/session_test.go` (escalate authors structured)

**Interfaces:**
- Consumes: `author.AuthorOptions{Structured: true}`, `session.lastPB`, `playbook.Render`, `StreamOptions.Structured`/`Body`, `capturedMetaSeam`, `ui.SetFinalDraft` (via the structured RunStream).

**Context:** `authorPlaybook` (session.go ~379-464) today: `author.AuthorEvents(req, {Cfg, MCPConfigPath})` (markdown) → `agentstream.FanOut(events, closeFn, ActivityBuffer)` → `reader, activity, fo` → `ui.RunStream(reader, {Activity: activity, AskBridge: …, Reengage: …, Driver: sess.drv, …})` → after return, `body := fo.Body()` → cache store. create's `realCreateStream` already builds the `Structured:true` stream + the `body()` closure preferring `sess.lastPB.Load()` else `fo.Body()`.

- [ ] **Step 1: Write the failing test**

Add to `internal/launcher/session_test.go` (mirror Phase A's `TestCreate_StructuredRenderAndSeam`):
```go
func TestEscalate_AuthorsStructured(t *testing.T) {
	sess := /* a session with a real tools backend whose OnPlaybook stores into sess.lastPB */
	pb := playbook.Playbook{
		Title:    "Fix the build",
		Sections: []playbook.Section{{Heading: "Fix", Content: []playbook.ContentItem{
			{Kind: "code", Lang: "bash", Code: "make", ID: "fix"}}}},
		Meta: playbook.Meta{Description: "fix it", ProjectBound: true},
	}
	raw, _ := json.Marshal(pb)
	res, err := tools.Dial(sess.socket, tools.Call{Tool: "submit_playbook", Playbook: raw})
	if err != nil || !res.OK {
		t.Fatalf("submit: %+v err=%v", res, err)
	}
	body := structuredBody(sess, /* fanout fallback */ nil) // the shared body() closure
	if !strings.Contains(body, "# Fix the build") || !strings.Contains(body, "```bash {id=fix}") {
		t.Fatalf("escalate body must be the rendered captured playbook: %s", body)
	}
}
```
(Adapt to the actual shared-core function names produced in Step 3; the assertion is: escalate's body is `playbook.Render(sess.lastPB)`.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/launcher/ -run TestEscalate_AuthorsStructured`
Expected: FAIL — the shared body helper / structured escalate not wired.

- [ ] **Step 3: Implement**

In `internal/launcher/create_progress.go`, factor the structured stream + body closure into a shared helper both create and escalate call (rename `realCreateStream` generically or add `structuredStream(req, sess, cfg)` returning the `reader, activity, body()` triple; `body()` returns `playbook.Render(*sess.lastPB.Load())` else `fo.Body()`). Keep create using it.

In `internal/launcher/session.go` `authorPlaybook`:
- Build the structured stream via the shared helper (`Structured: true`).
- Call `ui.RunStream(reader, StreamOptions{... Structured: true, Body: body, Activity: activity, AskBridge: bridgeOf(sess), Reengage: re, Driver: sess.drv, Shell: cfg.Driver.Shell, Title: title})` — the structured RunStream shows the `ProgressWidget`, drains the reader, and renders `Body()` on EOF as a `finalDraft`.
- Build the Reengage with `capturedMetaSeam(sess)` (no separate metadata pass) so the saved front matter + `project_bound` come from the captured `meta` (same as create's `newCreateReengage`).
- After `RunStream` returns, cache-store `body()` (the rendered playbook) as today.
- The failure context (`req.Command`/`Exit`/`Scrollback`) flows through `SystemPrompt`/`BuildUserMessage` unchanged.

- [ ] **Step 4: Run tests + full suite**

Run: `go test ./internal/launcher/ -run TestEscalate_AuthorsStructured`
Run: `go build ./... && go test ./...`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/launcher/session.go internal/launcher/create_progress.go internal/launcher/session_test.go
git commit -m "feat(escalate): structured authoring via submit_playbook (shared core; in-viewer progress; finalDraft)"
```

---

## Self-Review

**Spec coverage:** ProgressWidget extraction (Tasks 1-3) ✓; escalate → structured with in-viewer progress widget + render-captured-on-EOF + finalDraft (Tasks 4-5) ✓; shared structured-authoring core (Task 5) ✓; failure context preserved (Task 5) ✓; re-engagement stays markdown / adapt deferred (Global Constraints) ✓.

**Deferred (not in B1):** re-engagement → structured + collapse finalize (B3); adapt + `project_bound` gating + `workdir` removal (B2); mux side-pane is just the existing docked viewer placement (no work); the `file=`/diff + viewer-UX-polish specs.

**Open item for the implementer to confirm (Task 5):** whether `RunStream`'s non-structured markdown-stream path still has callers after escalate flips; if escalate was the only one, leave it as the structured fallback render (Body() → fo.Body()) — do not remove it (the fallback relies on it).

**Type consistency:** `ProgressWidget` methods (Task 1) match the call sites in Tasks 2-3; `model.structured`/`bodyProvider` + `StreamOptions.Structured`/`Body` (Task 4) match Task 5's `RunStream` call; `capturedMetaSeam`/`sess.lastPB`/`playbook.Render` are the Phase A names.

**Placeholder scan:** code steps carry real code; the two test snippets that depend on exact local type/constructor names (Task 2 `newProgressAskModel`, Task 5 session test harness) say so explicitly and state the invariant to assert — the implementer matches the real names from the file.
