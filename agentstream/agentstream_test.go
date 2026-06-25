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
// partial text/thinking deltas, a full assistant message with a thinking block, a
// tool_use for a `run` command, interim text, and a final result.
const claudeSession = `{"type":"system","subtype":"init","session_id":"abc"}
{"type":"stream_event","event":{"type":"content_block_start","content_block":{"type":"text"}}}
{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"Here'"}}}
{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"s the plan"}}}
{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"let me check the logs"}}}
{"type":"stream_event","event":{"type":"content_block_stop","index":0}}
{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"I should run the build"},{"type":"text","text":"Running the build now."},{"type":"tool_use","name":"run","input":{"command":"make build"}}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"Done."}]}}
{"type":"result","result":"# Fix\n\nrun make build\n","is_error":false}
`

func TestClaudeAdapter_FullSession(t *testing.T) {
	a, _ := Get("claude")
	got := collect(t, a, strings.NewReader(claudeSession))
	want := []Event{
		{Kind: TextDelta, Text: "Here'"},
		{Kind: TextDelta, Text: "s the plan"},
		{Kind: Reasoning, Text: "let me check the logs"},
		{Kind: Reasoning, Text: "I should run the build"},
		{Kind: TextDelta, Text: "Running the build now."},
		{Kind: ToolActivity, Text: "run: make build"},
		{Kind: TextDelta, Text: "Done."},
		{Kind: Final, Text: "# Fix\n\nrun make build\n"},
	}
	assertEvents(t, got, want)
}

// TestClaudeAdapter_MalformedAndBlankLinesSkipped: garbage and blank lines are
// skipped, valid lines around them still parse.
func TestClaudeAdapter_MalformedAndBlankLinesSkipped(t *testing.T) {
	a, _ := Get("claude")
	in := strings.Join([]string{
		``,
		`not json at all`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"ok"}]}}`,
		`{"type":"assistant",`, // truncated/malformed JSON
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
	got := collect(t, a, strings.NewReader(in))
	if len(got) != 1 || got[0].Kind != ToolActivity {
		t.Fatalf("want one ToolActivity, got %+v", got)
	}
	if n := len([]rune(got[0].Text)); n > toolSummaryMaxCols {
		t.Fatalf("summary %d cols > max %d: %q", n, toolSummaryMaxCols, got[0].Text)
	}
	if !strings.HasPrefix(got[0].Text, "run: echo") {
		t.Fatalf("summary should start with the run command: %q", got[0].Text)
	}
	if !strings.HasSuffix(got[0].Text, "…") {
		t.Fatalf("truncated summary should end with ellipsis: %q", got[0].Text)
	}
}

// TestClaudeAdapter_ToolSummaryNonRun: a non-run tool with no command key falls
// back to compact JSON of the whole input, still single-line.
func TestClaudeAdapter_ToolSummaryNonRun(t *testing.T) {
	a, _ := Get("claude")
	in := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"read","input":{"path":"/tmp/x"}}]}}` + "\n"
	got := collect(t, a, strings.NewReader(in))
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
