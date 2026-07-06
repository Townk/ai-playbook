package agentstream

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// init installs the claude adapter under its registry name — registration lives
// HERE (the claude adapter's own file) so the shared registry never names a
// concrete harness (the ADR-0012 leak discipline).
func init() {
	registry["claude"] = claudeAdapter{}
}

// claudeAdapter parses Claude Code's `--output-format stream-json --verbose
// --include-partial-messages` output: one JSON object per line (NDJSON). It
// normalizes that wire format into the shared Event model.
//
// Dedup rule (the reason for the deltas-vs-assistant split below). With
// --include-partial-messages, Claude Code emits each piece of output TWICE: once
// as incremental `stream_event` content_block_delta chunks (the streaming source
// of truth), and again as a full top-level `assistant` message that REPEATS the
// COMPLETE text/thinking of the just-finished block. Emitting from both doubled
// the playbook in the doc and — worse — that late full-text assistant message
// reached the ui as a textEvent, flipping m.thinking off mid-work and killing the
// spinner + activity line. So this adapter takes the deltas as the streaming
// truth and pulls ONLY tool_use from the assistant message:
//
//   - stream_event content_block_delta: text_delta → TextDelta,
//     thinking_delta → Reasoning. content_block_start/stop → ignored.
//   - assistant message content: tool_use → ToolActivity (tool name + a short
//     input rendering). The assistant message's text and thinking blocks are
//     NOT re-emitted — they DUPLICATE the deltas. tool_use is taken from the
//     assistant message because tool_use input is NOT reconstructable from the
//     partial input_json_delta stream, so the assembled message is the right
//     source for tools.
//   - result → Final (the authoritative complete playbook text).
//   - system and any unknown envelope type/field → ignored gracefully.
//
// Assumption: --include-partial-messages is set. Our owned invocation always
// sets it, so the deltas are guaranteed to carry the text/thinking; the simple
// rule above (never re-emit assistant text/thinking) is therefore safe. A
// no-partial fallback would have no text_delta stream and would need to fall
// back to the assistant message's text — out of scope here, since the owned
// invocation guarantees partials.
//
// Strictness (A5b): the wire format is NDJSON — one JSON object per line — and
// a successful `claude --print` run ALWAYS terminates with a `result` envelope.
// Parse therefore returns an error for a non-blank line that is not valid JSON
// (a stream truncated MID-LINE, or a bin that isn't speaking stream-json at
// all) and for a clean EOF with no `result` seen (a stream truncated at a line
// boundary, including a completely empty stream). Before this, both were
// silently skipped, so a corrupted/truncated stream on a clean exit (0) was
// indistinguishable from success. Blank lines and valid-JSON envelopes of
// UNKNOWN type stay tolerated (forward compat with new claude envelope types).
// Very long lines are handled (bufio.Reader, not a fixed-size Scanner buffer).
//
// Empty-activity drop: Reasoning and ToolActivity events whose Text is
// empty/whitespace are never emitted. Claude --print REDACTS thinking — the
// thinking content_block emits only signature_delta (no thinking text), so the
// thinking_delta path yields empty Reasoning that would otherwise clobber the
// live activity line. Dropping it means that with claude the activity line shows
// TOOL activity only; the model's reasoning text is not exposed by claude
// --print. pi (--mode json, thinkingText) surfaces real reasoning later.
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

// claudeContentBlock decodes only the fields the adapter uses from an assistant
// message block. The assistant message is the source for tool_use ONLY (Name +
// Input); its text/thinking are intentionally not decoded — they duplicate the
// stream_event deltas (see the dedup rule on claudeAdapter).
type claudeContentBlock struct {
	Type  string          `json:"type"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
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

// malformedSnippetMax caps how much of an offending line is quoted in a
// stream-contract error (a corrupted line can be hundreds of KB).
const malformedSnippetMax = 120

func (claudeAdapter) Parse(r io.Reader, emit func(Event)) error {
	br := bufio.NewReader(r)
	sawResult := false
	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			gotResult, perr := parseClaudeLine(line, emit)
			if perr != nil {
				return perr
			}
			sawResult = sawResult || gotResult
		}
		if err != nil {
			if err == io.EOF {
				if !sawResult {
					// A successful claude --print run always terminates with a result
					// envelope; a clean EOF without one is a TRUNCATED stream (A5b).
					return errors.New("claude stream-json ended without a result envelope (truncated stream)")
				}
				return nil
			}
			return err
		}
	}
}

// parseClaudeLine decodes one NDJSON line and emits its normalized events,
// reporting whether the line was the terminal `result` envelope. A blank line is
// skipped; a non-blank line that is not valid JSON is a stream-contract
// violation and returns an error (A5b — see the strictness note on
// claudeAdapter). Valid JSON of an unknown envelope type stays tolerated.
func parseClaudeLine(line string, emit func(Event)) (sawResult bool, err error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false, nil
	}
	var l claudeLine
	if uerr := json.Unmarshal([]byte(trimmed), &l); uerr != nil {
		return false, fmt.Errorf("invalid stream-json line: %q", truncateCols(trimmed, malformedSnippetMax))
	}
	switch l.Type {
	case "assistant":
		if l.Message == nil {
			return false, nil
		}
		// ONLY tool_use is taken from the assembled assistant message; its text and
		// thinking blocks DUPLICATE the stream_event deltas and are dropped (see the
		// dedup rule on claudeAdapter). Empty/whitespace summaries are not emitted.
		for _, b := range l.Message.Content {
			if b.Type != "tool_use" {
				continue
			}
			emitActivity(emit, ToolActivity, toolSummary(b.Name, b.Input))
		}
	case "stream_event":
		if l.Event == nil || l.Event.Delta == nil {
			return false, nil
		}
		switch l.Event.Delta.Type {
		case "text_delta":
			// TextDelta is the playbook; emit it verbatim (empty deltas are harmless
			// here — they neither double the doc nor clobber the activity line).
			emit(Event{Kind: TextDelta, Text: l.Event.Delta.Text})
		case "thinking_delta":
			// claude --print redacts thinking (signature_delta only), so this is
			// typically empty; emitActivity drops empty Reasoning so it can't clobber
			// the tool-activity line.
			emitActivity(emit, Reasoning, l.Event.Delta.Thinking)
		}
	case "result":
		emit(Event{Kind: Final, Text: l.Result})
		return true, nil
	}
	// "system" and any unknown type fall through → ignored (forward compat).
	return false, nil
}

// emitActivity emits a Reasoning/ToolActivity event only when text has
// non-whitespace content. Empty/whitespace activity (notably claude's redacted
// thinking, which yields empty Reasoning) is dropped so it never overwrites the
// live activity line with nothing.
func emitActivity(emit func(Event), kind EventKind, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	emit(Event{Kind: kind, Text: text})
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
