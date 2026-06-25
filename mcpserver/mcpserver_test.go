package mcpserver

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"ai-playbook/tools"
)

// fakeBackend is a minimal stand-in for the tools backend: it accepts one
// connection, decodes the request, records it, and writes a canned reply. It lets
// us test the MCP adapter's forwarding without a real driver/session.
type fakeBackend struct {
	socket string
	reply  tools.Result

	mu   sync.Mutex
	got  tools.Call
	gotN int
}

func startFakeBackend(t *testing.T, reply tools.Result) *fakeBackend {
	t.Helper()
	// Short socket path (darwin sun_path ~104 bytes; a nested t.TempDir overflows).
	dir, err := os.MkdirTemp("", "fsock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "f.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	fb := &fakeBackend{socket: socket, reply: reply}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go fb.serve(conn)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return fb
}

func (fb *fakeBackend) serve(conn net.Conn) {
	defer conn.Close()
	sc := bufio.NewScanner(conn)
	if !sc.Scan() {
		return
	}
	var call tools.Call
	_ = json.Unmarshal(sc.Bytes(), &call)
	fb.mu.Lock()
	fb.got = call
	fb.gotN++
	fb.mu.Unlock()
	_ = json.NewEncoder(conn).Encode(fb.reply)
}

func (fb *fakeBackend) lastCall() tools.Call {
	fb.mu.Lock()
	defer fb.mu.Unlock()
	return fb.got
}

func TestForward_Run(t *testing.T) {
	fb := startFakeBackend(t, tools.Result{Out: "build ok", Exit: 0})

	res, err := forward(fb.socket, tools.Call{Tool: "run", Cmd: "gg build", ID: "fix"})
	if err != nil {
		t.Fatalf("forward: %v", err)
	}
	if res.IsError {
		t.Errorf("unexpected IsError for a healthy forward: %+v", res)
	}

	// The backend received the forwarded call verbatim.
	got := fb.lastCall()
	if got.Tool != "run" || got.Cmd != "gg build" || got.ID != "fix" {
		t.Errorf("backend got %+v, want run/gg build/fix", got)
	}

	// The reply is rendered as MCP text output carrying exit + stdout.
	text := contentText(t, res)
	if !strings.Contains(text, "exit: 0") || !strings.Contains(text, "build ok") {
		t.Errorf("run result text = %q, want exit + stdout", text)
	}
}

func TestForward_Remember(t *testing.T) {
	fb := startFakeBackend(t, tools.Result{OK: true})

	res, err := forward(fb.socket, tools.Call{Tool: "remember", Fact: "uses bazel"})
	if err != nil {
		t.Fatalf("forward: %v", err)
	}
	if got := fb.lastCall(); got.Tool != "remember" || got.Fact != "uses bazel" {
		t.Errorf("backend got %+v, want remember/uses bazel", got)
	}
	if text := contentText(t, res); text != "saved" {
		t.Errorf("remember result text = %q, want %q", text, "saved")
	}
}

func TestForward_AskSentinel(t *testing.T) {
	fb := startFakeBackend(t, tools.Result{Unavailable: true, Error: "interactive ask not available in this context"})

	res, err := forward(fb.socket, tools.Call{Tool: "ask", Prompt: "which env?"})
	if err != nil {
		t.Fatalf("forward: %v", err)
	}
	if text := contentText(t, res); !strings.Contains(text, "not available") {
		t.Errorf("ask result text = %q, want the unavailable sentinel", text)
	}
}

func TestForward_BackendUnreachable(t *testing.T) {
	// No backend listening on this socket → forward surfaces an MCP tool error
	// (IsError) rather than a Go error, so the agent sees a usable message.
	res, err := forward(filepath.Join(t.TempDir(), "nope.sock"), tools.Call{Tool: "run", Cmd: "x"})
	if err != nil {
		t.Fatalf("forward should not return a Go error on a dead backend: %v", err)
	}
	if !res.IsError {
		t.Errorf("forward to a dead backend should set IsError")
	}
}

// contentText extracts the first text content block from a tool result.
func contentText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatalf("result has no content: %+v", res)
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("first content is not text: %T", res.Content[0])
	}
	return tc.Text
}
