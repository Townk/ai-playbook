package launcher

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Townk/ai-playbook/internal/author"
	"github.com/Townk/ai-playbook/internal/capture"
	"github.com/Townk/ai-playbook/internal/driver"
	"github.com/Townk/ai-playbook/internal/mux"
)

// ── waitingModel (inline_input.go) ─────────────────────────────────────────────
// The non-alt-screen "Working…" model classifyWithProgress drives. The live
// tea.Program loop is integration-only, but the model's own Init/Update/View/
// command builders are pure and unit-testable in isolation.

// TestWaitingModel_NewDefaults asserts newWaitingModel seeds an 80-col width and
// retains the supplied channels.
func TestWaitingModel_NewDefaults(t *testing.T) {
	actCh := make(chan string)
	resCh := make(chan wResult)
	m := newWaitingModel(actCh, resCh)
	if m.width != 80 {
		t.Errorf("default width = %d, want 80", m.width)
	}
	if m.actCh == nil || m.resCh == nil {
		t.Error("newWaitingModel must retain the activity + result channels")
	}
}

// TestWaitingModel_Init asserts Init returns a (batched) command to drive the
// tick + the two channel receivers.
func TestWaitingModel_Init(t *testing.T) {
	m := newWaitingModel(make(chan string), make(chan wResult))
	if m.Init() == nil {
		t.Error("Init must return a non-nil batch command")
	}
}

