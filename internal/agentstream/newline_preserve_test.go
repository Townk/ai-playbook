package agentstream

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// jsonTextDeltaLine builds a stream-json content_block_delta/text_delta NDJSON
// line carrying text, using the encoder so newlines are escaped correctly.
func jsonTextDeltaLine(t *testing.T, text string) string {
	t.Helper()
	l := claudeLine{
		Type: "stream_event",
		Event: &claudeStreamEvent{
			Type:  "content_block_delta",
			Delta: &claudeDelta{Type: "text_delta", Text: text},
		},
	}
	b, err := json.Marshal(l)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// TestNewlinePreservation_AdapterThenFanOut feeds TextDelta chunks whose text
// contains newlines — including a delta that ENDS at "\n" with the next delta
// starting the following line, and a delta boundary that splits right around a
// closing ``` fence — through the claude adapter AND FanOut, asserting the
// reconstructed playbook is byte-for-byte identical to the model's intended text
// (no dropped, merged, or trailing-stripped newline anywhere).
func TestNewlinePreservation_AdapterThenFanOut(t *testing.T) {
	// The model's intended text. Note the closing ``` is on its own line and the
	// prose that follows is on the NEXT line (the well-formed case). We split it
	// into deltas at deliberately awkward boundaries.
	want := "```bash\ngg build\n```\nSDK is at /Users/x/sdk.\n"

	// Delta boundaries chosen to stress newline handling:
	//   - a delta ending exactly at "\n"
	//   - a delta starting the following line
	//   - a boundary that splits the closing "```\n" between the backticks and \n
	deltas := []string{
		"```bash\n",  // ends at a newline
		"gg build\n", // a whole line
		"```",        // closing fence backticks, no newline yet
		"\nSDK is ",  // newline that belongs to the closing fence + start of prose
		"at /Users/x/sdk.\n",
	}

	// Build a stream-json session of text_delta lines, one per delta, terminated
	// by the result envelope a real claude run always ends with (the strict
	// adapter treats a missing result as a truncated stream).
	var sb strings.Builder
	for _, d := range deltas {
		sb.WriteString(jsonTextDeltaLine(t, d))
		sb.WriteByte('\n') // NDJSON line terminator (NOT part of the payload)
	}
	resultLine, err := jsonResultLine(want)
	if err != nil {
		t.Fatal(err)
	}
	sb.WriteString(resultLine)
	sb.WriteByte('\n')

	a, _ := Get("claude")

	// Adapter → events.
	var events []Event
	if err := a.Parse(strings.NewReader(sb.String()), func(e Event) { events = append(events, e) }); err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// Reconstruct directly from adapter events (the adapter contract).
	var fromAdapter strings.Builder
	for _, e := range events {
		if e.Kind == TextDelta {
			fromAdapter.WriteString(e.Text)
		}
	}
	if fromAdapter.String() != want {
		t.Fatalf("adapter reconstruction mismatch:\n got %q\nwant %q", fromAdapter.String(), want)
	}

	// Adapter events → FanOut → playbook pipe; must be byte-for-byte identical.
	ch := make(chan Event)
	reader, activity, _ := FanOut(ch, func() error { return nil }, 16)
	go func() {
		for _, e := range events {
			ch <- e
		}
		close(ch)
	}()
	go drainActivity(activity)

	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	if string(got) != want {
		t.Fatalf("FanOut pipe newline mismatch:\n got %q\nwant %q", got, want)
	}
}
