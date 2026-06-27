package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Townk/ai-playbook/internal/askbridge"
	"github.com/Townk/ai-playbook/internal/input"
)

func TestAskOverlay_OpensAndRespondsOnSubmit(t *testing.T) {
	b := askbridge.New()
	answered := make(chan askbridge.Answer, 1)
	go func() { answered <- b.Ask("which env?", "line", nil) }()

	m := newModel("agent", "# Playbook\n\nbody")
	m.width = 100
	m.height = 30
	m.askBridge = b

	// Deliver the pending ask (recvAskCmd would produce this msg at runtime).
	req := <-b.Requests()
	m2, _ := m.Update(askOpenMsg{req: req})
	m = m2.(model)
	if !m.askMode {
		t.Fatal("askOpenMsg must enter askMode")
	}
	if !strings.Contains(m.viewString(), "which env?") {
		t.Error("overlay View must show the ask prompt")
	}

	// Type then submit.
	m3, _ := m.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	m = m3.(model)
	m4, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = m4.(model)
	if m.askMode {
		t.Error("submit must leave askMode")
	}
	if a := <-answered; !a.Submitted || a.Value != "p" {
		t.Fatalf("bridge answer = %+v, want {p,true}", a)
	}
}

func TestAskOverlay_EscCancelsAndRespondsUnsubmitted(t *testing.T) {
	b := askbridge.New()
	answered := make(chan askbridge.Answer, 1)
	go func() { answered <- b.Ask("which env?", "line", nil) }()

	m := newModel("agent", "# Playbook\n\nbody")
	m.width = 100
	m.height = 30
	m.askBridge = b

	req := <-b.Requests()
	m2, _ := m.Update(askOpenMsg{req: req})
	m = m2.(model)

	m3, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = m3.(model)
	if m.askMode {
		t.Error("esc must leave askMode")
	}
	if a := <-answered; a.Submitted {
		t.Fatalf("bridge answer = %+v, want a cancel", a)
	}
}

func TestAskOverlay_EscCancelsAndReplies(t *testing.T) {
	b := askbridge.New()
	answered := make(chan askbridge.Answer, 1)
	go func() { answered <- b.Ask("name?", "line", nil) }()

	m := newModel("agent", "# P")
	m.width, m.height, m.askBridge = 80, 24, b
	req := <-b.Requests()
	m2, _ := m.Update(askOpenMsg{req: req})
	m = m2.(model)
	m3, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = m3.(model)
	if m.askMode {
		t.Error("esc must close the overlay")
	}
	if a := <-answered; a.Submitted {
		t.Fatalf("esc answer = %+v, want Submitted=false", a)
	}
}

func TestAskOverlay_ChooseDeliversChoicesAndResponds(t *testing.T) {
	b := askbridge.New()
	answered := make(chan askbridge.Answer, 1)
	go func() { answered <- b.Ask("pick env", "choose", []string{"dev", "stage", "prod"}) }()

	m := newModel("agent", "# Playbook\n\nbody")
	m.width = 100
	m.height = 30
	m.askBridge = b

	req := <-b.Requests()
	m2, _ := m.Update(askOpenMsg{req: req})
	m = m2.(model)
	if !strings.Contains(m.viewString(), "stage") {
		t.Error("choose overlay must render the options")
	}

	m3, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown}) // highlight "stage"
	m = m3.(model)
	m4, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = m4.(model)
	if m.askMode {
		t.Error("choose submit must leave askMode")
	}
	if a := <-answered; !a.Submitted || a.Value != "stage" {
		t.Fatalf("bridge answer = %+v, want {stage,true}", a)
	}
}

// TestAskOverlay_StreamEventsPassThrough verifies that while the ask overlay is
// open (askMode=true), a streamEventsMsg is NOT swallowed by handleAskKey but
// instead falls through to the main Update switch, which re-arms the stream
// reader. Without the fix the streamEventsMsg was diverted to handleAskKey →
// the events were dropped and readStream was never re-issued → the viewer
// stalled after the user answered the ask.
func TestAskOverlay_StreamEventsPassThrough(t *testing.T) {
	m := newModel("T", "# Playbook\n")
	m.width, m.height = 80, 24
	m.reader = strings.NewReader("")
	m.parser = &streamParser{}

	// Enter askMode with a live ask widget (mirrors openAsk).
	m.askMode = true
	m.ask = input.NewAsk("ai-playbook", "which env?", "", "line", nil)

	// A streamEventsMsg carrying no events (EOF=false) should be processed by
	// the main switch, which always issues readStream as its first cmd.
	_, cmd := m.Update(streamEventsMsg{events: nil, eof: false})
	if cmd == nil {
		t.Fatal("streamEventsMsg while askMode must re-arm the stream (non-nil cmd); overlay is freezing the stream")
	}
}
