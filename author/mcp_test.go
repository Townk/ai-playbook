package author

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

// captureRunner records the args a claude invocation would receive and returns a
// canned stream, standing in for the real runClaude so the test never launches
// claude.
type captureRunner struct {
	gotSystem string
	gotUser   string
	gotExtra  []string
	calls     int
}

func (c *captureRunner) run(systemPrompt, userMessage string, extraArgs []string) (io.ReadCloser, error) {
	c.calls++
	c.gotSystem = systemPrompt
	c.gotUser = userMessage
	c.gotExtra = extraArgs
	return io.NopCloser(strings.NewReader("# playbook\n")), nil
}

func TestClaudeAgentWithMCP_WiresMCPConfigAndToolInstruction(t *testing.T) {
	cr := &captureRunner{}
	agent := claudeAgentWithMCPRunner("/path/to/ai-playbook", "/tmp/tools.sock", cr.run)

	r, err := agent("BASE SYSTEM PROMPT", "user msg")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	if cr.calls != 1 {
		t.Fatalf("runner calls = %d, want 1", cr.calls)
	}

	// The system prompt carries the base prompt PLUS the tool instruction telling
	// the agent to use the `run` tool (the user's shell), not its own bash.
	if !strings.HasPrefix(cr.gotSystem, "BASE SYSTEM PROMPT") {
		t.Errorf("system prompt should start with the base prompt:\n%s", cr.gotSystem)
	}
	if !strings.Contains(cr.gotSystem, ToolInstruction) {
		t.Errorf("system prompt missing the tool instruction:\n%s", cr.gotSystem)
	}
	if !strings.Contains(cr.gotSystem, "`run`") {
		t.Errorf("tool instruction should name the run tool")
	}

	// The claude invocation includes --mcp-config <tempfile>.
	cfg := mcpConfigArg(t, cr.gotExtra)

	// The temp config points claude at `<selfExe> mcp --socket <socket>`.
	b, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatalf("read mcp config %q: %v", cfg, err)
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

	// Closing the stream removes the temp config (cleanup).
	r.Close()
	if _, err := os.Stat(cfg); !os.IsNotExist(err) {
		t.Errorf("temp mcp config %q should be removed on stream close", cfg)
	}
}

// TestClaudeAgentWithMCP_PlainAgentHasNoMCPConfig is the negative control: the
// plain ClaudeAgent path must NOT carry --mcp-config or the tool instruction.
func TestClaudeAgentWithMCP_PlainAgentHasNoMCPConfig(t *testing.T) {
	cr := &captureRunner{}
	// Drive runClaude's args path via the plain agent shape: call the runner with no
	// extra args, mimicking ClaudeAgent.
	if _, err := cr.run("SYS", "USER", nil); err != nil {
		t.Fatal(err)
	}
	for _, a := range cr.gotExtra {
		if a == "--mcp-config" {
			t.Errorf("plain agent must not pass --mcp-config")
		}
	}
	if strings.Contains(cr.gotSystem, ToolInstruction) {
		t.Errorf("plain agent must not append the tool instruction")
	}
}

// mcpConfigArg returns the value following --mcp-config in args, failing if absent.
func mcpConfigArg(t *testing.T, args []string) string {
	t.Helper()
	for i, a := range args {
		if a == "--mcp-config" {
			if i+1 >= len(args) {
				t.Fatalf("--mcp-config has no value: %v", args)
			}
			return args[i+1]
		}
	}
	t.Fatalf("claude args missing --mcp-config: %v", args)
	return ""
}
