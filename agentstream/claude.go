package agentstream

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
)

// claudeAdapter parses Claude Code's `--output-format stream-json --verbose
// --include-partial-messages` output: one JSON object per line (NDJSON). It
// normalizes that wire format into the shared Event model per the classification
// rule documented in agentstream.go:
//
//   - assistant message content: text → TextDelta, thinking → Reasoning,
//     tool_use → ToolActivity (tool name + a short input rendering).
//   - stream_event content_block_delta: text_delta → TextDelta,
//     thinking_delta → Reasoning. content_block_start/stop → ignored.
//   - result → Final (the authoritative complete playbook text).
//   - system and any unknown envelope type/field → ignored gracefully.
//
// Robustness: blank and malformed lines are skipped, not fatal; very long lines
// are handled (bufio.Reader, not a fixed-size Scanner buffer).
type claudeAdapter struct{}

// toolSummaryMaxCols bounds the single-line tool-activity summary width.
const toolSummaryMaxCols = 60

// claudeLine is the envelope: every NDJSON line has a "type". The remaining
// fields are decoded only for the types we handle.
type claudeLine struct {
	Type string `json:"type"`

	// type == "assistant"
	Message *claudeMessage `json:"message,omitempty"`

	// type == "stream_event"
	Event *claudeStreamEvent `json:"event,omitempty"`

	// type == "result"
	Result string `json:"result,omitempty"`
}

type claudeMessage struct {
	Content []claudeContentBlock `json:"content,omitempty"`
}

type claudeContentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Thinking string          `json:"thinking,omitempty"`
	Name     string          `json:"name,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
}

type claudeStreamEvent struct {
	Type  string       `json:"type"`
	Delta *claudeDelta `json:"delta,omitempty"`
}

type claudeDelta struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Thinking string `json:"thinking,omitempty"`
}

func (claudeAdapter) Parse(r io.Reader, emit func(Event)) error {
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			parseClaudeLine(line, emit)
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// parseClaudeLine decodes one NDJSON line and emits its normalized events. A
// blank or malformed line is silently skipped (no emit, no error).
func parseClaudeLine(line string, emit func(Event)) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return
	}
	var l claudeLine
	if err := json.Unmarshal([]byte(trimmed), &l); err != nil {
		return // malformed → skip, not fatal
	}
	switch l.Type {
	case "assistant":
		if l.Message == nil {
			return
		}
		for _, b := range l.Message.Content {
			switch b.Type {
			case "text":
				emit(Event{Kind: TextDelta, Text: b.Text})
			case "thinking":
				emit(Event{Kind: Reasoning, Text: b.Thinking})
			case "tool_use":
				emit(Event{Kind: ToolActivity, Text: toolSummary(b.Name, b.Input)})
			}
		}
	case "stream_event":
		if l.Event == nil || l.Event.Delta == nil {
			return
		}
		switch l.Event.Delta.Type {
		case "text_delta":
			emit(Event{Kind: TextDelta, Text: l.Event.Delta.Text})
		case "thinking_delta":
			emit(Event{Kind: Reasoning, Text: l.Event.Delta.Thinking})
		}
	case "result":
		emit(Event{Kind: Final, Text: l.Result})
	}
	// "system" and any unknown type fall through → ignored.
}

// toolSummary renders a one-line, width-bounded activity label for a tool_use:
// the tool name plus a short rendering of its input. For a `run` tool the
// command field is surfaced; otherwise the compacted raw input is used.
func toolSummary(name string, input json.RawMessage) string {
	detail := toolInputDetail(name, input)
	summary := name
	if detail != "" {
		summary = name + ": " + detail
	}
	return truncateCols(singleLine(summary), toolSummaryMaxCols)
}

// toolInputDetail extracts the most useful single value from a tool's input.
// For a `run` tool that's the command; otherwise it falls back to the compact
// JSON of the whole input object.
func toolInputDetail(name string, input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(input, &fields); err != nil {
		return singleLine(string(input))
	}
	// Prefer a command-like key for run-style tools.
	for _, key := range []string{"command", "cmd"} {
		if raw, ok := fields[key]; ok {
			var s string
			if err := json.Unmarshal(raw, &s); err == nil {
				return s
			}
			return singleLine(string(raw))
		}
	}
	// Fallback: compact JSON of the whole input.
	return singleLine(string(input))
}

// singleLine collapses any run of whitespace (incl. newlines/tabs) to a single
// space and trims the ends, so a summary never spans lines.
func singleLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// truncateCols caps s at n runes, appending an ellipsis when it overflows.
func truncateCols(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n <= 1 {
		return string(runes[:n])
	}
	return string(runes[:n-1]) + "…"
}
