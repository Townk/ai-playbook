package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Townk/ai-playbook/internal/draft"
	"github.com/Townk/ai-playbook/internal/tools"
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

func TestForward_SubmitPlaybook(t *testing.T) {
	fb := startFakeBackend(t, tools.Result{OK: true})

	pb := draft.Playbook{Title: "T", Sections: []draft.Section{{Heading: "S",
		Content: []draft.ContentItem{{Kind: "code", Lang: "bash", Code: "x", ID: "fix"}}}}}
	raw, _ := json.Marshal(pb)
	res, err := forward(fb.socket, tools.Call{Tool: "submit_playbook", Playbook: raw})
	if err != nil {
		t.Fatalf("forward: %v", err)
	}
	if res.IsError {
		t.Errorf("healthy submit should not be IsError: %+v", res)
	}
	got := fb.lastCall()
	if got.Tool != "submit_playbook" || len(got.Playbook) == 0 {
		t.Errorf("backend got %+v, want submit_playbook with payload", got)
	}
	if txt := contentText(t, res); !strings.Contains(txt, "saved") {
		t.Errorf("ok submit result = %q, want 'saved'", txt)
	}
}

// TestSubmitPlaybook_SchemaShape asserts that the submit_playbook tool's input
// schema is generated from draft.Playbook (i.e. it mentions the playbook
// fields). The brief uses srv.ListTools which does not exist on *mcp.Server;
// ListTools lives on mcp.ClientSession. We connect a client to the server via
// mcp.NewInMemoryTransports and call cs.ListTools instead.
func TestSubmitPlaybook_SchemaShape(t *testing.T) {
	ctx := context.Background()
	srv := newServer("/tmp/unused.sock")
	st, ct := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer cs.Close()

	result, err := cs.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	var sp *mcp.Tool
	for _, tl := range result.Tools {
		if tl.Name == "submit_playbook" {
			sp = tl
			break
		}
	}
	if sp == nil {
		t.Fatal("submit_playbook tool not registered")
	}
	schema, _ := json.Marshal(sp.InputSchema)
	for _, want := range []string{"title", "sections", "verify", "project_bound"} {
		if !strings.Contains(string(schema), want) {
			t.Errorf("submit_playbook input schema missing %q:\n%s", want, schema)
		}
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
