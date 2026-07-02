package input

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestAsk_SubmitReturnsValue(t *testing.T) {
	a := NewAsk("ai-playbook", "which env?", "prod", "line", nil, "", "")
	_, done, submitted, value := a.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !done || !submitted || value != "prod" {
		t.Fatalf("submit = (done=%v submitted=%v value=%q), want (true,true,prod)", done, submitted, value)
	}
}

func TestAsk_EscCancels(t *testing.T) {
	a := NewAsk("ai-playbook", "which env?", "", "line", nil, "", "")
	_, done, submitted, _ := a.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !done || submitted {
		t.Fatalf("esc = (done=%v submitted=%v), want (true,false)", done, submitted)
	}
}

func TestAsk_TextTypingThenSubmit(t *testing.T) {
	a := NewAsk("ai-playbook", "details?", "", "text", nil, "", "")
	a.Update(tea.KeyPressMsg{Code: 'h', Text: "h"})
	a.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	_, done, submitted, value := a.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !done || !submitted || value != "hi" {
		t.Fatalf("text submit = (done=%v submitted=%v value=%q), want (true,true,hi)", done, submitted, value)
	}
}

func TestAsk_ConfirmYes(t *testing.T) {
	a := NewAsk("ai-playbook", "proceed?", "", "confirm", nil, "", "")
	// Default focus is the affirmative button; Enter submits "yes".
	_, done, submitted, value := a.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !done || !submitted || value != "yes" {
		t.Fatalf("confirm = (done=%v submitted=%v value=%q), want (true,true,yes)", done, submitted, value)
	}
}

func TestAsk_ConfirmNoViaArrow(t *testing.T) {
	a := NewAsk("ai-playbook", "proceed?", "", "confirm", nil, "", "")
	a.Update(tea.KeyPressMsg{Code: tea.KeyRight}) // focus negative
	_, done, submitted, value := a.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !done || !submitted || value != "no" {
		t.Fatalf("confirm-no = (done=%v submitted=%v value=%q), want (true,true,no)", done, submitted, value)
	}
}

func TestAsk_ChooseSelectSecond(t *testing.T) {
	a := NewAsk("ai-playbook", "pick env", "", "choose", []string{"dev", "stage", "prod"}, "", "")
	a.Update(tea.KeyPressMsg{Code: tea.KeyDown}) // highlight "stage"
	_, done, submitted, value := a.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !done || !submitted || value != "stage" {
		t.Fatalf("choose = (done=%v submitted=%v value=%q), want (true,true,stage)", done, submitted, value)
	}
}

func TestAsk_ViewShowsPrompt(t *testing.T) {
	a := NewAsk("ai-playbook", "which env?", "", "line", nil, "", "")
	if !strings.Contains(a.View(57), "which env?") {
		t.Error("View must render the prompt")
	}
}

func TestAsk_ViewChooseShowsOptions(t *testing.T) {
	a := NewAsk("ai-playbook", "pick env", "", "choose", []string{"dev", "prod"}, "", "")
	v := a.View(57)
	if !strings.Contains(v, "dev") || !strings.Contains(v, "prod") {
		t.Errorf("choose View must render the options, got:\n%s", v)
	}
}

// TestAsk_ConfirmPromptHasMantleBackground exercises the ACTUAL code path
// behind the reported var-gate dialog: confirm_gate.go's raiseGroupConfirm
// builds its "Variables" dialog via NewAsk(..., "confirm", ...), which routes
// through model.render() in input.go (NOT confirmModel in confirm.go — that
// type only backs the standalone `ai-playbook input --type confirm` CLI). The
// prompt/var-list line must carry the dialog's Mantle background, not bleed to
// the terminal default.
func TestAsk_ConfirmPromptHasMantleBackground(t *testing.T) {
	a := NewAsk("Variables", "Confirm these variables for this run:", "", "confirm", nil, "Confirm", "Customize")
	out := a.View(57)
	promptLine := findLine(t, out, "Confirm these variables")
	if !strings.Contains(promptLine, mantleBgSGR) {
		t.Fatalf("confirm-ask prompt line missing Mantle background SGR:\n%q", promptLine)
	}
}

// TestAsk_LinePromptHasMantleBackground covers the per-var Customize edit
// (confirm_gate.go's raiseVarEdit calls NewAsk(..., "line", ...)) — the same
// model.render() path, same bleed risk for the prompt/label line above the box.
func TestAsk_LinePromptHasMantleBackground(t *testing.T) {
	a := NewAsk("Customize", "MY_VAR — why this value", "value", "line", nil, "", "")
	out := a.View(57)
	promptLine := findLine(t, out, "MY_VAR")
	if !strings.Contains(promptLine, mantleBgSGR) {
		t.Fatalf("line-ask prompt line missing Mantle background SGR:\n%q", promptLine)
	}
}

func TestNewAsk_ConfirmCustomLabels(t *testing.T) {
	a := NewAsk("t", "p", "", "confirm", nil, "Confirm", "Customize")
	v := a.View(60)
	if !strings.Contains(v, "Confirm") || !strings.Contains(v, "Customize") {
		t.Fatalf("confirm view missing custom labels:\n%s", v)
	}
	// empty labels fall back to Yes/No
	b := NewAsk("t", "p", "", "confirm", nil, "", "")
	if vb := b.View(60); !strings.Contains(vb, "Yes") || !strings.Contains(vb, "No") {
		t.Fatalf("confirm view missing default labels:\n%s", vb)
	}
}
