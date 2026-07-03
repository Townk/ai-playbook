package agentstream

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// collect runs an adapter over r and returns the emitted events.
func collect(t *testing.T, a Adapter, r io.Reader) []Event {
	t.Helper()
	var got []Event
	if err := a.Parse(r, func(e Event) { got = append(got, e) }); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return got
}

// collectFragment parses a stream FRAGMENT — envelope lines without the terminal
// result the strict claude contract demands (A5b) — by appending a benign empty
// result envelope and stripping its trailing Final event, so the single-envelope
// mapping tests keep asserting exactly their envelope's events.
func collectFragment(t *testing.T, a Adapter, in string) []Event {
	t.Helper()
	got := collect(t, a, strings.NewReader(in+`{"type":"result","result":""}`+"\n"))
	if len(got) == 0 || got[len(got)-1] != (Event{Kind: Final, Text: ""}) {
		t.Fatalf("fragment parse: expected the appended terminal result event last, got %+v", got)
	}
	return got[:len(got)-1]
}

func assertEvents(t *testing.T, got, want []Event) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("event count = %d, want %d\n got: %+v\nwant: %+v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event[%d] = {%s %q}, want {%s %q}",
				i, got[i].Kind, got[i].Text, want[i].Kind, want[i].Text)
		}
	}
}

func TestEventKindString(t *testing.T) {
	cases := map[EventKind]string{
		Reasoning:     "Reasoning",
		ToolActivity:  "ToolActivity",
		TextDelta:     "TextDelta",
		Final:         "Final",
		EventKind(99): "EventKind(?)",
	}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", int(k), got, want)
		}
	}
}

func TestRegistry(t *testing.T) {
	if _, ok := Get("claude"); !ok {
		t.Error("claude adapter not registered")
	}
	if _, ok := Get("text"); !ok {
		t.Error("text adapter not registered")
	}
	if _, ok := Get("nope"); ok {
		t.Error("unexpected adapter for unknown name")
	}
}

// TestTextAdapter_Passthrough: every read chunk → TextDelta, then one Final with
// the full accumulated text at EOF.
func TestTextAdapter_Passthrough(t *testing.T) {
	a, _ := Get("text")
	got := collect(t, a, strings.NewReader("hello world"))
	// A small reader yields one chunk, so: one TextDelta + one Final.
	want := []Event{
		{Kind: TextDelta, Text: "hello world"},
		{Kind: Final, Text: "hello world"},
	}
	assertEvents(t, got, want)
}

// TestTextAdapter_EmptyEOF: an empty stream still produces a single Final with
// the empty accumulated text (preserving "always a Final" for the consumer).
func TestTextAdapter_EmptyEOF(t *testing.T) {
	a, _ := Get("text")
	got := collect(t, a, strings.NewReader(""))
	want := []Event{{Kind: Final, Text: ""}}
	assertEvents(t, got, want)
}

// claudeSession is a representative stream-json session: a system line (ignored),
// partial text/thinking deltas, a full assistant message that REPEATS the text +
// thinking of the just-finished block and carries a tool_use for a `run` command,
// a second assistant message repeating the next text block, and a final result.
//
// The duplication is exactly what Claude Code emits under
// --include-partial-messages: the streaming deltas are the source of truth, and
// the assembled `assistant` message repeats the complete text/thinking. The
// adapter must dedup by taking the deltas and pulling ONLY tool_use from the
// assistant message — never re-emitting the assistant message's text/thinking.
const claudeSession = `{"type":"system","subtype":"init","session_id":"abc"}
{"type":"stream_event","event":{"type":"content_block_start","content_block":{"type":"text"}}}
{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"Here'"}}}
{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"s the plan"}}}
{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"let me check the logs"}}}
{"type":"stream_event","event":{"type":"content_block_stop","index":0}}
{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"I should run the build"},{"type":"text","text":"Here's the plan"},{"type":"tool_use","name":"run","input":{"command":"make build"}}]}}
{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"Running the build now."}}}
{"type":"assistant","message":{"content":[{"type":"text","text":"Running the build now."}]}}
{"type":"result","result":"# Fix\n\nrun make build\n","is_error":false}
`

