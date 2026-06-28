package launcher

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Townk/ai-playbook/internal/askbridge"
	"github.com/Townk/ai-playbook/internal/cache"
	"github.com/Townk/ai-playbook/internal/capture"
	"github.com/Townk/ai-playbook/internal/config"
	"github.com/Townk/ai-playbook/internal/mux"
	"github.com/Townk/ai-playbook/internal/orchestrator"
	"github.com/Townk/ai-playbook/internal/playbook"
	"github.com/Townk/ai-playbook/internal/tools"
	"github.com/Townk/ai-playbook/internal/triage"
)

// ── progress+ask host ───────────────────────────────────────────────────────

// TestProgressAskModel_ActivityUpdatesLine asserts a model-activity value updates
// the rendered waiting line (the spinner + activity block).
func TestProgressAskModel_ActivityUpdatesLine(t *testing.T) {
	m := newProgressAskModel(nil, nil, nil)
	mi, _ := m.Update(paActMsg{s: "running go test", ok: true})
	m = mi.(progressAskModel)
	if m.activity != "running go test" {
		t.Fatalf("activity = %q, want %q", m.activity, "running go test")
	}
	if !strings.Contains(m.View().Content, "running go test") {
		t.Errorf("View must render the activity line, got:\n%s", m.View().Content)
	}
}

// TestProgressAskModel_DoneQuits asserts the done signal quits the program.
func TestProgressAskModel_DoneQuits(t *testing.T) {
	m := newProgressAskModel(nil, nil, nil)
	_, cmd := m.Update(paDoneMsg{})
	if cmd == nil {
		t.Fatal("done must return a command (tea.Quit)")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("done command = %T, want tea.QuitMsg", cmd())
	}
}

// TestProgressAskModel_AskRoundTrip asserts the no-mux ask flow during authoring: a
// bridge request embeds input.Ask (pausing the waiting line); the user's answer is
// delivered back to the blocked Ask caller via Respond; the ask clears (resumes).
func TestProgressAskModel_AskRoundTrip(t *testing.T) {
	b := askbridge.New()
	answered := make(chan askbridge.Answer, 1)
	go func() { answered <- b.Ask("which env?", "line", nil) }()
	req := <-b.Requests()

	m := newProgressAskModel(nil, nil, b.Requests())

	// Deliver the ask → it embeds the dialog (waiting line paused).
	mi, _ := m.Update(paAskMsg{req: req})
	m = mi.(progressAskModel)
	if m.ask == nil {
		t.Fatal("an ask request must embed input.Ask")
	}
	if !strings.Contains(m.View().Content, "which env?") {
		t.Errorf("paused view must render the ask prompt, got:\n%s", m.View().Content)
	}

	// Type an answer and submit.
	mi, _ = m.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	m = mi.(progressAskModel)
	mi, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = mi.(progressAskModel)
	if m.ask != nil {
		t.Error("ask must clear after submit (waiting line resumes)")
	}
	if cmd == nil {
		t.Error("resolving an ask must re-arm the bridge receiver")
	}

	ans := <-answered
	if ans.Value != "p" || !ans.Submitted {
		t.Fatalf("bridge answer = %+v, want {p,true}", ans)
	}
}

// TestProgressAskModel_NilBridgeNoSubscribe asserts a nil ask channel produces no
// subscription command (the mux-present path never reaches the bridge).
func TestProgressAskModel_NilBridgeNoSubscribe(t *testing.T) {
	if paRecvAsk(nil) != nil {
		t.Error("a nil ask channel must yield a nil command (no subscription)")
	}
}

// ── create author core ──────────────────────────────────────────────────────

// fakeCreateStream builds a createStream over a canned body, mirroring the fan-out
// contract: the reader yields the body then EOF, and body() returns it.
func fakeCreateStream(body string) createStream {
	return createStream{
		reader:   io.NopCloser(strings.NewReader(body)),
		activity: closedActivity(),
		body:     func() string { return body },
		close:    func() {},
	}
}

func closedActivity() <-chan string {
	ch := make(chan string)
	close(ch)
	return ch
}

