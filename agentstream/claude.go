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
//
// Reasoning caveat: this adapter maps thinking / thinking_delta → Reasoning, but
// Claude Code only EMITS thinking blocks when extended thinking is enabled. The
// owned invocation enables it via MAX_THINKING_TOKENS (see author.events:
// claudeThinkingTokens, driven by config [agent].thinking). Even then, in
// `--print --output-format stream-json` Claude Code OMITS the thinking block
// text (the readable summary is not surfaced), so Reasoning events fire — driving
// the "model is reasoning" activity — but their Text is typically empty. pi
// (--mode json, thinkingText) surfaces the reasoning text natively.
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

// toolSummary renders a one-line, width-bounded activity label for a tool_use.
// The agent reaches our tools via an MCP server, so the wire name arrives as
// `mcp__<server>__<tool>` (e.g. mcp__ai-playbook__run); already-bare names are
// also tolerated. The bare tool is what matters, and for our own tools the tool
// NAME is mostly noise — the payload is the signal — so each is mapped to a
// glyph + the useful detail:
//
//   - run      → ❯ <command>   (the command, from the command/cmd input field)
//   - ask      → ❓ <prompt>    (truncated)
//   - remember → 📝 <fact>      (truncated; or "📝 noted" when no fact field)
//   - other    → <tool>: <compact-input>  (mcp prefix stripped)
//
// The result is always single-line and capped at toolSummaryMaxCols.
func toolSummary(name string, input json.RawMessage) string {
	bare := stripMCPPrefix(name)
	var summary string
	switch bare {
	case "run":
		cmd := inputField(input, "command", "cmd")
		summary = "❯ " + cmd
	case "ask":
		summary = "❓ " + inputField(input, "prompt", "question", "q")
	case "remember":
		fact := inputField(input, "fact", "text", "note")
		if fact == "" {
			summary = "📝 noted"
		} else {
			summary = "📝 " + fact
		}
	default:
		detail := toolInputDetail(input)
		summary = bare
		if detail != "" {
			summary = bare + ": " + detail
		}
	}
	return truncateCols(singleLine(summary), toolSummaryMaxCols)
}

// stripMCPPrefix removes a leading `mcp__<server>__` so the bare tool name
// remains. Names without the prefix pass through unchanged. The server segment
// may itself contain no `__`, so we drop exactly the first two `__`-delimited
// segments when the name starts with `mcp__`.
func stripMCPPrefix(name string) string {
	const p = "mcp__"
	if !strings.HasPrefix(name, p) {
		return name
	}
	rest := name[len(p):] // <server>__<tool>
	if i := strings.Index(rest, "__"); i >= 0 {
		return rest[i+len("__"):]
	}
	return rest
}

// inputField returns the first present key's string value from the tool input,
// trying keys in order. Empty string when the input is absent/malformed or none
// of the keys are present.
func inputField(input json.RawMessage, keys ...string) string {
	if len(input) == 0 {
		return ""
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(input, &fields); err != nil {
		return ""
	}
	for _, key := range keys {
		if raw, ok := fields[key]; ok {
			var s string
			if err := json.Unmarshal(raw, &s); err == nil {
				return s
			}
			return singleLine(string(raw))
		}
	}
	return ""
}

// toolInputDetail extracts the most useful single value from an unknown tool's
// input: a command-like key if present, else the compact JSON of the whole
// input object.
func toolInputDetail(input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	if v := inputField(input, "command", "cmd"); v != "" {
		return v
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
