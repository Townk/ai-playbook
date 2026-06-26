package input

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// isQuit reports whether cmd, when executed, yields tea.QuitMsg{}.
func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

// hasBraille reports whether s contains any glyph in the Braille block.
func hasBraille(s string) bool {
	for _, r := range s {
		if r >= 0x2800 && r <= 0x28FF {
			return true
		}
	}
	return false
}

func newThinkingModel(t *testing.T, outFile string) model {
	t.Helper()
	m := newInputModel(defaultTheme(), "default", "ai-playbook", "How can I help you today?", "hello", "", 4, 1, 1, false, "")
	m.width = 60
	m.resize()
	m.thinkingEnabled = true
	m.outFile = outFile
	return m
}

// TestSubmitEntersThinkingWhenEnabled: with --thinking, a fieldDone (Enter)
// flips thinking=true, does NOT quit, and returns a (batch) command.
func TestSubmitEntersThinkingWhenEnabled(t *testing.T) {
	out := filepath.Join(t.TempDir(), "req")
	m := newThinkingModel(t, out)

	res, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	rm := res.(model)
	if !rm.submitted {
		t.Fatal("submit must mark the model submitted")
	}
	if !rm.thinking {
		t.Fatal("with --thinking, submit must enter the thinking state")
	}
	if cmd == nil {
		t.Fatal("thinking transition must return a command batch")
	}
	if isQuit(cmd) {
		t.Fatal("with --thinking, submit must NOT quit")
	}
	// The returned batch is a BatchMsg (a slice of cmds), not a quit.
	if _, ok := cmd().(tea.BatchMsg); !ok {
		t.Fatalf("expected a BatchMsg from the thinking transition, got %T", cmd())
	}
}

// TestSubmitQuitsWithoutThinking: the non-thinking path is unchanged — submit
// quits and does not enter the thinking state.
func TestSubmitQuitsWithoutThinking(t *testing.T) {
	m := newInputModel(defaultTheme(), "default", "ai-playbook", "", "hi", "", 1, 1, 1, true, "")
	m.width = 60
	m.resize()

	res, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	rm := res.(model)
	if !rm.submitted {
		t.Fatal("submit must mark the model submitted")
	}
	if rm.thinking {
		t.Fatal("without --thinking, submit must NOT enter the thinking state")
	}
	if !isQuit(cmd) {
		t.Fatal("without --thinking, submit must quit")
	}
}

// TestWriteOutCmdWritesValue: the submit-time write cmd hands the value to
// outFile so the launcher can read it while the float animates.
func TestWriteOutCmdWritesValue(t *testing.T) {
	out := filepath.Join(t.TempDir(), "req")
	msg := writeOutCmd(out, "list the last 3 commits")()
	if _, ok := msg.(outWrittenMsg); !ok {
		t.Fatalf("expected outWrittenMsg, got %T", msg)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("outFile not written: %v", err)
	}
	if string(got) != "list the last 3 commits" {
		t.Fatalf("outFile = %q, want the submitted value", string(got))
	}
}

// TestThinkingRender: while thinking, render() shows the Thinking… prompt and a
// Braille wave inside a rounded border, and drops the submit/newline hint.
func TestThinkingRender(t *testing.T) {
	m := newThinkingModel(t, "")
	m.thinking = true
	m.phase = 0.7
	plain := strip(m.render())

	if !strings.Contains(plain, "Thinking…") {
		t.Fatalf("thinking render must show the Thinking… prompt:\n%s", plain)
	}
	if !hasBraille(plain) {
		t.Fatal("thinking render must contain Braille wave glyphs")
	}
	// Braille must sit inside a rounded box (the field border chrome).
	if !strings.Contains(plain, "╭") || !strings.Contains(plain, "╰") {
		t.Fatal("thinking render must keep the rounded box border")
	}
	if strings.Contains(plain, "submit") || strings.Contains(plain, "newline") {
		t.Fatalf("thinking render must drop the submit/newline hint:\n%s", plain)
	}
	if strings.Contains(plain, "How can I help you today?") {
		t.Fatal("thinking render must replace the prompt, not keep it")
	}
}

