package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ai-playbook/author"
	"ai-playbook/cache"
	"ai-playbook/capture"
	"ai-playbook/tools"
)

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
	sess := openSession(capture.Request{ProjectRoot: t.TempDir()})
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
// fake claude (AI_ASSIST_CLAUDE_BIN) to capture the argv.
func TestAuthoringAgent_InvokesClaudeWithMCPConfig(t *testing.T) {
	minimalZDOTDIR(t)
	argvFile := filepath.Join(t.TempDir(), "argv")
	t.Setenv("AI_ASSIST_CLAUDE_BIN", fakeClaude(t, argvFile))

	sess := openSession(capture.Request{ProjectRoot: t.TempDir()})
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
	io.Copy(io.Discard, stream)
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
