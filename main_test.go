package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ai-playbook/capture"
	"ai-playbook/tools"
)

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
