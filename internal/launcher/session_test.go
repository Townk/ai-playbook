package launcher

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Townk/ai-playbook/internal/author"
	"github.com/Townk/ai-playbook/internal/cache"
	"github.com/Townk/ai-playbook/internal/capture"
	"github.com/Townk/ai-playbook/internal/mux"
	"github.com/Townk/ai-playbook/internal/tools"
	"github.com/Townk/ai-playbook/internal/triage"
	"github.com/Townk/ai-playbook/internal/ui"
)

// TestOpenSessionAsync_DeliversOnce asserts the async session opener returns a
// buffered (cap 1) channel that yields the built session exactly once, so the
// cached-render path can proceed without blocking on the shell's blank-pane startup.
func TestOpenSessionAsync_DeliversOnce(t *testing.T) {
	minimalZDOTDIR(t)
	ch := openSessionAsync(capture.Request{ProjectRoot: t.TempDir()}, mux.Null())
	if c := cap(ch); c != 1 {
		t.Errorf("openSessionAsync channel cap = %d, want 1 (buffered so the goroutine never blocks)", c)
	}
	select {
	case sess := <-ch:
		if sess == nil {
			t.Fatal("openSessionAsync delivered nil (driver/tools setup failed)")
		}
		defer sess.close()
		if sess.drv == nil {
			t.Error("delivered session has no shared driver")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("openSessionAsync did not deliver a session")
	}
}

// TestReengageReady_NilSession_Degraded asserts the cached-replay ready builder maps
// a failed background open (nil session) to an empty OrchReady{} — the signal the ui
// uses to clear pending state and stay degraded (shell buttons disabled) rather than
// hang.
func TestReengageReady_NilSession_Degraded(t *testing.T) {
	got := reengageReady(triage.Decision{}, capture.Request{}, nil, "")
	if got.Orch != nil {
		t.Error("nil session: OrchReady.Orch should be nil (degraded)")
	}
	if got.Asker != nil {
		t.Error("nil session: OrchReady.Asker should be nil (degraded)")
	}
}

// TestReengageReady_LiveSession_BuildsOrch asserts that a live session yields a fully
// wired OrchReady: a non-nil orchestrator (built via ui.BuildOrch with the session's
// shared driver + the re-engagement context) and the request-input-float asker.
// We pass a non-null mux (&launchMux{}) so session.asker returns a non-nil closure
// (it is nil for the null mux because the TUI owns the terminal inline).
func TestReengageReady_LiveSession_BuildsOrch(t *testing.T) {
	minimalZDOTDIR(t)
	t.Setenv("AI_PLAYBOOK_DATA_DIR", t.TempDir())
	sess := openSession(capture.Request{ProjectRoot: t.TempDir()}, &launchMux{})
	if sess == nil {
		t.Fatal("openSession returned nil (driver/tools setup failed)")
	}
	defer sess.close()

	got := reengageReady(triage.Decision{}, capture.Request{ProjectRoot: t.TempDir()}, sess, "/tmp")
	if got.Orch == nil {
		t.Error("live session: OrchReady.Orch should be non-nil")
	}
	if sess.selfExe != "" && got.Asker == nil {
		t.Error("live session with selfExe: OrchReady.Asker should be non-nil")
	}
}

// TestServeCachedReadyLifecycle_NilSession exercises the serveCachedPlaybook ready
// goroutine's lifecycle wiring (held/done) against a failed background open: the
// goroutine reads the nil session off sessCh, records it in held, delivers the
// degraded OrchReady{}, and closes done — so the post-ui cleanup (<-done; close held)
// never hangs and never panics. Mirrors the inline goroutine in serveCachedPlaybook.
func TestServeCachedReadyLifecycle_NilSession(t *testing.T) {
	sessCh := make(chan *session, 1)
	sessCh <- nil // background open failed

	readyCh := make(chan ui.OrchReady, 1)
	held := (*session)(nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		sess := <-sessCh
		held = sess
		readyCh <- reengageReady(triage.Decision{}, capture.Request{}, sess, "")
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ready goroutine did not close done")
	}
	if held != nil {
		t.Error("held should be nil after a failed background open")
	}
	if got := <-readyCh; got.Orch != nil || got.Asker != nil {
		t.Error("failed open should deliver a degraded OrchReady{}")
	}
	// Cleanup is a no-op on a nil session — must not panic.
	held.close()
}

// TestAnswerRegenReCachesAnswer exercises the cached-ANSWER reload closure
// (answerRegenFunc): it re-runs the cheap classify (faked via the answerClassify
// seam), streams the fresh prose back, AND re-caches it under the SAME (ctx,req)
// keys with kind=answer. AI_PLAYBOOK_DATA_DIR isolates the store.
//
// NOTE: this uses the answerClassify package var as an injection seam so the closure
// can run without the live triage model (flag for review).
func TestAnswerRegenReCachesAnswer(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AI_PLAYBOOK_DATA_DIR", dir)

	orig := answerClassify
	t.Cleanup(func() { answerClassify = orig })
	answerClassify = func(req capture.Request, opts author.AuthorOptions) (author.Classification, error) {
		return author.Classification{Kind: author.KindAnswer, Content: "FRESH PROSE ANSWER", Title: "Fresh Title"}, nil
	}

	req := capture.Request{
		ProjectRoot: "/proj",
		CWD:         "/proj/sub",
		UserRequest: "what is HEAD?",
	}
	r, err := answerRegenFunc(req)()
	if err != nil {
		t.Fatalf("answerRegenFunc closure err = %v", err)
	}
	body, _ := io.ReadAll(r)
	if string(body) != "FRESH PROSE ANSWER" {
		t.Errorf("closure reader = %q, want the fresh content", string(body))
	}

	// The fresh prose must be re-cached under the SAME (ctx,req) keys, kind=answer.
	ctxH := cache.ContextHash(cache.Request{
		ProjectRoot: req.ProjectRoot, CWD: req.CWD,
		CommandText: req.Command, CommandExit: req.Exit, Scrollback: req.Scrollback,
	})
	reqH := cache.RequestHash(req.UserRequest)
	entry := filepath.Join(dir, "cache", ctxH, reqH+".md")
	raw, err := os.ReadFile(entry)
	if err != nil {
		t.Fatalf("re-cached entry not found at %s: %v", entry, err)
	}
	content := string(raw)
	if kind, _ := cache.Field(content, "kind"); kind != "answer" {
		t.Errorf("re-cached kind = %q, want answer", kind)
	}
	if got := cache.Body(content); !strings.Contains(got, "FRESH PROSE ANSWER") {
		t.Errorf("re-cached body = %q, want the fresh content", got)
	}
	if title, _ := cache.Field(content, "title"); title != "Fresh Title" {
		t.Errorf("re-cached title = %q, want Fresh Title", title)
	}
}

// minimalZDOTDIR points the driver at a controlled rc (no p10k/mise) so
// openSession's shared driver comes up deterministically in the test environment.
func minimalZDOTDIR(t *testing.T) {
	t.Helper()
	zdot := t.TempDir()
	if err := os.WriteFile(filepath.Join(zdot, ".zshrc"), []byte("# minimal rc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ZDOTDIR", zdot)
}

// TestOpenSession_SharedDriverAndToolsBackend asserts the stage-5 lifecycle: ONE
// driver is created at session start and the tools backend serves THAT driver
// (the backend's `run` executes in the shared shell). The session's driver is the
// instance the run path reuses (via StreamOptions.Driver / ui.SetDriver), so the
// agent and the playbook drive the same shell.
func TestOpenSession_SharedDriverAndToolsBackend(t *testing.T) {
	minimalZDOTDIR(t)
	sess := openSession(capture.Request{ProjectRoot: t.TempDir()}, mux.Null())
	if sess == nil {
		t.Fatal("openSession returned nil (driver/tools setup failed)")
	}
	defer sess.close()

	if sess.drv == nil {
		t.Fatal("session has no shared driver")
	}
	if sess.socket == "" {
		t.Fatal("session has no tools socket")
	}

	// The tools backend is live and drives the SHARED session driver: a run RPC
	// executes in that shell. We prove shared state via CWD, which persists across
	// runs by design (auto-env on cd depends on it). NB: a block's raw `export` is
	// intentionally isolated to its subshell so a block's `set -e` can't kill the
	// hosted shell; cross-block data flows through AAS_OUT_<id>/LAST_* (driver-managed
	// in the main context), not bare exports.
	sess.drv.Run("builtin cd -- /tmp", 5*time.Second)
	res, err := tools.Dial(sess.socket, tools.Call{Tool: "run", Cmd: "pwd"})
	if err != nil {
		t.Fatalf("dial tools backend: %v", err)
	}
	if res.Out != "/tmp" {
		t.Errorf("tools backend run pwd = %q, want %q (backend must drive the shared session driver)", res.Out, "/tmp")
	}
}

// fakeClaude builds a fake claude executable that writes its full argv (one arg
// per line) to argvFile and prints a canned playbook, so the test can assert the
// real authoring invocation (runClaude) includes --mcp-config without launching
// claude.
func fakeClaude(t *testing.T, argvFile string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	// NUL-separate the captured argv so multi-line args (the system prompt) survive.
	script := "#!/usr/bin/env bash\nprintf '%s\\0' \"$@\" > " + shq(argvFile) + "\necho '# playbook'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// TestAuthoringAgent_InvokesClaudeWithMCPConfig asserts that, with a live session,
// the authoring agent's claude invocation includes --mcp-config (the MCP adapter
// wiring) and that the system prompt carries the run-tool instruction. It uses a
// fake claude (AI_PLAYBOOK_CLAUDE_BIN) to capture the argv.
func TestAuthoringAgent_InvokesClaudeWithMCPConfig(t *testing.T) {
	minimalZDOTDIR(t)
	argvFile := filepath.Join(t.TempDir(), "argv")
	t.Setenv("AI_PLAYBOOK_CLAUDE_BIN", fakeClaude(t, argvFile))

	sess := openSession(capture.Request{ProjectRoot: t.TempDir()}, mux.Null())
	if sess == nil {
		t.Fatal("openSession returned nil")
	}
	defer sess.close()
	// A session built without os.Executable resolving would fall back to the plain
	// agent; in tests os.Executable resolves to the test binary, so selfExe is set.
	if sess.selfExe == "" {
		t.Skip("os.Executable unavailable; cannot assert MCP wiring")
	}

	agent := sess.authoringAgent()
	stream, err := agent("BASE SYS", "user message")
	if err != nil {
		t.Fatalf("authoring agent: %v", err)
	}
	// Drain + close so the fake claude runs to completion and writes its argv.
	_, _ = io.Copy(io.Discard, stream)
	stream.Close()

	argv, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("read captured argv: %v", err)
	}
	args := strings.Split(strings.TrimRight(string(argv), "\x00"), "\x00")

	if !contains(args, "--mcp-config") {
		t.Errorf("claude invocation missing --mcp-config\nargs: %v", args)
	}
	// The --mcp-config value is a JSON file mentioning our mcp subcommand wiring.
	cfgPath := after(args, "--mcp-config")
	if cfgPath != "" {
		if b, rerr := os.ReadFile(cfgPath); rerr == nil {
			if !strings.Contains(string(b), "\"mcp\"") || !strings.Contains(string(b), sess.socket) {
				t.Errorf("mcp config does not point at `mcp --socket %s`:\n%s", sess.socket, b)
			}
		}
	}
	// The system prompt (passed via --append-system-prompt) carries the tool
	// instruction.
	sysVal := after(args, "--append-system-prompt")
	if !strings.Contains(sysVal, "run") {
		t.Errorf("system prompt should mention the run tool; got %q", sysVal)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func after(ss []string, key string) string {
	for i, s := range ss {
		if s == key && i+1 < len(ss) {
			return ss[i+1]
		}
	}
	return ""
}

// ── writeMCPConfig coverage ────────────────────────────────────────────────

// TestWriteMCPConfig_NilSession asserts the nil-session fast path returns an empty
// path and a safe-to-call no-op remove func.
func TestWriteMCPConfig_NilSession(t *testing.T) {
	var s *session
	path, remove := s.writeMCPConfig()
	if path != "" {
		t.Errorf("nil session: want empty path, got %q", path)
	}
	remove() // must not panic
}

// TestWriteMCPConfig_NoSelfExe asserts that a session with no selfExe falls through
// the nil-selfExe guard and returns an empty path.
func TestWriteMCPConfig_NoSelfExe(t *testing.T) {
	s := &session{selfExe: ""}
	path, remove := s.writeMCPConfig()
	if path != "" {
		t.Errorf("no selfExe: want empty path, got %q", path)
	}
	remove()
}

// TestWriteMCPConfig_LiveSession asserts the success path: a live session with a
// resolved selfExe writes a valid config file and the remove func cleans it up.
func TestWriteMCPConfig_LiveSession(t *testing.T) {
	minimalZDOTDIR(t)
	sess := openSession(capture.Request{ProjectRoot: t.TempDir()}, mux.Null())
	if sess == nil {
		t.Fatal("openSession returned nil")
	}
	defer sess.close()
	if sess.selfExe == "" {
		t.Skip("os.Executable unavailable; cannot test writeMCPConfig success path")
	}

	path, remove := sess.writeMCPConfig()
	if path == "" {
		t.Fatal("writeMCPConfig: expected a non-empty config path")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("writeMCPConfig: config file not created at %s: %v", path, err)
	}
	remove()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("writeMCPConfig remove func did not clean up %s", path)
	}
}

// ── asker coverage ─────────────────────────────────────────────────────────

// TestAsker_ReturnsNilForNilSession asserts the asker is nil when the session is nil.
func TestAsker_ReturnsNilForNilSession(t *testing.T) {
	var s *session
	if s.asker("/") != nil {
		t.Error("nil session: asker must return nil")
	}
}

// TestAsker_ReturnsNilForNullMux asserts the asker is nil when the session's mux is
// null — with no multiplexer the terminal is owned by the inline TUI.
func TestAsker_ReturnsNilForNullMux(t *testing.T) {
	s := &session{selfExe: "/bin/foo", m: mux.Null()}
	if s.asker("/") != nil {
		t.Error("null mux: asker must return nil (terminal owned by inline TUI)")
	}
}

// TestAsker_ReturnsNilForNoSelfExe asserts asker returns nil when selfExe is empty
// (we can't spawn ourselves without knowing our own path).
func TestAsker_ReturnsNilForNoSelfExe(t *testing.T) {
	s := &session{selfExe: "", m: &launchMux{}}
	if s.asker("/") != nil {
		t.Error("no selfExe: asker must return nil")
	}
}

// TestStrippedAmendBase verifies the served-playbook amend base is FM-stripped:
// a body that begins with playbook front matter is reduced to the literate content
// (H1 + body); a body without front matter is returned unchanged.
func TestStrippedAmendBase(t *testing.T) {
	withFM := "---\nname: Playbook — X\ndescription: do x\n---\n\n# Playbook — X\n\nstep\n"
	got := strippedAmendBase(withFM)
	if strings.Contains(got, "description:") || strings.HasPrefix(got, "---") {
		t.Errorf("amend base must be FM-stripped, got %q", got)
	}
	if got != "# Playbook — X\n\nstep\n" {
		t.Errorf("amend base must be the literate body, got %q", got)
	}

	noFM := "# Playbook — Y\n\nstep\n"
	if strippedAmendBase(noFM) != noFM {
		t.Errorf("no-FM body must be unchanged, got %q", strippedAmendBase(noFM))
	}
}
