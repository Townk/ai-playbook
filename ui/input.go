package ui

import (
	"bufio"
	"io"

	tea "charm.land/bubbletea/v2"
)

// streamEventsMsg carries one chunk's worth of parsed events from the reader
// command; eof signals the input stream closed.
type streamEventsMsg struct {
	events []streamEvent
	eof    bool
}

// readStream returns a command that reads the next chunk from r, feeds it through
// the parser, and reports the resulting events. eof is set on any read error
// (including io.EOF); the caller stops re-issuing the command once eof is seen.
func readStream(r io.Reader, p *streamParser) tea.Cmd {
	br, ok := r.(*bufio.Reader)
	if !ok {
		br = bufio.NewReader(r)
	}
	return func() tea.Msg {
		buf := make([]byte, 4096)
		n, err := br.Read(buf)
		events := p.feed(buf[:n])
		return streamEventsMsg{events: events, eof: err != nil}
	}
}

// dle (Data Link Escape, 0x10) brackets a control record on the input stream:
// DLE <cmd> <payload> DLE. It never appears in markdown/prose, so it cleanly
// separates out-of-band control from rendered text.
const dle = 0x10

// streamEvent is one item produced by the parser: text to render, or a control
// signal. The marker method keeps the set closed and lets callers type-switch.
type streamEvent interface{ streamEvent() }

type textEvent struct{ text string }   // markdown bytes to append + render
type thinkEvent struct{ label string } // start/replace thinking; "" → default label
type quitEvent struct{}               // \x10q\x10 — shell signals the pager to quit

func (textEvent) streamEvent()  {}
func (thinkEvent) streamEvent() {}
func (quitEvent) streamEvent()  {}

const (
	psText = iota // default: bytes are markdown text
	psCmd         // just saw DLE: this byte is the command
	psLabel       // inside a record: accumulate payload until the closing DLE
)

// streamParser turns a byte stream (delivered in arbitrary chunks) into ordered
// streamEvents, carrying partial-record state across chunk boundaries. Text runs
// within a chunk are coalesced into one textEvent.
type streamParser struct {
	state int
	cmd   byte
	label []byte
}

func (p *streamParser) feed(chunk []byte) []streamEvent {
	var events []streamEvent
	var text []byte
	flush := func() {
		if len(text) > 0 {
			events = append(events, textEvent{string(text)})
			text = nil
		}
	}
	for _, b := range chunk {
		switch p.state {
		case psText:
			if b == dle {
				flush()
				p.state = psCmd
			} else {
				text = append(text, b)
			}
		case psCmd:
			p.cmd = b
			p.label = p.label[:0]
			p.state = psLabel // for 't' we keep the label; unknown cmds just skip to closing DLE
		case psLabel:
			if b == dle {
				if p.cmd == 't' {
					events = append(events, thinkEvent{string(p.label)})
				} else if p.cmd == 'q' {
					events = append(events, quitEvent{})
				}
				p.label = p.label[:0]
				p.state = psText
			} else {
				p.label = append(p.label, b)
			}
		}
	}
	flush()
	return events
}