func TestClaudeAdapter_FullSession(t *testing.T) {
	a, _ := Get("claude")
	got := collect(t, a, strings.NewReader(claudeSession))
	// Text comes ONLY from the deltas; the assistant messages' text/thinking are
	// NOT re-emitted (no doubling). tool_use IS taken from the assistant message.
	want := []Event{
		{Kind: TextDelta, Text: "Here'"},
		{Kind: TextDelta, Text: "s the plan"},
		{Kind: Reasoning, Text: "let me check the logs"},
		{Kind: ToolActivity, Text: "❯ make build"},
		{Kind: TextDelta, Text: "Running the build now."},
		{Kind: Final, Text: "# Fix\n\nrun make build\n"},
	}
	assertEvents(t, got, want)
}

// TestClaudeAdapter_DedupReplayedCapture replays the real captured shape from the
// live `claude -p --output-format stream-json --include-partial-messages` run:
// the SAME complete text streams as text_delta chunks AND is repeated in a full
// top-level assistant[text] message, then again in result. The adapter must emit
// the text ONCE (only from the deltas); the assistant text block must NOT be
// re-emitted, and Final must equal the result text.
func TestClaudeAdapter_DedupReplayedCapture(t *testing.T) {
	a, _ := Get("claude")
	const full = "At 60 mph for 2.5 hours: 60 × 2.5 = 150, so the train travels **150 miles**."
	in := strings.Join([]string{
		`{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"At 60 mph for 2"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":".5 hours: 60 × 2.5 = 150, so the train travels **150 miles**."}}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"` + full + `"}]}}`,
		`{"type":"result","result":"` + full + `"}`,
	}, "\n") + "\n"
	got := collect(t, a, strings.NewReader(in))
	want := []Event{
		{Kind: TextDelta, Text: "At 60 mph for 2"},
		{Kind: TextDelta, Text: ".5 hours: 60 × 2.5 = 150, so the train travels **150 miles**."},
		{Kind: Final, Text: full},
	}
	assertEvents(t, got, want)

	// Belt-and-suspenders: the playbook reconstructed from the TextDeltas equals the
	// complete text exactly once — no doubling from the assistant message.
	var sb strings.Builder
	for _, e := range got {
		if e.Kind == TextDelta {
			sb.WriteString(e.Text)
		}
	}
	if sb.String() != full {
		t.Fatalf("reconstructed playbook = %q, want %q (deltas only, no double-emit)", sb.String(), full)
	}
}

// TestClaudeAdapter_AssistantToolUseOnly: a full assistant message carrying a
// thinking block, a text block, AND a tool_use must yield EXACTLY one
// ToolActivity — the text and thinking blocks are dropped (they duplicate the
// deltas).
func TestClaudeAdapter_AssistantToolUseOnly(t *testing.T) {
	a, _ := Get("claude")
	in := `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"deciding"},{"type":"text","text":"Running it."},{"type":"tool_use","name":"run","input":{"command":"make build"}}]}}` + "\n"
	got := collectFragment(t, a, in)
	want := []Event{{Kind: ToolActivity, Text: "❯ make build"}}
	assertEvents(t, got, want)
}

// TestClaudeAdapter_RedactedThinkingNoEmptyReasoning: the redacted-thinking shape
// — a signature_delta (no text) on the stream and an assistant thinking block
// with empty text — must produce NO Reasoning events at all. An empty Reasoning
// would otherwise clobber the live tool-activity line.
func TestClaudeAdapter_RedactedThinkingNoEmptyReasoning(t *testing.T) {
	a, _ := Get("claude")
	in := strings.Join([]string{
		`{"type":"stream_event","event":{"type":"content_block_start","content_block":{"type":"thinking","thinking":"","signature":""}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"abc123"}}}`,
		`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"","signature":"abc123"}]}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"thinking_delta","thinking":"   "}}}`,
	}, "\n") + "\n"
	got := collectFragment(t, a, in)
	if len(got) != 0 {
		t.Fatalf("redacted/empty thinking should emit nothing, got %+v", got)
	}
}

// TestClaudeAdapter_ThinkingDeltaNonEmpty: a non-empty thinking_delta (the pi /
// non-redacted path) yields exactly one Reasoning event with the text.
func TestClaudeAdapter_ThinkingDeltaNonEmpty(t *testing.T) {
	a, _ := Get("claude")
	in := `{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"weighing the options"}}}` + "\n"
	got := collectFragment(t, a, in)
	want := []Event{{Kind: Reasoning, Text: "weighing the options"}}
	assertEvents(t, got, want)
}

