// Package mcpserver is the claude harness adapter: an MCP stdio server that
// claude launches (via --mcp-config) and whose tool calls dial the session's
// tools backend over its unix socket (package tools). It is a thin forwarder —
// the tool semantics live in package tools; this package only translates between
// MCP's tools/call and the backend's line-delimited JSON RPC.
//
// `ai-playbook mcp --socket <path>` runs Run: an MCP server over stdin/stdout
// exposing `run` / `remember` / `ask`. claude (or any MCP client) calls a tool;
// the handler builds a tools.Call, dials the socket, and returns the backend
// reply as MCP tool output. Keeping the forwarding in one typed handler (forward)
// makes it unit-testable against a fake socket server without an MCP handshake.
package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"ai-playbook/internal/tools"
)

// runInput is the `run` tool's arguments: a command to execute in the user's real
// shell, with an optional block id for value-passing (AAS_OUT_<id>).
type runInput struct {
	Cmd string `json:"cmd" jsonschema:"the command line to run in the user's real interactive shell (their cwd and environment)"`
	ID  string `json:"id,omitempty" jsonschema:"optional short id; exports AAS_OUT_<id>/AAS_ERR_<id>/AAS_EXIT_<id> so a later call can reference this command's output"`
}

// rememberInput is the `remember` tool's arguments: a distilled fact to persist
// for this project, with an optional project-root override.
type rememberInput struct {
	Fact        string `json:"fact" jsonschema:"a durable, distilled fact about this project to save for future requests; never secrets or env dumps"`
	ProjectRoot string `json:"projectRoot,omitempty" jsonschema:"optional project root override; defaults to the session's project root"`
}

// askInput is the `ask` tool's arguments: a question for the user.
type askInput struct {
	Prompt string `json:"prompt" jsonschema:"the question to ask the user"`
	Type   string `json:"type,omitempty" jsonschema:"input type: free|line|confirm|choose (default free)"`
}

// Run starts the MCP stdio server forwarding to the tools backend at socketPath
// and blocks until the client (claude) disconnects. It is the body of the
// `ai-playbook mcp --socket <path>` subcommand.
func Run(socketPath string) error {
	server := newServer(socketPath)
	return server.Run(context.Background(), &mcp.StdioTransport{})
}

// newServer builds the MCP server with the three forwarding tools bound. Split
// out from Run so a test can construct the server (and exercise the handlers)
// without taking over stdio.
func newServer(socketPath string) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "ai-playbook",
		Version: "1.0.0",
	}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "run",
		Description: "Run a command in the user's real interactive shell (their cwd and environment) and return its stdout, stderr, and exit code. Use this — NOT your own shell — so commands execute in the user's environment. Keep them read-only or idempotent.",
	}, runHandler(socketPath))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "remember",
		Description: "Save a durable, distilled fact about this project for future requests.",
	}, rememberHandler(socketPath))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "ask",
		Description: "Ask the user a question and return their answer. The only way to get input from the user.",
	}, askHandler(socketPath))

	return server
}

// forward dials the tools backend with the given Call and renders the reply as an
// MCP tool result (a text content block). A transport error (backend down) is
// surfaced as an MCP tool error (IsError) rather than a Go error so the agent
// sees a usable message instead of the connection breaking. Exposed (lowercase,
// package-internal) and used by all three handlers so the forwarding logic is
// tested once.
func forward(socketPath string, call tools.Call) (*mcp.CallToolResult, error) {
	res, err := tools.Dial(socketPath, call)
	if err != nil {
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{&mcp.TextContent{Text: "ai-playbook tools backend unreachable: " + err.Error()}},
		}, nil
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: renderResult(call.Tool, res)}},
	}, nil
}

// renderResult turns a backend Result into the text an LLM tool-caller reads.
// For `run` it surfaces exit/stdout/stderr; for `remember` a saved/failed line;
// for `ask` the answer or the unavailable sentinel.
func renderResult(tool string, res tools.Result) string {
	if res.Error != "" && tool != "ask" {
		return "error: " + res.Error
	}
	switch tool {
	case "run":
		out := fmt.Sprintf("exit: %d", res.Exit)
		if res.Out != "" {
			out += "\n--- stdout ---\n" + res.Out
		}
		if res.Err != "" {
			out += "\n--- stderr ---\n" + res.Err
		}
		return out
	case "remember":
		if res.OK {
			return "saved"
		}
		return "not saved"
	case "ask":
		if res.Unavailable {
			return res.Error // the "interactive ask not available in this context" sentinel
		}
		return res.Answer
	default:
		return fmt.Sprintf("%+v", res)
	}
}

func runHandler(socketPath string) mcp.ToolHandlerFor[runInput, any] {
	return func(_ context.Context, _ *mcp.CallToolRequest, in runInput) (*mcp.CallToolResult, any, error) {
		r, err := forward(socketPath, tools.Call{Tool: "run", ID: in.ID, Cmd: in.Cmd})
		return r, nil, err
	}
}

func rememberHandler(socketPath string) mcp.ToolHandlerFor[rememberInput, any] {
	return func(_ context.Context, _ *mcp.CallToolRequest, in rememberInput) (*mcp.CallToolResult, any, error) {
		r, err := forward(socketPath, tools.Call{Tool: "remember", Fact: in.Fact, ProjectRoot: in.ProjectRoot})
		return r, nil, err
	}
}

func askHandler(socketPath string) mcp.ToolHandlerFor[askInput, any] {
	return func(_ context.Context, _ *mcp.CallToolRequest, in askInput) (*mcp.CallToolResult, any, error) {
		r, err := forward(socketPath, tools.Call{Tool: "ask", Prompt: in.Prompt, Type: in.Type})
		return r, nil, err
	}
}
