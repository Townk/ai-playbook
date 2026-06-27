// Package askbridge is a tiny, dependency-free bridge between the tools backend's
// `ask` MCP call (which blocks for an answer on its own goroutine) and the
// running no-mux viewer (which renders the ask overlay and replies). It is a leaf
// package (channels + plain structs) so both the internal/tools-side adapter and
// internal/ui can depend on it without import cycles.
package askbridge

// Answer is the user's reply to an ask. Submitted is false on cancel.
type Answer struct {
	Value     string
	Submitted bool
}

// Request is one pending ask delivered to the UI. Respond unblocks the caller.
// Choices carries the options for a "choose" ask (nil for the other types).
type Request struct {
	Prompt  string
	Type    string
	Choices []string
	reply   chan Answer
}

// Respond delivers the user's answer back to the blocked Ask caller.
func (r Request) Respond(a Answer) { r.reply <- a }

// Bridge carries asks from the tools goroutine to the UI.
type Bridge struct {
	reqs chan Request
}

// New creates a Bridge with an unbuffered request channel (the UI drains it).
func New() *Bridge { return &Bridge{reqs: make(chan Request)} }

// Ask is called from the tools goroutine; it blocks until the UI calls Respond.
// choices supplies the options for a "choose" ask (nil for text/line/confirm/free).
func (b *Bridge) Ask(prompt, typ string, choices []string) Answer {
	r := Request{Prompt: prompt, Type: typ, Choices: choices, reply: make(chan Answer, 1)}
	b.reqs <- r
	return <-r.reply
}

// Requests is the UI-side stream of pending asks.
func (b *Bridge) Requests() <-chan Request { return b.reqs }
