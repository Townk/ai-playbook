package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ai-playbook/internal/author"
	"ai-playbook/internal/cache"
	"ai-playbook/internal/capture"
	"ai-playbook/internal/input"
	"ai-playbook/internal/mux"
)

// isolateCache points the cache at a throwaway temp root (AI_PLAYBOOK_DATA_DIR)
// so the launcher's cache-by-kind lookup/store touch an isolated store — never the
// user's real ~/.local/share/ai-playbook. Returns the *cache.Cache rooted there so
// HIT tests can pre-populate entries. Every launch-driving test calls this so a
// MISS-path store (command/answer) writes into the temp dir, not the real cache.
func isolateCache(t *testing.T) *cache.Cache {
	t.Helper()
	t.Setenv("AI_PLAYBOOK_DATA_DIR", t.TempDir())
	return cache.Open()
}

// fastFloatWait shrinks the waitFloatClosed seam so launcher tests run quickly and
// never block on the live ~2s cap. Restored after the test. Every test that drives
// launch through a submit (which now writes <out>.done then waits for <out>.closed)
// calls this.
func fastFloatWait(t *testing.T) {
	t.Helper()
	sp, sc, sm := floatClosePoll, floatCloseCap, floatCloseMargin
	floatClosePoll = time.Millisecond
	floatCloseCap = 200 * time.Millisecond
	floatCloseMargin = time.Millisecond
	t.Cleanup(func() {
		floatClosePoll, floatCloseCap, floatCloseMargin = sp, sc, sm
	})
}

// fakeClassify builds a classifyFunc that always returns the given Classification
// (and error), so the routing tests drive each kind deterministically without a
// live model.
func fakeClassify(cls author.Classification, err error) classifyFunc {
	return func(capture.Request, author.AuthorOptions) (author.Classification, error) {
		return cls, err
	}
}

// escalateClassify is the default fake: classify → escalate (the current
// always-author behavior), used by the topology tests that pre-date stage C.
func escalateClassify() classifyFunc {
	return fakeClassify(author.Classification{Kind: author.KindEscalate}, nil)
}

// launchMux is a recording fake Mux for the launcher topology tests. SpawnFloat
// simulates the floated `input --out <file>` by writing answer to that file (so
// the launcher's poll observes a submit, or — with floatCancel — a cancel).
// SpawnDocked records the docked-pane argv + cwd and snapshots the request-JSON
// file's contents (the launcher→session context hand-off) before the session
// would consume it.
type launchMux struct {
	floats       [][]string
	docked       [][]string
	dockedCwd    string
	dockedReq    string // contents of the --request file at spawn time
	dockedAnswer string // contents of the `run <answer.md>` file at spawn time
	typedPane    string // pane id passed to the last TypeInto (command route)
	typedText    string // text passed to the last TypeInto (no CR)
	typedCount   int    // number of TypeInto calls
	answer       string
	floatCancel  bool
	noClose      bool // when true, the simulated float never writes <out>.closed
	// (so the launcher's waitFloatClosed exercises the timeout/cap path).

	out string // the float's --out path (recorded on SpawnInputFloat)
	// Snapshots taken when the ROUTE fires (SpawnDocked / TypeInto), proving the
	// launcher closed + waited for the float BEFORE routing: doneAtRoute = was
	// <out>.done written, closedAtRoute = had the float written <out>.closed.
	doneAtRoute   bool
	closedAtRoute bool
}

// snapshotRoute records, at the moment a route fires, whether the float's <out>.done
// (written by writeDoneFile before the wait) and <out>.closed (written by the
// simulated float once it sees .done) exist — proving done→wait-closed→route order.
func (m *launchMux) snapshotRoute() {
	if m.out == "" {
		return
	}
	_, derr := os.Stat(m.out + input.DoneSuffix)
	_, cerr := os.Stat(m.out + input.ClosedSuffix)
	m.doneAtRoute = derr == nil
	m.closedAtRoute = cerr == nil
}

func (m *launchMux) DumpScreen(string) (string, error) { return "", nil }
func (m *launchMux) SpawnPane(mux.SpawnOptions) error  { return nil }
func (m *launchMux) SpawnFloat(mux.SpawnOptions) error { return nil }

// TypeInto records the command route's no-CR origin-pane write (mux.TypeInto →
// `zellij action write-chars`): the command is staged at the prompt for the user.
func (m *launchMux) TypeInto(pane, text string) error {
	m.snapshotRoute()
	m.typedCount++
	m.typedPane = pane
	m.typedText = text
	return nil
}

