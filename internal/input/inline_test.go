package input

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestClearInline_EmitsCursorUpAndErase(t *testing.T) {
	var b strings.Builder
	ClearInline(&b, 3)
	if got, want := b.String(), "\x1b[3F\x1b[0J"; got != want {
		t.Fatalf("ClearInline(3) = %q, want %q", got, want)
	}
}

func TestClearInline_ZeroHeightNoop(t *testing.T) {
	var b strings.Builder
	ClearInline(&b, 0)
	if b.String() != "" {
		t.Fatalf("ClearInline(0) wrote %q, want empty", b.String())
	}
}

func TestRecvThink_LineThenDone(t *testing.T) {
	ch := make(chan ThinkUpdate, 2)
	ch <- ThinkUpdate{Line: "deciding…"}
	msg := recvThink(ch)().(doneSignalMsg)
	if msg.done || msg.thinking != "deciding…" {
		t.Fatalf("first recvThink = %+v, want {done:false thinking:%q}", msg, "deciding…")
	}
	ch <- ThinkUpdate{Done: true}
	if d := recvThink(ch)().(doneSignalMsg); !d.done {
		t.Fatalf("second recvThink = %+v, want done=true", d)
	}
}

func TestRecvThink_ClosedChannelIsDone(t *testing.T) {
	ch := make(chan ThinkUpdate)
	close(ch)
	if d := recvThink(ch)().(doneSignalMsg); !d.done {
		t.Fatalf("closed channel recvThink = %+v, want done=true", d)
	}
}

// On submit with an inlineSubmit wired, the model must enter the in-box wave
// (thinking) state and start animating — NOT quit — and seed the prep line.
func TestModel_InlineSubmitEntersThinking(t *testing.T) {
	m := newInputModel(defaultTheme(), "default", "ai-playbook", "How can I help?", "fix the build", "", 3, 1, 1, false, "")
	m.inlineSubmit = func(string) <-chan ThinkUpdate { return make(chan ThinkUpdate) }
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := next.(model)
	if !got.thinking {
		t.Fatal("Enter with inlineSubmit must enter the thinking state")
	}
	if got.thinkingLine != thinkingPrepLine {
		t.Fatalf("thinkingLine = %q, want %q", got.thinkingLine, thinkingPrepLine)
	}
	if cmd == nil {
		t.Fatal("thinking transition must return a batched cmd (waveTick + recvThink)")
	}
}
