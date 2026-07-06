package agentstream

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readCursorFixture loads a cursor-agent stream fixture from testdata. Like the
// pi fixtures, the cursor-*.ndjson files are RAW LIVE CAPTURES — recorded from
// cursor-agent 2026.07.01-777f564 (`-p --output-format stream-json
// --stream-partial-output --mode ask --trust`). The RequireHarness-gated live
// tests in internal/author re-verify against the real CLI wherever it exists.
func readCursorFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(b)
}

// parseCursor runs the registered cursor adapter over input and returns the
// emitted events plus Parse's error.
func parseCursor(t *testing.T, input string) ([]Event, error) {
	t.Helper()
	a, ok := Get("cursor")
	if !ok {
		t.Fatal("cursor adapter not registered")
	}
	var events []Event
	err := a.Parse(strings.NewReader(input), func(e Event) { events = append(events, e) })
	return events, err
}

// TestCursorAdapter_HappyFixture replays the real trivial capture: the
// --stream-partial-output delta (timestamp_ms, no model_call_id) becomes
// TextDelta, the end-of-turn duplicate flush (neither field) is skipped, and
// the result envelope yields the authoritative Final. This trivial turn does
// no thinking, so no Reasoning (thinking coverage is
// TestCursorAdapter_ThinkingBecomesReasoning).
func TestCursorAdapter_HappyFixture(t *testing.T) {
	events, err := parseCursor(t, readCursorFixture(t, "cursor-happy.ndjson"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []Event{
		{Kind: TextDelta, Text: "ok"},
		{Kind: Final, Text: "ok"},
	}
	if len(events) != len(want) {
		t.Fatalf("events = %+v, want %+v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Errorf("events[%d] = %+v, want %+v", i, events[i], want[i])
		}
	}
}

// TestCursorAdapter_ToolUseFixture replays the real MULTI-SEGMENT capture (two
// assistant segments split by one read tool call, and a `result` field that is
// the live NO-SEPARATOR concatenation of both): one ToolActivity for the
// tool_call started (the readToolCall wrapper → bare "read", picked out from
// beside its sibling metadata keys; completed is ignored), the buffered
// pre-tool flush (timestamp_ms + model_call_id) and the end-of-turn flush
// (neither field) are NOT re-emitted as text, and the Final is the LAST
// segment — the final answer — NEVER the envelope's glued concatenation (the
// Final policy on cursorAdapter).
func TestCursorAdapter_ToolUseFixture(t *testing.T) {
	events, err := parseCursor(t, readCursorFixture(t, "cursor-tool-use.ndjson"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var tools []string
	var text strings.Builder
	var final string
	for _, e := range events {
		switch e.Kind {
		case ToolActivity:
			tools = append(tools, e.Text)
		case TextDelta:
			text.WriteString(e.Text)
		case Final:
			final = e.Text
		}
	}
	// Exactly one tool activity, the read wrapper resolved to the bare name
	// despite the sibling toolCallId/startedAtMs/hookAdditionalContexts keys.
	if len(tools) != 1 || !strings.HasPrefix(tools[0], "read: ") || !strings.Contains(tools[0], "notes.txt") {
		t.Errorf("ToolActivity = %v, want a single read: …notes.txt", tools)
	}
	// The duplicates (buffered pre-tool flush + end-of-turn flush) must not
	// double the streamed text: the accumulated deltas equal exactly the glued
	// all-segment concatenation the CLI reports in the `result` field.
	glued := "STEP ONE done\n\nI'll read the file now." + "STEP TWO done XYZZY-4217"
	if got := text.String(); got != glued {
		t.Errorf("accumulated TextDelta = %q, want %q (duplicate flushes must be skipped)", got, glued)
	}
	// The Final policy: the LAST segment (the answer), not the envelope's glued
	// concatenation — that concat would corrupt the stored body downstream.
	if want := "STEP TWO done XYZZY-4217"; final != want {
		t.Errorf("Final = %q, want the last assistant segment %q", final, want)
	}
	if final == glued {
		t.Error("Final must never be the result envelope's glued all-segment concatenation")
	}
	if events[len(events)-1].Kind != Final {
		t.Errorf("last event = %+v, want the Final (result is the terminal envelope)", events[len(events)-1])
	}
}

// TestCursorAdapter_ThinkingBecomesReasoning pins the reasoning mapping against
// real thinking lines (captured verbatim from cursor-agent): a `thinking`
// subtype "delta" carries reasoning text at the top-level `text` field and
// becomes a Reasoning event; the text-less "completed" event drops via the
// empty-activity rule. (Contradicts the doc claim that thinking is suppressed
// in print mode.)
func TestCursorAdapter_ThinkingBecomesReasoning(t *testing.T) {
	input := `{"type":"thinking","subtype":"delta","text":"I","session_id":"s","timestamp_ms":1}` + "\n" +
		`{"type":"thinking","subtype":"delta","text":"'m going to read the file.","session_id":"s","timestamp_ms":2}` + "\n" +
		`{"type":"thinking","subtype":"completed","session_id":"s","timestamp_ms":3}` + "\n" +
		`{"type":"result","subtype":"success","is_error":false,"result":"done","session_id":"s"}` + "\n"
	events, err := parseCursor(t, input)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var reasoning []string
	for _, e := range events {
		if e.Kind == Reasoning {
			reasoning = append(reasoning, e.Text)
		}
	}
	want := []string{"I", "'m going to read the file."}
	if len(reasoning) != len(want) || reasoning[0] != want[0] || reasoning[1] != want[1] {
		t.Errorf("Reasoning = %v, want %v (the text-less completed event must drop)", reasoning, want)
	}
}

// TestCursorAdapter_FinalPolicy pins finalText's fallback ladder (the result
// note on cursorAdapter): (a) the current segment when the turn ends in text;
// (b) the last completed non-empty segment when the turn ends on a tool call
// with no trailing text; (c) the result envelope's own text ONLY when no delta
// ever streamed (a non-partial stream, where every assistant event is a
// skipped no-field flush).
func TestCursorAdapter_FinalPolicy(t *testing.T) {
	delta := func(text string) string {
		return `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"` + text + `"}]},"session_id":"s","timestamp_ms":1}` + "\n"
	}
	noField := func(text string) string {
		return `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"` + text + `"}]},"session_id":"s"}` + "\n"
	}
	tool := `{"type":"tool_call","subtype":"started","call_id":"c","tool_call":{"readToolCall":{"args":{"path":"f"}}},"session_id":"s"}` + "\n"
	result := func(text string) string {
		return `{"type":"result","subtype":"success","is_error":false,"result":"` + text + `","session_id":"s"}` + "\n"
	}

	cases := map[string]struct {
		input string
		want  string
	}{
		"turn ends in text: the current segment wins": {
			input: delta("narration") + tool + delta("answer") + result("narrationanswer"),
			want:  "answer",
		},
		"turn ends on a tool call: the last non-empty segment wins": {
			input: delta("the answer") + tool + result("the answer"),
			want:  "the answer",
		},
		"no deltas streamed (non-partial stream): the result text is the only fallback": {
			input: noField("whole message") + result("whole message"),
			want:  "whole message",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			events, err := parseCursor(t, tc.input)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if len(events) == 0 || events[len(events)-1].Kind != Final {
				t.Fatalf("events = %+v, want a terminal Final", events)
			}
			if got := events[len(events)-1].Text; got != tc.want {
				t.Errorf("Final = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestCursorAdapter_PartialOutputDedupRule pins the documented three-way
// assistant dedup (cursor.com/docs/cli/reference/output-format): emit iff
// timestamp_ms is present AND model_call_id is absent; every other combination
// is a documented duplicate.
func TestCursorAdapter_PartialOutputDedupRule(t *testing.T) {
	const terminal = `{"type":"result","subtype":"success","is_error":false,"result":"done","session_id":"s"}` + "\n"
	cases := map[string]struct {
		line string
		emit bool
	}{
		"timestamp only (streaming delta)": {
			line: `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"new"}]},"session_id":"s","timestamp_ms":1}`,
			emit: true,
		},
		"timestamp + model_call_id (buffered pre-tool flush)": {
			line: `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"dup"}]},"session_id":"s","timestamp_ms":1,"model_call_id":"m1"}`,
			emit: false,
		},
		"neither (end-of-turn flush)": {
			line: `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"dup"}]},"session_id":"s"}`,
			emit: false,
		},
		"model_call_id only": {
			line: `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"dup"}]},"session_id":"s","model_call_id":"m1"}`,
			emit: false,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			events, err := parseCursor(t, tc.line+"\n"+terminal)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			var deltas []string
			for _, e := range events {
				if e.Kind == TextDelta {
					deltas = append(deltas, e.Text)
				}
			}
			if tc.emit && (len(deltas) != 1 || deltas[0] != "new") {
				t.Errorf("TextDelta = %v, want exactly [new]", deltas)
			}
			if !tc.emit && len(deltas) != 0 {
				t.Errorf("TextDelta = %v, want none (documented duplicate)", deltas)
			}
		})
	}
}

// TestCursorAdapter_TruncatedStreamIsFatal enforces the terminal-envelope rule
// (A5b): a stream that ends cleanly WITHOUT a result envelope — the happy
// fixture minus its last line, and the empty stream — is an error, never
// silent success.
func TestCursorAdapter_TruncatedStreamIsFatal(t *testing.T) {
	full := readCursorFixture(t, "cursor-happy.ndjson")
	lines := strings.Split(strings.TrimRight(full, "\n"), "\n")
	if !strings.Contains(lines[len(lines)-1], `"result"`) {
		t.Fatal("fixture sanity: last line should be the result envelope")
	}
	truncated := strings.Join(lines[:len(lines)-1], "\n") + "\n"

	for name, input := range map[string]string{
		"missing result envelope": truncated,
		"empty stream":            "",
	} {
		events, err := parseCursor(t, input)
		if err == nil {
			t.Errorf("%s: Parse = nil error, want a truncated-stream error", name)
			continue
		}
		if !strings.Contains(err.Error(), "result") {
			t.Errorf("%s: err = %v, want it to name the missing result envelope", name, err)
		}
		for _, e := range events {
			if e.Kind == Final {
				t.Errorf("%s: a truncated stream must not emit Final", name)
			}
		}
	}
}

// TestCursorAdapter_GarbageLineIsFatal enforces the NDJSON rule (A5b): a
// non-blank line that is not valid JSON — a stream truncated mid-line, or a
// bin that is not speaking stream-json — is an error, with the offending
// snippet capped.
func TestCursorAdapter_GarbageLineIsFatal(t *testing.T) {
	full := readCursorFixture(t, "cursor-happy.ndjson")
	garbage := full[:len(full)/2] // cut mid-line: the tail line is not valid JSON

	if _, err := parseCursor(t, garbage); err == nil {
		t.Error("mid-line truncation: Parse = nil error, want an invalid-line error")
	}

	long := strings.Repeat("x", 5000) + "\n" + full
	_, err := parseCursor(t, long)
	if err == nil {
		t.Fatal("garbage line: Parse = nil error, want an invalid-line error")
	}
	if len(err.Error()) > 400 {
		t.Errorf("error snippet not capped: %d chars", len(err.Error()))
	}
}

// TestCursorAdapter_BlankAndUnknownLinesTolerated pins forward compat: blank
// lines and valid-JSON envelopes of unknown/unhandled type (system init, user,
// a future kind) parse cleanly and emit nothing extra — the docs promise
// backward-compatible additions and tell consumers to ignore unknown fields.
func TestCursorAdapter_BlankAndUnknownLinesTolerated(t *testing.T) {
	input := "\n" +
		`{"type":"system","subtype":"init","apiKeySource":"env","cwd":"/x","session_id":"s","model":"Auto","permissionMode":"default"}` + "\n" +
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"hi"}]},"session_id":"s"}` + "\n" +
		`{"type":"some_future_envelope","payload":{"x":1}}` + "\n" +
		"\n" +
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]},"session_id":"s","timestamp_ms":1}` + "\n" +
		`{"type":"result","subtype":"success","is_error":false,"result":"hi","session_id":"s"}` + "\n"
	events, err := parseCursor(t, input)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []Event{{Kind: TextDelta, Text: "hi"}, {Kind: Final, Text: "hi"}}
	if len(events) != len(want) || events[0] != want[0] || events[1] != want[1] {
		t.Errorf("events = %+v, want %+v", events, want)
	}
}

// TestCursorAdapter_ToolNameDerivation pins cursorToolName's wrapper-key rule:
// the ToolCall suffix is trimmed to the bare name (shellToolCall → shell, with
// a command arg the shared toolSummary surfaces), the wrapper is picked out
// from beside the real sibling metadata keys (toolCallId/startedAtMs/
// hookAdditionalContexts), a suffix-less key passes through bare, and an empty
// tool_call object yields an empty summary that the empty-activity rule then
// drops entirely.
func TestCursorAdapter_ToolNameDerivation(t *testing.T) {
	const terminal = `{"type":"result","subtype":"success","is_error":false,"result":"","session_id":"s"}` + "\n"
	cases := map[string]struct {
		line string
		want []string // expected ToolActivity texts, nil for none
	}{
		"suffix trimmed + command surfaced": {
			line: `{"type":"tool_call","subtype":"started","call_id":"c1","tool_call":{"shellToolCall":{"args":{"command":"echo hi"}}},"session_id":"s"}`,
			want: []string{"shell: echo hi"},
		},
		"wrapper picked out from real sibling metadata keys": {
			line: `{"type":"tool_call","subtype":"started","call_id":"c5","tool_call":{"readToolCall":{"args":{"path":"f"}},"hookAdditionalContexts":[],"toolCallId":"c5","startedAtMs":"1"},"session_id":"s"}`,
			want: []string{`read: {"path":"f"}`},
		},
		"suffix-less key passes through": {
			line: `{"type":"tool_call","subtype":"started","call_id":"c2","tool_call":{"grep":{"args":{"pattern":"x"}}},"session_id":"s"}`,
			want: []string{`grep: {"pattern":"x"}`},
		},
		"completed is ignored": {
			line: `{"type":"tool_call","subtype":"completed","call_id":"c3","tool_call":{"readToolCall":{"args":{"path":"f"}}},"session_id":"s"}`,
			want: nil,
		},
		"empty tool_call dropped": {
			line: `{"type":"tool_call","subtype":"started","call_id":"c4","tool_call":{},"session_id":"s"}`,
			want: nil,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			events, err := parseCursor(t, tc.line+"\n"+terminal)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			var got []string
			for _, e := range events {
				if e.Kind == ToolActivity {
					got = append(got, e.Text)
				}
			}
			if len(got) != len(tc.want) {
				t.Fatalf("ToolActivity = %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("ToolActivity[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}
