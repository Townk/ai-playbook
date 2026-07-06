package author

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/config"
)

// fakeArgvHarness writes a fake harness binary that records its full argv (one
// NUL-separated entry, so a multi-line system prompt survives) to argvFile and
// emits one canned stream-json result line — standing in for a real claude so the
// test asserts the OWNED events argv without launching claude.
func fakeArgvHarness(t *testing.T, argvFile string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake-harness shell script requires a POSIX shell")
	}
	p := filepath.Join(t.TempDir(), "claude")
	script := "#!/bin/sh\n" +
		"for a in \"$@\"; do printf '%s\\0' \"$a\"; done > " + shquoteTest(argvFile) + "\n" +
		"printf '%s\\n' '{\"type\":\"result\",\"result\":\"# playbook\"}'\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func shquoteTest(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func recordedArgv(t *testing.T, argvFile string) []string {
	t.Helper()
	b, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("read captured argv: %v", err)
	}
	return strings.Split(strings.TrimRight(string(b), "\x00"), "\x00")
}

func argAfter(args []string, key string) string {
	for i, a := range args {
		if a == key && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// TestToolInstruction_TeachesRememberClassification asserts the (markdown-authoring)
// tool instruction teaches the kind taxonomy (K2): lessons are classified by
// closeness to the topic at hand across the four kinds.
func TestToolInstruction_TeachesRememberClassification(t *testing.T) {
	for _, want := range []string{
		"`kind`",
		"topic at hand",
		"`system`",
		"`user`",
		"`environment`",
		"`topic`",
	} {
		if !strings.Contains(ToolInstruction, want) {
			t.Errorf("tool instruction missing remember-classification guidance %q", want)
		}
	}
}

// TestHarnessAgentWithMCP_WiresMCPConfigAndToolInstruction asserts the tools-wired
// production Agent routes through the events path: the OWNED argv carries
// --mcp-config <tempfile> (pointing at our mcp subcommand) and the system prompt
// (via --append-system-prompt) carries the run-tool instruction the events path
// folds in. The temp config is removed on stream close.
func TestHarnessAgentWithMCP_WiresMCPConfigAndToolInstruction(t *testing.T) {
	argvFile := filepath.Join(t.TempDir(), "argv")
	cfg := harnessCfg(fakeArgvHarness(t, argvFile))

	agent := HarnessAgentWithMCP(cfg, "/path/to/ai-playbook", "/tmp/tools.sock")
	r, err := agent("BASE SYSTEM PROMPT", "user msg")
	if err != nil {
		t.Fatal(err)
	}
	// Drain to EOF: the fake harness has now run (writing its argv) and been reaped,
	// but the temp mcp-config is still present — it is removed only on Close, which we
	// defer so the config content can be asserted first.
	_, _ = io.Copy(io.Discard, r)

	args := recordedArgv(t, argvFile)

	// The events path is stream-json (not the legacy --output-format text).
	joined := strings.Join(args, "\x00")
	if !strings.Contains(joined, "--output-format\x00stream-json") {
		t.Errorf("owned argv must be stream-json (events path): %v", args)
	}

	// The claude invocation includes --mcp-config <tempfile>.
	cfgPath := argAfter(args, "--mcp-config")
	if cfgPath == "" {
		t.Fatalf("owned argv missing --mcp-config: %v", args)
	}
	b, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read mcp config %q: %v", cfgPath, err)
	}
	var doc mcpConfig
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("mcp config not valid JSON: %v\n%s", err, b)
	}
	spec, ok := doc.McpServers["ai-playbook"]
	if !ok {
		t.Fatalf("mcp config missing the ai-playbook server: %s", b)
	}
	if spec.Command != "/path/to/ai-playbook" {
		t.Errorf("mcp server command = %q, want the self exe", spec.Command)
	}
	wantArgs := []string{"mcp", "--socket", "/tmp/tools.sock"}
	if strings.Join(spec.Args, " ") != strings.Join(wantArgs, " ") {
		t.Errorf("mcp server args = %v, want %v", spec.Args, wantArgs)
	}

	// The system prompt carries the base prompt PLUS the tool instruction (folded in
	// by the events path when MCPConfigPath is set), telling the agent to use `run`.
	sysVal := argAfter(args, "--append-system-prompt")
	if !strings.HasPrefix(sysVal, "BASE SYSTEM PROMPT") {
		t.Errorf("system prompt should start with the base prompt:\n%s", sysVal)
	}
	if !strings.Contains(sysVal, ToolInstruction) {
		t.Errorf("system prompt missing the tool instruction:\n%s", sysVal)
	}
	if !strings.Contains(sysVal, "`run`") {
		t.Errorf("tool instruction should name the run tool")
	}

	// Closing the stream removes the temp config (cleanup).
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Errorf("temp mcp config %q should be removed on stream close", cfgPath)
	}
}

// TestHarnessAgent_PlainHasNoMCPConfig is the negative control: the plain Agent
// (no tools backend) must NOT carry --mcp-config or the tool instruction.
func TestHarnessAgent_PlainHasNoMCPConfig(t *testing.T) {
	argvFile := filepath.Join(t.TempDir(), "argv")
	cfg := harnessCfg(fakeArgvHarness(t, argvFile))

	agent := HarnessAgent(AuthorOptions{Cfg: cfg})
	r, err := agent("SYS", "USER")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, r)
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	args := recordedArgv(t, argvFile)
	for _, a := range args {
		if a == "--mcp-config" {
			t.Errorf("plain agent must not pass --mcp-config: %v", args)
		}
	}
	if strings.Contains(argAfter(args, "--append-system-prompt"), ToolInstruction) {
		t.Errorf("plain agent must not append the tool instruction")
	}
}

// TestHarnessAgent_HonorsConfiguredHarness is the A5c fix: a text Agent built for a
// harness with no shipped adapter must NOT silently run the default — it surfaces
// the configured harness's not-yet-supported error. The legacy path ignored
// [agent].harness here. (The probe name is deliberately one that can never ship.)
func TestHarnessAgent_HonorsConfiguredHarness(t *testing.T) {
	cfg := config.Default()
	cfg.Agent.Harness = "no-such-harness"

	agent := HarnessAgent(AuthorOptions{Cfg: cfg})
	_, err := agent("SYS", "USER")
	if err == nil {
		t.Fatal("expected the configured harness to be honored (not-yet-supported error)")
	}
	if !strings.Contains(err.Error(), "no-such-harness") || !strings.Contains(err.Error(), "not yet supported") {
		t.Errorf("error = %q, want it to name the configured harness", err)
	}
}
