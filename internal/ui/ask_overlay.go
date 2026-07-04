package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Townk/ai-playbook/internal/askbridge"
	"github.com/Townk/ai-playbook/pkg/dialog"
)

// askOpenMsg delivers one pending agent ask to the model (produced by recvAskCmd
// reading the bridge).
type askOpenMsg struct{ req askbridge.Request }

// recvAskCmd blocks for the next pending ask on the bridge and re-arms after each
// one resolves, so the overlay can be raised repeatedly within a session. A nil
// bridge yields nil (no-op) so the non-no-mux paths never subscribe.
func recvAskCmd(b *askbridge.Bridge) tea.Cmd {
	if b == nil {
		return nil
	}
	return func() tea.Msg { return askOpenMsg{req: <-b.Requests()} }
}

// openAsk raises the ask overlay for req, building the embedded field from the
// request type (and choices for "choose").
func (m *model) openAsk(req askbridge.Request) tea.Cmd {
	m.askMode = true
	m.askReq = req
	m.ask = dialog.NewAsk("ai-playbook", req.Prompt, "", req.Type, req.Choices, "", "")
	return m.ask.Init()
}

// handleAskKey routes a message to the embedded ask while the overlay is open and,
// on submit/cancel, replies to the agent and re-arms the bridge reader.
func (m *model) handleAskKey(msg tea.Msg) tea.Cmd {
	cmd, done, submitted, value := m.ask.Update(msg)
	if !done {
		return cmd
	}
	// A VIEWER-initiated overlay (refine) routes its result to askCompletion and does
	// NOT reply to the agent or re-arm the bridge — it wasn't a bridge ask.
	if complete := m.askCompletion; complete != nil {
		m.askMode = false
		m.ask = nil
		m.askCompletion = nil
		out := complete(value, submitted)
		return func() tea.Msg { return out }
	}
	m.askReq.Respond(askbridge.Answer{Value: value, Submitted: submitted})
	m.askMode = false
	m.ask = nil
	return recvAskCmd(m.askBridge) // re-arm for the next ask
}

// askOverlay composites the ask dialog centered over the live document, exactly
// like the help modal (spliceOver), so the playbook keeps rendering behind it.
func (m model) askOverlay() string {
	base := m.normalLines()
	box := strings.Split(m.ask.View(dialog.FloatWidthDefault), "\n")
	boxH := len(box)
	boxW := 0
	if boxH > 0 {
		boxW = lipgloss.Width(box[0])
	}
	left := (m.width - boxW) / 2
	if left < 0 {
		left = 0
	}
	top := 2 + (m.height-4-boxH)/2
	if top < 2 {
		top = 2
	}
	for i, bl := range box {
		if r := top + i; r >= 0 && r < len(base) {
			base[r] = spliceOver(base[r], bl, left)
		}
	}
	return strings.Join(base, "\n")
}

// drainAskCancel auto-cancels every pending ask on b until stop is closed. It
// guards the headless (no-TTY) viewer branch, which never raises the overlay: an
// agent `ask` would otherwise block the tools goroutine (and deadlock the drain
// loop) forever. Each ask gets an unsubmitted (cancel) answer so the agent always
// gets a definite, non-hanging reply. A nil bridge is a no-op.
func drainAskCancel(b *askbridge.Bridge, stop <-chan struct{}) {
	if b == nil {
		return
	}
	for {
		select {
		case req := <-b.Requests():
			req.Respond(askbridge.Answer{Submitted: false})
		case <-stop:
			return
		}
	}
}
