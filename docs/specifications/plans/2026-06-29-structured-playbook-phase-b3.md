# Structured Playbook Phase B3 — Implementation Plan (re-engagement → structured + follow-up-aware finalize)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make in-viewer re-engagement author the playbook structured (`submit_playbook` → `sess.lastPB`), render that captured playbook per-stream at EOF, and make saving follow-up-aware — persist the clean run as-is, re-author when the run diverged — collapsing the always-regenerate `w`.

**Architecture:** The escalate viewer is already structured (B1). B3 makes the structured-EOF render **per-stream** (each re-arm declares whether its stream produced a structured playbook), wires a `Reengage.Body` closure (the live `sess.lastPB` render) so both the escalate and cached-replay viewers can render a re-engaged playbook, flips the playbook-producing re-engagements to `Structured:true`, and replaces the troubleshoot `w`'s always-regenerate with a `hadFollowup`-pivoted save decision.

**Tech Stack:** Go; bubbletea v2 viewer (`internal/ui`); `internal/launcher` (session/reengage); `internal/orchestrator`; the B1 structured core (`structuredStream`/`structuredBody`/`RunStream` Structured) + Phase-A `submit_playbook`/`sess.lastPB`.

## Global Constraints

- Module `github.com/Townk/ai-playbook`. gpg-signed Conventional Commits; NO `Co-Authored-By`; `git add` explicit paths; verify `git log -1 --format=%G?` == `G`.
- **Per-stream structured render:** `m.structured`/`m.bodyProvider` are currently set once in `RunStream` (`stream_run.go:173-174`) and never reset, and the EOF overwrite `if m.structured && m.bodyProvider != nil { m.md = m.bodyProvider() }` (`model.go:621-625`) fires on EVERY re-arm. B3 sets `m.structured` per re-arm so a structured re-engagement renders `sess.lastPB` while a markdown followup is NOT clobbered.
- **`hadFollowup`** is the save pivot: set when any follow-up launches (`beginFollowupStream`, `model.go:2459` — covers auto verify-fail + both manual buttons); reset after a re-author. `Followup` itself stays markdown.
- **Save decision** (shared by the troubleshoot `w` branch `model.go:1079-1084` and `resolveConfirm` yes-arm `model.go:2422`): `hadFollowup ? re-author (beginFinalPlaybookInProc) : commitPlaybookCmd(m.md)`.
- **Collapse:** remove the `persistOnFinish` auto-baseline (`model.go:668-672`) + its set (`inprocess.go:296`); `w` no longer always-regenerates.
- **Not-verified gate:** "fully verified" = `m.blockStates[m.verifyBlockID()].Status == "ok"` (`results.go:10`, `model.go:2355`); a `w` on an unverified run raises a confirm first.
- Reuse: B1 `structuredBody` (`create_progress.go:260`), the `submit_playbook`/`OnPlaybook`/`sess.lastPB` capture, the confirm overlay (`m.ask`/`askMode`/`askCompletion`, pattern at `model.go:1106-1114`).
- The non-structured `RunStream` path stays (the `authorPlaybookText` fallback). `gofmt -l`/`go vet` clean; touched packages pass `go test` (and `-race` for `internal/ui`).

---

### Task 1: `Reengage.Body` — a live structured-render closure for re-engagement

**Files:**
- Modify: `internal/orchestrator/orchestrator.go` (add `Body` to `Reengage`)
- Modify: `internal/launcher/session.go` + `internal/launcher/create_progress.go` (set `Body` at the reengage-build sites)
- Test: `internal/launcher/create_progress_test.go`

**Interfaces:**
- Produces: `orchestrator.Reengage.Body func() string` — returns `playbook.Render(*sess.lastPB)` (Portabilized when project_bound) live, or "" when none captured. Consumed by Task 3 (the model reads `m.orch.Reengage.Body`).

**Context:** Re-engagement renders the captured playbook from `sess.lastPB`, but the model (`internal/ui`) cannot reach the session. B1 solved this for the *initial* escalate stream via `RunStream`'s `Body` closure (`structuredBody(sess,…)`, live atomic read). For *in-viewer* re-engagement there is no `RunStream` call, so the body source must ride on the `Reengage` struct the orchestrator already holds (`m.orch.Reengage`, accessed at `model.go:2461`). `structuredBody` (`create_progress.go:260`) is the live closure to reuse.

