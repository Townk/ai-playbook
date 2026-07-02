# `validate` AI-review progress feedback (Implementation Plan)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `validate`'s AI review shows live progress — the `create`-style inline spinner + model-activity on a TTY, and a discreet `.`-every-2s heartbeat to stderr in CI/non-TTY — instead of blocking silently. Spec: `docs/specifications/validate-ai-progress.md`.

**Architecture:** Swap `validate`'s blocking `author.ReviewOnce` for a streaming `author.ReviewStream`; in `validatecmd.go` fan the events out (`agentstream.FanOut`), drain the reader for the review text, and drive progress by TTY presence — `runCreateProgressFn` (reused from `create`, same package) on a terminal, else a stderr dot-heartbeat. Progress never touches stdout.

**Tech Stack:** Go; `internal/author` (`RunHarnessEvents`), `internal/agentstream` (`FanOut`), `internal/launcher` (`runCreateProgress`, `validatecmd.go`), `internal/config`.

## Global Constraints

- Module `github.com/Townk/ai-playbook`. gpg-signed Conventional Commits (`git commit`, NEVER `--no-gpg-sign`; if signing times out, STOP and report BLOCKED — the user re-unlocks gpg with `! echo x | gpg --clearsign`); verify `git log -1 --format=%G?` == `G`. **NO `Co-Authored-By`/AI-attribution trailers.** `git add` explicit paths only.
- **Repo at `~/Projects/langs/go/ai-playbook`** — `go`/`git` run there (`cd …` or `git -C …`).
- **Progress never writes to stdout.** Spinner → `/dev/tty` (via `runCreateProgress`); heartbeat dots → **stderr**. The report → stdout, unchanged. Exit code still driven only by structural errors.
- **The activity channel MUST be drained** in the no-TTY path (an unread buffered `FanOut` activity channel stalls the fan-out → the reader drain → `done` never closes).
- Gates: `gofmt -l`, `go build ./...`, `go vet ./...`, `go run github.com/gordonklaus/ineffassign@v0.2.0 ./...` (clean), `go test` on touched packages.

---

### Task 1: `internal/author` — `ReviewStream` (replace `ReviewOnce`)

**Files:**
- Modify: `internal/author/review.go` (replace `ReviewOnce` with `ReviewStream`), `internal/author/review_test.go`

**Interfaces:**
- Produces (consumed by Task 2): `func ReviewStream(cfg *config.Config, systemPrompt, userMessage string) (<-chan agentstream.Event, func() error, error)` — the streaming one-shot review.
- Removes: `func ReviewOnce(...)` (only `validate` used it; Task 2 switches to `ReviewStream`).

**Context:** `ReviewOnce` currently builds `AuthorOptions{MCPConfigPath:"", Bare:true, NoThinking:true, Cfg:cfg, Command:reviewProcess}` and calls the BLOCKING `runMetadataOnce`. `ReviewStream` builds the SAME options EXCEPT `NoThinking:false` (so the activity feed carries model activity to display), and calls the STREAMING `author.RunHarnessEvents(systemPrompt, userMessage, opts) (<-chan agentstream.Event, func() error, error)` (`internal/author/events.go:181`), returning its `(events, closeFn, err)` directly. Keep the `reviewProcess` test seam (`Command: reviewProcess`). Read `internal/author/review.go` + how `runMetadataOnce` calls `RunHarnessEvents` (`metadata.go:113`) to copy the exact option/argv shape — the only differences are: return the stream instead of draining it, and `NoThinking:false`.

- [ ] **Step 1: Write the failing tests** — port `ReviewOnce`'s tests to `ReviewStream` using the same `reviewProcess`/harness seam. Assert: (a) on success the returned event stream, when drained, yields the harness text (drain `events` collecting `Final`/delta text — mirror how `runMetadataOnce`/an existing events test drains); (b) a no-backend start error is returned as the 3rd value (not swallowed); (c) the argv is Bare/no-MCP (reuse `ReviewOnce`'s argv assertion against `ClaudeArgs`). Delete the `ReviewOnce`-named tests.

