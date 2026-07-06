package agentstream

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// init installs the pi adapter under its registry name — registration lives HERE
// (the pi adapter's own file) so the shared registry never names a concrete
// harness (the ADR-0012 leak discipline).
func init() {
	registry["pi"] = piAdapter{}
}

// piAdapter parses pi's `--mode json` output: one JSON object per line (NDJSON),
// live-characterized against pi 0.80.3 (fixtures under testdata/pi-*.ndjson are
// raw captures; the envelope kinds match pi's documented AgentSessionEvent
// union, docs/json.md in the pi package). It normalizes that wire format into
// the shared Event model:
//
//   - message_update + assistantMessageEvent text_delta → TextDelta (the
//     `delta` field). pi streams each assistant block exactly once as deltas,
//     so unlike claude there is no partial/assistant dedup problem.
//   - message_update + assistantMessageEvent thinking_delta → Reasoning. pi
//     surfaces the REAL reasoning text natively (the delta carries the
//     readable thinking, not a redacted signature), so with pi the live
//     activity line shows the model's actual reasoning.
//   - tool_execution_start → ToolActivity (tool name + args, rendered by the
//     shared toolSummary — pi tool names arrive bare, e.g. `run`, which
//     stripMCPPrefix passes through). tool_execution_update/end and the
//     toolcall_* assistant events are ignored: start is the single
//     complete-args moment, so emitting only it keeps one activity line per
//     call.
//   - agent_end → Final. agent_end is pi's TERMINAL envelope: it carries the
//     full message transcript, and the authoritative final text is the LAST
//     assistant message's concatenated text blocks (earlier assistant
//     messages are interim turn commentary between tool calls — the same
//     final-turn semantics as claude's `result`).
//   - session / agent_start / turn_* / message_start / message_end /
//     queue_update / compaction_* and any unknown envelope type → ignored
//     gracefully (forward compat).
//
// Strictness (A5b, the same discipline as the claude adapter): the wire format
// is NDJSON and a successful `pi -p --mode json` run ALWAYS terminates with an
// agent_end envelope (live-verified: happy, bare, and tool-use runs all end
// with it; a failed startup — bad model — emits NOTHING on stdout and exits
// non-zero). Parse therefore returns an error for a non-blank line that is not
// valid JSON and for a clean EOF with no agent_end seen (a truncated stream,
// including a completely empty one). Blank lines and valid-JSON envelopes of
// unknown type stay tolerated. Very long lines are handled (bufio.Reader).
//
// Empty-activity drop: Reasoning/ToolActivity whose text is empty/whitespace
// are never emitted (shared emitActivity) — pi's thinking stream includes
// pure-newline deltas between reasoning paragraphs that would otherwise blank
// the live activity line.
type piAdapter struct{}

// piLine is the envelope: every NDJSON line has a "type". The remaining fields
// are decoded only for the types the adapter handles.
type piLine struct {
	Type string `json:"type"`

	// type == "message_update"
	AssistantMessageEvent *piAssistantEvent `json:"assistantMessageEvent,omitempty"`

	// type == "tool_execution_start"
	ToolName string          `json:"toolName,omitempty"`
	Args     json.RawMessage `json:"args,omitempty"`

	// type == "agent_end"
	Messages []piMessage `json:"messages,omitempty"`
}

// piAssistantEvent is the streaming sub-event inside a message_update. Only the
// delta kinds matter; the block start/end kinds repeat text the deltas already
// carried.
type piAssistantEvent struct {
	Type  string `json:"type"`
	Delta string `json:"delta,omitempty"`
}

// piMessage is one transcript message inside agent_end. Only the last
// assistant message's text blocks are read (the Final text).
type piMessage struct {
	Role    string           `json:"role"`
	Content []piContentBlock `json:"content,omitempty"`
}

type piContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

func (piAdapter) Parse(r io.Reader, emit func(Event)) error {
	br := bufio.NewReader(r)
	sawEnd := false
	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			gotEnd, perr := parsePiLine(line, emit)
			if perr != nil {
				return perr
			}
			sawEnd = sawEnd || gotEnd
		}
		if err != nil {
			if err == io.EOF {
				if !sawEnd {
					// A successful pi --mode json run always terminates with agent_end;
					// a clean EOF without one is a TRUNCATED stream (A5b).
					return errors.New("pi json stream ended without an agent_end envelope (truncated stream)")
				}
				return nil
			}
			return err
		}
	}
}

// parsePiLine decodes one NDJSON line and emits its normalized events,
// reporting whether the line was the terminal agent_end envelope. A blank line
// is skipped; a non-blank line that is not valid JSON is a stream-contract
// violation and returns an error (A5b). Valid JSON of an unknown envelope type
// stays tolerated.
func parsePiLine(line string, emit func(Event)) (sawEnd bool, err error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false, nil
	}
	var l piLine
	if uerr := json.Unmarshal([]byte(trimmed), &l); uerr != nil {
		return false, fmt.Errorf("invalid pi json line: %q", truncateCols(trimmed, malformedSnippetMax))
	}
	switch l.Type {
	case "message_update":
		if l.AssistantMessageEvent == nil {
			return false, nil
		}
		switch l.AssistantMessageEvent.Type {
		case "text_delta":
			// TextDelta is the playbook; emit it verbatim (empty deltas are harmless).
			emit(Event{Kind: TextDelta, Text: l.AssistantMessageEvent.Delta})
		case "thinking_delta":
			// pi surfaces real reasoning text; whitespace-only deltas (paragraph
			// separators) are dropped so they never blank the activity line.
			emitActivity(emit, Reasoning, l.AssistantMessageEvent.Delta)
		}
	case "tool_execution_start":
		emitActivity(emit, ToolActivity, toolSummary(l.ToolName, l.Args))
	case "agent_end":
		emit(Event{Kind: Final, Text: piFinalText(l.Messages)})
		return true, nil
	}
	// session/agent_start/turn_*/message_start/message_end and any unknown type
	// fall through → ignored (forward compat).
	return false, nil
}

// piFinalText extracts the authoritative final text from agent_end's transcript:
// the LAST assistant message's text blocks, concatenated in order (matching what
// that message's text_delta stream produced). Empty when the transcript carries
// no assistant text (the Final event is still emitted — agent_end is the
// terminal marker, and an authoritative empty result mirrors claude's empty
// `result`).
func piFinalText(messages []piMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "assistant" {
			continue
		}
		var b strings.Builder
		for _, c := range messages[i].Content {
			if c.Type == "text" {
				b.WriteString(c.Text)
			}
		}
		return b.String()
	}
	return ""
}
