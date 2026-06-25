package ui

import (
	"strings"
	"testing"
)

func collect(p *streamParser, chunks ...string) []streamEvent {
	var all []streamEvent
	for _, c := range chunks {
		all = append(all, p.feed([]byte(c))...)
	}
	return all
}

func TestParserPlainText(t *testing.T) {
	got := collect(&streamParser{}, "# Hello\nworld")
	if len(got) != 1 {
		t.Fatalf("want 1 event, got %d (%#v)", len(got), got)
	}
	te, ok := got[0].(textEvent)
	if !ok || te.text != "# Hello\nworld" {
		t.Fatalf("want textEvent %q, got %#v", "# Hello\nworld", got[0])
	}
}

func TestParserThinkWithLabel(t *testing.T) {
	// "ab" + DLE t "Reading…" DLE + "cd"
	got := collect(&streamParser{}, "ab\x10tReading…\x10cd")
	if len(got) != 3 {
		t.Fatalf("want 3 events, got %d (%#v)", len(got), got)
	}
	if te, ok := got[0].(textEvent); !ok || te.text != "ab" {
		t.Fatalf("event0 want text ab, got %#v", got[0])
	}
	if th, ok := got[1].(thinkEvent); !ok || th.label != "Reading…" {
		t.Fatalf("event1 want think Reading…, got %#v", got[1])
	}
	if te, ok := got[2].(textEvent); !ok || te.text != "cd" {
		t.Fatalf("event2 want text cd, got %#v", got[2])
	}
}

func TestParserThinkNoLabel(t *testing.T) {
	got := collect(&streamParser{}, "\x10t\x10")
	if len(got) != 1 {
		t.Fatalf("want 1 event, got %d (%#v)", len(got), got)
	}
	if th, ok := got[0].(thinkEvent); !ok || th.label != "" {
		t.Fatalf("want empty-label thinkEvent, got %#v", got[0])
	}
}

func TestParserRecordSplitAcrossChunks(t *testing.T) {
	// The DLE record is split mid-label across three feeds.
	got := collect(&streamParser{}, "x\x10tSear", "ching", "…\x10y")
	if len(got) != 3 {
		t.Fatalf("want 3 events, got %d (%#v)", len(got), got)
	}
	if th, ok := got[1].(thinkEvent); !ok || th.label != "Searching…" {
		t.Fatalf("want reassembled label Searching…, got %#v", got[1])
	}
	if te, ok := got[2].(textEvent); !ok || te.text != "y" {
		t.Fatalf("want trailing text y, got %#v", got[2])
	}
}

func TestParserQuitEvent(t *testing.T) {
	// \x10q\x10 — DLE q DLE with empty payload — must yield exactly one quitEvent.
	got := collect(&streamParser{}, "\x10q\x10")
	if len(got) != 1 {
		t.Fatalf("want 1 event, got %d (%#v)", len(got), got)
	}
	if _, ok := got[0].(quitEvent); !ok {
		t.Fatalf("want quitEvent, got %T (%#v)", got[0], got[0])
	}
}

func TestParserQuitEventSplitAcrossChunks(t *testing.T) {
	// The DLE q DLE record is delivered across two feed() calls.
	got := collect(&streamParser{}, "\x10q", "\x10")
	if len(got) != 1 {
		t.Fatalf("want 1 event across split chunks, got %d (%#v)", len(got), got)
	}
	if _, ok := got[0].(quitEvent); !ok {
		t.Fatalf("want quitEvent from split chunks, got %T (%#v)", got[0], got[0])
	}
}

func TestParserQuitEventWithSurroundingText(t *testing.T) {
	// Text before and after the quit record are emitted normally; the quit
	// sentinel is in the middle.
	got := collect(&streamParser{}, "before\x10q\x10after")
	if len(got) != 3 {
		t.Fatalf("want 3 events (text+quit+text), got %d (%#v)", len(got), got)
	}
	if te, ok := got[0].(textEvent); !ok || te.text != "before" {
		t.Fatalf("event0 want text 'before', got %#v", got[0])
	}
	if _, ok := got[1].(quitEvent); !ok {
		t.Fatalf("event1 want quitEvent, got %T (%#v)", got[1], got[1])
	}
	if te, ok := got[2].(textEvent); !ok || te.text != "after" {
		t.Fatalf("event2 want text 'after', got %#v", got[2])
	}
}

// reconstructText concatenates the text of every textEvent in order.
func reconstructText(events []streamEvent) string {
	var sb strings.Builder
	for _, e := range events {
		if te, ok := e.(textEvent); ok {
			sb.WriteString(te.text)
		}
	}
	return sb.String()
}

// TestParserPreservesNewlines feeds RAW playbook text (no DLE control records,
// which is what the fan-out delivers) in awkward chunk splits — including a
// leading newline, a trailing newline, a chunk that ENDS at "\n" with the next
// chunk starting the following line, and a split right around a closing ```
// fence — and asserts the reconstructed text is byte-for-byte identical. The
// streamParser was built for the old DLE think/quit protocol; this guards that
// it never strips, merges, or eats a leading/trailing newline in raw text.
func TestParserPreservesNewlines(t *testing.T) {
	want := "\n```bash\ngg build\n```\nSDK is at /Users/x/sdk.\n"
	splits := [][]string{
		// Whole input in one feed.
		{want},
		// Split so a chunk ends exactly at "\n" and the next starts the new line.
		{"\n```bash\n", "gg build\n", "```\n", "SDK is at /Users/x/sdk.\n"},
		// Split right between the closing backticks and their newline.
		{"\n```bash\ngg build\n```", "\nSDK is at /Users/x/sdk.\n"},
		// Byte-at-a-time: the most adversarial boundary placement.
		bytesAtATime(want),
	}
	for i, chunks := range splits {
		got := reconstructText(collect(&streamParser{}, chunks...))
		if got != want {
			t.Fatalf("split %d: reconstructed %q, want %q", i, got, want)
		}
	}
}

// bytesAtATime splits s into one-byte chunks (multi-byte runes are fine: the
// parser is byte-oriented for everything except the DLE sentinel).
func bytesAtATime(s string) []string {
	out := make([]string, len(s))
	for i := 0; i < len(s); i++ {
		out[i] = s[i : i+1]
	}
	return out
}

func TestReadStreamYieldsEventsThenEOF(t *testing.T) {
	r := strings.NewReader("hi\x10tWork\x10")
	p := &streamParser{}
	// Drain the reader the way Update would: call the command, collect events,
	// repeat until eof.
	var events []streamEvent
	for {
		msg := readStream(r, p)().(streamEventsMsg)
		events = append(events, msg.events...)
		if msg.eof {
			break
		}
	}
	if len(events) < 2 {
		t.Fatalf("want >=2 events (text + think), got %d (%#v)", len(events), events)
	}
	if _, ok := events[len(events)-1].(thinkEvent); !ok {
		t.Fatalf("last event should be the think record, got %#v", events[len(events)-1])
	}
}