```go
// sketch — adapt to the real harness seam + event-drain helper in internal/author tests
func TestReviewStream_StreamsHarnessText(t *testing.T) {
	// stub reviewProcess to a fake harness emitting review text
	events, closeFn, err := ReviewStream(config.Default(), "you are a reviewer", "the body")
	if err != nil { t.Fatalf("err: %v", err) }
	defer closeFn()
	var got string
	for ev := range events { got += ev.Text() /* or the real field carrying text */ }
	if got == "" { t.Fatal("ReviewStream's events must carry the review text") }
}
func TestReviewStream_PropagatesNoBackendError(t *testing.T) {
	// stub reviewProcess to a missing/exit-fail harness; assert err != nil (start error surfaced)
}
```

- [ ] **Step 2: Run to verify they fail** — `cd ~/Projects/langs/go/ai-playbook && go test ./internal/author/ -run ReviewStream` → FAIL.
- [ ] **Step 3: Implement** `ReviewStream`; delete `ReviewOnce`.
- [ ] **Step 4: Run to verify they pass** — `go test ./internal/author/`; `go build ./...` (will FAIL to build `internal/launcher` until Task 2 switches the caller — that's expected; confirm `internal/author` itself builds + tests pass, and note the launcher break is resolved in Task 2). To keep the build green at commit time, you MAY do Task 2's one-line caller switch is NOT allowed here (separate task) — instead, if `go build ./...` breaks only on `internal/launcher`'s `ReviewOnce` reference, that's acceptable for THIS commit since Task 2 immediately follows; verify `go build ./internal/author/` + `go vet ./internal/author/` are clean and note the pending launcher switch.
- [ ] **Step 5: Commit** — `git add internal/author/review.go internal/author/review_test.go && git commit -m "feat(author): ReviewStream streaming review (replaces ReviewOnce)"`

*(Note: because removing `ReviewOnce` breaks `internal/launcher`'s current caller, prefer to run Tasks 1 and 2 back-to-back; the controller reviews Task 1's diff before Task 2. If you'd rather keep every commit fully `go build ./...`-green, keep `ReviewOnce` as a thin wrapper temporarily and delete it in Task 2 — implementer's choice, note which you did.)*

---

### Task 2: `validatecmd` — stream + spinner/heartbeat progress

**Files:**
- Modify: `internal/launcher/validatecmd.go`, `internal/launcher/validatecmd_test.go`

**Interfaces:**
- Consumes: `author.ReviewStream` (Task 1), `agentstream.FanOut`, `runCreateProgressFn` (existing seam in `create_progress.go`, same package), `ActivityBuffer` (the const `create`'s `structuredStream` passes to `FanOut`).
- Produces: `var reviewStreamFn = author.ReviewStream` (replaces the old `reviewFn` seam); helpers `hasTTY() bool` and `heartbeat(w io.Writer, done <-chan struct{}, every time.Duration)`.

**Context:** Replace the current AI-pass block (the `reviewFn(cfg, sysPrompt, body)` call + note handling). New flow, only when NOT `--no-ai`:
1. `events, closeFn, err := reviewStreamFn(cfg, reviewSystemPrompt, body)`. On `err` → the existing no-backend/failed note (via `isNoBackend`); skip the rest of the AI pass (no progress).
2. `reader, activity, _ := agentstream.FanOut(events, closeFn, ActivityBuffer)`.
3. `done := make(chan struct{})`; goroutine: `b, _ := io.ReadAll(reader); aiText = strings.TrimSpace(string(b)); close(done)`.
4. Progress:
   - `if hasTTY() { runCreateProgressFn(activity, nil, done) }` — spinner + model activity on `/dev/tty` (it drains `activity` itself).
   - `else { go func(){ for range activity {} }(); heartbeat(os.Stderr, done, 2*time.Second) }` — drain+discard activity (mandatory), and print a `.` to **stderr** every 2s until `done`, then a trailing `"\n"`.
5. `<-done` (ensure `aiText` captured); print `aiText` in the `AI review:` block as before.
- `hasTTY()`: `f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); if err != nil { return false }; f.Close(); return true` (mirrors `runCreateProgress`'s own check so the two agree).
- `heartbeat(w, done, every)`: `t := time.NewTicker(every); defer t.Stop(); for { select { case <-t.C: fmt.Fprint(w, "."); case <-done: fmt.Fprintln(w); return } }`.
- Remove the old `reviewFn` seam. The report → stdout is UNCHANGED; only the progress mechanism changed.

- [ ] **Step 1: Write the failing tests**

```go
// heartbeat unit — no TTY/model needed
func TestHeartbeat_DotsThenNewline(t *testing.T) {
	var buf bytes.Buffer
	done := make(chan struct{})
	go func() { time.Sleep(120 * time.Millisecond); close(done) }()
	heartbeat(&buf, done, 40*time.Millisecond) // ≥2 ticks before done
	out := buf.String()
	if !strings.Contains(out, ".") { t.Fatalf("expected dots, got %q", out) }
	if !strings.HasSuffix(out, "\n") { t.Fatalf("expected trailing newline, got %q", out) }
}

// AI-pass wiring — stub the stream + the progress host so no TTY/model is used
func TestValidateMain_AITextCaptured(t *testing.T) {
	defer swap(&reviewStreamFn, func(_ *config.Config, _, _ string) (<-chan agentstream.Event, func() error, error) {
		ch := make(chan agentstream.Event, 1)
		ch <- /* an event carrying "looks good" as its text */ ; close(ch)
		return ch, func() error { return nil }, nil
	})()
	defer swap(&runCreateProgressFn, func(_ <-chan string, _ *askbridge.Bridge, done <-chan struct{}) { <-done })()
	// clean playbook temp file, os.Args validate --file <path> (no --no-ai) → the AI text is drained + printed; exit 0.
	// assert exit 0 and (if the report is captured) that "looks good" appears in the AI review block.
}

func TestValidateMain_NoAISkipsStream(t *testing.T) {
	var called bool
	defer swap(&reviewStreamFn, func(_ *config.Config, _, _ string) (<-chan agentstream.Event, func() error, error) {
		called = true; return nil, nil, nil
	})()
	// clean file + --no-ai → reviewStreamFn NOT called, exit 0.
	if /* ValidateMain */ 0 != 0 || called { t.Fatal("--no-ai must not call the review stream") }
}
```
(Adapt the fake `agentstream.Event` construction to the real `Event` type's text field/constructor — read `internal/agentstream`. Reuse the launcher tests' `swap` + temp-md + os.Args helpers.)

- [ ] **Step 2: Run to verify they fail** — `go test ./internal/launcher/ -run 'Heartbeat|ValidateMain_AIText|ValidateMain_NoAI'` → FAIL.
- [ ] **Step 3: Implement** the AI-pass rework + `hasTTY`/`heartbeat` + the `reviewStreamFn` seam (remove `reviewFn`).
- [ ] **Step 4: Run to verify they pass** — `go test ./internal/launcher/`; `go build ./...` (now green — the `ReviewOnce` reference is gone); `go vet`/`gofmt`/`ineffassign` clean.
- [ ] **Step 5: Commit** — `git add internal/launcher/validatecmd.go internal/launcher/validatecmd_test.go && git commit -m "feat(validate): live AI-review progress — TTY spinner + CI stderr heartbeat"`

---

## Final verification (after all tasks)

- [ ] `cd ~/Projects/langs/go/ai-playbook && gofmt -l internal/author internal/launcher` → empty; `go build ./... && go vet ./...` clean; `go run github.com/gordonklaus/ineffassign@v0.2.0 ./...` clean.
- [ ] `go test ./internal/author/ ./internal/launcher/ ./internal/validate/` → PASS.
- [ ] `go install ./cmd/ai-playbook`, then live-verify (needs the Claude CLI for a real AI pass):
  - Interactive: `ai-playbook validate --file examples/07-run-modes.md` → the inline spinner + model-activity shows while reviewing, then the report.
  - CI/piped: `ai-playbook validate --file examples/07-run-modes.md 2>err.log | cat` → dots accumulate in `err.log` (stderr) during the review; stdout report stays clean.
  - `--no-ai` → no progress, deterministic only.
  - No backend (Claude CLI unavailable) → skip note, no spinner/dots.

## Self-review notes (coverage vs spec)

- Spec design §1 → Task 1 (`ReviewStream`, `NoThinking:false`, remove `ReviewOnce`). §2 (FanOut + TTY spinner / stderr heartbeat + drain-activity + capture text) → Task 2. §3 (stdout report unchanged) → Task 2 leaves the report path intact. Testing: heartbeat unit + stubbed stream/progress seams cover both arms without a real TTY/model.