// TestCreateAuthorWithProgress_DrainsCollectsAndViews is the create-core seam test:
// the authoring stream is drained to the COMPLETE body, the body is cache-stored
// under the createDecision keys, and the viewer is invoked with the complete body —
// all without a live harness/TTY/model. triage.Route/classify is never consulted
// (the path's only model sink is the injected author stream).
func TestCreateAuthorWithProgress_DrainsCollectsAndViews(t *testing.T) {
	c := isolateCache(t)

	origStream, origProg, origView := createStreamFn, runCreateProgressFn, createViewFn
	t.Cleanup(func() { createStreamFn, runCreateProgressFn, createViewFn = origStream, origProg, origView })

	const fullBody = "# Playbook — Fix It\n\nrun `go test`\n"
	createStreamFn = func(_ capture.Request, _ *session, _ *config.Config) (createStream, error) {
		return fakeCreateStream(fullBody), nil
	}
	// Headless progress: wait for the drain to finish (so body() is ready) — no TTY.
	runCreateProgressFn = func(_ <-chan string, _ *askbridge.Bridge, done <-chan struct{}) { <-done }

	var gotBody string
	var gotRe *orchestrator.Reengage
	viewCalled := false
	createViewFn = func(body string, _ *session, re *orchestrator.Reengage, _ *config.Config, _ capture.Request) int {
		viewCalled = true
		gotBody = body
		gotRe = re
		return 0
	}

	req := capture.Request{UserRequest: "fix it", ProjectRoot: "/proj", CWD: "/proj", Command: "go test", Exit: "1", Scrollback: "boom"}
	d := createDecision(req)
	code := createAuthorWithProgress(req, d, c, false, nil, config.Default())

	if code != 0 {
		t.Fatalf("exit = %d, want 0 (the viewer seam's code)", code)
	}
	if !viewCalled {
		t.Fatal("the viewer must be invoked with the complete playbook")
	}
	if gotBody != fullBody {
		t.Errorf("viewer body = %q, want the COMPLETE body %q", gotBody, fullBody)
	}
	if gotRe == nil || gotRe.CtxHash != d.CtxHash || gotRe.ReqHash != d.ReqHash {
		t.Errorf("viewer Reengage keys = %+v, want createDecision keys (%q,%q)", gotRe, d.CtxHash, d.ReqHash)
	}

	// The body must be cache-stored under the createDecision keys (so a later assist hits).
	path, ok := c.Lookup(d.CtxHash, d.ReqHash)
	if !ok {
		t.Fatal("the authored body must be cache-stored under the createDecision keys")
	}
	if !strings.Contains(cache.Body(mustRead(t, path)), "Fix It") {
		t.Errorf("cached body must hold the authored playbook")
	}
}

// TestCreateAuthorWithProgress_StreamErrorFallsBack asserts that when the author
// stream can't start (harness missing), the flow does NOT open the viewer — it
// degrades to the text author path (which here fails fast with no live harness).
func TestCreateAuthorWithProgress_StreamErrorFallsBack(t *testing.T) {
	c := isolateCache(t)
	t.Setenv("AI_PLAYBOOK_CLAUDE_BIN", "/nonexistent/claude-binary")

	origStream, origView := createStreamFn, createViewFn
	t.Cleanup(func() { createStreamFn, createViewFn = origStream, origView })

	createStreamFn = func(_ capture.Request, _ *session, _ *config.Config) (createStream, error) {
		return createStream{}, io.ErrUnexpectedEOF
	}
	viewCalled := false
	createViewFn = func(_ string, _ *session, _ *orchestrator.Reengage, _ *config.Config, _ capture.Request) int {
		viewCalled = true
		return 0
	}

	req := capture.Request{UserRequest: "x", CWD: "/p"}
	d := createDecision(req)
	_ = captureStderr(t, func() {
		createAuthorWithProgress(req, d, c, false, nil, config.Default())
	})

	if viewCalled {
		t.Error("on a stream-start failure the viewer must NOT be opened (text fallback owns it)")
	}
}

// ── phase-2 viewer ──────────────────────────────────────────────────────────

// TestCreateViewPlaybook_ReshapesToRunFile asserts the phase-2 viewer reshapes
// os.Args to `run --file <tmp>` (no --cached badge) and the temp file holds the
// COMPLETE playbook body handed to ui.Main.
func TestCreateViewPlaybook_ReshapesToRunFile(t *testing.T) {
	savedArgs, savedMain := os.Args, uiMainFn
	t.Cleanup(func() { os.Args, uiMainFn = savedArgs, savedMain })
	os.Args = []string{"ai-playbook", "create", "fix it"}

	const fullBody = "# Playbook — Reshape\n\nstep one\n"
	var gotArgs []string
	var fileContent string
	uiMainFn = func() int {
		gotArgs = append([]string(nil), os.Args...)
		for i, a := range os.Args {
			if a == "--file" && i+1 < len(os.Args) {
				fileContent = mustRead(t, os.Args[i+1])
			}
		}
		return 0
	}

	req := capture.Request{ProjectRoot: "/proj", CWD: "/proj"}
	code := createViewPlaybook(fullBody, nil, &orchestrator.Reengage{}, config.Default(), req)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if len(gotArgs) < 2 || gotArgs[1] != "run" {
		t.Fatalf("os.Args not reshaped to `run`: %v", gotArgs)
	}
	if !contains(gotArgs, "--file") {
		t.Errorf("viewer must pass --file: %v", gotArgs)
	}
	if contains(gotArgs, "--cached") {
		t.Errorf("create must NOT pass --cached (no badge): %v", gotArgs)
	}
	if fileContent != fullBody {
		t.Errorf("temp file content = %q, want the COMPLETE body %q", fileContent, fullBody)
	}
}