// TestThinkingViewDims: thinkingView produces the box with a WaveFrame interior —
// rounded border present, and exactly taHeight wave rows.
func TestThinkingViewDims(t *testing.T) {
	m := newThinkingModel(t, "")
	tf := m.fld.(*textField)
	out := tf.thinkingView(m.innerW(), 0.3, m.theme.Border, thinkingWaveRed, m.theme.Accent)
	plain := strip(out)
	lines := strings.Split(plain, "\n")
	// border top + taHeight body rows + border bottom.
	if want := tf.taHeight + boxBorder; len(lines) != want {
		t.Fatalf("thinkingView has %d lines, want %d (taHeight=%d + border)", len(lines), want, tf.taHeight)
	}
	if !strings.HasPrefix(lines[0], "╭") || !strings.HasPrefix(lines[len(lines)-1], "╰") {
		t.Fatal("thinkingView must keep the rounded border")
	}
	// Count interior rows that carry Braille — should equal taHeight.
	waveRows := 0
	for _, l := range lines {
		if hasBraille(l) {
			waveRows++
		}
	}
	if waveRows != tf.taHeight {
		t.Fatalf("thinkingView wave rows = %d, want taHeight %d", waveRows, tf.taHeight)
	}
	// Full-width: the wave fills the interior border-to-border — each rendered line
	// is exactly innerW wide (cols == innerW - boxBorder, only the border removed,
	// no icon col / padding / scroll reserve).
	for _, l := range lines {
		if w := lipgloss.Width(l); w != m.innerW() {
			t.Fatalf("thinkingView line width = %d, want innerW %d (full-width wave)\n%q", w, m.innerW(), l)
		}
	}
	// And the prompt-icon glyph must NOT appear in the thinking interior.
	if strings.Contains(plain, promptIcon) {
		t.Fatalf("full-width thinkingView must drop the prompt-icon column (%q):\n%s", promptIcon, plain)
	}
}

// TestThinkingRenderNoIcon: the full-width thinking render drops the prompt-icon
// column entirely (the wave spans the whole interior).
func TestThinkingRenderNoIcon(t *testing.T) {
	m := newThinkingModel(t, "")
	m.thinking = true
	m.phase = 0.7
	plain := strip(m.render())
	if strings.Contains(plain, promptIcon) {
		t.Fatalf("thinking render must not contain the prompt icon %q:\n%s", promptIcon, plain)
	}
}

// TestLerpHexColor: endpoints and midpoint of the breathing interpolation.
func TestLerpHexColor(t *testing.T) {
	const a, b = "#000000", "#ffffff"
	if got := lerpHexColor(a, b, 0); got != a {
		t.Errorf("t=0 → %q, want %q", got, a)
	}
	if got := lerpHexColor(a, b, 1); got != b {
		t.Errorf("t=1 → %q, want %q", got, b)
	}
	if got := lerpHexColor(a, b, 0.5); got != "#808080" {
		t.Errorf("t=0.5 → %q, want #808080 (midpoint)", got)
	}
	// Per-channel midpoint between two real colors.
	if got := lerpHexColor("#ffffff", "#fab387", 0.5); got != "#fdd9c3" {
		t.Errorf("white↔peach midpoint = %q, want #fdd9c3", got)
	}
	// t is clamped; a malformed input falls back to the other color.
	if got := lerpHexColor(a, b, 2); got != b {
		t.Errorf("t>1 clamps to b: %q, want %q", got, b)
	}
	if got := lerpHexColor("garbage", b, 0); got != b {
		t.Errorf("malformed a falls back to b: %q, want %q", got, b)
	}
}

