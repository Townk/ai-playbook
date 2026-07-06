package agentstream

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readPiFixture loads a raw captured `pi -p --mode json` stream from testdata.
// The pi-*.ndjson files are UNEDITED live captures from pi 0.80.3 (the
// characterization probes) — the always-run baseline the multi-harness spec
// requires for every adapter.
func readPiFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(b)
}

// parsePi runs the registered pi adapter over input and returns the emitted
// events plus Parse's error.
func parsePi(t *testing.T, input string) ([]Event, error) {
	t.Helper()
	a, ok := Get("pi")
	if !ok {
		t.Fatal("pi adapter not registered")
	}
	var events []Event
	err := a.Parse(strings.NewReader(input), func(e Event) { events = append(events, e) })
	return events, err
}

// TestPiAdapter_AppendHappyFixture replays the captured append-mode run
// (thinking enabled): the reasoning text arrives as Reasoning events (pi
// surfaces REAL thinking text, unlike claude --print), whitespace-only
// thinking deltas are dropped, the answer streams as TextDelta, and agent_end
// yields the authoritative Final.
func TestPiAdapter_AppendHappyFixture(t *testing.T) {
	events, err := parsePi(t, readPiFixture(t, "pi-append-happy.ndjson"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// The capture streamed thinking deltas "ok" and "\n" (the latter dropped as
	// whitespace-only), one text delta "ok", and an agent_end whose last
	// assistant message text is "ok".
	want := []Event{
		{Kind: Reasoning, Text: "ok"},
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

// TestPiAdapter_BareHappyFixture replays the captured bare-mode run (thinking
// off): no Reasoning events, the answer as TextDelta, agent_end as Final.
func TestPiAdapter_BareHappyFixture(t *testing.T) {
	events, err := parsePi(t, readPiFixture(t, "pi-bare-happy.ndjson"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var final string
	sawFinal := false
	var text strings.Builder
	for _, e := range events {
		switch e.Kind {
		case Reasoning:
			t.Errorf("bare (thinking off) run emitted Reasoning %q", e.Text)
		case TextDelta:
			text.WriteString(e.Text)
		case Final:
			sawFinal = true
			final = e.Text
		}
	}
	if !sawFinal {
		t.Fatal("no Final event from agent_end")
	}
	if final != "ok" {
		t.Errorf("Final = %q, want ok", final)
	}
	if text.String() != final {
		t.Errorf("accumulated TextDelta %q != Final %q (single-turn streams must agree)", text.String(), final)
	}
	// Final must be the LAST event (agent_end is the terminal envelope).
	if events[len(events)-1].Kind != Final {
		t.Errorf("last event = %+v, want the Final", events[len(events)-1])
	}
}

// TestPiAdapter_ToolUseFixture replays the captured tool-use run (the embedded
// extension's `run` tool against the fake socket backend): exactly one
// ToolActivity with the run glyph + command, and a Final from the LAST
// assistant message (the first assistant message — the tool-calling turn — is
// interim commentary and must NOT become the Final).
func TestPiAdapter_ToolUseFixture(t *testing.T) {
	events, err := parsePi(t, readPiFixture(t, "pi-tool-use.ndjson"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var tools []string
	var final string
	for _, e := range events {
		switch e.Kind {
		case ToolActivity:
			tools = append(tools, e.Text)
		case Final:
			final = e.Text
		}
	}
	if len(tools) != 1 || tools[0] != "❯ echo hi" {
		t.Errorf("ToolActivity = %v, want exactly [\"❯ echo hi\"]", tools)
	}
	// The fake backend replied PONG-7391; the model's final answer quotes it.
	if !strings.Contains(final, "PONG-7391") {
		t.Errorf("Final %q does not carry the tool result from the last assistant message", final)
	}
}

// TestPiAdapter_TruncatedStreamIsFatal enforces the terminal-envelope rule
// (A5b): a stream that ends cleanly WITHOUT agent_end — here the bare capture
// minus its last line, and the empty stream — is an error, never silent
// success.
func TestPiAdapter_TruncatedStreamIsFatal(t *testing.T) {
	full := readPiFixture(t, "pi-bare-happy.ndjson")
	lines := strings.Split(strings.TrimRight(full, "\n"), "\n")
	if !strings.Contains(lines[len(lines)-1], `"agent_end"`) {
		t.Fatal("fixture sanity: last line should be the agent_end envelope")
	}
	truncated := strings.Join(lines[:len(lines)-1], "\n") + "\n"

	for name, input := range map[string]string{
		"missing agent_end": truncated,
		"empty stream":      "",
	} {
		events, err := parsePi(t, input)
		if err == nil {
			t.Errorf("%s: Parse = nil error, want a truncated-stream error", name)
			continue
		}
		if !strings.Contains(err.Error(), "agent_end") {
			t.Errorf("%s: err = %v, want it to name the missing agent_end envelope", name, err)
		}
		for _, e := range events {
			if e.Kind == Final {
				t.Errorf("%s: a truncated stream must not emit Final", name)
			}
		}
	}
}

// TestPiAdapter_GarbageLineIsFatal enforces the NDJSON rule (A5b): a non-blank
// line that is not valid JSON — a stream truncated mid-line, or a bin that is
// not speaking --mode json — is an error, with the offending snippet capped.
func TestPiAdapter_GarbageLineIsFatal(t *testing.T) {
	full := readPiFixture(t, "pi-bare-happy.ndjson")
	garbage := full[:len(full)/2] // cut mid-line: the tail line is not valid JSON

	if _, err := parsePi(t, garbage); err == nil {
		t.Error("mid-line truncation: Parse = nil error, want an invalid-line error")
	}

	long := strings.Repeat("x", 5000) + "\n" + full
	_, err := parsePi(t, long)
	if err == nil {
		t.Fatal("garbage line: Parse = nil error, want an invalid-line error")
	}
	if len(err.Error()) > 400 {
		t.Errorf("error snippet not capped: %d chars", len(err.Error()))
	}
}

// TestPiAdapter_BlankAndUnknownLinesTolerated pins forward compat: blank lines
// and valid-JSON envelopes of unknown/unhandled type (queue_update, turn_end,
// a future kind) parse cleanly and emit nothing extra.
func TestPiAdapter_BlankAndUnknownLinesTolerated(t *testing.T) {
	input := "\n" +
		`{"type":"queue_update","steering":[],"followUp":[]}` + "\n" +
		`{"type":"some_future_envelope","payload":{"x":1}}` + "\n" +
		"\n" +
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"hi"}}` + "\n" +
		`{"type":"agent_end","messages":[{"role":"assistant","content":[{"type":"text","text":"hi"}]}]}` + "\n"
	events, err := parsePi(t, input)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []Event{{Kind: TextDelta, Text: "hi"}, {Kind: Final, Text: "hi"}}
	if len(events) != len(want) || events[0] != want[0] || events[1] != want[1] {
		t.Errorf("events = %+v, want %+v", events, want)
	}
}

// TestPiAdapter_EmptyReasoningDropped pins the empty-activity rule: a
// whitespace-only thinking delta (pi's paragraph separators) never becomes a
// Reasoning event that would blank the live activity line.
func TestPiAdapter_EmptyReasoningDropped(t *testing.T) {
	input := `{"type":"message_update","assistantMessageEvent":{"type":"thinking_delta","contentIndex":0,"delta":"\n\n"}}` + "\n" +
		`{"type":"message_update","assistantMessageEvent":{"type":"thinking_delta","contentIndex":0,"delta":"real thought"}}` + "\n" +
		`{"type":"agent_end","messages":[]}` + "\n"
	events, err := parsePi(t, input)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []Event{{Kind: Reasoning, Text: "real thought"}, {Kind: Final, Text: ""}}
	if len(events) != len(want) || events[0] != want[0] || events[1] != want[1] {
		t.Errorf("events = %+v, want %+v", events, want)
	}
}

// TestPiAdapter_FinalTakesLastAssistantMessage pins piFinalText's transcript
// rule: the Final is the LAST assistant message's concatenated text blocks —
// not an earlier turn's commentary, not the user's message, and not thinking.
func TestPiAdapter_FinalTakesLastAssistantMessage(t *testing.T) {
	input := `{"type":"agent_end","messages":[` +
		`{"role":"user","content":[{"type":"text","text":"the request"}]},` +
		`{"role":"assistant","content":[{"type":"text","text":"interim turn"}]},` +
		`{"role":"toolResult","content":[{"type":"text","text":"tool output"}]},` +
		`{"role":"assistant","content":[{"type":"thinking","thinking":"hidden"},{"type":"text","text":"part one, "},{"type":"text","text":"part two"}]}` +
		`]}` + "\n"
	events, err := parsePi(t, input)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(events) != 1 || events[0].Kind != Final {
		t.Fatalf("events = %+v, want exactly one Final", events)
	}
	if events[0].Text != "part one, part two" {
		t.Errorf("Final = %q, want the last assistant message's concatenated text", events[0].Text)
	}
}
