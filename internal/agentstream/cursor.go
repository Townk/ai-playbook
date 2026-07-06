package agentstream

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// init installs the cursor adapter under its registry name — registration
// lives HERE (the cursor adapter's own file) so the shared registry never
// names a concrete harness (the ADR-0012 leak discipline).
func init() {
	registry["cursor"] = cursorAdapter{}
}

// cursorAdapter parses cursor-agent's `-p --output-format stream-json
// --stream-partial-output` output: one JSON object per line (NDJSON). The
// event schema is DOCUMENTATION-DERIVED (fixture-first — the cursor CLI was
// not available on the authoring machine): every envelope below is taken from
// Cursor's published stream-json reference,
// cursor.com/docs/cli/reference/output-format, whose verbatim examples seeded
// the testdata/cursor-*.ndjson fixtures. The RequireHarness-gated live tests
// re-verify the mapping wherever the CLI exists. It normalizes that wire
// format into the shared Event model:
//
//   - assistant → TextDelta, under the documented --stream-partial-output
//     dedup rule. The CLI emits THREE kinds of assistant events, told apart by
//     field presence: `timestamp_ms` without `model_call_id` is a streaming
//     delta carrying NEW text (emit, appending); `timestamp_ms` WITH
//     `model_call_id` is a buffered flush before a tool call that DUPLICATES
//     already-streamed text (skip); NEITHER field is the final whole-message
//     flush at end of turn, also a duplicate (skip). Emit iff timestamp_ms is
//     present and model_call_id is absent. Assumption: our owned invocation
//     always sets --stream-partial-output; a no-partial stream would emit only
//     no-field assistant events (all skipped) and still yield its Final from
//     the result envelope.
//   - tool_call subtype "started" → ToolActivity. The tool_call object wraps
//     the call in a single tool-named key (the documented example:
//     `readToolCall` with an `args` object); the adapter derives the bare tool
//     name by trimming the ToolCall suffix and renders it with the shared
//     toolSummary. subtype "completed" repeats the args plus a result and is
//     ignored — started is the single complete-args moment, one activity line
//     per call (the same rule as pi).
//   - result → Final (the documented terminal envelope and the stream's
//     success marker). The Final TEXT is NOT taken from the envelope's
//     `result` field: the documented example shows that field is the
//     NO-SEPARATOR CONCATENATION of every assistant segment in the turn
//     ("I'll read the README.md fileBased on the README, …" — narration glued
//     straight onto the answer), which would corrupt the stored playbook body
//     downstream (fanout prefers Final's text). Instead the adapter
//     accumulates the streamed deltas per SEGMENT (a tool_call started event
//     closes the current segment — segments are the text runs between tool
//     calls, per the same page) and Final carries the LAST non-empty
//     segment's full text: the model's final answer, the same semantics as
//     claude's `result` and pi's last assistant message. Only when no delta
//     ever streamed (a non-partial stream, where every assistant event is a
//     skipped no-field flush) does the envelope's `result` text serve as the
//     fallback — the only text available there.
//   - system (subtype init) / user / unknown envelope types → ignored
//     gracefully (the docs promise backward-compatible field additions and
//     tell consumers to ignore unknown fields).
//
// Reasoning is NEVER emitted: "thinking events are suppressed in print mode
// and will not appear in any output format"
// (cursor.com/docs/cli/reference/output-format) — with cursor the live
// activity line shows tool activity only, like claude --print.
//
// Strictness (A5b, the same discipline as the claude and pi adapters): the
// wire format is NDJSON and a successful run ALWAYS terminates with a result
// envelope; on failure "the process exits with a non-zero code and the stream
// may end early without a terminal event; an error message is written to
// stderr" (same page). Parse therefore returns an error for a non-blank line
// that is not valid JSON and for a clean EOF with no result seen (a truncated
// stream, including a completely empty one — and the documented failure shape,
// which wait() pairs with the non-zero exit). Blank lines and valid-JSON
// envelopes of unknown type stay tolerated. Very long lines are handled
// (bufio.Reader).
type cursorAdapter struct{}

// cursorLine is the envelope: every NDJSON line has a "type". The remaining
// fields are decoded only for the types the adapter handles.
type cursorLine struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype,omitempty"`

	// type == "assistant"
	Message *cursorMessage `json:"message,omitempty"`
	// Presence-only dedup markers (see the --stream-partial-output rule on
	// cursorAdapter). RawMessage keeps the check purely presence-based — the
	// values' types never matter.
	TimestampMs json.RawMessage `json:"timestamp_ms,omitempty"`
	ModelCallID json.RawMessage `json:"model_call_id,omitempty"`

	// type == "tool_call": a single tool-named wrapper key → its body.
	ToolCall map[string]cursorToolCallBody `json:"tool_call,omitempty"`

	// type == "result"
	Result string `json:"result,omitempty"`
}

// cursorMessage is the documented message object on user/assistant events:
// role + an array of content blocks.
type cursorMessage struct {
	Role    string               `json:"role"`
	Content []cursorContentBlock `json:"content,omitempty"`
}

type cursorContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// cursorToolCallBody is the value under a tool_call's tool-named wrapper key;
// only the args matter (the completed variant's result is ignored).
type cursorToolCallBody struct {
	Args json.RawMessage `json:"args,omitempty"`
}

func (cursorAdapter) Parse(r io.Reader, emit func(Event)) error {
	br := bufio.NewReader(r)
	p := &cursorParser{}
	sawResult := false
	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			gotResult, perr := p.parseLine(line, emit)
			if perr != nil {
				return perr
			}
			sawResult = sawResult || gotResult
		}
		if err != nil {
			if err == io.EOF {
				if !sawResult {
					// A successful cursor-agent print run always terminates with a
					// result envelope; a clean EOF without one is a TRUNCATED
					// stream (A5b) — and the documented failure shape ("the stream
					// may end early without a terminal event").
					return errors.New("cursor stream-json ended without a result envelope (truncated stream)")
				}
				return nil
			}
			return err
		}
	}
}

// cursorParser carries the per-stream segment state the Final policy needs
// (see the result note on cursorAdapter): the current segment's accumulated
// delta text and the last COMPLETED non-empty segment.
type cursorParser struct {
	// seg accumulates the streaming deltas of the current assistant segment
	// (the text run since the last tool_call started, or the turn start).
	seg strings.Builder
	// lastSegment is the most recent completed non-empty segment — the answer
	// fallback when the turn ends on a tool call with no trailing text.
	lastSegment string
}

// parseLine decodes one NDJSON line and emits its normalized events, reporting
// whether the line was the terminal result envelope. A blank line is skipped;
// a non-blank line that is not valid JSON is a stream-contract violation and
// returns an error (A5b). Valid JSON of an unknown envelope type stays
// tolerated.
func (p *cursorParser) parseLine(line string, emit func(Event)) (sawResult bool, err error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false, nil
	}
	var l cursorLine
	if uerr := json.Unmarshal([]byte(trimmed), &l); uerr != nil {
		return false, fmt.Errorf("invalid cursor stream-json line: %q", truncateCols(trimmed, malformedSnippetMax))
	}
	switch l.Type {
	case "assistant":
		// The dedup rule: only a streaming delta (timestamp_ms present,
		// model_call_id absent) carries NEW text; the buffered pre-tool flush
		// and the end-of-turn flush are documented duplicates.
		if len(l.TimestampMs) == 0 || len(l.ModelCallID) > 0 {
			return false, nil
		}
		if l.Message == nil {
			return false, nil
		}
		for _, b := range l.Message.Content {
			if b.Type == "text" {
				// TextDelta is the playbook; emit it verbatim (empty deltas are
				// harmless) and accumulate it into the current segment.
				emit(Event{Kind: TextDelta, Text: b.Text})
				p.seg.WriteString(b.Text)
			}
		}
	case "tool_call":
		if l.Subtype != "started" {
			return false, nil
		}
		// A tool call closes the current assistant segment (segments are the
		// text runs BETWEEN tool calls, per the output-format reference).
		p.closeSegment()
		name, args := cursorToolName(l.ToolCall)
		emitActivity(emit, ToolActivity, toolSummary(name, args))
	case "result":
		emit(Event{Kind: Final, Text: p.finalText(l.Result)})
		return true, nil
	}
	// system/user and any unknown type fall through → ignored (forward compat).
	return false, nil
}

// closeSegment completes the current segment, remembering it as the answer
// fallback when non-empty.
func (p *cursorParser) closeSegment() {
	if p.seg.Len() > 0 {
		p.lastSegment = p.seg.String()
		p.seg.Reset()
	}
}

// finalText picks the authoritative Final text (the policy on cursorAdapter's
// result note): the LAST non-empty assistant segment — the final answer, never
// the result envelope's glued all-segment concatenation
// (cursor.com/docs/cli/reference/output-format documents `result` as the
// no-separator concat of every segment in the turn). The envelope's text is
// used only when NO delta ever streamed (a non-partial stream), where it is
// the only text available.
func (p *cursorParser) finalText(resultField string) string {
	p.closeSegment()
	if p.lastSegment != "" {
		return p.lastSegment
	}
	return resultField
}

// cursorToolName extracts the bare tool name + args from a tool_call object's
// tool-named wrapper key (the documented shape: {"readToolCall": {"args":
// {...}}} → "read"). The docs show exactly one wrapper key; if a future
// envelope carried several, the first in sorted order keeps the choice
// deterministic. A wrapper key without the ToolCall suffix passes through
// bare.
func cursorToolName(tc map[string]cursorToolCallBody) (name string, args json.RawMessage) {
	keys := make([]string, 0, len(tc))
	for k := range tc {
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return "", nil
	}
	sort.Strings(keys)
	key := keys[0]
	name = strings.TrimSuffix(key, "ToolCall")
	if name == "" {
		name = key
	}
	return name, tc[key].Args
}