// TestWaitingModel_Update walks each message branch of the model's Update.
func TestWaitingModel_Update(t *testing.T) {
	base := newWaitingModel(make(chan string, 1), make(chan wResult, 1))

	// WindowSizeMsg resizes.
	m, _ := base.Update(tea.WindowSizeMsg{Width: 120})
	if got := m.(waitingModel).width; got != 120 {
		t.Errorf("after WindowSizeMsg width = %d, want 120", got)
	}

	// wTickMsg advances frame + ticks and re-arms the tick.
	m, cmd := base.Update(wTickMsg{})
	wm := m.(waitingModel)
	if wm.frame != 1 || wm.ticks != 1 {
		t.Errorf("after wTickMsg frame=%d ticks=%d, want 1/1", wm.frame, wm.ticks)
	}
	if cmd == nil {
		t.Error("wTickMsg must re-arm the tick (non-nil cmd)")
	}

	// A non-empty wActMsg sets the activity line; an empty one leaves it.
	m, cmd = base.Update(wActMsg("compiling"))
	if got := m.(waitingModel).activity; got != "compiling" {
		t.Errorf("activity = %q, want %q", got, "compiling")
	}
	if cmd == nil {
		t.Error("wActMsg must re-arm the activity receiver (non-nil cmd)")
	}
	seeded := base
	seeded.activity = "keep"
	m, _ = seeded.Update(wActMsg(""))
	if got := m.(waitingModel).activity; got != "keep" {
		t.Errorf("empty wActMsg cleared activity to %q, want it preserved", got)
	}

	// wDoneMsg stores the result and quits.
	want := wResult{cls: author.Classification{Kind: author.KindAnswer}}
	m, cmd = base.Update(wDoneMsg{r: want})
	if got := m.(waitingModel).res; got.cls.Kind != author.KindAnswer {
		t.Errorf("after wDoneMsg res kind = %q, want answer", got.cls.Kind)
	}
	if cmd == nil {
		t.Fatal("wDoneMsg must return a quit command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("wDoneMsg command must be tea.Quit")
	}

	// An unknown message is a no-op (nil cmd).
	if _, c := base.Update(struct{}{}); c != nil {
		t.Error("unknown message must yield a nil command")
	}
}

// TestWaitingModel_LastHeight: 1 rendered line when idle, 2 once an activity line
// is present (the clear-on-exit math).
func TestWaitingModel_LastHeight(t *testing.T) {
	m := newWaitingModel(nil, nil)
	if got := m.lastHeight(); got != 1 {
		t.Errorf("idle lastHeight = %d, want 1", got)
	}
	m.activity = "doing a thing"
	if got := m.lastHeight(); got != 2 {
		t.Errorf("active lastHeight = %d, want 2", got)
	}
}

// TestWaitingModel_View renders a non-empty waiting line.
func TestWaitingModel_View(t *testing.T) {
	m := newWaitingModel(nil, nil)
	m.activity = "running env"
	if v := m.View(); strings.TrimSpace(v.Content) == "" {
		t.Error("View must render a non-empty waiting line")
	}
}

// TestWaitingModel_RecvCommands exercises the channel-receiver command builders.
func TestWaitingModel_RecvCommands(t *testing.T) {
	// wRecvAct on a delivered value forwards it as a wActMsg.
	actCh := make(chan string, 1)
	actCh <- "linking"
	if msg := wRecvAct(actCh)(); msg != wActMsg("linking") {
		t.Errorf("wRecvAct delivered %v, want wActMsg(linking)", msg)
	}
	// wRecvAct on a closed channel yields an empty wActMsg (drained, not blocked).
	closed := make(chan string)
	close(closed)
	if msg := wRecvAct(closed)(); msg != wActMsg("") {
		t.Errorf("wRecvAct on closed chan = %v, want empty wActMsg", msg)
	}
	// wRecvDone forwards the result as a wDoneMsg.
	resCh := make(chan wResult, 1)
	resCh <- wResult{cls: author.Classification{Kind: author.KindCommand}}
	dm, ok := wRecvDone(resCh)().(wDoneMsg)
	if !ok || dm.r.cls.Kind != author.KindCommand {
		t.Errorf("wRecvDone = %#v, want wDoneMsg carrying the result", dm)
	}
	// wTick eventually emits a wTickMsg.
	if _, ok := wTick()().(wTickMsg); !ok {
		t.Error("wTick command must emit a wTickMsg")
	}
}

// ── bridgeOf (session.go) ──────────────────────────────────────────────────────

// TestBridgeOf: nil session → nil; a session carrying a bridge returns it.
func TestBridgeOf(t *testing.T) {
	if bridgeOf(nil) != nil {
		t.Error("bridgeOf(nil) must be nil")
	}
	if got := bridgeOf(&session{}); got != nil {
		t.Error("bridgeOf of a bridge-less session must be nil")
	}
}

// ── reqCwd (inline_input.go) ───────────────────────────────────────────────────

// TestReqCwd: prefers the project root, falls back to the cwd.
func TestReqCwd(t *testing.T) {
	if got := reqCwd(capture.Request{ProjectRoot: "/root", CWD: "/cwd"}); got != "/root" {
		t.Errorf("reqCwd with a project root = %q, want /root", got)
	}
	if got := reqCwd(capture.Request{CWD: "/cwd"}); got != "/cwd" {
		t.Errorf("reqCwd without a project root = %q, want /cwd", got)
	}
}

// ── dbg open-failure branch (debug.go) ─────────────────────────────────────────

// TestDbg_OpenFailureSwallowed: when the log path can't be opened for append (here
// it is a directory), dbg swallows the error rather than panicking.
func TestDbg_OpenFailureSwallowed(t *testing.T) {
	dbgInit(t.TempDir()) // a directory: OpenFile(O_WRONLY) fails
	t.Cleanup(func() { dbgInit("") })
	dbg("this should be swallowed") // must not panic
}

// ── buildEnvLookup (session.go) ────────────────────────────────────────────────

// TestBuildEnvLookup_NilDriver: a nil driver yields an always-miss lookup (the
// referenced var is simply omitted from the front matter). Calling twice exercises
// the once.Do cache.
func TestBuildEnvLookup_NilDriver(t *testing.T) {
	lookup := buildEnvLookup(nil)
	if v, ok := lookup("PATH"); ok || v != "" {
		t.Errorf("nil-driver lookup = (%q,%v), want (\"\",false)", v, ok)
	}
	if _, ok := lookup("HOME"); ok { // second call: once.Do already ran
		t.Error("nil-driver lookup must stay an always-miss")
	}
}

// TestBuildEnvLookup_LiveDriver: against a live session shell the lookup dumps the
// shell env once and reflects it — a set var hits, an absent one misses.
func TestBuildEnvLookup_LiveDriver(t *testing.T) {
	minimalZDOTDIR(t)
	t.Setenv("AAPB_ENVLOOKUP_PROBE", "probe-value")
	drv, err := driver.Open(driver.Options{Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("driver.Open: %v", err)
	}
	defer drv.Close()

	lookup := buildEnvLookup(drv)
	if v, ok := lookup("AAPB_ENVLOOKUP_PROBE"); !ok || v != "probe-value" {
		t.Errorf("live lookup of the probe var = (%q,%v), want (probe-value,true)", v, ok)
	}
	if _, ok := lookup("AAPB_DEFINITELY_UNSET_VAR_XYZ"); ok {
		t.Error("an unset var must miss")
	}
}

// ── authoringAgent fallback (session.go) ───────────────────────────────────────

// TestAuthoringAgent_Fallback: a nil session and a session without a resolvable
// selfExe both fall back to the plain (non-MCP) claude agent — a usable, non-nil
// Agent (authoring still works without the tools backend).
func TestAuthoringAgent_Fallback(t *testing.T) {
	var nilSess *session
	if nilSess.authoringAgent() == nil {
		t.Error("nil session must fall back to a non-nil plain agent")
	}
	if (&session{selfExe: ""}).authoringAgent() == nil {
		t.Error("empty selfExe must fall back to a non-nil plain agent")
	}
}

// ── asker success path (session.go) ────────────────────────────────────────────

// TestAsker_SuccessPathSubmit: with a real mux + selfExe the asker returns a live
// closure that spawns the input float and returns the submitted value.
func TestAsker_SuccessPathSubmit(t *testing.T) {
	s := &session{selfExe: "/bin/ai-playbook", m: &launchMux{answer: "rename the func", noClose: true}}
	ask := s.asker("/proj")
	if ask == nil {
		t.Fatal("real mux + selfExe must yield a non-nil asker")
	}
	val, ok := ask("What should I change?")
	if !ok || val != "rename the func" {
		t.Errorf("asker returned (%q,%v), want (rename the func,true)", val, ok)
	}
}

// TestAsker_SuccessPathCancel: a cancelled float yields submitted=false.
func TestAsker_SuccessPathCancel(t *testing.T) {
	s := &session{selfExe: "/bin/ai-playbook", m: &launchMux{floatCancel: true}}
	ask := s.asker("/proj")
	if ask == nil {
		t.Fatal("real mux + selfExe must yield a non-nil asker")
	}
	if val, ok := ask("change?"); ok || val != "" {
		t.Errorf("cancelled asker = (%q,%v), want (\"\",false)", val, ok)
	}
}

// ── request JSON decode error paths (session.go) ───────────────────────────────

// TestDecodeRequestJSON_Invalid: malformed JSON surfaces an error.
func TestDecodeRequestJSON_Invalid(t *testing.T) {
	if _, err := decodeRequestJSON([]byte("{not json")); err == nil {
		t.Error("decodeRequestJSON must error on malformed JSON")
	}
}

// TestReadRequestJSON_MissingFile: an unreadable path surfaces an error.
func TestReadRequestJSON_MissingFile(t *testing.T) {
	if _, err := readRequestJSON(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Error("readRequestJSON must error on a missing file")
	}
}

// ── prefillTemplate missing-exit branch (session.go) ───────────────────────────

// TestPrefillTemplate_MissingExit: an error request with no exit code renders the
// "exit ?" placeholder.
func TestPrefillTemplate_MissingExit(t *testing.T) {
	got := prefillTemplate(capture.Request{Kind: "error", Command: "make", Project: capture.Project{Name: "app"}})
	if !strings.Contains(got, "exit ?") {
		t.Errorf("missing-exit prefill = %q, want the 'exit ?' placeholder", got)
	}
}

// ── extractJSONContent escape handling (launcher.go) ───────────────────────────

// TestExtractJSONContent_Escapes covers the remaining JSON escape decodings the
// existing suite doesn't: \t \r \b \f, a \u code point, and an incomplete \u tail.
func TestExtractJSONContent_Escapes(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"content":"a\tb"`, "a\tb"},
		{`{"content":"a\rb"`, "a\rb"},
		{`{"content":"a\bb"`, "a\bb"},
		{`{"content":"a\fb"`, "a\fb"},
		{`{"content":"a\/b"`, "a/b"},
		{`{"content":"a\xb"`, "axb"},     // unknown escape → default: emit the escaped byte
		{`{"content":"tail\u00`, "tail"}, // incomplete \uXXXX at the stream tail
		{`{"content":`, ""},              // colon present, value not started
		{`{"content" "no colon"`, ""},    // no colon after the key
	}
	for _, c := range cases {
		if got := extractJSONContent(c.in); got != c.want {
			t.Errorf("extractJSONContent(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// ── thinking-file write helpers (launcher.go) ──────────────────────────────────

// TestWriteThinkingFile_EmptyPath: an empty out path is a no-op (no panic).
func TestWriteThinkingFile_EmptyPath(t *testing.T) {
	writeThinkingFile("", "line") // must not panic
}

// TestWriteThinkingFile_UnwritableDir: a write into a non-existent directory is
// swallowed best-effort (cosmetic line must never block the classify).
func TestWriteThinkingFile_UnwritableDir(t *testing.T) {
	out := filepath.Join(t.TempDir(), "no-such-dir", "req")
	writeThinkingFile(out, "line") // must not panic
	if _, err := os.Stat(out + ".thinking"); err == nil {
		t.Error("write into a missing dir must not create the thinking file")
	}
}

// TestWriteDoneFile_EmptyPath: an empty out path is a no-op (no panic, no file).
func TestWriteDoneFile_EmptyPath(t *testing.T) {
	writeDoneFile("") // must not panic
}

// ── answerRegenFunc error path (launcher.go) ───────────────────────────────────

// TestAnswerRegenFunc_ClassifyError: a classify failure surfaces through the
// reload closure as an error (and never re-caches).
func TestAnswerRegenFunc_ClassifyError(t *testing.T) {
	t.Setenv("AI_PLAYBOOK_DATA_DIR", t.TempDir())
	orig := answerClassify
	t.Cleanup(func() { answerClassify = orig })
	answerClassify = func(capture.Request, author.AuthorOptions) (author.Classification, error) {
		return author.Classification{}, errors.New("classify boom")
	}
	if rc, err := answerRegenFunc(capture.Request{UserRequest: "q"})(); err == nil || rc != nil {
		t.Errorf("classify error must propagate: rc=%v err=%v", rc, err)
	}
}

// ── spawn error paths (launcher.go) ────────────────────────────────────────────

// failDockedMux is a Mux whose SpawnDocked always fails, so spawnAnswer/spawnSession
// exercise their spawn-failure cleanup (temp file removed, exit 1).
type failDockedMux struct{}

func (failDockedMux) DumpScreen(string) (string, error) { return "", nil }
func (failDockedMux) SpawnFloat(mux.SpawnOptions) error { return nil }
func (failDockedMux) SpawnInputFloat(mux.SpawnOptions) error {
	return nil
}
func (failDockedMux) SpawnPane(mux.SpawnOptions) error { return nil }
func (failDockedMux) TypeInto(string, string) error    { return nil }
func (failDockedMux) SpawnDocked(mux.SpawnOptions) error {
	return errors.New("spawn docked failed")
}

// TestSpawnAnswer_SpawnDockedError: a SpawnDocked failure returns exit 1.
func TestSpawnAnswer_SpawnDockedError(t *testing.T) {
	code := spawnAnswer(failDockedMux{}, "/bin/ai-playbook",
		capture.Request{ProjectRoot: "/proj"}, "prose", "title", "")
	if code != 1 {
		t.Errorf("spawnAnswer on SpawnDocked failure = %d, want 1", code)
	}
}

// TestSpawnSession_SpawnDockedError: a SpawnDocked failure returns exit 1.
func TestSpawnSession_SpawnDockedError(t *testing.T) {
	code := spawnSession(failDockedMux{}, "/bin/ai-playbook",
		capture.Request{ProjectRoot: "/proj"}, "title")
	if code != 1 {
		t.Errorf("spawnSession on SpawnDocked failure = %d, want 1", code)
	}
}