// TestClaudeAdapter_BlankAndUnknownLinesTolerated: blank lines and valid-JSON
// envelopes of unknown type (forward compat: system/init and whatever claude
// adds later) are skipped; valid lines around them still parse and a clean,
// result-terminated stream returns nil.
func TestClaudeAdapter_BlankAndUnknownLinesTolerated(t *testing.T) {
	a, _ := Get("claude")
	in := strings.Join([]string{
		``,
		`{"type":"system","subtype":"init"}`,
		`{"type":"future_envelope","payload":{"x":1}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"ok"}}}`,
		``,
		`{"type":"result","result":"final"}`,
	}, "\n") + "\n"
	got := collect(t, a, strings.NewReader(in))
	want := []Event{
		{Kind: TextDelta, Text: "ok"},
		{Kind: Final, Text: "final"},
	}
	assertEvents(t, got, want)
}

// TestClaudeAdapter_MalformedLineIsFatal is A5b-strict: a non-blank line that is
// not valid JSON violates the stream-json contract (NDJSON, one object per line)
// — most commonly a stream truncated MID-LINE — and must surface as a Parse
// error instead of being silently skipped (which made a corrupted stream on a
// clean exit 0 indistinguishable from success).
func TestClaudeAdapter_MalformedLineIsFatal(t *testing.T) {
	a, _ := Get("claude")
	in := strings.Join([]string{
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"ok"}}}`,
		`{"type":"assistant",`, // truncated mid-line
	}, "\n") + "\n"
	var got []Event
	err := a.Parse(strings.NewReader(in), func(e Event) { got = append(got, e) })
	if err == nil {
		t.Fatal("Parse = nil on a malformed stream-json line, want a stream-contract error (A5b)")
	}
	if !strings.Contains(err.Error(), "stream-json") {
		t.Errorf("error = %q, want it to name the stream-json contract violation", err)
	}
	// Events before the violation were already emitted (streaming), that's fine.
	assertEvents(t, got, []Event{{Kind: TextDelta, Text: "ok"}})
}

// TestClaudeAdapter_MalformedErrorSnippetCapped: the offending line is quoted in
// the error but capped — a 200KB corrupted line must not dump itself whole into
// the error chain.
func TestClaudeAdapter_MalformedErrorSnippetCapped(t *testing.T) {
	a, _ := Get("claude")
	in := "x" + strings.Repeat("y", 200*1024) + "\n"
	err := a.Parse(strings.NewReader(in), func(Event) {})
	if err == nil {
		t.Fatal("Parse = nil on a giant malformed line, want an error")
	}
	if len(err.Error()) > 512 {
		t.Errorf("error length = %d, want the quoted line capped (<= 512 total)", len(err.Error()))
	}
}

// TestClaudeAdapter_TruncatedStreamNoResultIsFatal is the other A5b-strict half:
// claude --print stream-json ALWAYS terminates a successful run with a `result`
// envelope, so a clean EOF without one means the stream was truncated at a line
// boundary (or the configured bin isn't actually claude) — an error, not a
// silent empty/partial playbook.
func TestClaudeAdapter_TruncatedStreamNoResultIsFatal(t *testing.T) {
	a, _ := Get("claude")
	cases := map[string]string{
		"empty stream":         "",
		"deltas but no result": `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"partial"}}}` + "\n",
	}
	for name, in := range cases {
		if err := a.Parse(strings.NewReader(in), func(Event) {}); err == nil {
			t.Errorf("%s: Parse = nil, want a truncated-stream error (no result envelope)", name)
		} else if !strings.Contains(err.Error(), "result") {
			t.Errorf("%s: error = %q, want it to name the missing result envelope", name, err)
		}
	}
}

// TestClaudeAdapter_BigLine: a single very long line (larger than the default
// bufio.Scanner 64K buffer) must parse, exercising the bufio.Reader path.
func TestClaudeAdapter_BigLine(t *testing.T) {
	a, _ := Get("claude")
	big := strings.Repeat("x", 200*1024) // 200 KB of text
	b, err := jsonResultLine(big)
	if err != nil {
		t.Fatal(err)
	}
	got := collect(t, a, strings.NewReader(b+"\n"))
	want := []Event{{Kind: Final, Text: big}}
	assertEvents(t, got, want)
}

// TestClaudeAdapter_ToolSummaryTruncated: a long run command is rendered on one
// line and truncated to the summary width.
func TestClaudeAdapter_ToolSummaryTruncated(t *testing.T) {
	a, _ := Get("claude")
	in := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"run","input":{"command":"echo this is a very long command that should certainly be truncated well past sixty columns"}}]}}` + "\n"
	got := collectFragment(t, a, in)
	if len(got) != 1 || got[0].Kind != ToolActivity {
		t.Fatalf("want one ToolActivity, got %+v", got)
	}
	if n := len([]rune(got[0].Text)); n > toolSummaryMaxCols {
		t.Fatalf("summary %d cols > max %d: %q", n, toolSummaryMaxCols, got[0].Text)
	}
	if !strings.HasPrefix(got[0].Text, "❯ echo") {
		t.Fatalf("summary should start with the run glyph + command: %q", got[0].Text)
	}
	if !strings.HasSuffix(got[0].Text, "…") {
		t.Fatalf("truncated summary should end with ellipsis: %q", got[0].Text)
	}
}

