package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Townk/ai-playbook/internal/driver"
	"github.com/Townk/ai-playbook/internal/tools"
)

// TestE2E_MCPForwardsToBackend is a live end-to-end of the claude path: a real
// tools backend over a unix socket, the real `ai-playbook mcp` subcommand as an
// MCP stdio server (launched as a subprocess), and the SDK MCP client calling the
// `run` tool — proving a full MCP handshake forwards the call into the user's
// shell via the backend. Skipped if the binary can't be built.
func TestE2E_MCPForwardsToBackend(t *testing.T) {
	zdot := t.TempDir()
	if err := os.WriteFile(filepath.Join(zdot, ".zshrc"), []byte("# minimal\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Pin zsh: zsh-specific fixture; the default now honors $SHELL (bash on CI).
	d, err := driver.Open(driver.Options{Shell: "zsh", Env: append(os.Environ(), "ZDOTDIR="+zdot)})
	if err != nil {
		t.Fatalf("driver: %v", err)
	}
	defer d.Close()

	// Short socket path (darwin sun_path ~104 bytes; a nested t.TempDir overflows).
	dir, err := os.MkdirTemp("", "e2esock")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "t.sock")
	srv, err := tools.Serve(socket, tools.Deps{Driver: d})
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	defer srv.Close()

	// The CLI dispatch (this package) is built from cmd/ai-playbook — "." here
	// would be internal/cli itself, a non-main package, so the binary under
	// test is built by import path rather than by directory.
	bin := filepath.Join(t.TempDir(), "ai-playbook")
	if out, berr := exec.Command("go", "build", "-o", bin, "github.com/Townk/ai-playbook/cmd/ai-playbook").CombinedOutput(); berr != nil {
		t.Skipf("build: %v\n%s", berr, out)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "check", Version: "0"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	sess, err := client.Connect(ctx, &mcp.CommandTransport{Command: exec.Command(bin, "mcp", "--socket", socket)}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer sess.Close()

	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "run",
		Arguments: map[string]any{"cmd": "print -r -- mcp-e2e-ok"},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	found := false
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok && strings.Contains(tc.Text, "mcp-e2e-ok") {
			found = true
		}
	}
	if !found {
		t.Fatalf("run result missing expected output: %+v", res.Content)
	}
}