- [ ] **Step 1: Write the failing test**

```go
func TestReengageBody_RendersCapturedPlaybook(t *testing.T) {
	sess := openSession(capture.Request{ProjectRoot: t.TempDir()}, mux.Null(), nil, "")
	defer sess.close()
	pb := playbook.Playbook{Title: "T", Sections: []playbook.Section{{Heading: "S",
		Content: []playbook.ContentItem{{Kind: "code", Lang: "bash", ID: "fix", Code: "echo hi"}}}}}
	raw, _ := json.Marshal(pb)
	res, _ := tools.Dial(sess.socket, tools.Call{Tool: "submit_playbook", Playbook: raw})
	if !res.OK {
		t.Fatalf("submit_playbook: %v", res)
	}
	re := newCreateReengage(sess, capture.Request{}, nil, "", config.Default())
	if re.Body == nil {
		t.Fatal("Reengage.Body must be set")
	}
	if got := re.Body(); !strings.Contains(got, "echo hi") {
		t.Fatalf("Reengage.Body did not render the captured playbook: %q", got)
	}
}
```
(Match the real `newCreateReengage` signature — read it; adapt the call. The assertion — `Body()` renders the captured pb — is the point.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/launcher/ -run TestReengageBody_RendersCapturedPlaybook`
Expected: FAIL — `Reengage.Body` undefined.

- [ ] **Step 3: Implement**

In `internal/orchestrator/orchestrator.go`, add to the `Reengage` struct (near `Metadata`):
```go
	// Body, when set, renders the currently-captured structured playbook (live).
	// Re-engagement uses it so the in-viewer stream EOF can show the re-authored
	// playbook from the session's submit_playbook capture, not the streamed text.
	Body func() string
```
In `internal/launcher/create_progress.go` `newCreateReengage` (and the other two reengage-build sites — `authorPlaybook` and `reengageReady` in `session.go`), set `Body` to a live closure over `structuredBody`:
```go
	home, _ := os.UserHomeDir()
	re.Body = func() string { return structuredBody(sess, req.ProjectRoot, home, nil) }
```
(Use the `req`/`sess` in scope at each site; `structuredBody` with a nil fallback returns "" when nothing captured. Factor a tiny `reengageBody(sess, req)` helper in create_progress.go to avoid repeating the closure at all three sites.)

- [ ] **Step 4: Run test + the package**

Run: `go test ./internal/launcher/ ./internal/orchestrator/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/orchestrator/orchestrator.go internal/launcher/session.go internal/launcher/create_progress.go internal/launcher/create_progress_test.go
git commit -m "feat(reengage): Body closure rendering the captured structured playbook"
```

---

### Task 2: Re-engagement authors structured (`buildReengageEvents`)

**Files:**
- Modify: `internal/launcher/session.go` (`buildReengageEvents`)
- Test: `internal/launcher/session_test.go`

**Interfaces:**
- Consumes: `orchestrator.ReengageKind` (`KindReengageFinalPlaybook`/`KindReengageRegenerate`/`KindReengageFollowup`).
- Produces: for the playbook-producing kinds, the re-engaged agent calls `submit_playbook` (→ `OnPlaybook` → `sess.lastPB`).

**Context:** `buildReengageEvents` (`session.go:481-520`) calls `author.RunHarnessEvents(sys, user, AuthorOptions{Cfg, MCPConfigPath})` WITHOUT `Structured` (`session.go:504-507`), so the agent writes `{id=…}` markdown. `RunHarnessEvents` honors `AuthorOptions.Structured` (`author/events.go:202`). FinalPlaybook + Regenerate should be structured; Followup stays markdown.

- [ ] **Step 1: Write the failing test** (assert the AuthorOptions per kind)

Add a seam if needed: have `buildReengageEvents` compute `structured := kind != orchestrator.KindReengageFollowup` and pass `Structured: structured` to `AuthorOptions`. Test it via a small extracted pure helper:
```go
func TestReengageStructuredByKind(t *testing.T) {
	if !reengageStructured(orchestrator.KindReengageFinalPlaybook) {
		t.Error("finalplaybook must be structured")
	}
	if !reengageStructured(orchestrator.KindReengageRegenerate) {
		t.Error("regenerate must be structured")
	}
	if reengageStructured(orchestrator.KindReengageFollowup) {
		t.Error("followup must stay markdown")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/launcher/ -run TestReengageStructuredByKind`
Expected: FAIL — `reengageStructured` undefined.

- [ ] **Step 3: Implement**

In `internal/launcher/session.go`, add:
```go
// reengageStructured reports whether a re-engagement kind authors a playbook
// (submit_playbook) vs continuing the troubleshoot in markdown. Followup is
// markdown continuation; FinalPlaybook + Regenerate produce a playbook.
func reengageStructured(kind orchestrator.ReengageKind) bool {
	return kind != orchestrator.KindReengageFollowup
}
```
In `buildReengageEvents`, pass it through:
```go
	events, wait, err := author.RunHarnessEvents(sys, user, author.AuthorOptions{
		Cfg:           cfg,
		MCPConfigPath: mcpPath,
		Structured:    reengageStructured(kind),
	})
```

- [ ] **Step 4: Run test + the package**

Run: `go test ./internal/launcher/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/launcher/session.go internal/launcher/session_test.go
git commit -m "feat(reengage): author FinalPlaybook + Regenerate structured (Followup stays markdown)"
```

---

### Task 3: Per-stream structured render on re-arm

**Files:**
- Modify: `internal/ui/inprocess.go` (`beginFinalPlaybookGenerate`, `beginRegenerate`, `beginFollowupInProc`)
- Test: `internal/ui/inprocess_test.go` (or `reengage_test.go`)

**Interfaces:**
- Consumes: `m.orch.Reengage.Body` (Task 1); the model fields `structured`/`bodyProvider`.
- Produces: each re-arm leaves `m.structured` correct for its stream (true for FinalPlaybook/Regenerate, false for Followup) and, when structured, `m.bodyProvider = m.orch.Reengage.Body`.

**Context (the latent-bug fix):** `m.structured`/`m.bodyProvider` are set once by `RunStream` and never reset, so the EOF overwrite (`model.go:621`) re-renders the *stale* `sess.lastPB` on EVERY re-arm — clobbering a markdown re-engagement. B3 sets these per re-arm. The structured re-engagements (now updating `sess.lastPB`, Task 2) want the render; Followup (markdown, APPEND) must turn it OFF so its streamed content survives.

- [ ] **Step 1: Write the failing tests**

```go
func TestRearm_StructuredFinalPlaybook(t *testing.T) {
	m := model{orch: orchWithReengageBody(func() string { return "# Re-authored\n" })}
	m = m.armStructured() // helper extracted below; sets structured + bodyProvider
	if !m.structured || m.bodyProvider == nil {
		t.Fatal("a structured re-arm must set structured + bodyProvider")
	}
	if m.bodyProvider() != "# Re-authored\n" {
		t.Fatalf("bodyProvider not wired to Reengage.Body")
	}
}

func TestRearm_FollowupNotStructured(t *testing.T) {
	m := model{structured: true, orch: orchWithReengageBody(func() string { return "x" })}
	m = m.armMarkdown() // followup path
	if m.structured {
		t.Fatal("a followup re-arm must clear structured so its markdown is not clobbered")
	}
}
```
(`orchWithReengageBody` builds an `*orchestrator.Orchestrator` with a `Reengage{Body: …}`; read the orchestrator constructors to build it minimally. `armStructured`/`armMarkdown` are tiny helpers you extract from the begin* funcs — or assert through the real `beginFinalPlaybookGenerate`/`beginFollowupInProc` if they're testable without a live driver.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ui/ -run TestRearm_`
Expected: FAIL — helpers/behavior absent.

- [ ] **Step 3: Implement**

In `internal/ui/inprocess.go`:
- `beginFinalPlaybookGenerate` (~307) and `beginRegenerate` (~186): after the REPLACE setup, set the structured render for this stream:
```go
	m.structured = true
	if m.orch != nil && m.orch.Reengage != nil && m.orch.Reengage.Body != nil {
		m.bodyProvider = m.orch.Reengage.Body
	}
```
- `beginFollowupInProc` (~240): clear it so the appended markdown survives the EOF:
```go
	m.structured = false
```
(If extracting `armStructured()`/`armMarkdown()` helpers makes the begin* funcs cleaner + testable, do so; otherwise set the fields inline and test via the begin* funcs.)

- [ ] **Step 4: Run tests + the package**

Run: `go test ./internal/ui/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/inprocess.go internal/ui/inprocess_test.go
git commit -m "fix(ui): per-stream structured render on re-arm (structured re-engagement renders sess.lastPB; followup stays markdown)"
```

---

### Task 4: `hadFollowup` tracking

**Files:**
- Modify: `internal/ui/model.go` (field) + `internal/ui/inprocess.go`/`model.go` (set/reset)
- Test: `internal/ui/reengage_test.go`

**Interfaces:**
- Produces: `model.hadFollowup bool` — true after any follow-up launches; reset after a re-author. Consumed by Task 5.

**Context:** Both auto verify-fail and manual "try another fix" funnel through `beginFollowupStream` (`model.go:2459`). The re-author is `beginFinalPlaybookInProc` (`inprocess.go:287`).

- [ ] **Step 1: Write the failing test**

```go
func TestHadFollowup_SetByFollowup(t *testing.T) {
	m := &model{orch: orchWithReengage(t)} // an orch whose Reengage != nil so beginFollowupStream proceeds
	_ = m.beginFollowupStream("verify", "false")
	if !m.hadFollowup {
		t.Fatal("a follow-up must set hadFollowup")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ui/ -run TestHadFollowup_SetByFollowup`
Expected: FAIL — `hadFollowup` undefined.

- [ ] **Step 3: Implement**

Add the field to `model` (`model.go`, near `followups int`):
```go
	hadFollowup bool // a follow-up (auto or manual) ran → the run diverged from the proposed playbook
```
Set it at the top of `beginFollowupStream` (`model.go:2459`, before the early return so it records the intent even if the in-proc actuator no-ops):
```go
	m.hadFollowup = true
```
Reset it in `beginFinalPlaybookGenerate` (`inprocess.go:307`, the re-author entry — after a re-author the doc reflects the resolution):
```go
	m.hadFollowup = false
```

- [ ] **Step 4: Run test + the package**

Run: `go test ./internal/ui/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/model.go internal/ui/inprocess.go internal/ui/reengage_test.go
git commit -m "feat(ui): track hadFollowup (the run diverged from the proposed playbook)"
```

---

### Task 5: The follow-up-aware save decision + finalize collapse

**Files:**
- Modify: `internal/ui/model.go` (the `w` troubleshoot branch + `resolveConfirm`; remove the `persistOnFinish` auto-baseline) + `internal/ui/inprocess.go` (remove the `persistOnFinish` set)
- Test: `internal/ui/reengage_test.go`

**Interfaces:**
- Consumes: `hadFollowup` (Task 4), `commitPlaybookCmd` (`inprocess.go:349`), `beginFinalPlaybookInProc` (`inprocess.go:287`).
- Produces: `func (m *model) saveDecision() tea.Cmd` — `hadFollowup ? beginFinalPlaybookInProc() : commitPlaybookCmd(m.md)`. Consumed by Task 6.

**Context:** Today the troubleshoot `w` (`model.go:1079-1084`) and `resolveConfirm` yes (`model.go:2422`) ALWAYS call `beginFinalPlaybookInProc` (regenerate), and a `persistOnFinish` auto-baseline commits at EOF (`model.go:668-672`). With re-engagement structured, the clean run already holds the captured playbook render, so `w` should persist it directly when no follow-up occurred.

- [ ] **Step 1: Write the failing tests**

```go
func TestSaveDecision_NoFollowupPersists(t *testing.T) {
	m := &model{hadFollowup: false, md: "# P\n\n```bash {id=fix}\ntrue\n```\n",
		orch: orchWithReengage(t)}
	cmd := m.saveDecision()
	if cmd == nil {
		t.Fatal("no-followup save must return the commit cmd")
	}
	if m.regenerating { // a flag/marker that beginFinalPlaybookInProc started a generation
		t.Fatal("no-followup save must NOT re-author")
	}
}

func TestSaveDecision_FollowupReauthors(t *testing.T) {
	m := &model{hadFollowup: true, orch: orchWithReengage(t)}
	_ = m.saveDecision()
	// after a re-author the pivot resets (so the re-authored doc is then final)
	if m.hadFollowup {
		t.Fatal("re-author must reset hadFollowup")
	}
}
```
(Pick an observable signal for "re-authored vs persisted" that exists in the model — e.g. `beginFinalPlaybookInProc` sets `m.streaming`/`m.reengageStream` or `m.status`; assert on whichever the real code sets. The point: no-followup → commit path, followup → re-author path + reset.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ui/ -run TestSaveDecision_`
Expected: FAIL — `saveDecision` undefined.

- [ ] **Step 3: Implement**

Add the shared decision to `model.go`:
```go
// saveDecision finalizes the troubleshoot result: if the run diverged from the
// proposed playbook (a follow-up occurred), re-author a fresh structured playbook
// folding in the resolution; otherwise the rendered playbook IS the result, so
// persist it as-is.
func (m *model) saveDecision() tea.Cmd {
	if m.hadFollowup {
		return m.beginFinalPlaybookInProc() // re-author (resets hadFollowup via beginFinalPlaybookGenerate)
	}
	return m.commitPlaybookCmd(m.md)
}
```
Replace the troubleshoot `w` branch (`model.go:1079-1084`) — keep `m.wrappedUp = true`, swap the `beginFinalPlaybookInProc()` call for `m.saveDecision()`. Replace `resolveConfirm`'s yes-arm (`model.go:2422`) `beginFinalPlaybookInProc()` with `m.saveDecision()`.
Remove the `persistOnFinish` auto-baseline block (`model.go:668-672`) and the `m.persistOnFinish = true` set in `beginFinalPlaybookInProc` (`inprocess.go:296`). (Leave the field declaration if other code reads it; grep — if nothing else uses it after these removals, delete the field too.)

- [ ] **Step 4: Run tests + the package (-race)**

Run: `go test ./internal/ui/ -run TestSaveDecision_`
Run: `go test -race ./internal/ui/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/model.go internal/ui/inprocess.go internal/ui/reengage_test.go
git commit -m "feat(ui): follow-up-aware save (persist clean run, re-author diverged) + drop persistOnFinish auto-baseline"
```

---

### Task 6: The not-verified confirm before saving

**Files:**
- Modify: `internal/ui/model.go` (the `w` handler)
- Test: `internal/ui/reengage_test.go`

**Interfaces:**
- Consumes: `saveDecision` (Task 5); the confirm overlay (`m.ask`/`askMode`/`askCompletion`); `m.blockStates[m.verifyBlockID()].Status`.

**Context:** A `w` on a run that hasn't passed its verify should warn the user. "Verified" = `m.blockStates[m.verifyBlockID()].Status == "ok"` (`results.go:10`, `model.go:2355`). Reuse the B2b confirm overlay (the `r`/refine pattern at `model.go:1106-1114`).

- [ ] **Step 1: Write the failing test**

```go
func TestW_NotVerifiedRaisesConfirm(t *testing.T) {
	m := &model{md: "# P\n\n```bash {id=verify}\ntrue\n```\n", orch: orchWithReengage(t)}
	// no verify run recorded → not verified
	cmd := m.handleW() // the extracted w-handler; or drive the key via Update
	if !m.askMode {
		t.Fatal("w on an unverified run must raise the confirm overlay")
	}
	_ = cmd
}

func TestW_VerifiedSavesDirectly(t *testing.T) {
	m := &model{md: "# P\n\n```bash {id=verify}\ntrue\n```\n", orch: orchWithReengage(t)}
	m.blockStates = map[string]blockRunState{"verify": {Status: "ok"}}
	_ = m.handleW()
	if m.askMode {
		t.Fatal("w on a verified run must not raise the confirm")
	}
}
```
(If the `w` handling isn't a standalone method, extract `handleW()` from the `case "w":` body, or drive `Update` with the key msg and assert `m.askMode`. The verify block id in the fixture is `"verify"`.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ui/ -run TestW_`
Expected: FAIL.

- [ ] **Step 3: Implement**

In the `w` handler, before the save: if the run is finalDraft-not-committed (the create/escalate persist path) keep it as-is; for the troubleshoot save, gate on verified:
```go
	verified := m.blockStates[m.verifyBlockID()].Status == "ok"
	if !verified {
		m.askMode = true
		m.ask = input.NewAsk("ai-playbook",
			"This playbook wasn't fully run, so we couldn't verify it works. Save this state as a new playbook anyway?",
			"", "confirm", nil, "Save", "Cancel")
		m.askCompletion = func(value string, submitted bool) tea.Msg {
			return saveConfirmMsg{ok: submitted && value == "yes"}
		}
		return m, m.ask.Init()
	}
	return m, m.saveDecision()
```
Add the `saveConfirmMsg` + its `Update` arm:
```go
	case saveConfirmMsg:
		if msg.ok {
			return m, m.saveDecision()
		}
		return m, nil
```
(`saveConfirmMsg struct{ ok bool }`. Reuse `handleAskKey`'s `askCompletion` routing — confirmed wired in B2b.)

- [ ] **Step 4: Run tests + the package**

Run: `go test ./internal/ui/ -run TestW_`
Run: `go build ./... && go test ./internal/ui/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/model.go internal/ui/reengage_test.go
git commit -m "feat(ui): confirm before saving an unverified troubleshoot run"
```

---

## Self-Review

**Spec coverage (B3):** `hadFollowup` (T4) ✓; structured re-engagement producers (T2) + the body source (T1) + per-stream render/clobber-fix (T3) ✓; the save decision (T5) ✓; the not-verified confirm (T6) ✓; the collapse — remove always-regenerate + persistOnFinish auto-baseline (T5) ✓; Followup stays markdown (T2/T3) ✓; non-structured `RunStream` kept (untouched) ✓.

**The latent clobber bug** (re-engagement clobbered by the stale `sess.lastPB` render in a structured viewer) is fixed by T3 (per-stream `m.structured`) + T1/T2 (structured re-engagement actually refreshes `sess.lastPB`). Both the escalate and cached-replay viewers are covered because the body source rides on `Reengage.Body` (T1), set at all three reengage-build sites.

**Type consistency:** `Reengage.Body func() string` (T1) ↔ `m.orch.Reengage.Body` (T3); `reengageStructured` (T2) gates the producer; `hadFollowup` (T4) ↔ `saveDecision` (T5) ↔ the `w`/confirm (T6); `saveConfirmMsg`/`saveDecision` (T5/T6) consistent.

**Deferred (NOT B3):** the assisted-run feature; viewer-UX-polish; `file=`/diff. After B3, Phase B is complete.

**Open items the implementer must confirm against real code (flagged, not placeheld):**
- T1: the exact signatures of `newCreateReengage`/`authorPlaybook`/`reengageReady` and the `req`/`sess`/`home` in scope at each — set `Body` with the live `structuredBody(sess, req.ProjectRoot, home, nil)` closure (factor a `reengageBody` helper).
- T3: whether `beginFinalPlaybookGenerate`/`beginRegenerate`/`beginFollowupInProc` are best edited inline or via extracted `armStructured`/`armMarkdown` helpers (pick what tests cleanly); confirm the EOF overwrite at `model.go:621` is the only structured-render site.
- T5: the observable "re-authored vs persisted" signal for the tests; whether `persistOnFinish` the field is fully removable after dropping its set + auto-baseline (grep).
- T6: whether the `w` body is a standalone method or inline in `Update` (extract `handleW()` if it helps testing); confirm `verifyBlockID()` returns `"verify"` for the fixture.