// TestRealCreateAuthor_NullMuxBuildsBridge is a light wiring guard: realCreateAuthor
// on the null mux drives createAuthorWithProgress (the inline path), reaching the
// stream seam — proving create no longer routes through ui.RunStream.
func TestRealCreateAuthor_NullMuxReachesStreamSeam(t *testing.T) {
	isolateCache(t)
	minimalZDOTDIR(t)

	origStream, origProg, origView := createStreamFn, runCreateProgressFn, createViewFn
	t.Cleanup(func() { createStreamFn, runCreateProgressFn, createViewFn = origStream, origProg, origView })

	reached := false
	createStreamFn = func(_ capture.Request, _ *session, _ *config.Config) (createStream, error) {
		reached = true
		return fakeCreateStream("# x\n"), nil
	}
	runCreateProgressFn = func(_ <-chan string, _ *askbridge.Bridge, done <-chan struct{}) { <-done }
	createViewFn = func(_ string, _ *session, _ *orchestrator.Reengage, _ *config.Config, _ capture.Request) int {
		return 0
	}

	if code := realCreateAuthor(capture.Request{UserRequest: "x", ProjectRoot: t.TempDir()}, mux.Null()); code != 0 {
		t.Fatalf("realCreateAuthor exit = %d, want 0", code)
	}
	if !reached {
		t.Error("realCreateAuthor must drive createAuthorWithProgress → the author-stream seam")
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// TestCreate_StructuredRenderAndSeam is the Phase A integration test: create
// authors via submit_playbook — a (faked) agent tool call delivers a structured
// playbook to the session's tools backend, the create stream's body is the
// DETERMINISTIC render of the captured playbook, and the reengage Metadata seam
// returns the captured schema meta (Description/Category/Tags/ProjectBound) with
// NO metadata model pass. It drives the REAL openSession wiring (a live tools.Serve
// whose Deps.OnPlaybook stores into sess.lastPB / sess.pb).
func TestCreate_StructuredRenderAndSeam(t *testing.T) {
	minimalZDOTDIR(t)
	sess := openSession(capture.Request{ProjectRoot: t.TempDir()}, mux.Null(), nil, "")
	if sess == nil {
		t.Fatal("openSession returned nil (driver/tools setup failed)")
	}
	defer sess.close()

	pb := playbook.Playbook{
		Title:    "Restore wrapper",
		Sections: []playbook.Section{{Heading: "Fix", Content: []playbook.ContentItem{{Kind: "code", Lang: "bash", Code: "gradle wrapper", ID: "fix"}}}},
		Meta:     playbook.Meta{Description: "Restore the wrapper", Category: "Android / build", Tags: []string{"gradle"}, ProjectBound: true},
	}
	raw, _ := json.Marshal(pb)

	// Simulate the agent's tool call hitting the backend (the MCP adapter forwards a
	// submit_playbook Call to exactly this socket). OnPlaybook captures into sess.
	res, err := tools.Dial(sess.socket, tools.Call{Tool: "submit_playbook", Playbook: raw})
	if err != nil || !res.OK {
		t.Fatalf("submit: %+v err=%v", res, err)
	}
	if sess.lastPB.Load() == nil {
		t.Fatal("submit_playbook did not capture into sess.lastPB")
	}

	// The captured playbook renders deterministically (body-only markdown).
	body := playbook.Render(*sess.lastPB.Load())
	if !strings.Contains(body, "# Restore wrapper") || !strings.Contains(body, "```bash {id=fix}") {
		t.Fatalf("rendered body wrong:\n%s", body)
	}

	// The create reengage Metadata seam returns the captured meta — NO model call.
	re := newCreateReengage(capture.Request{}, triage.Decision{Disabled: true}, nil, true, sess, config.Default())
	meta, err := re.Metadata(body)
	if err != nil {
		t.Fatalf("seam err: %v", err)
	}
	if meta.Description != "Restore the wrapper" || !meta.ProjectBound || meta.Category != "Android / build" {
		t.Fatalf("seam meta = %+v, want captured schema meta", meta)
	}
	if len(meta.Tags) != 1 || meta.Tags[0] != "gradle" {
		t.Fatalf("seam meta tags = %v, want [gradle]", meta.Tags)
	}
}