// TestClaudeAdapter_ToolSummaryMCPPrefixStripped: the agent reaches our tools via
// an MCP server, so names arrive as mcp__<server>__<tool>. The prefix must be
// stripped and run mapped to the ❯ glyph + bare command.
func TestClaudeAdapter_ToolSummaryMCPPrefixStripped(t *testing.T) {
	a, _ := Get("claude")
	in := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"mcp__ai-playbook__run","input":{"command":"cd /x/y"}}]}}` + "\n"
	got := collectFragment(t, a, in)
	if len(got) != 1 || got[0].Kind != ToolActivity {
		t.Fatalf("want one ToolActivity, got %+v", got)
	}
	if got[0].Text != "❯ cd /x/y" {
		t.Fatalf("mcp-prefixed run summary = %q, want %q", got[0].Text, "❯ cd /x/y")
	}
}

// TestClaudeAdapter_ToolGlyphs: ask → ❓ prompt, remember → 📝 fact (and 📝 noted
// with no fact), for both mcp-prefixed and bare names; an unknown tool keeps its
// bare name with compact input and strips the mcp prefix.
func TestClaudeAdapter_ToolGlyphs(t *testing.T) {
	a, _ := Get("claude")
	cases := []struct {
		name string
		line string
		want string
	}{
		{"ask mcp", `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"mcp__ai-playbook__ask","input":{"prompt":"which env?"}}]}}`, "❓ which env?"},
		{"ask bare", `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"ask","input":{"prompt":"go?"}}]}}`, "❓ go?"},
		{"remember fact", `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"mcp__ai-playbook__remember","input":{"fact":"uses pnpm"}}]}}`, "📝 uses pnpm"},
		{"remember nofact", `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"remember","input":{}}]}}`, "📝 noted"},
		{"unknown strips prefix", `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"mcp__ai-playbook__read","input":{"path":"/tmp/x"}}]}}`, `read: {"path":"/tmp/x"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := collectFragment(t, a, c.line+"\n")
			if len(got) != 1 || got[0].Kind != ToolActivity {
				t.Fatalf("want one ToolActivity, got %+v", got)
			}
			if got[0].Text != c.want {
				t.Fatalf("summary = %q, want %q", got[0].Text, c.want)
			}
		})
	}
}

// TestClaudeAdapter_ToolSummaryNonRun: a non-run tool with no command key falls
// back to compact JSON of the whole input, still single-line.
func TestClaudeAdapter_ToolSummaryNonRun(t *testing.T) {
	a, _ := Get("claude")
	in := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"read","input":{"path":"/tmp/x"}}]}}` + "\n"
	got := collectFragment(t, a, in)
	if len(got) != 1 || got[0].Kind != ToolActivity {
		t.Fatalf("want one ToolActivity, got %+v", got)
	}
	if !strings.HasPrefix(got[0].Text, "read: ") {
		t.Fatalf("non-run summary should start with tool name: %q", got[0].Text)
	}
	if strings.ContainsAny(got[0].Text, "\n\t") {
		t.Fatalf("summary must be single line: %q", got[0].Text)
	}
}

// jsonResultLine builds a valid `result` NDJSON line carrying text, using the
// json encoder so escaping is correct for the big-line test.
func jsonResultLine(text string) (string, error) {
	b, err := json.Marshal(claudeLine{Type: "result", Result: text})
	return string(b), err
}
