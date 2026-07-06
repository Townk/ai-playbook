// mcpconfig.go — the shared `mcpServers` document (the MCP stdio config shape
// claude's --mcp-config and cursor's .cursor/mcp.json BOTH emit). The two FULL
// harnesses that speak MCP over a spawned stdio server point it at the SAME
// re-exec — `<SelfExe> mcp --socket <path>` (package mcpserver, forwarding tool
// calls to the session's tools backend) — and differ only in HOW they attach
// the document (claude: an --mcp-config flag; cursor: a HOME-redirected config
// root). Factoring the writer here keeps that one JSON shape in one place; the
// per-harness attach argv stays in each harness file (the ADR-0012 seam).
package author

import "encoding/json"

// mcpServerName is the server key under `mcpServers` for OUR tools backend. It
// is the identifier the harness's MCP client shows in `mcp list` and namespaces
// our run/ask/remember/submit_playbook tools under — so cursor's isolation guard
// keys on it to assert NO foreign server leaked into the redirected config root.
const mcpServerName = "ai-playbook"

// mcpConfig is the `mcpServers` document: a map of server name → an stdio server
// spec (command + args) the harness launches and speaks MCP to over its stdio.
type mcpConfig struct {
	McpServers map[string]mcpServerSpec `json:"mcpServers"`
}

type mcpServerSpec struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// mcpServersDocument marshals the single-server `mcpServers` document that
// points a harness at `<selfExe> mcp --socket <socketPath>`. Both the claude
// transport (--mcp-config JSON) and the cursor transport (.cursor/mcp.json under
// the redirect root) write exactly this shape.
func mcpServersDocument(selfExe, socketPath string) ([]byte, error) {
	return json.Marshal(mcpConfig{McpServers: map[string]mcpServerSpec{
		mcpServerName: {
			Command: selfExe,
			Args:    []string{"mcp", "--socket", socketPath},
		},
	}})
}
