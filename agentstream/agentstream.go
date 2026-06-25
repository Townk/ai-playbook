// Package agentstream defines ONE normalized event model that every supported
// coding-harness's stream output is funneled into, plus the Adapter interface and
// a registry of the built-in adapters.
//
// The harness invocation flags and the stream parser are a single matched
// contract owned in-tree: package author owns each harness's argv, and the
// matching Adapter here owns turning that harness's stdout into the normalized
// Event stream the ui consumes. The ui never sees a harness's raw wire format —
// only the four EventKinds below.
//
// Classification rule (the contract the consumer relies on):
//
//   - TextDelta / Final → the PLAYBOOK. TextDelta is the playbook streamed
//     incrementally as the model emits it; Final is the authoritative complete
//     playbook text (a harness that reports a final result wins over the
//     accumulated deltas).
//   - Reasoning → the live model reasoning (the harness's "thinking"), rendered
//     as transient activity, not part of the playbook.
//   - ToolActivity → a one-line summary of a tool the model invoked, also
//     transient activity.
//
// Part 2 wires the consumer: it renders TextDelta/Final as the playbook and
// Reasoning/ToolActivity as the transient activity line. Adapters here only emit
// the normalized events.
package agentstream

import "io"

// EventKind is the normalized class of a streamed harness event.
type EventKind int

const (
	// Reasoning is the model's live reasoning ("thinking"); transient activity.
	Reasoning EventKind = iota
	// ToolActivity is a one-line summary of a tool the model invoked; transient.
	ToolActivity
	// TextDelta is a chunk of the playbook, streamed as the model emits it.
	TextDelta
	// Final is the authoritative complete playbook text.
	Final
)

// String renders the EventKind for logs and tests.
func (k EventKind) String() string {
	switch k {
	case Reasoning:
		return "Reasoning"
	case ToolActivity:
		return "ToolActivity"
	case TextDelta:
		return "TextDelta"
	case Final:
		return "Final"
	default:
		return "EventKind(?)"
	}
}

// Event is one normalized streamed item. Text carries the payload: the text
// chunk for TextDelta, the reasoning text for Reasoning, the tool summary for
// ToolActivity, and the complete playbook for Final.
type Event struct {
	Kind EventKind
	Text string
}

// Adapter normalizes one harness's stdout into the Event model. Parse reads r to
// EOF, calling emit once per normalized event in stream order, and returns nil on
// clean EOF. A malformed/garbage line is skipped (not fatal); Parse returns a
// non-nil error only on an unrecoverable read failure.
type Adapter interface {
	Parse(r io.Reader, emit func(Event)) error
}

// registry maps a harness/adapter name to its built-in Adapter.
var registry = map[string]Adapter{
	"claude": claudeAdapter{},
	"text":   textAdapter{},
}

// Get returns the registered Adapter for name and whether it exists.
func Get(name string) (Adapter, bool) {
	a, ok := registry[name]
	return a, ok
}