// TestDonePollSignalsOnFile: pollDoneCmd reports the presence/absence of the
// <out>.done marker; the model quits on done and re-arms otherwise.
func TestDonePollSignalsOnFile(t *testing.T) {
	saved := donePollInterval
	donePollInterval = time.Millisecond
	defer func() { donePollInterval = saved }()

	out := filepath.Join(t.TempDir(), "req")

	// Absent: not done.
	if msg := pollDoneCmd(out)().(doneSignalMsg); msg.done {
		t.Fatal("poll must report not-done while <out>.done is absent")
	}
	// Present: done.
	if err := os.WriteFile(out+DoneSuffix, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if msg := pollDoneCmd(out)().(doneSignalMsg); !msg.done {
		t.Fatal("poll must report done once <out>.done exists")
	}

	// A thinking model quits on a done signal, and re-arms (non-quit) otherwise.
	m := newThinkingModel(t, out)
	m.thinking = true
	_, cmd := m.Update(doneSignalMsg{done: true})
	if !isQuit(cmd) {
		t.Fatal("done signal must quit the thinking float")
	}
	_, cmd = m.Update(doneSignalMsg{done: false})
	if cmd == nil || isQuit(cmd) {
		t.Fatal("a not-yet signal must re-arm the poll (non-nil, non-quit)")
	}
}

// TestPollPicksUpThinkingLine: pollDoneCmd reads <out>.thinking into the carried
// signal, and a thinking model adopts it into thinkingLine on the tick; an absent
// file leaves the line empty.
func TestPollPicksUpThinkingLine(t *testing.T) {
	saved := donePollInterval
	donePollInterval = time.Millisecond
	defer func() { donePollInterval = saved }()

	out := filepath.Join(t.TempDir(), "req")

	// Absent .thinking → empty carried content, and the model's line stays empty.
	msg := pollDoneCmd(out)().(doneSignalMsg)
	if msg.thinking != "" {
		t.Fatalf("absent .thinking must carry empty content, got %q", msg.thinking)
	}
	if ThinkingSuffix != ".thinking" {
		t.Errorf("ThinkingSuffix = %q, want .thinking (the launcher shares this contract)", ThinkingSuffix)
	}

	// Present .thinking → its trimmed content is carried and adopted into the line.
	if err := os.WriteFile(out+ThinkingSuffix, []byte("  weighing command vs answer  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	msg = pollDoneCmd(out)().(doneSignalMsg)
	if msg.thinking != "weighing command vs answer" {
		t.Fatalf("thinking content = %q, want trimmed line", msg.thinking)
	}
	m := newThinkingModel(t, out)
	m.thinking = true
	nm, _ := m.Update(msg)
	if got := nm.(model).thinkingLine; got != "weighing command vs answer" {
		t.Errorf("thinkingLine = %q, want the polled content adopted", got)
	}

	// A subsequent tick with the file removed clears the line back to empty.
	if err := os.Remove(out + ThinkingSuffix); err != nil {
		t.Fatal(err)
	}
	msg = pollDoneCmd(out)().(doneSignalMsg)
	nm, _ = nm.(model).Update(msg)
	if got := nm.(model).thinkingLine; got != "" {
		t.Errorf("thinkingLine = %q, want empty once .thinking is gone", got)
	}
}

// TestWriteClosedFileWritesMarker: the thinking-exit marker write (writeClosedFile,
// called on the thinking exit path in runInput before os.Exit) creates
// <out>.closed so the launcher can detect the float has fully torn down. An empty
// path is a no-op.
func TestWriteClosedFileWritesMarker(t *testing.T) {
	out := filepath.Join(t.TempDir(), "req")
	writeClosedFile(out)
	if _, err := os.Stat(out + ClosedSuffix); err != nil {
		t.Fatalf("writeClosedFile must create %s%s: %v", out, ClosedSuffix, err)
	}
	if ClosedSuffix != ".closed" {
		t.Errorf("ClosedSuffix = %q, want .closed (the launcher shares this contract)", ClosedSuffix)
	}
	// Empty path is a no-op (must not panic / create stray files).
	writeClosedFile("")
}

// TestThinkingBackstopQuits: the backstop msg quits a thinking float, and the
// backstop cmd fires the message after its (shortened) duration.
func TestThinkingBackstopQuits(t *testing.T) {
	m := newThinkingModel(t, "")
	m.thinking = true
	_, cmd := m.Update(thinkingBackstopMsg{})
	if !isQuit(cmd) {
		t.Fatal("the thinking backstop must quit the float")
	}

	saved := thinkingBackstopAfter
	thinkingBackstopAfter = time.Millisecond
	defer func() { thinkingBackstopAfter = saved }()
	if _, ok := backstopCmd()().(thinkingBackstopMsg); !ok {
		t.Fatal("backstopCmd must emit a thinkingBackstopMsg")
	}
}

// TestCancelDuringThinking: Escape/ctrl+c mid-think quits (cancel is allowed).
func TestCancelDuringThinking(t *testing.T) {
	m := newThinkingModel(t, "")
	m.thinking = true
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape, Text: "esc"})
	if !isQuit(cmd) {
		t.Fatal("Escape during thinking must quit")
	}
}

// TestWaveTickAdvancesOnlyWhileThinking: the tick advances phase and re-ticks
// only while thinking; a stray tick when not thinking is a no-op.
func TestWaveTickAdvancesOnlyWhileThinking(t *testing.T) {
	m := newThinkingModel(t, "")
	m.thinking = true
	res, cmd := m.Update(waveTickMsg{})
	if res.(model).phase <= 0 {
		t.Fatal("tick must advance the phase while thinking")
	}
	if cmd == nil {
		t.Fatal("tick must re-schedule while thinking")
	}

	m2 := newThinkingModel(t, "")
	res2, cmd2 := m2.Update(waveTickMsg{})
	if res2.(model).phase != 0 {
		t.Fatal("tick must not advance the phase when not thinking")
	}
	if cmd2 != nil {
		t.Fatal("tick must not re-schedule when not thinking")
	}
}