// SpawnInputFloat is the launcher's request-float seam (Asker.Ask now spawns the
// borderless input float through it). It records the argv, simulates the floated
// `input --out <file>` writing the submitted value (or cancel marker), and — on a
// submit — simulates the REAL thinking float's teardown: a goroutine waits for the
// launcher's <out>.done close signal, then writes <out>.closed (the torn-down
// marker the launcher's waitFloatClosed polls for). This makes the launcher's
// close→wait handshake observable + deterministic.
func (m *launchMux) SpawnInputFloat(opts mux.SpawnOptions) error {
	m.floats = append(m.floats, opts.Cmd)
	out := argAfter(opts.Cmd, "--out")
	m.out = out
	if out == "" {
		return nil
	}
	if m.floatCancel {
		// Simulate the float writing the cancel marker on dismiss (no .closed: the
		// cancel path never enters the thinking/close handshake).
		_ = os.WriteFile(out+input.CancelSuffix, nil, 0o600)
		return nil
	}
	_ = os.WriteFile(out, []byte(m.answer), 0o600)
	if m.noClose {
		return nil
	}
	go func() {
		for {
			if _, err := os.Stat(out + input.DoneSuffix); err == nil {
				_ = os.WriteFile(out+input.ClosedSuffix, nil, 0o600)
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	return nil
}

func (m *launchMux) SpawnDocked(opts mux.SpawnOptions) error {
	m.snapshotRoute()
	m.docked = append(m.docked, opts.Cmd)
	m.dockedCwd = opts.Cwd
	// Snapshot the request file the launcher wrote (the session reads + removes it).
	for i, a := range opts.Cmd {
		if a == "--request" && i+1 < len(opts.Cmd) {
			if b, err := os.ReadFile(opts.Cmd[i+1]); err == nil {
				m.dockedReq = string(b)
			}
		}
	}
	// Snapshot the answer markdown the launcher wrote. The `answer` subcommand passes
	// it via --content <file>; the legacy `run <answer.md>` route passes it as the last
	// positional. Read whichever applies (the docked pager reads it asynchronously).
	if c := argAfter(opts.Cmd, "--content"); c != "" {
		if b, err := os.ReadFile(c); err == nil {
			m.dockedAnswer = string(b)
		}
	} else if len(opts.Cmd) >= 3 && opts.Cmd[1] == "run" {
		if b, err := os.ReadFile(opts.Cmd[len(opts.Cmd)-1]); err == nil {
			m.dockedAnswer = string(b)
		}
	}
	return nil
}

// TestLaunch_FloatThenDocked asserts the topology: the launcher spawns the input
// float with the right `ai-playbook input` command (prefilled), reads the
// submitted request from the out-file, then spawns the docked pane with the right
// `ai-playbook session --request <json>` command carrying the captured context +
// the submitted request.
func TestLaunch_FloatThenDocked(t *testing.T) {
	fastFloatWait(t)
	isolateCache(t)
	m := &launchMux{answer: "please fix it"}
	req := capture.Request{
		Kind:        "error",
		Command:     "gg build",
		Exit:        "1",
		CWD:         "/proj/dir",
		ProjectRoot: "/proj",
		Project:     capture.Project{Name: "proj"},
	}

	if code := launch(m, "/bin/ai-playbook", req, escalateClassify()); code != 0 {
		t.Fatalf("launch exit = %d, want 0", code)
	}

	// 1) One input float, prefilled with the error template, in --thinking mode.
	if len(m.floats) != 1 {
		t.Fatalf("expected 1 SpawnFloat, got %d", len(m.floats))
	}
	fargv := m.floats[0]
	if fargv[0] != "/bin/ai-playbook" || fargv[1] != "input" {
		t.Fatalf("float argv prefix = %v, want [/bin/ai-playbook input …]", fargv[:2])
	}
	if !contains(fargv, "--thinking") {
		t.Errorf("request float must be spawned with --thinking\nargv: %v", fargv)
	}
	prefill := argAfter(fargv, "--value")
	if !strings.Contains(prefill, "gg build") || !strings.Contains(prefill, "exit 1") {
		t.Errorf("float --value (prefill) = %q, want the error template", prefill)
	}
	// The request float carries --history <data-root>/request-history.jsonl so it
	// recalls + appends. The ask/`f` floats must NOT (asserted separately).
	if got, want := argAfter(fargv, "--history"), requestHistoryPath(); got != want {
		t.Errorf("float --history = %q, want %q", got, want)
	}

	// 2) One docked session pane, carrying the context + submitted request.
	if len(m.docked) != 1 {
		t.Fatalf("expected 1 SpawnDocked, got %d", len(m.docked))
	}
	dargv := m.docked[0]
	if dargv[0] != "/bin/ai-playbook" || dargv[1] != "session" {
		t.Fatalf("docked argv prefix = %v, want [/bin/ai-playbook session …]", dargv[:2])
	}
	if argAfter(dargv, "--request") == "" {
		t.Errorf("docked pane missing --request <json>\nargv: %v", dargv)
	}
	if m.dockedCwd != "/proj" {
		t.Errorf("docked cwd = %q, want project root /proj", m.dockedCwd)
	}
	// The request JSON carries the captured context AND the user's submitted request.
	if !strings.Contains(m.dockedReq, "gg build") {
		t.Errorf("docked request JSON missing captured command:\n%s", m.dockedReq)
	}
	if !strings.Contains(m.dockedReq, "please fix it") {
		t.Errorf("docked request JSON missing submitted request:\n%s", m.dockedReq)
	}
	// 3) The launcher wrote <out>.done to close the thinking float, then WAITED for
	// the float to tear down (<out>.closed), and ONLY THEN routed (SpawnDocked) —
	// so the result pane spawns with the origin tiled pane focused, not the float.
	if out := argAfter(fargv, "--out"); !fileExists(out + ".done") {
		t.Errorf("escalate route must write %s.done to close the float", out)
	}
	if !m.doneAtRoute {
		t.Error("launcher must write <out>.done BEFORE routing (escalate)")
	}
	if !m.closedAtRoute {
		t.Error("launcher must wait for <out>.closed (float torn down) BEFORE routing (escalate)")
	}
}

// TestLaunch_CommandRoute asserts the classify "command" route: the command is
// typed into the ORIGIN pane (no CR, via TypeInto/write-chars), the float is closed
// (<out>.done written), and NO docked pane is spawned.
func TestLaunch_CommandRoute(t *testing.T) {
	fastFloatWait(t)
	isolateCache(t)
	m := &launchMux{answer: "list last week's commits"}
	req := capture.Request{CWD: "/proj/dir", ProjectRoot: "/proj", PaneID: "terminal_7"}
	classify := fakeClassify(author.Classification{Kind: author.KindCommand, Content: "git log --since='last week' -n 3"}, nil)

	if code := launch(m, "/bin/ai-playbook", req, classify); code != 0 {
		t.Fatalf("launch exit = %d, want 0", code)
	}
	if m.typedCount != 1 {
		t.Fatalf("expected 1 TypeInto (origin-pane stage), got %d", m.typedCount)
	}
	if m.typedText != "git log --since='last week' -n 3" {
		t.Errorf("typed text = %q, want the command verbatim (no CR)", m.typedText)
	}
	if strings.ContainsAny(m.typedText, "\r\n") {
		t.Errorf("typed text must carry NO trailing CR/newline: %q", m.typedText)
	}
	if m.typedPane != "terminal_7" {
		t.Errorf("typed into pane %q, want the origin pane terminal_7", m.typedPane)
	}
	if len(m.docked) != 0 {
		t.Fatalf("command route must spawn NO docked pane, got %d", len(m.docked))
	}
	if out := argAfter(m.floats[0], "--out"); !fileExists(out + ".done") {
		t.Errorf("command route must write %s.done to close the float", out)
	}
	// The command route also closes + waits for the float BEFORE typing into the
	// origin, so focus is back on the origin tiled pane when write-chars lands.
	if !m.doneAtRoute {
		t.Error("command route must write <out>.done BEFORE typing into the origin")
	}
	if !m.closedAtRoute {
		t.Error("command route must wait for <out>.closed BEFORE typing into the origin")
	}
}

// TestLaunch_AnswerRoute asserts the classify "answer" route: a docked pager renders
// the prose via `ai-playbook run <answer.md>` (the md holds the content), the float
// is closed, and NO session pane is spawned.
func TestLaunch_AnswerRoute(t *testing.T) {
	fastFloatWait(t)
	isolateCache(t)
	m := &launchMux{answer: "what is HEAD?"}
	req := capture.Request{CWD: "/proj/dir", ProjectRoot: "/proj"}
	classify := fakeClassify(author.Classification{Kind: author.KindAnswer, Content: "HEAD is the current commit your working tree is based on."}, nil)

	if code := launch(m, "/bin/ai-playbook", req, classify); code != 0 {
		t.Fatalf("launch exit = %d, want 0", code)
	}
	if m.typedCount != 0 {
		t.Errorf("answer route must not type into the origin pane, got %d TypeInto", m.typedCount)
	}
	if len(m.docked) != 1 {
		t.Fatalf("expected 1 docked pager pane, got %d", len(m.docked))
	}
	dargv := m.docked[0]
	if dargv[0] != "/bin/ai-playbook" || dargv[1] != "answer" {
		t.Fatalf("docked argv prefix = %v, want [/bin/ai-playbook answer …]", dargv[:2])
	}
	if contains(dargv, "session") {
		t.Errorf("answer route must NOT spawn a session, got %v", dargv)
	}
	// The `answer` pane carries the request JSON so the cached pill's reload can
	// re-run the cheap classify in place.
	if argAfter(dargv, "--request") == "" {
		t.Errorf("answer route must pass --request <json> (for the reload re-classify), got %v", dargv)
	}
	if !strings.Contains(m.dockedAnswer, "HEAD is the current commit") {
		t.Errorf("answer md missing the prose content:\n%s", m.dockedAnswer)
	}
	if out := argAfter(m.floats[0], "--out"); !fileExists(out + ".done") {
		t.Errorf("answer route must write %s.done to close the float", out)
	}
	if !m.doneAtRoute {
		t.Error("answer route must write <out>.done BEFORE spawning the pager")
	}
	if !m.closedAtRoute {
		t.Error("answer route must wait for <out>.closed BEFORE spawning the pager")
	}
}

// TestLaunch_AnswerRouteCarriesTitle asserts the classify-supplied title is passed
// to the answer pager as `--title <title>` (it becomes the pane header).
func TestLaunch_AnswerRouteCarriesTitle(t *testing.T) {
	fastFloatWait(t)
	isolateCache(t)
	m := &launchMux{answer: "what is HEAD?"}
	req := capture.Request{CWD: "/proj/dir", ProjectRoot: "/proj"}
	classify := fakeClassify(author.Classification{Kind: author.KindAnswer, Content: "HEAD is the current commit.", Title: "What Is HEAD"}, nil)

	if code := launch(m, "/bin/ai-playbook", req, classify); code != 0 {
		t.Fatalf("launch exit = %d, want 0", code)
	}
	if len(m.docked) != 1 {
		t.Fatalf("expected 1 docked pager pane, got %d", len(m.docked))
	}
	if got := argAfter(m.docked[0], "--title"); got != "What Is HEAD" {
		t.Errorf("answer pager --title = %q, want %q\nargv: %v", got, "What Is HEAD", m.docked[0])
	}
}

// TestLaunch_EscalateRouteCarriesTitle asserts the classify-supplied title is
// passed to the docked session as `--title <title>` (the working pane header).
func TestLaunch_EscalateRouteCarriesTitle(t *testing.T) {
	fastFloatWait(t)
	isolateCache(t)
	m := &launchMux{answer: "fix the build"}
	req := capture.Request{CWD: "/proj/dir", ProjectRoot: "/proj"}
	classify := fakeClassify(author.Classification{Kind: author.KindEscalate, Title: "Fix Gradle Build"}, nil)

	if code := launch(m, "/bin/ai-playbook", req, classify); code != 0 {
		t.Fatalf("launch exit = %d, want 0", code)
	}
	if len(m.docked) != 1 || m.docked[0][1] != "session" {
		t.Fatalf("expected 1 docked session pane, got docked=%v", m.docked)
	}
	if got := argAfter(m.docked[0], "--title"); got != "Fix Gradle Build" {
		t.Errorf("session --title = %q, want %q\nargv: %v", got, "Fix Gradle Build", m.docked[0])
	}
}

// TestLaunch_NoTitleNoFlag asserts that an empty classify title omits the --title
// flag entirely (the pane keeps its default/H1 header).
func TestLaunch_NoTitleNoFlag(t *testing.T) {
	fastFloatWait(t)
	isolateCache(t)
	m := &launchMux{answer: "fix it"}
	req := capture.Request{CWD: "/proj/dir", ProjectRoot: "/proj"}
	if code := launch(m, "/bin/ai-playbook", req, escalateClassify()); code != 0 {
		t.Fatalf("launch exit = %d, want 0", code)
	}
	if contains(m.docked[0], "--title") {
		t.Errorf("empty title must omit --title, got argv: %v", m.docked[0])
	}
}

// TestLaunch_ClassifyErrorEscalates asserts a classify error degrades to the
// escalate route (a docked session pane), never blocking the user.
func TestLaunch_ClassifyErrorEscalates(t *testing.T) {
	fastFloatWait(t)
	isolateCache(t)
	m := &launchMux{answer: "do a thing"}
	req := capture.Request{CWD: "/proj/dir", ProjectRoot: "/proj"}
	classify := fakeClassify(author.Classification{Kind: author.KindEscalate}, os.ErrDeadlineExceeded)

	if code := launch(m, "/bin/ai-playbook", req, classify); code != 0 {
		t.Fatalf("launch exit = %d, want 0", code)
	}
	if len(m.docked) != 1 || m.docked[0][1] != "session" {
		t.Fatalf("classify error must escalate to a docked session, got docked=%v", m.docked)
	}
}

// TestLaunch_CancelNoSession asserts that cancelling the request float exits
// cleanly (0) and spawns NO docked session pane.
func TestLaunch_CancelNoSession(t *testing.T) {
	fastFloatWait(t)
	isolateCache(t)
	m := &launchMux{floatCancel: true}
	code := launch(m, "/bin/ai-playbook", capture.Request{CWD: "/x"}, escalateClassify())
	if code != 0 {
		t.Fatalf("cancelled launch exit = %d, want 0", code)
	}
	if len(m.docked) != 0 {
		t.Fatalf("cancel should spawn no docked pane, got %d", len(m.docked))
	}
	if m.typedCount != 0 {
		t.Fatalf("cancel should type nothing into the origin pane, got %d", m.typedCount)
	}
	// On cancel the launcher writes NO .done (the float exits itself on its .cancel).
	if out := argAfter(m.floats[0], "--out"); fileExists(out + ".done") {
		t.Errorf("cancel must NOT write %s.done", out)
	}
}

// TestLaunch_WaitTimeoutStillRoutes asserts the wait is bounded: if the float
// never writes <out>.closed (a crash/stall), the launcher still proceeds past the
// cap and routes (never blocks the user). doneAtRoute holds; closedAtRoute is false.
func TestLaunch_WaitTimeoutStillRoutes(t *testing.T) {
	fastFloatWait(t)
	isolateCache(t)
	m := &launchMux{answer: "do a thing", noClose: true}
	req := capture.Request{CWD: "/proj/dir", ProjectRoot: "/proj"}

	start := time.Now()
	if code := launch(m, "/bin/ai-playbook", req, escalateClassify()); code != 0 {
		t.Fatalf("launch exit = %d, want 0", code)
	}
	if len(m.docked) != 1 {
		t.Fatalf("timeout path must still route (1 docked), got %d", len(m.docked))
	}
	if !m.doneAtRoute {
		t.Error("launcher must still write <out>.done before routing on the timeout path")
	}
	if m.closedAtRoute {
		t.Error("no <out>.closed was written; closedAtRoute should be false on the timeout path")
	}
	// The cap (200ms in fastFloatWait) bounds the wait — far under the live 2s.
	if waited := time.Since(start); waited > time.Second {
		t.Errorf("wait took %v, want bounded by the (shrunk) cap", waited)
	}
}

// TestWaitFloatClosed_EmptyPathNoWait asserts the inline/off-zellij case (no float,
// empty out) is a no-op: waitFloatClosed returns immediately, sleeping no margin.
func TestWaitFloatClosed_EmptyPathNoWait(t *testing.T) {
	fastFloatWait(t)
	isolateCache(t)
	start := time.Now()
	waitFloatClosed("")
	if waited := time.Since(start); waited > 50*time.Millisecond {
		t.Errorf("empty-path waitFloatClosed took %v, want ~0 (no-op)", waited)
	}
}

// TestReadRequestJSON_RoundTrip asserts requestJSON → readRequestJSON preserves
// the launcher→session context fields.
func TestReadRequestJSON_RoundTrip(t *testing.T) {
	in := capture.Request{
		Kind:        "error",
		Command:     "make",
		Exit:        "2",
		DurationMs:  "1500",
		CWD:         "/c",
		ProjectRoot: "/r",
		PaneID:      "terminal_3",
		Scrollback:  "boom",
		UserRequest: "fix make",
		Project:     capture.Project{Name: "r", Branch: "main"},
	}
	f, err := os.CreateTemp(t.TempDir(), "req-*.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(requestJSON(in)); err != nil {
		t.Fatal(err)
	}
	f.Close()

	got, err := readRequestJSON(f.Name())
	if err != nil {
		t.Fatalf("readRequestJSON: %v", err)
	}
	if got != in {
		t.Errorf("round-trip mismatch:\n got=%+v\nwant=%+v", got, in)
	}
}

// TestPrefillTemplate ports assist::prefill_template's behavior.
func TestPrefillTemplate(t *testing.T) {
	errReq := capture.Request{Kind: "error", Command: "gg build", Exit: "1", Project: capture.Project{Name: "app"}}
	if got, want := prefillTemplate(errReq), "Diagnose and fix why `gg build` failed (exit 1) in app"; got != want {
		t.Errorf("error prefill = %q, want %q", got, want)
	}
	// A non-error (question) request has an empty prefill.
	if got := prefillTemplate(capture.Request{Kind: "question"}); got != "" {
		t.Errorf("question prefill = %q, want empty", got)
	}
	// Missing project name falls back to "this directory".
	got := prefillTemplate(capture.Request{Kind: "error", Command: "x", Exit: "3"})
	if !strings.Contains(got, "this directory") {
		t.Errorf("missing-project prefill = %q, want fallback 'this directory'", got)
	}
}

func argAfter(ss []string, key string) string {
	for i, s := range ss {
		if s == key && i+1 < len(ss) {
			return ss[i+1]
		}
	}
	return ""
}

// fileExists reports whether path exists (used to assert the <out>.done marker).
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// TestThinkingTail: collapses whitespace runs to single spaces and returns the
// LAST maxRunes runes (a sliding tail), rune-safe.
func TestThinkingTail(t *testing.T) {
	if got := thinkingTail("  a\t b\n\nc  ", 200); got != "a b c" {
		t.Errorf("collapse = %q, want %q", got, "a b c")
	}
	// Tail window: the last N runes of the collapsed text.
	if got := thinkingTail("one two three four", 9); got != "hree four" {
		t.Errorf("tail = %q, want %q", got, "hree four")
	}
	// Rune-safe: a multi-byte tail is cut on a rune boundary, count == maxRunes.
	got := thinkingTail(strings.Repeat("é ", 300), thinkingTailRunes)
	if n := len([]rune(got)); n > thinkingTailRunes {
		t.Errorf("tail rune count = %d, want ≤ %d", n, thinkingTailRunes)
	}
	if strings.Contains(got, "�") {
		t.Errorf("tail corrupted a multi-byte rune: %q", got)
	}
}

// TestNewThinkingWriter: the onDelta callback writes a single-line, ≤thinkingTailRunes,
// whitespace-collapsed tail to <out>.thinking; an empty out path yields a nil callback.
func TestNewThinkingWriter(t *testing.T) {
	if newThinkingWriter("") != nil {
		t.Error("empty out path must yield a nil (no-op) callback")
	}

	out := filepath.Join(t.TempDir(), "req")
	// Shrink the throttle so the second call writes promptly in the test.
	saved := thinkingWriteEvery
	thinkingWriteEvery = 0
	defer func() { thinkingWriteEvery = saved }()

	w := newThinkingWriter(out)
	// Input is the streaming classify JSON; the writer surfaces only the "content"
	// value (decoding \n) and collapses it to a single line.
	w(`{"kind":"answer","content":"step one\nstep two   step three"`)
	b, err := os.ReadFile(out + input.ThinkingSuffix)
	if err != nil {
		t.Fatalf("thinking file not written: %v", err)
	}
	if got := string(b); got != "step one step two step three" {
		t.Errorf("thinking line = %q, want collapsed single line", got)
	}
	if strings.ContainsAny(string(b), "\n\t") {
		t.Errorf("thinking line must be single-line: %q", b)
	}

	// A long content value is tailed to ≤ thinkingTailRunes.
	w(`{"content":"` + strings.Repeat("x", 1000))
	b, _ = os.ReadFile(out + input.ThinkingSuffix)
	if n := len([]rune(string(b))); n > thinkingTailRunes {
		t.Errorf("written tail = %d runes, want ≤ %d", n, thinkingTailRunes)
	}
}

// TestNewThinkingWriter_Throttle: with a wide throttle window, only the FIRST call
// writes; a rapid second call is skipped (the file keeps the first content).
func TestNewThinkingWriter_Throttle(t *testing.T) {
	out := filepath.Join(t.TempDir(), "req")
	saved := thinkingWriteEvery
	thinkingWriteEvery = time.Hour // effectively never re-write within the test
	defer func() { thinkingWriteEvery = saved }()

	w := newThinkingWriter(out)
	w(`{"content":"first`)
	w(`{"content":"second`) // throttled out
	b, err := os.ReadFile(out + input.ThinkingSuffix)
	if err != nil {
		t.Fatalf("first write missing: %v", err)
	}
	if got := string(b); got != "first" {
		t.Errorf("throttled content = %q, want the first write %q", got, "first")
	}
}

// extractJSONContent shows just the streaming "content" value (the answer/command
// text forming), decoding escapes, robust to truncated/streaming input.
func TestExtractJSONContent(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"kind":"answer","content":"Git merge`, "Git merge"},                         // still streaming, unclosed
		{`{"kind":"answer","content":"Use \"git log\"","title":"X"}`, `Use "git log"`}, // escapes + closed
		{`{"kind":"answer","content":"a\nb"}`, "a\nb"},                                 // \n decoded
		{`{"kind":"answer",`, ""},                                                      // content not started yet
		{`{"kind":"command","content":"git log","title"`, "git log"},                   // value closed, trailing JSON
		{`{"kind":"answer","content":"tail\`, "tail"},                                  // dangling escape at the tail
	}
	for _, c := range cases {
		if got := extractJSONContent(c.in); got != c.want {
			t.Errorf("extractJSONContent(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// failClassify is a classifyFunc that fails the test if invoked — the cache-hit
// (and no-cache-with-store-skip) paths must serve WITHOUT calling the haiku.
func failClassify(t *testing.T) classifyFunc {
	t.Helper()
	return func(capture.Request, author.AuthorOptions) (author.Classification, error) {
		t.Error("classify must NOT be called on a cache hit")
		return author.Classification{Kind: author.KindEscalate}, nil
	}
}

// launchKeys computes the (ctxHash, reqHash) the launcher's triage.Route would
// derive for req with the given SUBMITTED request value (the float answer becomes
// req.UserRequest, so the request hash keys on the submitted text, not req's).
func launchKeys(req capture.Request, submitted string) (string, string) {
	cr := cache.Request{
		ProjectRoot: req.ProjectRoot,
		CWD:         req.CWD,
		CommandText: req.Command,
		CommandExit: req.Exit,
		Scrollback:  req.Scrollback,
	}
	return cache.ContextHash(cr), cache.RequestHash(submitted)
}

// TestLaunch_CacheHitCommand: a repeat request whose cached entry is kind=command
// is served straight from the cache — classify is NOT called and the cached body
// is typed into the origin pane (no docked pane).
func TestLaunch_CacheHitCommand(t *testing.T) {
	fastFloatWait(t)
	c := isolateCache(t)
	const submitted = "list last week's commits"
	req := capture.Request{CWD: "/proj/dir", ProjectRoot: "/proj", PaneID: "terminal_7"}
	ctx, rh := launchKeys(req, submitted)
	if _, err := c.Store(ctx, rh, "command", "git log --since='last week' -n 3", nil, ""); err != nil {
		t.Fatal(err)
	}

	m := &launchMux{answer: submitted}
	if code := launch(m, "/bin/ai-playbook", req, failClassify(t)); code != 0 {
		t.Fatalf("launch exit = %d, want 0", code)
	}
	if m.typedCount != 1 {
		t.Fatalf("expected 1 TypeInto from the cached command, got %d", m.typedCount)
	}
	if m.typedText != "git log --since='last week' -n 3" {
		t.Errorf("typed text = %q, want the cached command body", m.typedText)
	}
	if m.typedPane != "terminal_7" {
		t.Errorf("typed into pane %q, want origin terminal_7", m.typedPane)
	}
	if len(m.docked) != 0 {
		t.Fatalf("cached command must spawn NO docked pane, got %d", len(m.docked))
	}
}

// TestLaunch_CacheHitAnswer: a repeat request whose cached entry is kind=answer is
// served straight from the cache — classify is NOT called, a docked pager renders
// the cached body, and the pager carries --cached <created_at> (the badge pill).
func TestLaunch_CacheHitAnswer(t *testing.T) {
	fastFloatWait(t)
	c := isolateCache(t)
	const submitted = "what is HEAD?"
	req := capture.Request{CWD: "/proj/dir", ProjectRoot: "/proj"}
	ctx, rh := launchKeys(req, submitted)
	if _, err := c.Store(ctx, rh, "answer", "HEAD is the current commit.", map[string]string{"title": "What Is HEAD"}, ""); err != nil {
		t.Fatal(err)
	}

	m := &launchMux{answer: submitted}
	if code := launch(m, "/bin/ai-playbook", req, failClassify(t)); code != 0 {
		t.Fatalf("launch exit = %d, want 0", code)
	}
	if m.typedCount != 0 {
		t.Errorf("cached answer must not type into the origin, got %d", m.typedCount)
	}
	if len(m.docked) != 1 {
		t.Fatalf("expected 1 docked pager pane, got %d", len(m.docked))
	}
	dargv := m.docked[0]
	if dargv[0] != "/bin/ai-playbook" || dargv[1] != "answer" {
		t.Fatalf("docked argv prefix = %v, want [/bin/ai-playbook answer …]", dargv[:2])
	}
	if !strings.Contains(m.dockedAnswer, "HEAD is the current commit") {
		t.Errorf("answer md missing the cached body:\n%s", m.dockedAnswer)
	}
	if got := argAfter(dargv, "--cached"); got == "" {
		t.Errorf("cached answer must carry --cached <created_at>\nargv: %v", dargv)
	}
	if got := argAfter(dargv, "--title"); got != "What Is HEAD" {
		t.Errorf("cached answer --title = %q, want the stored title extra", got)
	}
}

// TestLaunch_CacheHitPlaybook: a repeat request whose cached entry is kind=playbook
// is served by spawning the docked SESSION pane (which re-runs triage.Route and
// serves the cached playbook); classify is NOT called.
// A cached PLAYBOOK is NOT short-circuited: the launcher re-classifies (the same
// request classifies differently across contexts, so a frozen playbook must not pop
// a pane for a prompt the user now wants as a command). When the re-classification
// is still escalate, the session re-serves this cached playbook.
func TestLaunch_CacheHitPlaybookReclassifies(t *testing.T) {
	fastFloatWait(t)
	c := isolateCache(t)
	const submitted = "fix the build"
	req := capture.Request{CWD: "/proj/dir", ProjectRoot: "/proj"}
	ctx, rh := launchKeys(req, submitted)
	if _, err := c.Store(ctx, rh, "playbook", "# Fix\n", nil, ""); err != nil {
		t.Fatal(err)
	}

	called := false
	classify := func(capture.Request, author.AuthorOptions) (author.Classification, error) {
		called = true
		return author.Classification{Kind: author.KindEscalate}, nil
	}
	m := &launchMux{answer: submitted}
	if code := launch(m, "/bin/ai-playbook", req, classify); code != 0 {
		t.Fatalf("launch exit = %d, want 0", code)
	}
	if !called {
		t.Error("a cached PLAYBOOK hit must re-classify, not short-circuit")
	}
	if len(m.docked) != 1 || m.docked[0][1] != "session" {
		t.Fatalf("re-classified escalate must spawn a docked session, got docked=%v", m.docked)
	}
}

// The regression fix: a request previously cached as a PLAYBOOK, but which the
// classify now decides is a COMMAND, must type the command into the origin pane —
// NOT pop the stale playbook's session pane.
func TestLaunch_CacheHitPlaybookButCommandNow(t *testing.T) {
	fastFloatWait(t)
	c := isolateCache(t)
	const submitted = "fix the build"
	req := capture.Request{CWD: "/proj/dir", ProjectRoot: "/proj", PaneID: "terminal_7"}
	ctx, rh := launchKeys(req, submitted)
	if _, err := c.Store(ctx, rh, "playbook", "# Fix\n", nil, ""); err != nil {
		t.Fatal(err)
	}

	classify := fakeClassify(author.Classification{Kind: author.KindCommand, Content: "make build"}, nil)
	m := &launchMux{answer: submitted}
	if code := launch(m, "/bin/ai-playbook", req, classify); code != 0 {
		t.Fatalf("launch exit = %d, want 0", code)
	}
	if m.typedCount != 1 || m.typedText != "make build" {
		t.Fatalf("a playbook-cached prompt now classified as command must type it, got count=%d text=%q", m.typedCount, m.typedText)
	}
	if len(m.docked) != 0 {
		t.Fatalf("no pane should spawn on a command, got docked=%v", m.docked)
	}
}

// TestLaunch_CacheMissStoresCommand: a MISS classified as command IS classified
// (the haiku ran) and the result is stored so the next identical request hits.
func TestLaunch_CacheMissStoresCommand(t *testing.T) {
	fastFloatWait(t)
	c := isolateCache(t)
	const submitted = "show the git log"
	req := capture.Request{CWD: "/proj/dir", ProjectRoot: "/proj", PaneID: "terminal_7"}
	classify := fakeClassify(author.Classification{Kind: author.KindCommand, Content: "git log -n 3"}, nil)

	m := &launchMux{answer: submitted}
	if code := launch(m, "/bin/ai-playbook", req, classify); code != 0 {
		t.Fatalf("launch exit = %d, want 0", code)
	}
	ctx, rh := launchKeys(req, submitted)
	path, ok := c.Lookup(ctx, rh)
	if !ok {
		t.Fatal("command MISS must store a cache entry")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(raw)
	if kind, _ := cache.Field(content, "kind"); kind != "command" {
		t.Errorf("stored kind = %q, want command", kind)
	}
	if body := cache.Body(content); body != "git log -n 3" {
		t.Errorf("stored body = %q, want the classified command", body)
	}
}

// TestLaunch_CacheMissStoresAnswer: a MISS classified as answer stores the answer
// entry carrying the classify title as the `title` front-matter extra.
func TestLaunch_CacheMissStoresAnswer(t *testing.T) {
	fastFloatWait(t)
	c := isolateCache(t)
	const submitted = "explain HEAD"
	req := capture.Request{CWD: "/proj/dir", ProjectRoot: "/proj"}
	classify := fakeClassify(author.Classification{Kind: author.KindAnswer, Content: "HEAD is the tip.", Title: "About HEAD"}, nil)

	m := &launchMux{answer: submitted}
	if code := launch(m, "/bin/ai-playbook", req, classify); code != 0 {
		t.Fatalf("launch exit = %d, want 0", code)
	}
	ctx, rh := launchKeys(req, submitted)
	path, ok := c.Lookup(ctx, rh)
	if !ok {
		t.Fatal("answer MISS must store a cache entry")
	}
	raw, _ := os.ReadFile(path)
	content := string(raw)
	if kind, _ := cache.Field(content, "kind"); kind != "answer" {
		t.Errorf("stored kind = %q, want answer", kind)
	}
	if body := cache.Body(content); body != "HEAD is the tip." {
		t.Errorf("stored body = %q, want the classified answer", body)
	}
	if title, _ := cache.Field(content, "title"); title != "About HEAD" {
		t.Errorf("stored title extra = %q, want About HEAD", title)
	}
}

// TestLaunch_CacheMissEscalateStoresNothing: a MISS classified as escalate writes
// NOTHING from the launcher (the session owns the playbook entry).
func TestLaunch_CacheMissEscalateStoresNothing(t *testing.T) {
	fastFloatWait(t)
	c := isolateCache(t)
	const submitted = "diagnose the failure"
	req := capture.Request{CWD: "/proj/dir", ProjectRoot: "/proj"}

	m := &launchMux{answer: submitted}
	if code := launch(m, "/bin/ai-playbook", req, escalateClassify()); code != 0 {
		t.Fatalf("launch exit = %d, want 0", code)
	}
	if len(m.docked) != 1 || m.docked[0][1] != "session" {
		t.Fatalf("escalate must spawn a docked session, got docked=%v", m.docked)
	}
	ctx, rh := launchKeys(req, submitted)
	if _, ok := c.Lookup(ctx, rh); ok {
		t.Error("escalate MISS must NOT write a cache entry from the launcher")
	}
}

// TestLaunch_NoCacheBypass: AI_PLAYBOOK_NO_CACHE bypasses the lookup (classify runs)
// and skips the store (no entry written), even for a command/answer classification.
func TestLaunch_NoCacheBypass(t *testing.T) {
	fastFloatWait(t)
	c := isolateCache(t)
	t.Setenv("AI_PLAYBOOK_NO_CACHE", "1")
	const submitted = "show the git log"
	req := capture.Request{CWD: "/proj/dir", ProjectRoot: "/proj", PaneID: "terminal_7"}

	classified := false
	classify := func(capture.Request, author.AuthorOptions) (author.Classification, error) {
		classified = true
		return author.Classification{Kind: author.KindCommand, Content: "git log -n 3"}, nil
	}

	m := &launchMux{answer: submitted}
	if code := launch(m, "/bin/ai-playbook", req, classify); code != 0 {
		t.Fatalf("launch exit = %d, want 0", code)
	}
	if !classified {
		t.Error("no-cache must still classify (lookup bypassed)")
	}
	ctx, rh := launchKeys(req, submitted)
	if _, ok := c.Lookup(ctx, rh); ok {
		t.Error("no-cache must NOT store the classified result")
	}
}
